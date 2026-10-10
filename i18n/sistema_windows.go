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
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procGetUserPreferredUILangs  = kernel32.NewProc("GetUserPreferredUILanguages")
	procGetUserDefaultLocaleName = kernel32.NewProc("GetUserDefaultLocaleName")
)

const (
	muiLanguageName     = 0x8 // MUI_LANGUAGE_NAME: etiquetas "en-US", no LANGID
	localeNameMaxLength = 85  // LOCALE_NAME_MAX_LENGTH
)

// osLanguages devuelve el idioma de la interfaz de Windows (el primero de la lista del usuario) y
// el de su formato regional, como etiquetas BCP 47 ("en-US", "es-419"). Lo que falle queda vacío.
func osLanguages() (ui, format string) {
	if procGetUserPreferredUILangs.Find() == nil {
		var num, size uint32
		r, _, _ := procGetUserPreferredUILangs.Call(muiLanguageName,
			uintptr(unsafe.Pointer(&num)), 0, uintptr(unsafe.Pointer(&size)))
		if r != 0 && size > 0 {
			buf := make([]uint16, size)
			r, _, _ = procGetUserPreferredUILangs.Call(muiLanguageName,
				uintptr(unsafe.Pointer(&num)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
			if r != 0 {
				ui = syscall.UTF16ToString(buf) // multi-cadena: corta en el primer NUL = el preferido
			}
		}
	}
	if procGetUserDefaultLocaleName.Find() == nil {
		buf := make([]uint16, localeNameMaxLength)
		n, _, _ := procGetUserDefaultLocaleName.Call(uintptr(unsafe.Pointer(&buf[0])), localeNameMaxLength)
		if n > 0 {
			format = syscall.UTF16ToString(buf)
		}
	}
	return ui, format
}
