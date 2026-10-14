//go:build windows

package i18n

import (
	"syscall"
	"unsafe"
)

// En Windows LANG no cuenta: Git Bash la completa sola con el idioma de la interfaz (ver el
// comentario del paquete). LC_ALL y LC_MESSAGES sí, porque nadie las escribe sin querer.
var localeVars = []string{"LC_ALL", "LC_MESSAGES"}

var (
	kernel32                    = syscall.NewLazyDLL("kernel32.dll")
	procGetUserPreferredUILangs = kernel32.NewProc("GetUserPreferredUILanguages")
)

// muiLanguageName pide etiquetas "en-US" (MUI_LANGUAGE_NAME), no LANGID.
const muiLanguageName = 0x8

// osLanguage devuelve el idioma de la INTERFAZ de Windows: el primero de la lista de preferencias
// del usuario, como etiqueta BCP 47 ("es-ES", "en-US"). Si la consulta falla, devuelve "" y la
// decisión cae al inglés. El formato regional no se consulta a propósito (ver el comentario del paquete).
func osLanguage() string {
	if procGetUserPreferredUILangs.Find() != nil {
		return ""
	}
	var num, size uint32
	r, _, _ := procGetUserPreferredUILangs.Call(muiLanguageName,
		uintptr(unsafe.Pointer(&num)), 0, uintptr(unsafe.Pointer(&size)))
	if r == 0 || size == 0 {
		return ""
	}
	buf := make([]uint16, size)
	r, _, _ = procGetUserPreferredUILangs.Call(muiLanguageName,
		uintptr(unsafe.Pointer(&num)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 {
		return ""
	}
	// La lista viene como cadenas separadas por NUL: UTF16ToString corta en el primer NUL, o sea la preferida.
	return syscall.UTF16ToString(buf)
}
