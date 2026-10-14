// Package i18n: castellano e inglés para la app.
//
// El castellano es el idioma FUENTE. El código se escribe en castellano y esas mismas frases son
// las claves del catálogo inglés (en.json, estilo gettext): el código se sigue leyendo igual que
// antes, cada llamada cambia lo mínimo y una frase nueva sin traducir no rompe nada (sale en
// castellano, y la prueba de cobertura la marca antes de publicar).
//
// Qué idioma se usa, en este orden (el primero que diga algo gana):
//
//  1. --lang en la línea de comandos                   Choice.Flag
//  2. lo que el usuario dejó elegido en la app         Choice.Setting
//  3. la variable de la app, p. ej. FOLIO_LANG         Choice.AppEnv
//  4. LC_ALL / LC_MESSAGES (y LANG, salvo en Windows)
//  5. automático: el idioma de la INTERFAZ de Windows. Si es castellano, castellano; si no, inglés.
//
// Automático (pedido del user, 14-oct-2026): si nadie eligió nada, la app se entera del idioma de
// Windows y lo sigue. El formato regional NO cuenta: una PC con Windows en inglés abre en inglés
// aunque tenga formato es-419. El inglés va primero en los selectores y en la ayuda; el castellano,
// segundo. LANG no cuenta en Windows: Git Bash la completa sola con el idioma de la interfaz, y nadie
// la eligió.
//
// Este archivo es idéntico en todas las apps; lo propio de cada una es su en.json.
package i18n

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
)

// Lang es un idioma soportado. El cero es ES: el idioma fuente es también el valor por omisión de
// cualquier estructura que guarde un Lang.
type Lang uint32

const (
	ES Lang = iota // castellano
	EN             // inglés
)

// All lista los idiomas en el orden en que se ofrecen en un selector: el inglés primero (pedido del user).
var All = [...]Lang{EN, ES}

// String es el código ISO 639-1: "es" o "en". Es lo que se guarda en los ajustes.
func (l Lang) String() string {
	if l == EN {
		return "en"
	}
	return "es"
}

// Name es el nombre del idioma en su propio idioma: lo que muestra un selector.
func (l Lang) Name() string {
	if l == EN {
		return "English"
	}
	return "Español"
}

// Tag es la etiqueta BCP 47 con la que se formatean números y fechas. Castellano rioplatense:
// punto de miles y coma decimal (12.345,6), como siempre mostraron estas apps.
func (l Lang) Tag() string {
	if l == EN {
		return "en-US"
	}
	return "es-AR"
}

// Source dice de dónde salió el idioma (para un diagnóstico, --version o --lang sin valor).
type Source uint8

const (
	FromDefault   Source = iota // nadie eligió y Windows no dijo nada: inglés
	FromOS                      // la interfaz de Windows: castellano si es castellano, si no inglés
	FromLocaleEnv               // LC_ALL / LC_MESSAGES (y LANG fuera de Windows)
	FromAppEnv                  // la variable propia de la app
	FromSetting                 // lo que el usuario dejó guardado en la app
	FromFlag                    // --lang
)

var sourceNames = [...]string{
	FromDefault: "default", FromOS: "os", FromLocaleEnv: "locale-env",
	FromAppEnv: "app-env", FromSetting: "setting", FromFlag: "flag",
}

func (s Source) String() string {
	if int(s) < len(sourceNames) {
		return sourceNames[s]
	}
	return "source(" + strconv.Itoa(int(s)) + ")"
}

// Choice junta lo que la app sabe de la elección EXPLÍCITA del usuario. Un campo vacío o "auto" no
// elige nada y deja decidir al siguiente.
type Choice struct {
	Flag    string // valor de --lang
	Setting string // ajuste guardado: "auto", "es" o "en"
	AppEnv  string // NOMBRE de la variable de la app, p. ej. "FOLIO_LANG"
}

// Resolve aplica el orden completo, deja el resultado como idioma actual y lo devuelve junto con
// su origen.
func Resolve(c Choice) (Lang, Source) {
	l, s := resolve(c, os.LookupEnv, osLanguage)
	Set(l)
	return l, s
}

// resolve es la decisión pura. El entorno y la interfaz de Windows llegan como funciones para poder
// probar cada rama sin depender de la PC de quien corre las pruebas.
func resolve(c Choice, env func(string) (string, bool), sys func() string) (Lang, Source) {
	if l, ok := Parse(c.Flag); ok {
		return l, FromFlag
	}
	if l, ok := Parse(c.Setting); ok {
		return l, FromSetting
	}
	if c.AppEnv != "" {
		if v, ok := env(c.AppEnv); ok {
			if l, ok := Parse(v); ok {
				return l, FromAppEnv
			}
		}
	}
	// POSIX: manda la PRIMERA variable con valor, aunque nombre un idioma que no tenemos. "C" y
	// "POSIX" piden la salida sin localizar de toda la vida, que para quien las escribe es inglés;
	// cualquier idioma que no sea castellano también cae en inglés, el más cercano a su intención.
	for _, k := range localeVars {
		v, ok := env(k)
		if !ok || strings.TrimSpace(v) == "" {
			continue
		}
		if l, ok := Parse(v); ok {
			return l, FromLocaleEnv
		}
		return EN, FromLocaleEnv
	}
	// Automático: la interfaz de Windows. Castellano -> castellano; cualquier otro idioma -> inglés.
	if ui := sys(); ui != "" {
		if isSpanish(ui) {
			return ES, FromOS
		}
		return EN, FromOS
	}
	// Nadie eligió y Windows no dijo nada: inglés, el idioma por defecto.
	return EN, FromDefault
}

// isSpanish dice si una etiqueta de idioma es castellano ("es-ES", "es-419", "español"...).
func isSpanish(tag string) bool {
	l, ok := Parse(tag)
	return ok && l == ES
}

// Parse entiende códigos y nombres: "es", "ES", "es-AR", "es_419.UTF-8", "spa", "español",
// "castellano", "spanish"; "en", "en-GB", "en_US.utf8@x", "eng", "english", "inglés". El vacío y
// "auto" no eligen nada (ok=false), igual que cualquier idioma que no sea uno de los dos.
func Parse(s string) (Lang, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.IndexAny(s, "-_.@"); i >= 0 { // es_AR.UTF-8@euro -> es
		s = s[:i]
	}
	switch s {
	case "es", "spa", "español", "espanol", "castellano", "spanish":
		return ES, true
	case "en", "eng", "english", "inglés", "ingles":
		return EN, true
	}
	return ES, false
}

// ---- idioma actual -----------------------------------------------------------------------------

// current arranca en ES (el cero) hasta que la app resuelva: lo que se imprima antes sale en el
// idioma fuente, nunca en un idioma a medio decidir.
var current atomic.Uint32

// Current es el idioma en uso.
func Current() Lang { return Lang(current.Load()) }

// Set cambia el idioma en uso (una app con ventana lo llama cuando el usuario elige otro).
func Set(l Lang) {
	if l != EN {
		l = ES
	}
	current.Store(uint32(l))
}

// ---- catálogo ----------------------------------------------------------------------------------

//go:embed en.json
var enJSON []byte

// en: castellano -> inglés. Se arma una sola vez, al iniciar el paquete.
var en = mustCatalog(enJSON)

func mustCatalog(b []byte) map[string]string {
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		// El catálogo viaja embebido en el binario: un JSON roto es un error de compilación
		// diferido, y conviene que explote en la primera prueba y no en la máquina de nadie.
		panic("i18n: en.json no es un objeto de textos: " + err.Error())
	}
	return m
}

// CatalogJSON devuelve el catálogo inglés tal como viaja en el binario: es lo que una app con UI
// web le inyecta a su página (window.__I18N_EN__), así hay un solo catálogo por app.
func CatalogJSON() []byte { return enJSON }

// T traduce al idioma actual una frase escrita en castellano. Si faltara en el catálogo devuelve
// el castellano: mejor una frase en el otro idioma que un hueco (la prueba de cobertura lo evita).
func T(es string) string { return In(Current(), es) }

// In traduce a un idioma dado, sin mirar el actual.
func In(l Lang, es string) string {
	if l == ES {
		return es
	}
	if s, ok := en[es]; ok {
		return s
	}
	return es
}

// F es T + fmt.Sprintf. Se traduce el FORMATO entero, así cada idioma ordena la frase a su modo.
func F(es string, args ...any) string { return fmt.Sprintf(T(es), args...) }

// N elige singular o plural según n y lo traduce, sin escribir el número: N(3, "línea", "líneas")
// da "líneas" o "lines". Castellano e inglés comparten la regla (sólo el 1 va en singular).
func N(n int, one, many string) string {
	if n == 1 {
		return T(one)
	}
	return T(many)
}

// Nf es N + fmt.Sprintf: Nf(n, "%d línea", "%d líneas", n).
func Nf(n int, one, many string, args ...any) string { return fmt.Sprintf(N(n, one, many), args...) }

// M marca una frase para el catálogo SIN traducirla: para tablas que se arman al iniciar y se
// traducen recién al mostrarse, con T(fila.Nombre). Devuelve la frase tal cual.
func M(es string) string { return es }

// Errorf es fmt.Errorf con el formato traducido: conserva %w (errors.Is / errors.As siguen andando).
func Errorf(es string, args ...any) error { return fmt.Errorf(T(es), args...) }

// Error es un error cuyo texto se traduce al MOSTRARLO, no al crearlo. Es para los errores
// centinela (var ErrX = i18n.Error("…")): nacen al iniciar el paquete, antes de que la app
// resuelva el idioma, y con errors.New quedarían para siempre en castellano. Como es un string,
// errors.Is compara por valor y sigue funcionando.
type Error string

func (e Error) Error() string { return T(string(e)) }

// ---- números -----------------------------------------------------------------------------------

// Int escribe un entero con el separador de miles del idioma: 12.345 / 12,345.
func Int(n int64) string { return group(strconv.FormatInt(n, 10), Current()) }

// Dec escribe un decimal con prec cifras y la coma o el punto del idioma: 51,8 / 51.8.
func Dec(f float64, prec int) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	s := strconv.FormatFloat(f, 'f', prec, 64)
	l := Current()
	ip, fp, hasFrac := strings.Cut(s, ".")
	out := group(ip, l)
	if hasFrac {
		if l == EN {
			out += "." + fp
		} else {
			out += "," + fp
		}
	}
	return out
}

// group agrega separadores de miles a una tira de dígitos con signo opcional. O(n).
func group(digits string, l Lang) string {
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	if len(digits) <= 3 {
		return sign + digits
	}
	sep := byte('.')
	if l == EN {
		sep = ','
	}
	var b strings.Builder
	b.Grow(len(sign) + len(digits) + len(digits)/3)
	b.WriteString(sign)
	head := len(digits) % 3
	if head == 0 {
		head = 3
	}
	b.WriteString(digits[:head])
	for i := head; i < len(digits); i += 3 {
		b.WriteByte(sep)
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}
