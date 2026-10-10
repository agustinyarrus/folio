package i18n

// cobertura_test.go — la prueba que no deja publicar una frase sin traducir.
//
// Recorre el módulo entero (el Go, y el JS y el HTML de la UI si la hay), junta cada frase marcada
// para traducir y la cruza con en.json:
//
//   - toda frase marcada tiene su traducción             (faltantes)
//   - toda traducción corresponde a una frase en uso     (sobrantes: el catálogo no junta polvo)
//   - los huecos coinciden: los %s %d de Go en el mismo orden, las {llaves} de JS en igual cantidad
//   - los blancos de los bordes se respetan              ("Abrir: " -> "Open: ")
//   - en.json no repite claves                           (json.Unmarshal se quedaría con la última)
//
// Marcas reconocidas:
//
//	Go    i18n.T("…")  i18n.F("…", …)  i18n.N(n, "…", "…")  i18n.Nf(n, "…", "…", …)
//	      i18n.M("…")  i18n.In(l, "…")  i18n.Errorf("…", …)  i18n.Error("…")
//	JS    t('…')  t("…", {…})  tn(n, '…', '…')  tm('…')     (también `…` sin ${})
//	HTML  un elemento con data-i18n: sus textos DIRECTOS y sus atributos title, placeholder,
//	      aria-label y alt; con data-i18n="attr", sólo los atributos
//
// Una llamada con algo que no es un literal (T(fila.Nombre)) no se puede verificar acá: la frase
// tiene que estar marcada donde nace, con i18n.M / tm. Este archivo es idéntico en todas las apps.

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// marca es una frase que el código pide traducir, con el primer lugar donde aparece.
type marca struct {
	texto, donde string
}

type marcas struct {
	porTexto  map[string]string // frase -> primer archivo:línea
	formato   map[string]bool   // frase -> se usa como FORMATO (F, Nf, Errorf): sus %d cuentan
	porTipo   map[string]int    // "go"/"js"/"html" -> cantidad de marcas
	dinamicas []string          // llamadas que no se pueden verificar (no son literales)
}

func (m *marcas) agregar(tipo, texto, archivo string, linea int) {
	if _, ok := m.porTexto[texto]; !ok {
		m.porTexto[texto] = archivo + ":" + strconv.Itoa(linea)
	}
	m.porTipo[tipo]++
}

// esFormato anota que la frase pasa por fmt: ahí un % es un verbo y tiene que coincidir.
func (m *marcas) esFormato(texto string) { m.formato[texto] = true }

func TestCobertura(t *testing.T) {
	raiz := raizDelModulo(t)
	m := juntarMarcas(t, raiz)
	verificarClavesUnicas(t)

	faltan := []marca{}
	for texto, donde := range m.porTexto {
		if _, ok := en[texto]; !ok {
			faltan = append(faltan, marca{texto, donde})
		}
	}
	sort.Slice(faltan, func(i, j int) bool { return faltan[i].donde < faltan[j].donde })
	for _, f := range faltan {
		t.Errorf("falta traducir %q  (%s)", f.texto, f.donde)
	}

	claves := make([]string, 0, len(en))
	for k := range en {
		claves = append(claves, k)
	}
	sort.Strings(claves)
	for _, es := range claves {
		ing := en[es]
		if _, ok := m.porTexto[es]; !ok {
			t.Errorf("sobra en en.json (nadie la usa): %q", es)
		}
		for _, p := range problemasDePar(es, ing, m.formato[es]) {
			t.Errorf("%q -> %q: %s", es, ing, p)
		}
	}
	t.Logf("%d frases marcadas (go %d · js %d · html %d) · %d traducciones · %d llamadas dinámicas",
		len(m.porTexto), m.porTipo["go"], m.porTipo["js"], m.porTipo["html"], len(en), len(m.dinamicas))
	if testing.Verbose() {
		for _, d := range m.dinamicas {
			t.Logf("  dinámica: %s", d)
		}
	}
}

// problemasDePar compara una frase con su traducción: huecos de formato (sólo si se formatea),
// llaves y blancos de borde.
func problemasDePar(es, ing string, formato bool) []string {
	var p []string
	if strings.TrimSpace(ing) == "" {
		return append(p, "traducción vacía")
	}
	if lead(es) != lead(ing) || trail(es) != trail(ing) {
		p = append(p, "los blancos del principio o del final no coinciden")
	}
	if a, b := verbos(es), verbos(ing); formato && a != b {
		p = append(p, "los huecos de formato no coinciden: "+strconv.Quote(a)+" vs "+strconv.Quote(b))
	}
	if a, b := llaves(es), llaves(ing); a != b {
		p = append(p, "las {llaves} no coinciden: "+strconv.Quote(a)+" vs "+strconv.Quote(b))
	}
	return p
}

func lead(s string) string  { return s[:len(s)-len(strings.TrimLeftFunc(s, unicode.IsSpace))] }
func trail(s string) string { return s[len(strings.TrimRightFunc(s, unicode.IsSpace)):] }

// verbos de fmt en orden (sin flags ni ancho): "No se pudo abrir %q: %v" -> "q v". %% no cuenta.
// Sólo los verbos que existen en fmt: "30 % menos" no tiene ninguno.
var reVerbo = regexp.MustCompile(`%(?:\[\d+\])?[-+# 0]*(?:\d+|\*)?(?:\.(?:\d+|\*))?([bcdeEfFgGoOpqstTUvxXw%])`)

func verbos(s string) string {
	var out []string
	for _, m := range reVerbo.FindAllStringSubmatch(s, -1) {
		if m[1] != "%" {
			idx := ""
			if i := strings.Index(m[0], "["); i >= 0 { // %[2]s: el índice es parte del contrato
				idx = m[0][i : strings.Index(m[0], "]")+1]
			}
			out = append(out, idx+m[1])
		}
	}
	return strings.Join(out, " ")
}

// llaves de interpolación de la UI ({n}, {nombre}) como multiconjunto: el orden puede cambiar
// entre idiomas ("{n} de {total}" / "{total}'s {n}"), la cantidad de cada una no.
var reLlave = regexp.MustCompile(`\{[A-Za-z_][A-Za-z0-9_]*\}|\{\d+\}`)

func llaves(s string) string {
	l := reLlave.FindAllString(s, -1)
	sort.Strings(l)
	return strings.Join(l, " ")
}

// verificarClavesUnicas lee en.json token por token: Unmarshal se quedaría callado con la última.
func verificarClavesUnicas(t *testing.T) {
	dec := json.NewDecoder(bytes.NewReader(enJSON))
	if _, err := dec.Token(); err != nil {
		t.Fatalf("en.json: %v", err)
	}
	vistas := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("en.json: %v", err)
		}
		k, _ := tok.(string)
		var v any
		if err := dec.Decode(&v); err != nil {
			t.Fatalf("en.json, valor de %q: %v", k, err)
		}
		if vistas[k] {
			t.Errorf("en.json repite la clave %q", k)
		}
		vistas[k] = true
	}
}

// ---- recorrer el módulo --------------------------------------------------------------------------

func raizDelModulo(t *testing.T) string {
	d, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		p := filepath.Dir(d)
		if p == d {
			t.Fatal("no encontré go.mod subiendo desde el paquete i18n")
		}
		d = p
	}
}

// carpetas que nunca son código propio de la app: dependencias, compilados, pruebas, scratch.
var saltear = map[string]bool{
	"vendor": true, "node_modules": true, "testdata": true, "dist": true, "build": true, "bin": true, "obj": true,
}

func juntarMarcas(t *testing.T, raiz string) *marcas {
	m := &marcas{porTexto: map[string]string{}, formato: map[string]bool{}, porTipo: map[string]int{}}
	err := filepath.WalkDir(raiz, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		nombre := d.Name()
		if d.IsDir() {
			if p != raiz && (saltear[strings.ToLower(nombre)] || strings.HasPrefix(nombre, ".") || strings.HasPrefix(nombre, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(raiz, p)
		rel = filepath.ToSlash(rel)
		low := strings.ToLower(nombre)
		switch {
		case strings.HasSuffix(low, ".go") && !strings.HasSuffix(low, "_test.go"):
			src, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			marcasGo(t, m, rel, src)
		case (strings.HasSuffix(low, ".js") || strings.HasSuffix(low, ".mjs") || strings.HasSuffix(low, ".cjs")) &&
			!strings.HasSuffix(low, ".min.js"):
			src, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			marcasJS(m, rel, string(src), 1)
		case strings.HasSuffix(low, ".html") || strings.HasSuffix(low, ".htm"):
			src, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			marcasHTML(m, rel, string(src))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// ---- Go ------------------------------------------------------------------------------------------

// argumentos que llevan frases, por función del paquete.
// funciones que pasan la frase por fmt (F, Nf, Errorf): en esas, un % es un verbo
var formatea = map[string]bool{"F": true, "Nf": true, "Errorf": true}

var argsGo = map[string][]int{"T": {0}, "F": {0}, "M": {0}, "Errorf": {0}, "Error": {0}, "In": {1}, "N": {1, 2}, "Nf": {1, 2}}

func marcasGo(t *testing.T, m *marcas, rel string, src []byte) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
	if err != nil {
		t.Errorf("no pude leer %s: %v", rel, err)
		return
	}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "i18n" {
			return true
		}
		for _, i := range argsGo[sel.Sel.Name] {
			if i >= len(call.Args) {
				continue
			}
			pos := fset.Position(call.Args[i].Pos())
			if s, ok := literalGo(call.Args[i]); ok {
				m.agregar("go", s, rel, pos.Line)
				if formatea[sel.Sel.Name] {
					m.esFormato(s)
				}
			} else {
				m.dinamicas = append(m.dinamicas, rel+":"+strconv.Itoa(pos.Line))
			}
		}
		return true
	})
}

// literalGo resuelve un literal de cadena, también partido con + en varias líneas.
func literalGo(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			s, err := strconv.Unquote(x.Value)
			return s, err == nil
		}
	case *ast.ParenExpr:
		return literalGo(x.X)
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			a, ok1 := literalGo(x.X)
			b, ok2 := literalGo(x.Y)
			return a + b, ok1 && ok2
		}
	}
	return "", false
}

// ---- JS ------------------------------------------------------------------------------------------

// tokJS: 'i' identificador · 's' cadena sin huecos (val = valor ya decodificado) · 'p' puntuación
// (val = el carácter) · 'x' cualquier otra cosa (número, regex, plantilla con ${}).
type tokJS struct {
	tipo  byte
	val   string
	linea int
}

// Después de estas palabras una / abre una regex, no divide.
var antesDeRegex = map[string]bool{
	"return": true, "typeof": true, "instanceof": true, "in": true, "of": true, "new": true, "delete": true,
	"void": true, "throw": true, "case": true, "do": true, "else": true, "yield": true, "await": true,
}

// lexJS parte el código en tokens lo justo para encontrar llamadas: cadenas con sus escapes,
// plantillas (con sus ${} lexeados adentro), comentarios y regex afuera. O(n).
func lexJS(src string, linea0 int) []tokJS {
	var toks []tokJS
	linea := linea0
	var llavesPlantilla []int // por cada ${ abierto, cuántas { normales hay adentro todavía
	i := 0
	n := len(src)
	regexPermitida := func() bool {
		if len(toks) == 0 {
			return true
		}
		p := toks[len(toks)-1]
		switch p.tipo {
		case 'p':
			return p.val != ")" && p.val != "]" && p.val != "}"
		case 'i':
			return antesDeRegex[p.val]
		}
		return false
	}
	// leerPlantilla lee una plantilla desde justo después de su ` (continuacion=false) o desde
	// justo después del } que cierra uno de sus ${…} (continuacion=true). Una plantilla sin huecos
	// es un literal ('s'); con huecos, un solo 'x' al principio, y el código de cada hueco se lexea
	// como cualquier otro (adentro puede haber un t('…')).
	var leerPlantilla func(continuacion bool)
	leerPlantilla = func(continuacion bool) {
		ini := linea
		var b strings.Builder
		for i < n {
			c := src[i]
			switch {
			case c == '\\' && i+1 < n:
				r, ancho := escapeJS(src[i:])
				b.WriteString(r)
				linea += strings.Count(src[i:i+ancho], "\n")
				i += ancho
			case c == '`':
				i++
				if !continuacion {
					toks = append(toks, tokJS{'s', b.String(), ini})
				}
				return
			case c == '$' && i+1 < n && src[i+1] == '{':
				i += 2
				if !continuacion {
					toks = append(toks, tokJS{'x', "", ini}) // la plantilla entera no es un literal
				}
				llavesPlantilla = append(llavesPlantilla, 0)
				return // sigue el código del hueco; el } que lo cierra vuelve acá
			default:
				if c == '\n' {
					linea++
				}
				b.WriteByte(c)
				i++
			}
		}
	}
	for i < n {
		c := src[i]
		switch {
		case c == '\n':
			linea++
			i++
		case c == ' ' || c == '\t' || c == '\r' || c == '\f' || c == '\v':
			i++
		case c == '/' && i+1 < n && src[i+1] == '/':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			fin := strings.Index(src[i+2:], "*/")
			if fin < 0 {
				fin = n - i - 2
			}
			linea += strings.Count(src[i:i+2+fin], "\n")
			i += fin + 4
		case c == '"' || c == '\'':
			ini := linea
			i++
			var b strings.Builder
			for i < n && src[i] != c && src[i] != '\n' {
				if src[i] == '\\' && i+1 < n {
					r, ancho := escapeJS(src[i:])
					b.WriteString(r)
					linea += strings.Count(src[i:i+ancho], "\n")
					i += ancho
					continue
				}
				b.WriteByte(src[i])
				i++
			}
			i++ // la comilla de cierre
			toks = append(toks, tokJS{'s', b.String(), ini})
		case c == '`':
			i++
			leerPlantilla(false)
		case c == '}' && len(llavesPlantilla) > 0 && llavesPlantilla[len(llavesPlantilla)-1] == 0:
			llavesPlantilla = llavesPlantilla[:len(llavesPlantilla)-1]
			i++
			leerPlantilla(true)
		case c == '/' && regexPermitida():
			ini := linea
			i++
			enClase := false
			for i < n && src[i] != '\n' {
				if src[i] == '\\' {
					i += 2
					continue
				}
				if src[i] == '[' {
					enClase = true
				} else if src[i] == ']' {
					enClase = false
				} else if src[i] == '/' && !enClase {
					break
				}
				i++
			}
			i++
			for i < n && (src[i] >= 'a' && src[i] <= 'z') {
				i++
			}
			toks = append(toks, tokJS{'x', "", ini})
		case esInicioIdent(src[i:]):
			j := i
			for j < n {
				r, w := utf8.DecodeRuneInString(src[j:])
				if !(r == '_' || r == '$' || r == '#' || unicode.IsLetter(r) || unicode.IsDigit(r)) {
					break
				}
				j += w
			}
			toks = append(toks, tokJS{'i', src[i:j], linea})
			i = j
		case c >= '0' && c <= '9':
			j := i
			for j < n && (src[j] == '.' || src[j] == '_' || (src[j] >= '0' && src[j] <= '9') ||
				(src[j] >= 'a' && src[j] <= 'z') || (src[j] >= 'A' && src[j] <= 'Z')) {
				j++
			}
			toks = append(toks, tokJS{'x', "", linea})
			i = j
		default:
			if len(llavesPlantilla) > 0 {
				if c == '{' {
					llavesPlantilla[len(llavesPlantilla)-1]++
				} else if c == '}' {
					llavesPlantilla[len(llavesPlantilla)-1]--
				}
			}
			_, w := utf8.DecodeRuneInString(src[i:])
			toks = append(toks, tokJS{'p', src[i : i+w], linea})
			i += w
		}
	}
	return toks
}

func esInicioIdent(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return r == '_' || r == '$' || r == '#' || unicode.IsLetter(r)
}

// escapeJS decodifica la secuencia de escape al principio de s (que empieza con \) y dice cuántos
// bytes ocupó.
func escapeJS(s string) (string, int) {
	if len(s) < 2 {
		return "", len(s)
	}
	switch s[1] {
	case 'n':
		return "\n", 2
	case 't':
		return "\t", 2
	case 'r':
		return "\r", 2
	case 'b':
		return "\b", 2
	case 'f':
		return "\f", 2
	case 'v':
		return "\v", 2
	case '0':
		return "\x00", 2
	case '\n':
		return "", 2 // continuación de línea
	case '\r':
		if len(s) > 2 && s[2] == '\n' {
			return "", 3
		}
		return "", 2
	case 'x':
		if len(s) >= 4 {
			if v, err := strconv.ParseUint(s[2:4], 16, 32); err == nil {
				return string(rune(v)), 4
			}
		}
	case 'u':
		if len(s) >= 3 && s[2] == '{' {
			if fin := strings.IndexByte(s, '}'); fin > 3 {
				if v, err := strconv.ParseUint(s[3:fin], 16, 32); err == nil {
					return string(rune(v)), fin + 1
				}
			}
		} else if len(s) >= 6 {
			if v, err := strconv.ParseUint(s[2:6], 16, 32); err == nil {
				return string(rune(v)), 6
			}
		}
	}
	r, w := utf8.DecodeRuneInString(s[1:])
	return string(r), 1 + w
}

// marcasJS busca t('…'), tn(n, '…', '…') y tm('…') en el código.
func marcasJS(m *marcas, rel, src string, linea0 int) {
	toks := lexJS(src, linea0)
	for i := 0; i+1 < len(toks); i++ {
		tk := toks[i]
		if tk.tipo != 'i' || (tk.val != "t" && tk.val != "tn" && tk.val != "tm") {
			continue
		}
		if toks[i+1].tipo != 'p' || toks[i+1].val != "(" {
			continue
		}
		if i > 0 && toks[i-1].tipo == 'i' && toks[i-1].val == "function" {
			continue // la definición, no una llamada
		}
		args := argumentosJS(toks, i+2)
		idx := []int{0}
		if tk.val == "tn" {
			idx = []int{1, 2}
		}
		for _, k := range idx {
			if k >= len(args) {
				continue
			}
			if s, ok := literalJS(args[k]); ok {
				m.agregar("js", s, rel, args[k][0].linea)
			} else if len(args[k]) > 0 {
				m.dinamicas = append(m.dinamicas, rel+":"+strconv.Itoa(args[k][0].linea))
			}
		}
	}
}

// argumentosJS separa los argumentos de una llamada (desde el token después del paréntesis) por
// las comas de primer nivel.
func argumentosJS(toks []tokJS, desde int) [][]tokJS {
	var args [][]tokJS
	var actual []tokJS
	prof := 0
	for j := desde; j < len(toks); j++ {
		tk := toks[j]
		if tk.tipo == 'p' {
			switch tk.val {
			case "(", "[", "{":
				prof++
			case ")", "]", "}":
				if prof == 0 {
					if len(actual) > 0 {
						args = append(args, actual)
					}
					return args
				}
				prof--
			case ",":
				if prof == 0 {
					args = append(args, actual)
					actual = nil
					continue
				}
			}
		}
		actual = append(actual, tk)
	}
	return args
}

// literalJS: una cadena, o varias unidas con +.
func literalJS(arg []tokJS) (string, bool) {
	if len(arg) == 0 || len(arg)%2 == 0 {
		return "", false
	}
	var b strings.Builder
	for k, tk := range arg {
		if k%2 == 0 {
			if tk.tipo != 's' {
				return "", false
			}
			b.WriteString(tk.val)
		} else if tk.tipo != 'p' || tk.val != "+" {
			return "", false
		}
	}
	return b.String(), true
}

// ---- HTML ----------------------------------------------------------------------------------------

var vacios = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true, "input": true,
	"link": true, "meta": true, "source": true, "track": true, "wbr": true, "param": true,
}

var atributosTraducibles = []string{"title", "placeholder", "aria-label", "alt"}

type elementoHTML struct {
	nombre  string
	marcado bool
}

// marcasHTML recorre etiquetas y textos con una pila de elementos abiertos: un texto cuenta si su
// padre DIRECTO tiene data-i18n (así un botón "Abrir<span>Ctrl O</span>" traduce "Abrir" y deja
// el atajo). Los <script> de la página se leen como JS.
func marcasHTML(m *marcas, rel, src string) {
	var pila []elementoHTML
	linea := 1
	i, n := 0, len(src)
	avanzar := func(hasta int) {
		linea += strings.Count(src[i:hasta], "\n")
		i = hasta
	}
	for i < n {
		switch {
		case strings.HasPrefix(src[i:], "<!--"):
			fin := strings.Index(src[i:], "-->")
			if fin < 0 {
				fin = n - i - 3
			}
			avanzar(i + fin + 3)
		case strings.HasPrefix(src[i:], "<!") || strings.HasPrefix(src[i:], "<?"):
			fin := strings.IndexByte(src[i:], '>')
			if fin < 0 {
				fin = n - i - 1
			}
			avanzar(i + fin + 1)
		case strings.HasPrefix(src[i:], "</"):
			fin := strings.IndexByte(src[i:], '>')
			if fin < 0 {
				fin = n - i - 1
			}
			nombre := strings.ToLower(strings.TrimSpace(src[i+2 : i+fin]))
			for k := len(pila) - 1; k >= 0; k-- {
				if pila[k].nombre == nombre {
					pila = pila[:k]
					break
				}
			}
			avanzar(i + fin + 1)
		case src[i] == '<' && i+1 < n && esLetraASCII(src[i+1]):
			nombre, attrs, fin, autocierre := leerEtiqueta(src, i)
			lineaEtiqueta := linea
			avanzar(fin)
			valor, marcado := attrs["data-i18n"]
			soloAtributos := marcado && valor == "attr" // el texto lo pone el código en vivo
			if marcado {
				for _, a := range atributosTraducibles {
					if v := normalizar(attrs[a]); v != "" {
						m.agregar("html", v, rel, lineaEtiqueta)
					}
				}
			}
			switch {
			case nombre == "script":
				cierre := indiceSinMayusculas(src[i:], "</script")
				if cierre < 0 {
					cierre = n - i
				}
				marcasJS(m, rel, src[i:i+cierre], linea)
				avanzar(i + cierre)
			case nombre == "style":
				cierre := indiceSinMayusculas(src[i:], "</style")
				if cierre < 0 {
					cierre = n - i
				}
				avanzar(i + cierre)
			case !vacios[nombre] && !autocierre:
				pila = append(pila, elementoHTML{nombre, marcado && !soloAtributos})
			}
		default:
			fin := strings.IndexByte(src[i:], '<')
			if fin < 0 {
				fin = n - i
			}
			if fin == 0 { // un < suelto que no abre etiqueta
				fin = 1
			}
			texto := normalizar(html.UnescapeString(src[i : i+fin]))
			if texto != "" && len(pila) > 0 && pila[len(pila)-1].marcado {
				m.agregar("html", texto, rel, linea+strings.Count(src[i:i+fin], "\n")-strings.Count(strings.TrimLeft(src[i:i+fin], " \t\r\n"), "\n"))
			}
			avanzar(i + fin)
		}
	}
}

func esLetraASCII(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func indiceSinMayusculas(s, sub string) int { return strings.Index(strings.ToLower(s), sub) }

// leerEtiqueta lee <nombre atr="v" atr2='v' atr3=v atr4> desde i y devuelve dónde termina.
func leerEtiqueta(src string, i int) (nombre string, attrs map[string]string, fin int, autocierre bool) {
	attrs = map[string]string{}
	j := i + 1
	for j < len(src) && !strings.ContainsRune(" \t\r\n/>", rune(src[j])) {
		j++
	}
	nombre = strings.ToLower(src[i+1 : j])
	for j < len(src) {
		for j < len(src) && strings.ContainsRune(" \t\r\n", rune(src[j])) {
			j++
		}
		if j >= len(src) {
			break
		}
		if src[j] == '>' {
			return nombre, attrs, j + 1, autocierre
		}
		if src[j] == '/' {
			autocierre = true
			j++
			continue
		}
		k := j
		for k < len(src) && !strings.ContainsRune(" \t\r\n=/>", rune(src[k])) {
			k++
		}
		clave := strings.ToLower(src[j:k])
		j = k
		for j < len(src) && strings.ContainsRune(" \t\r\n", rune(src[j])) {
			j++
		}
		valor := ""
		if j < len(src) && src[j] == '=' {
			j++
			for j < len(src) && strings.ContainsRune(" \t\r\n", rune(src[j])) {
				j++
			}
			if j < len(src) && (src[j] == '"' || src[j] == '\'') {
				q := src[j]
				fin := strings.IndexByte(src[j+1:], q)
				if fin < 0 {
					fin = len(src) - j - 1
				}
				valor = src[j+1 : j+1+fin]
				j += fin + 2
			} else {
				k := j
				for k < len(src) && !strings.ContainsRune(" \t\r\n>", rune(src[k])) {
					k++
				}
				valor = src[j:k]
				j = k
			}
		}
		if clave != "" {
			attrs[clave] = html.UnescapeString(valor)
		}
		autocierre = false
	}
	return nombre, attrs, len(src), autocierre
}

// normalizar colapsa los blancos como los muestra el navegador (es lo mismo que hace i18n.js).
func normalizar(s string) string { return strings.Join(strings.Fields(s), " ") }
