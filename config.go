package main

// config.go — preferencias persistentes en %AppData%\Folio\config.json.
//
// Por qué server-side y no localStorage: el server escucha en un puerto EFÍMERO distinto cada
// arranque (127.0.0.1:0), y localStorage está particionado por origen (incluye el puerto), así que
// cada apertura sería un origen nuevo y se perdería todo. Guardando acá sobrevive a los reinicios:
//   - rscale  : tamaño de letra de lectura (Ctrl +/-)
//   - tocOpen : índice visible o no
//   - window  : geometría de la ventana (se guarda al cerrar, en WM_CLOSE)

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type winGeom struct {
	X   int32 `json:"x"`
	Y   int32 `json:"y"`
	W   int32 `json:"w"`
	H   int32 `json:"h"`
	Max bool  `json:"max"`
}

type appConfig struct {
	RScale   float64  `json:"rscale"`
	TocOpen  *bool    `json:"tocOpen"`
	TocWidth int      `json:"tocWidth"` // ancho del índice en px (0 = default)
	Window   *winGeom `json:"window"`
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

var (
	gCfg   appConfig
	gCfgMu sync.Mutex
)

func configPath() string {
	d, err := os.UserConfigDir()
	if err != nil || d == "" {
		if d, err = os.UserCacheDir(); err != nil {
			d = os.TempDir()
		}
	}
	return filepath.Join(d, "Folio", "config.json")
}

func loadConfig() {
	gCfgMu.Lock()
	defer gCfgMu.Unlock()
	gCfg = appConfig{RScale: 1.0}
	b, err := os.ReadFile(configPath())
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &gCfg)
	if gCfg.RScale <= 0 {
		gCfg.RScale = 1.0
	}
}

// saveConfigLocked escribe el config a disco. El llamador debe tener gCfgMu tomado.
func saveConfigLocked() {
	p := configPath()
	os.MkdirAll(filepath.Dir(p), 0o755)
	b, err := json.MarshalIndent(gCfg, "", "  ")
	if err != nil {
		return
	}
	tmp := p + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, p) // reemplazo atómico
	}
}

func setUIPrefs(rscale float64, tocOpen bool, tocWidth int) {
	gCfgMu.Lock()
	defer gCfgMu.Unlock()
	if rscale > 0 {
		gCfg.RScale = rscale
	}
	t := tocOpen
	gCfg.TocOpen = &t
	if tocWidth > 0 {
		gCfg.TocWidth = clampInt(tocWidth, 120, 1200)
	}
	saveConfigLocked()
}

func saveWindowGeom(g winGeom) {
	if g.W < 200 || g.H < 150 {
		return // tamaño absurdo (minimizado/transitorio): no guardar
	}
	gCfgMu.Lock()
	defer gCfgMu.Unlock()
	gCfg.Window = &g
	saveConfigLocked()
}

func uiPrefs() (rscale float64, tocOpen bool, tocWidth int) {
	gCfgMu.Lock()
	defer gCfgMu.Unlock()
	rscale = gCfg.RScale
	tocOpen = true
	if gCfg.TocOpen != nil {
		tocOpen = *gCfg.TocOpen
	}
	tocWidth = gCfg.TocWidth
	if tocWidth <= 0 {
		tocWidth = 268
	}
	return
}
