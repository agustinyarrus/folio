package main

// Folio — lector de Markdown ultraminimalista, dark, frameless. Hermano de Lumen.
//
// Un solo .exe: embebe la UI (carpeta ui/) y la sirve por un server HTTP local a una ventana
// WebView2 SIN marco del sistema. La barra de titulo y los botones min/max/cerrar los dibuja la
// pagina; aca exponemos el puente JS -> Win32 (mover, redimensionar, botones, pantalla completa),
// renderizamos el Markdown a HTML con goldmark+chroma (ver render.go) y servimos los assets
// relativos del documento. Con recarga en vivo: si el .md cambia en disco, la vista se actualiza.
//
// Frameless (igual que Lumen / el host de IA History Reader): subclasamos el WndProc y devolvemos
// 0 en WM_NCCALCSIZE para que el area cliente ocupe toda la ventana; drag/resize via
// WM_NCLBUTTONDOWN (mantiene Aero Snap).

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

var debugLog = os.Getenv("FOLIO_DEBUG") != ""
var startTime = time.Now()

func dlog(args ...any) {
	if debugLog {
		ms := fmt.Sprintf("[folio +%dms]", time.Since(startTime).Milliseconds())
		fmt.Fprintln(os.Stderr, append([]any{ms}, args...)...)
	}
}

//go:embed ui
var uiFS embed.FS

const (
	wmNCCALCSIZE      = 0x0083
	wmNCLBUTTONDOWN   = 0x00A1
	wmCLOSE           = 0x0010
	wmERASEBKGND      = 0x0014
	whCBT             = 5
	hcbtCREATEWND     = 3
	smCXSCREEN        = 0
	smCYSCREEN        = 1
	smXVIRTUALSCREEN  = 76
	smYVIRTUALSCREEN  = 77
	smCXVIRTUALSCREEN = 78
	smCYVIRTUALSCREEN = 79
	htCAPTION         = 2
	htLEFT            = 10
	htRIGHT           = 11
	htTOP             = 12
	htTOPLEFT         = 13
	htTOPRIGHT        = 14
	htBOTTOM          = 15
	htBOTTOMLEFT      = 16
	htBOTTOMRIGHT     = 17
	swHIDE            = 0
	swSHOW            = 5
	swMINIMIZE        = 6
	swMAXIMIZE        = 3
	swRESTORE         = 9
	swSHOWMAXIMIZED   = 3
	swSHOWMINIMIZED   = 2
	smCXFRAME         = 32
	smCYFRAME         = 33
	smCXPADDEDBORDER  = 92
	swpFRAMECHANGED   = 0x0020
	swpNOMOVE         = 0x0002
	swpNOSIZE         = 0x0001
	swpNOZORDER       = 0x0004
	swpSHOWWINDOW     = 0x0040

	hwndTop      = 0
	hwndTopmost  = ^uintptr(0)     // (HWND)-1
	hwndNoTopmst = ^uintptr(0) - 1 // (HWND)-2
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	comctl32 = windows.NewLazySystemDLL("comctl32.dll")
	shcore   = windows.NewLazySystemDLL("shcore.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")

	pSetWindowSubclass        = comctl32.NewProc("SetWindowSubclass")
	pDefSubclassProc          = comctl32.NewProc("DefSubclassProc")
	pSetWindowPos             = user32.NewProc("SetWindowPos")
	pShowWindow               = user32.NewProc("ShowWindow")
	pSendMessageW             = user32.NewProc("SendMessageW")
	pPostMessageW             = user32.NewProc("PostMessageW")
	pReleaseCapture           = user32.NewProc("ReleaseCapture")
	pGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	pGetWindowPlacement       = user32.NewProc("GetWindowPlacement")
	pSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	pGetClientRect            = user32.NewProc("GetClientRect")
	pFillRect                 = user32.NewProc("FillRect")
	pCreateSolidBrush         = gdi32.NewProc("CreateSolidBrush")
	pSelectObject             = gdi32.NewProc("SelectObject")
	pDeleteObject             = gdi32.NewProc("DeleteObject")
	pCreatePen                = gdi32.NewProc("CreatePen")
	pPolyline                 = gdi32.NewProc("Polyline")
	pInvalidateRect           = user32.NewProc("InvalidateRect")
	pUpdateWindow             = user32.NewProc("UpdateWindow")
	pGetWindowRect            = user32.NewProc("GetWindowRect")
	pSetWindowsHookExW        = user32.NewProc("SetWindowsHookExW")
	pUnhookWindowsHookEx      = user32.NewProc("UnhookWindowsHookEx")
	pCallNextHookEx           = user32.NewProc("CallNextHookEx")
	pGetCurrentThreadId       = kernel32.NewProc("GetCurrentThreadId")
	pAllowSetForegroundWindow = user32.NewProc("AllowSetForegroundWindow")
	pDwmSetWindowAttribute    = dwmapi.NewProc("DwmSetWindowAttribute")

	// onShow lo setea main() una vez creada la ventana: lo invoca el handler /api/show cuando
	// otra invocación de folio.exe le pasa un documento (instancia única).
	onShow func(string)

	// canal "abrir documento": el server avisa al cliente por SSE qué .md mostrar. Más robusto que
	// un Eval directo cuando la ventana venía oculta (daemon caliente): la conexión SSE sobrevive.
	openSubsMu  sync.Mutex
	openSubs    = map[chan string]struct{}{}
	pendingOpen string // última ruta pedida; se reenvía a cada cliente que (re)conecta al canal

	darkBrush  uintptr
	subclassCB uintptr // callback de subclassProc; lo instala el CBT hook al crearse la ventana
	uiScale    = 1.0   // factor DPI, para el splash nativo
	splashDone bool    // una vez listo el contenido, el host deja de dibujar la marca
	fullscreen bool
	savedPlc   windowPlacement
	savedOK    bool

	// spawnX/spawnY: posición final (px físicos) con la que NACE la ventana. La setea main() antes
	// de crearla y la aplica cbtProc sobre el CREATESTRUCT, para que el primer pixel ya esté en el
	// lugar definitivo (guardado o centrado) y showWin no tenga que moverla -> sin "salto".
	spawnX, spawnY int32
	spawnPosSet    bool

	offscreenSpawn = os.Getenv("FOLIO_OFFSCREEN") != ""
)

type rect struct{ left, top, right, bottom int32 }
type point struct{ x, y int32 }
type nccalcsizeParams struct {
	rgrc  [3]rect
	lppos uintptr
}
type windowPlacement struct {
	length           uint32
	flags            uint32
	showCmd          uint32
	ptMinPosition    point
	ptMaxPosition    point
	rcNormalPosition rect
}

func sysMetric(i int) int32 {
	r, _, _ := pGetSystemMetrics.Call(uintptr(i))
	return int32(r)
}

type createstructW struct {
	lpCreateParams uintptr
	hInstance      uintptr
	hMenu          uintptr
	hwndParent     uintptr
	cy, cx, y, x   int32
	style          int32
	lpszName       uintptr
	lpszClass      uintptr
	dwExStyle      uint32
}
type cbtCreatewnd struct {
	lpcs            uintptr
	hwndInsertAfter uintptr
}

func u16ptrToString(p uintptr) string {
	if p == 0 {
		return ""
	}
	buf := make([]uint16, 0, 24)
	for i := uintptr(0); ; i += 2 {
		c := *(*uint16)(unsafe.Pointer(p + i))
		if c == 0 {
			break
		}
		buf = append(buf, c)
	}
	return windows.UTF16ToString(buf)
}

// cbtProc engancha el nacimiento de NUESTRA ventana (CBT hook, corre dentro de CreateWindowEx, antes
// del ShowWindow que hace go-webview2): la subclasa al instante para que nazca frameless + oscura, y
// le fija en el CREATESTRUCT la posición final (guardada/centrada). go-webview2 sólo expone Center
// (no X/Y) y muestra la ventana enseguida, así que sin esto nacería en CW_USEDEFAULT y recién showWin
// la movería al lugar guardado: se vería "saltar". (offscreen = -32000 queda como escape de debug.)
func cbtProc(nCode, wParam, lParam uintptr) uintptr {
	if int32(nCode) == hcbtCREATEWND && lParam != 0 {
		cbt := (*cbtCreatewnd)(unsafe.Pointer(lParam))
		if cbt.lpcs != 0 {
			cs := (*createstructW)(unsafe.Pointer(cbt.lpcs))
			if cs.hwndParent == 0 && cs.lpszClass > 0xFFFF && u16ptrToString(cs.lpszClass) == "webview" {
				// Subclasar AL INSTANTE de crearse: así la ventana nace frameless + oscura (con la
				// marca) desde el primer pixel, sin el flash de barra de título nativa + fondo claro
				// mientras WebView2 hace su cold-start (wParam = hwnd de la ventana naciendo).
				if subclassCB != 0 {
					pSetWindowSubclass.Call(wParam, subclassCB, 1, 0)
				}
				// Nacer YA en la posición final (guardada o centrada). Sin esto, go-webview2 crea la
				// ventana en CW_USEDEFAULT (cascada arriba-izquierda) y recién showWin la mueve al lugar
				// guardado: se la ve "saltar". offscreen queda como escape de depuración.
				if offscreenSpawn {
					cs.x, cs.y = -32000, -32000
				} else if spawnPosSet {
					cs.x, cs.y = spawnX, spawnY
				}
			}
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, nCode, wParam, lParam)
	return r
}

// Declarar Per-Monitor-V2 ANTES de crear ventanas; si no, WebView2 se renderiza a 96 DPI y
// Windows lo estira -> borroso.
func setDpiAware() {
	if p := user32.NewProc("SetProcessDpiAwarenessContext"); p.Find() == nil {
		if r, _, _ := p.Call(^uintptr(0) - 3); r != 0 { // PER_MONITOR_AWARE_V2 (-4)
			return
		}
	}
	if p := shcore.NewProc("SetProcessDpiAwareness"); p.Find() == nil {
		if r, _, _ := p.Call(2); r == 0 {
			return
		}
	}
	user32.NewProc("SetProcessDPIAware").Call()
}

func getDpiForSystem() int {
	if p := user32.NewProc("GetDpiForSystem"); p.Find() == nil {
		if r, _, _ := p.Call(); r != 0 {
			return int(r)
		}
	}
	return 96
}

// roundCorners pide a DWM esquinas redondeadas estilo Win11 (sólo build 22000+). Funciona aunque la
// ventana sea frameless (es un atributo de composición, ajeno al cálculo de área cliente) y Windows
// las cuadra solo al maximizar/snapear. En Win10 el atributo no existe -> devuelve error y se ignora.
func roundCorners(hwnd uintptr) {
	const dwmwaWindowCornerPreference = 33 // DWMWA_WINDOW_CORNER_PREFERENCE
	const dwmwcpRound = 2                  // DWMWCP_ROUND (redondeo estándar; 3 = ROUNDSMALL)
	pref := int32(dwmwcpRound)
	r, _, _ := pDwmSetWindowAttribute.Call(hwnd, dwmwaWindowCornerPreference,
		uintptr(unsafe.Pointer(&pref)), unsafe.Sizeof(pref))
	dlog("roundCorners hr=", int32(r))
}

// setDarkFrame oscurece el MARCO que DWM dibuja alrededor de la ventana en Win11. Sin esto, el borde
// (y la línea fina del contorno redondeado) sigue el tema del SISTEMA y sale claro/blancuzco sobre
// la app oscura. (1) modo oscuro inmersivo -> el frame se renderiza dark; (2) color de borde explícito
// (#262B36, COLORREF 0x00BBGGRR) -> borde dark sutil y determinista sin importar el tema del sistema.
func setDarkFrame(hwnd uintptr) {
	const (
		dwmwaUseImmersiveDarkMode = 20 // Win11/Win10 2004+ (en builds viejos era 19; devuelve error e ignora)
		dwmwaBorderColor          = 34 // DWMWA_BORDER_COLOR (build 22000+)
	)
	on := int32(1)
	pDwmSetWindowAttribute.Call(hwnd, dwmwaUseImmersiveDarkMode, uintptr(unsafe.Pointer(&on)), unsafe.Sizeof(on))
	border := uint32(0x00000000) // negro (COLORREF 0x00BBGGRR)
	r, _, _ := pDwmSetWindowAttribute.Call(hwnd, dwmwaBorderColor, uintptr(unsafe.Pointer(&border)), unsafe.Sizeof(border))
	dlog("setDarkFrame borderHr=", int32(r))
}

func isMaximized(hwnd uintptr) bool {
	var wp windowPlacement
	wp.length = uint32(unsafe.Sizeof(wp))
	pGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp)))
	return wp.showCmd == swSHOWMAXIMIZED
}

func isMinimized(hwnd uintptr) bool {
	var wp windowPlacement
	wp.length = uint32(unsafe.Sizeof(wp))
	pGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp)))
	return wp.showCmd == swSHOWMINIMIZED
}

// captureGeom guarda la geometría de la ventana en PÍXELES FÍSICOS (GetWindowRect, que bajo
// Per-Monitor-DPI-v2 devuelve device px) para que el round-trip con SetWindowPos (también device
// px) sea exacto en DPI altos. GetWindowPlacement, en cambio, devuelve unidades lógicas y descuadra
// la posición. El tamaño se recrea como W*scale en WindowOptions (go-webview2 lo divide por scale).
func captureGeom(hwnd uintptr) winGeom {
	max := isMaximized(hwnd)
	var rc rect
	pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
	return winGeom{X: rc.left, Y: rc.top, W: rc.right - rc.left, H: rc.bottom - rc.top, Max: max}
}

// targetWindowPos decide dónde va la ventana de tamaño ww*hh (px físicos): la posición guardada del
// último cierre, o centrada en el monitor primario si no hay. La usan POR IGUAL el nacimiento de la
// ventana (cbtProc) y el reveal (showWin), así nunca difieren y no hay salto. El clamp se hace contra
// el escritorio VIRTUAL (no sólo el primario) para respetar posiciones en un monitor secundario
// (vx/vy pueden ser negativos) sin dejar la ventana fuera de la vista.
func targetWindowPos(ww, hh int32) (x, y int32, maximized bool) {
	if g := gCfg.Window; g != nil && g.W > 200 && g.H > 150 {
		x, y, maximized = g.X, g.Y, g.Max
	} else {
		sw, sh := sysMetric(smCXSCREEN), sysMetric(smCYSCREEN)
		x, y = (sw-ww)/2, (sh-hh)/2
	}
	vx, vy := sysMetric(smXVIRTUALSCREEN), sysMetric(smYVIRTUALSCREEN)
	vw, vh := sysMetric(smCXVIRTUALSCREEN), sysMetric(smCYVIRTUALSCREEN)
	if vw <= 0 || vh <= 0 { // fallback si el virtual screen no responde
		vx, vy = 0, 0
		vw, vh = sysMetric(smCXSCREEN), sysMetric(smCYSCREEN)
	}
	if x > vx+vw-120 {
		x = vx + vw - 120
	}
	if y > vy+vh-80 {
		y = vy + vh - 80
	}
	if x < vx {
		x = vx
	}
	if y < vy {
		y = vy
	}
	return
}

// bringToFront restaura (si está minimizada) y trae la ventana al frente de forma fiable.
func bringToFront(hwnd uintptr) {
	if isMinimized(hwnd) {
		pShowWindow.Call(hwnd, swRESTORE)
	} else {
		pShowWindow.Call(hwnd, swSHOW)
	}
	pSetWindowPos.Call(hwnd, hwndTopmost, 0, 0, 0, 0, uintptr(swpNOMOVE|swpNOSIZE|swpSHOWWINDOW))
	pSetWindowPos.Call(hwnd, hwndNoTopmst, 0, 0, 0, 0, uintptr(swpNOMOVE|swpNOSIZE))
	pSetForegroundWindow.Call(hwnd)
}

// ---- instancia única (daemon caliente) ---------------------------------
// El primer folio.exe queda corriendo; cada invocación siguiente le manda la ruta por HTTP y
// sale al instante (sin pagar el cold-start de WebView2). El lock guarda "puerto\npid".

func lockPath() string {
	d, err := os.UserCacheDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, "Folio", "instance.lock")
}

func writeLock(port string) {
	p := lockPath()
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(port+"\n"+strconv.Itoa(os.Getpid())), 0o644)
}

func removeLock() { os.Remove(lockPath()) }

func readLock() (port string, pid int, ok bool) {
	b, err := os.ReadFile(lockPath())
	if err != nil {
		return "", 0, false
	}
	parts := strings.SplitN(strings.TrimSpace(string(b)), "\n", 2)
	if len(parts) < 2 || parts[0] == "" {
		return "", 0, false
	}
	pid, _ = strconv.Atoi(parts[1])
	return parts[0], pid, true
}

// tryHandoff devuelve true si había una instancia viva que aceptó mostrar el documento.
func tryHandoff(path string) bool {
	port, pid, ok := readLock()
	if !ok {
		return false
	}
	if pid > 0 {
		pAllowSetForegroundWindow.Call(uintptr(pid))
	}
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get("http://127.0.0.1:" + port + "/api/show?path=" + url.QueryEscape(path))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// drawSplashMark dibuja el glifo de Folio (pagina doblada) con GDI, centrado, sobre el HDC dado.
// Se usa durante el cold-start de WebView2: la ventana host esta visible y todavia NO la tapa el
// child del navegador, asi que en vez de un recuadro vacio se ve la marca al instante. Empalma con
// el splash web (misma marca) cuando la pagina pinta.
func drawSplashMark(hdc uintptr, rc rect) {
	const psSolid = 0
	cw, ch := rc.right-rc.left, rc.bottom-rc.top
	if cw < 60 || ch < 60 {
		return
	}
	s := (86.0 * uiScale) / 78.0 // alto del glifo en su espacio 0..100 (~11..89)
	tb := 38.0 * uiScale         // alto de la barra de titulo, para alinear con el splash web
	ox := float64(rc.left+cw/2) - 54*s
	oy := float64(rc.top) + (float64(ch)+tb)/2 - 50*s
	P := func(gx, gy float64) point { return point{int32(ox + gx*s), int32(oy + gy*s)} }

	w := int32(3.2 * uiScale)
	if w < 2 {
		w = 2
	}
	faint, _, _ := pCreatePen.Call(psSolid, uintptr(w), 0x005E4E47)  // #474E5E
	accent, _, _ := pCreatePen.Call(psSolid, uintptr(w), 0x00F7A27A) // #7AA2F7
	defer pDeleteObject.Call(faint)
	defer pDeleteObject.Call(accent)
	poly := func(pen uintptr, pts []point) {
		old, _, _ := pSelectObject.Call(hdc, pen)
		pPolyline.Call(hdc, uintptr(unsafe.Pointer(&pts[0])), uintptr(len(pts)))
		pSelectObject.Call(hdc, old)
	}
	poly(faint, []point{P(27, 11), P(61, 11), P(81, 31), P(81, 89), P(27, 89), P(27, 11)}) // pagina
	poly(accent, []point{P(61, 11), P(61, 31), P(81, 31)})                                 // esquina doblada
	poly(faint, []point{P(37, 46), P(71, 46)})
	poly(faint, []point{P(37, 58), P(71, 58)})
	poly(accent, []point{P(37, 70), P(58, 70)})
}

func subclassProc(hwnd, msg, wParam, lParam, uID, dwRef uintptr) uintptr {
	if msg == wmNCCALCSIZE && wParam != 0 {
		if !fullscreen && isMaximized(hwnd) {
			p := (*nccalcsizeParams)(unsafe.Pointer(lParam))
			cx := sysMetric(smCXFRAME) + sysMetric(smCXPADDEDBORDER)
			cy := sysMetric(smCYFRAME) + sysMetric(smCXPADDEDBORDER)
			p.rgrc[0].left += cx
			p.rgrc[0].top += cy
			p.rgrc[0].right -= cx
			p.rgrc[0].bottom -= cy
		}
		return 0
	}
	if msg == wmERASEBKGND && darkBrush != 0 {
		var rc rect
		pGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
		pFillRect.Call(wParam, uintptr(unsafe.Pointer(&rc)), darkBrush)
		if !splashDone { // mientras WebView2 arranca, mostramos la marca en vez de un recuadro vacio
			drawSplashMark(wParam, rc)
		}
		return 1
	}
	if msg == wmCLOSE {
		saveWindowGeom(captureGeom(hwnd)) // recordar tamaño/posición para el próximo arranque
	}
	r, _, _ := pDefSubclassProc.Call(hwnd, msg, wParam, lParam)
	return r
}

func htCode(dir string) uintptr {
	switch dir {
	case "l":
		return htLEFT
	case "r":
		return htRIGHT
	case "t":
		return htTOP
	case "b":
		return htBOTTOM
	case "tl":
		return htTOPLEFT
	case "tr":
		return htTOPRIGHT
	case "bl":
		return htBOTTOMLEFT
	case "br":
		return htBOTTOMRIGHT
	}
	return htCAPTION
}

func enterFullscreen(hwnd uintptr) {
	if fullscreen {
		return
	}
	savedPlc.length = uint32(unsafe.Sizeof(savedPlc))
	pGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&savedPlc)))
	savedOK = true
	fullscreen = true
	rc := monitorRect(hwnd)
	pSetWindowPos.Call(hwnd, hwndTopmost,
		uintptr(rc.left), uintptr(rc.top), uintptr(rc.right-rc.left), uintptr(rc.bottom-rc.top),
		uintptr(swpFRAMECHANGED|swpSHOWWINDOW))
}

func exitFullscreen(hwnd uintptr) {
	if !fullscreen {
		return
	}
	fullscreen = false
	if savedOK {
		pSetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&savedPlc)))
	}
	pSetWindowPos.Call(hwnd, hwndNoTopmst, 0, 0, 0, 0,
		uintptr(swpNOMOVE|swpNOSIZE|swpFRAMECHANGED))
}

func main() {
	runtime.LockOSThread()
	setDpiAware()
	scale := float64(getDpiForSystem()) / 96.0
	uiScale = scale
	loadConfig()

	initialPath := ""
	if len(os.Args) > 1 && strings.TrimSpace(os.Args[1]) != "" {
		if abs, err := filepath.Abs(os.Args[1]); err == nil {
			initialPath = abs
		}
	}

	if os.Getenv("FOLIO_NEW") == "" && tryHandoff(initialPath) {
		dlog("handoff a instancia existente; saliendo")
		return
	}

	addr := startServer(initialPath)
	pageURL := "http://" + addr + "/"
	dlog("server addr", addr, "initial", initialPath)
	if _, portStr, err := net.SplitHostPort(addr); err == nil {
		writeLock(portStr)
		defer removeLock()
	}

	// dark + callback listos ANTES del hook: el CBT hook subclasa la ventana al nacer, para que sea
	// frameless + oscura (con la marca) desde el primer pixel, sin flash de barra nativa / fondo claro.
	darkBrush, _, _ = pCreateSolidBrush.Call(0x000C0908) // COLORREF de #08090C (fondo Nocturne)
	subclassCB = windows.NewCallback(subclassProc)

	tid, _, _ := pGetCurrentThreadId.Call()
	cbtHook, _, _ := pSetWindowsHookExW.Call(uintptr(whCBT), windows.NewCallback(cbtProc), 0, tid)

	dataPath := ""
	if d, err := os.UserCacheDir(); err == nil {
		dataPath = filepath.Join(d, "Folio", "WebView2")
	}
	// Flags de Chromium: que NO frene el render con la ventana oculta/ocluida (clave para que la
	// página pinte mientras la mantenemos invisible hasta estar lista). Se anexan aunque el entorno
	// ya traiga args (p.ej. --remote-debugging-port en verificación).
	renderFlags := "--no-first-run --disable-background-networking --disable-component-update " +
		"--disable-backgrounding-occluded-windows --disable-renderer-backgrounding"
	if extra := os.Getenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS"); extra == "" {
		os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", renderFlags)
	} else {
		os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", extra+" "+renderFlags)
	}

	// tamaño de ventana: el guardado del último cierre, o el default centrado.
	// WindowOptions.Width/Height son px FÍSICOS (por eso el default es 1180*scale). captureGeom
	// guarda px físicos (GetWindowRect), así que recreamos con g.W/g.H tal cual: round-trip estable.
	winW, winH := uint(1180*scale), uint(840*scale)
	centerWin := true
	if g := gCfg.Window; g != nil && g.W > 200 && g.H > 150 {
		winW, winH = uint(g.W), uint(g.H)
		centerWin = false
	}
	// Posición definitiva ANTES de crear: cbtProc la clava en el CREATESTRUCT para que la ventana
	// nazca ahí. (Center sigue como fallback por si el hook no llegara a correr.)
	spawnX, spawnY, _ = targetWindowPos(int32(winW), int32(winH))
	spawnPosSet = true

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     debugLog,
		AutoFocus: true,
		DataPath:  dataPath,
		WindowOptions: webview2.WindowOptions{
			Title:  "Folio",
			Width:  winW,
			Height: winH,
			Center: centerWin,
			IconId: 1, // RT_GROUP_ICON embebido por rsrc.syso (folio.ico)
		},
	})
	if cbtHook != 0 {
		pUnhookWindowsHookEx.Call(cbtHook)
	}
	if w == nil {
		panic("no se pudo crear WebView2")
	}
	defer w.Destroy()

	hwnd := uintptr(w.Window())
	roundCorners(hwnd)          // esquinas redondeadas Win11 (frameless-friendly; no-op en Win10)
	setDarkFrame(hwnd)          // borde/marco DWM en oscuro (si no, sale claro siguiendo el tema del sistema)
	setWebViewDarkBackground(w) // about:blank OSCURO (la ventana ya nació dark+frameless por el hook)
	pSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0, uintptr(swpNOMOVE|swpNOSIZE|swpNOZORDER|swpFRAMECHANGED))
	// pintar la marca de Folio YA (el host esta visible y WebView2 todavia no lo tapa)
	pInvalidateRect.Call(hwnd, 0, 1)
	pUpdateWindow.Call(hwnd)

	w.SetSize(int(640*scale), int(480*scale), webview2.HintMin)

	// ---- puentes JS -> ventana ----
	w.Bind("folioMin", func() {
		w.Dispatch(func() { pShowWindow.Call(hwnd, swMINIMIZE) })
	})
	w.Bind("folioMaxToggle", func() {
		w.Dispatch(func() {
			if isMaximized(hwnd) {
				pShowWindow.Call(hwnd, swRESTORE)
			} else {
				pShowWindow.Call(hwnd, swMAXIMIZE)
			}
		})
	})
	w.Bind("folioClose", func() {
		w.Dispatch(func() { pPostMessageW.Call(hwnd, wmCLOSE, 0, 0) })
	})
	w.Bind("folioDrag", func() {
		w.Dispatch(func() {
			pReleaseCapture.Call()
			pSendMessageW.Call(hwnd, wmNCLBUTTONDOWN, htCAPTION, 0)
		})
	})
	w.Bind("folioResize", func(dir string) {
		w.Dispatch(func() {
			pReleaseCapture.Call()
			pSendMessageW.Call(hwnd, wmNCLBUTTONDOWN, htCode(dir), 0)
		})
	})
	w.Bind("folioFullscreen", func(on bool) {
		w.Dispatch(func() {
			if on {
				enterFullscreen(hwnd)
			} else {
				exitFullscreen(hwnd)
			}
		})
	})
	// Dialogo nativo de apertura, filtrado a Markdown.
	w.Bind("folioPick", func() {
		w.Dispatch(func() {
			if p := pickMarkdown(hwnd); p != "" {
				if b, err := json.Marshal(p); err == nil {
					w.Eval("window.__folioOpen(" + string(b) + ")")
				}
			}
		})
	})
	// Abrir un enlace externo en el navegador del sistema.
	w.Bind("folioOpenExternal", func(target string) {
		go shellOpen(target)
	})
	// Abrir un archivo local con la app por defecto del sistema.
	w.Bind("folioOpenPath", func(p string) {
		go shellOpen(p)
	})

	// mostrar/centrar la ventana recien cuando la pagina aviso que pinto (evita flash en blanco)
	var shownOnce sync.Once
	showWin := func() {
		shownOnce.Do(func() {
			splashDone = true // el contenido ya esta: dejamos de dibujar la marca nativa
			var rc rect
			pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
			ww, hh := rc.right-rc.left, rc.bottom-rc.top
			// MISMA posición con la que nació la ventana (cbtProc usó este mismo cálculo): este
			// SetWindowPos no la mueve, sólo la trae al frente y dispara el nudge del swapchain.
			cx, cy, maximized := targetWindowPos(ww, hh)
			after := uintptr(0)
			flags := uintptr(swpNOSIZE | swpNOZORDER | swpSHOWWINDOW)
			if os.Getenv("FOLIO_TOPMOST") != "" { // solo para captura/verificacion
				after = hwndTopmost
				flags = uintptr(swpNOSIZE | swpSHOWWINDOW)
			}
			pSetWindowPos.Call(hwnd, after, uintptr(cx), uintptr(cy), 0, 0, flags)
			pSetForegroundWindow.Call(hwnd)
			if maximized {
				pShowWindow.Call(hwnd, swMAXIMIZE) // ShowWindow ya dispara WM_SIZE (compone el swapchain)
			} else {
				// Nudge de tamaño: fuerza WM_SIZE -> WebView2 re-presenta su swapchain en la
				// ventana ya visible (sin esto el contenido se renderiza pero no se compone).
				pSetWindowPos.Call(hwnd, 0, 0, 0, uintptr(ww), uintptr(hh+1), uintptr(swpNOMOVE|swpNOZORDER))
				pSetWindowPos.Call(hwnd, 0, 0, 0, uintptr(ww), uintptr(hh), uintptr(swpNOMOVE|swpNOZORDER))
			}
		})
	}
	w.Bind("folioReady", func() { w.Dispatch(showWin) })
	time.AfterFunc(8*time.Second, func() { w.Dispatch(showWin) })

	rs, tocOpen, tocW := uiPrefs()
	initJS := "window.__FOLIO_HOST__=true;"
	initJS += fmt.Sprintf("window.__FOLIO_RSCALE__=%.3f;window.__FOLIO_TOC__=%t;window.__FOLIO_TOCW__=%d;", rs, tocOpen, tocW)
	if debugLog {
		initJS += "window.__FOLIO_DEBUG__=true;"
	}
	onShow = func(p string) {
		dlog("show", p)
		w.Dispatch(func() {
			if p != "" {
				broadcastOpen(p) // avisa al cliente por SSE qué documento mostrar (robusto)
			}
			bringToFront(hwnd)
		})
	}

	w.Init(initJS)
	dlog("navigating to", pageURL)
	w.Navigate(pageURL)
	dlog("entering run loop")
	w.Run()
}

// ----------------------------------------------------------------------
// Server HTTP local: UI embebida + render del Markdown + assets + recarga en vivo (SSE).
// ----------------------------------------------------------------------

func startServer(initialPath string) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}

	sub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		panic(err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(sub)))

	// ruta inicial (argumento de CLI) que la pagina consulta al cargar
	mux.HandleFunc("/api/initial", func(wr http.ResponseWriter, r *http.Request) {
		writeJSON(wr, map[string]string{"path": initialPath})
	})

	// preferencias persistentes (tamaño de letra, índice). Ver config.go. El tamaño de letra
	// se inyecta además en el initJS para no parpadear al abrir.
	mux.HandleFunc("/api/settings", func(wr http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var body struct {
				RScale   float64 `json:"rscale"`
				TocOpen  bool    `json:"tocOpen"`
				TocWidth int     `json:"tocWidth"`
			}
			if json.NewDecoder(r.Body).Decode(&body) == nil {
				setUIPrefs(body.RScale, body.TocOpen, body.TocWidth)
			}
			wr.WriteHeader(http.StatusNoContent)
			return
		}
		rs, tocOpen, tocW := uiPrefs()
		writeJSON(wr, map[string]any{"rscale": rs, "tocOpen": tocOpen, "tocWidth": tocW})
	})

	// instancia única: otra invocación de folio.exe nos manda acá el documento a mostrar.
	mux.HandleFunc("/api/show", func(wr http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("path")
		if p != "" {
			if abs, err := filepath.Abs(p); err == nil {
				p = abs
			}
		}
		if onShow != nil {
			onShow(p)
		}
		wr.WriteHeader(http.StatusOK)
	})

	// render: lee el .md y devuelve HTML + titulo + TOC + metadatos.
	mux.HandleFunc("/render", func(wr http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("path")
		if p == "" {
			writeJSON(wr, map[string]any{"ok": false, "error": "sin ruta"})
			return
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			wr.WriteHeader(http.StatusNotFound)
			writeJSON(wr, map[string]any{"ok": false, "error": "no se pudo abrir"})
			return
		}
		src, err := os.ReadFile(p)
		if err != nil {
			wr.WriteHeader(http.StatusInternalServerError)
			writeJSON(wr, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		res, err := Render(src, p)
		if err != nil {
			wr.WriteHeader(http.StatusInternalServerError)
			writeJSON(wr, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(wr, map[string]any{
			"ok":    true,
			"html":  res.HTML,
			"title": res.Title,
			"toc":   res.Toc,
			"words": res.Words,
			"path":  p,
			"dir":   filepath.Dir(p),
			"name":  filepath.Base(p),
			"mtime": fi.ModTime().UnixMilli(),
		})
	})

	// render-text: render de Markdown crudo (arrastrar-y-soltar, sin ruta en disco).
	mux.HandleFunc("/render-text", func(wr http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			wr.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		name := r.URL.Query().Get("name")
		if strings.TrimSpace(name) == "" {
			name = "documento.md"
		}
		src, err := io.ReadAll(http.MaxBytesReader(wr, r.Body, 32<<20))
		if err != nil {
			wr.WriteHeader(http.StatusBadRequest)
			writeJSON(wr, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		res, err := Render(src, name)
		if err != nil {
			wr.WriteHeader(http.StatusInternalServerError)
			writeJSON(wr, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(wr, map[string]any{
			"ok": true, "html": res.HTML, "title": res.Title, "toc": res.Toc,
			"words": res.Words, "path": "", "dir": "", "name": filepath.Base(name),
		})
	})

	// asset: sirve un archivo local referenciado por el documento (imagenes relativas, etc.).
	mux.HandleFunc("/asset", func(wr http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("path")
		if p == "" {
			wr.WriteHeader(http.StatusForbidden)
			return
		}
		f, err := os.Open(p)
		if err != nil {
			wr.WriteHeader(http.StatusNotFound)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || st.IsDir() {
			wr.WriteHeader(http.StatusNotFound)
			return
		}
		wr.Header().Set("Cache-Control", "max-age=3600")
		http.ServeContent(wr, r, filepath.Base(p), st.ModTime(), f)
	})

	// CSS de resaltado de codigo (chroma) generado del estilo Folio.
	mux.HandleFunc("/chroma.css", func(wr http.ResponseWriter, r *http.Request) {
		wr.Header().Set("Content-Type", "text/css; charset=utf-8")
		wr.Header().Set("Cache-Control", "max-age=86400")
		fmt.Fprint(wr, ChromaCSS())
	})

	// recarga en vivo: SSE que avisa cuando el .md cambia en disco.
	mux.HandleFunc("/events", func(wr http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		flusher, ok := wr.(http.Flusher)
		if !ok || path == "" {
			wr.WriteHeader(http.StatusInternalServerError)
			return
		}
		h := wr.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no")
		fmt.Fprint(wr, ": ok\n\n")
		flusher.Flush()

		last := time.Time{}
		if fi, err := os.Stat(path); err == nil {
			last = fi.ModTime()
		}
		ticker := time.NewTicker(400 * time.Millisecond)
		defer ticker.Stop()
		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fi, err := os.Stat(path)
				if err != nil {
					continue
				}
				if fi.ModTime().After(last) {
					last = fi.ModTime()
					fmt.Fprintf(wr, "data: reload\n\n")
					flusher.Flush()
				}
			}
		}
	})

	// canal "abrir documento": el cliente se suscribe una vez; el server le empuja la ruta del .md
	// a mostrar (handoff del daemon caliente). Sobrevive a ocultar/mostrar la ventana.
	mux.HandleFunc("/openevents", func(wr http.ResponseWriter, r *http.Request) {
		flusher, ok := wr.(http.Flusher)
		if !ok {
			wr.WriteHeader(http.StatusInternalServerError)
			return
		}
		h := wr.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no")
		ch := make(chan string, 4)
		openSubsMu.Lock()
		openSubs[ch] = struct{}{}
		pend := pendingOpen
		openSubsMu.Unlock()
		defer func() {
			openSubsMu.Lock()
			delete(openSubs, ch)
			openSubsMu.Unlock()
		}()
		fmt.Fprint(wr, ": ok\n\n")
		if pend != "" { // si reconectó tras ocultarse, recupera la última ruta pedida
			fmt.Fprintf(wr, "data: %s\n\n", pend)
		}
		flusher.Flush()
		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case p := <-ch:
				fmt.Fprintf(wr, "data: %s\n\n", p)
				flusher.Flush()
			}
		}
	})

	// canal de logs desde la pagina (window.onerror / pasos de arranque)
	mux.HandleFunc("/log", func(wr http.ResponseWriter, r *http.Request) {
		dlog("JS:", r.URL.Query().Get("m"))
		wr.WriteHeader(http.StatusNoContent)
	})

	var handler http.Handler = mux
	if debugLog {
		handler = http.HandlerFunc(func(wr http.ResponseWriter, r *http.Request) {
			dlog("HTTP", r.Method, r.URL.Path)
			mux.ServeHTTP(wr, r)
		})
	}
	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)
	return ln.Addr().String()
}

func writeJSON(wr http.ResponseWriter, v any) {
	wr.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(wr).Encode(v)
}

// broadcastOpen empuja una ruta a todos los clientes SSE suscritos a /openevents.
func broadcastOpen(path string) {
	openSubsMu.Lock()
	defer openSubsMu.Unlock()
	pendingOpen = path
	for ch := range openSubs {
		select {
		case ch <- path:
		default:
		}
	}
}
