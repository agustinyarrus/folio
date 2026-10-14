//go:build !windows

package i18n

// Fuera de Windows el idioma lo dice el entorno POSIX completo, LANG incluida.
var localeVars = []string{"LC_ALL", "LC_MESSAGES", "LANG"}
