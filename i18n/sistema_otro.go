//go:build !windows

package i18n

// Fuera de Windows el idioma lo dice el entorno POSIX completo, LANG incluida.
var localeVars = []string{"LC_ALL", "LC_MESSAGES", "LANG"}

// osLanguage no tiene a quién preguntarle más allá del entorno: lo resuelven las variables.
func osLanguage() string { return "" }
