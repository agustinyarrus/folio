//go:build windows

package i18n

// En Windows LANG no cuenta: Git Bash la completa sola con el idioma de la interfaz (ver el
// comentario del paquete). LC_ALL y LC_MESSAGES sí, porque nadie las escribe sin querer.
// La interfaz de Windows ya no se consulta: el idioma por defecto es el inglés.
var localeVars = []string{"LC_ALL", "LC_MESSAGES"}
