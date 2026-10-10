//go:build !windows

package i18n

// Fuera de Windows el idioma lo dice el entorno POSIX completo, LANG incluida.
var localeVars = []string{"LC_ALL", "LC_MESSAGES", "LANG"}

// osLanguages no tiene a quién preguntarle más allá del entorno: lo resuelven las variables.
func osLanguages() (ui, format string) { return "", "" }
