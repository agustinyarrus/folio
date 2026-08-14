package main

// formats.go — Folio no lee solo Markdown.
//
// La idea es una sola: lo que no es Markdown se CONVIERTE a Markdown y despues pasa por el mismo
// goldmark de siempre. Asi el indice, la busqueda, el resaltado de codigo, la tipografia y los
// estilos salen gratis para cada formato nuevo, sin tocar el motor de render ni la UI.
//
// Los conversores viven en fmt_data.go (JSON/CSV/YAML/XML/codigo…), fmt_markup.go (reST,
// AsciiDoc, Org, MediaWiki, HTML) y fmt_docs.go (docx, odt, epub, ipynb). Todos son stdlib +
// chroma, que ya venia vendorizado: ni una dependencia nueva.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/alecthomas/chroma/v2/lexers"
)

// converter transforma el contenido de un archivo en Markdown.
type converter func(src []byte, path string) ([]byte, error)

type format struct {
	Name string    // nombre legible ("JSON", "Word", …), se muestra en la barra de estado
	Exts []string  // extensiones que lo activan, con punto
	Conv converter // nil = ya es Markdown, se pasa derecho
}

// El registro. El orden importa solo para armar el filtro del dialogo de apertura.
var formats = []format{
	{Name: "Markdown", Exts: []string{".md", ".markdown", ".mdown", ".mkd", ".mkdn", ".mdwn",
		".mdtxt", ".mdtext", ".text", ".rmd", ".qmd", ".mdx", ".mdc"}, Conv: nil},

	// --- datos -----------------------------------------------------------
	{Name: "JSON", Exts: []string{".json", ".jsonc", ".json5", ".webmanifest", ".ipynb_checkpoints"}, Conv: convJSON},
	{Name: "JSON Lines", Exts: []string{".jsonl", ".ndjson"}, Conv: convJSONL},
	{Name: "CSV", Exts: []string{".csv"}, Conv: convCSV},
	{Name: "TSV", Exts: []string{".tsv", ".tab"}, Conv: convCSV},
	{Name: "YAML", Exts: []string{".yaml", ".yml"}, Conv: convHighlight},
	{Name: "TOML", Exts: []string{".toml"}, Conv: convHighlight},
	{Name: "INI", Exts: []string{".ini", ".cfg", ".conf", ".properties", ".env", ".editorconfig"}, Conv: convHighlight},
	{Name: "XML", Exts: []string{".xml", ".xsd", ".xsl", ".xslt", ".rss", ".atom", ".plist", ".resx", ".csproj", ".props"}, Conv: convXML},
	{Name: "Diff", Exts: []string{".diff", ".patch"}, Conv: convHighlight},
	{Name: "Texto", Exts: []string{".txt", ".log", ".nfo", ".me", ".readme", ".1st"}, Conv: convText},

	// --- marcado ---------------------------------------------------------
	{Name: "reStructuredText", Exts: []string{".rst", ".rest"}, Conv: convRST},
	{Name: "AsciiDoc", Exts: []string{".adoc", ".asciidoc", ".asc"}, Conv: convAsciiDoc},
	{Name: "Org", Exts: []string{".org"}, Conv: convOrg},
	{Name: "MediaWiki", Exts: []string{".wiki", ".mediawiki"}, Conv: convWiki},
	{Name: "HTML", Exts: []string{".html", ".htm", ".xhtml"}, Conv: convHTML},

	// --- documentos ------------------------------------------------------
	{Name: "Jupyter", Exts: []string{".ipynb"}, Conv: convNotebook},
	{Name: "Word", Exts: []string{".docx"}, Conv: convDocx},
	{Name: "OpenDocument", Exts: []string{".odt"}, Conv: convODT},
	{Name: "EPUB", Exts: []string{".epub"}, Conv: convEPUB},
}

var (
	extFormat  = map[string]*format{}
	mdExtsOnly = map[string]bool{}
)

func init() {
	for i := range formats {
		f := &formats[i]
		for _, e := range f.Exts {
			extFormat[e] = f
			if f.Conv == nil {
				mdExtsOnly[e] = true
			}
		}
	}
}

// ---- consultas que usa el resto del programa ----------------------------

func extOf(name string) string { return strings.ToLower(filepath.Ext(name)) }

// IsMarkdown: Markdown de verdad (lo usan los wikilinks, que agregan ".md").
func IsMarkdown(name string) bool { return mdExtsOnly[extOf(name)] }

// IsSupported: cualquier cosa que Folio sepa mostrar, incluido todo lo que chroma
// reconozca como codigo fuente. Es lo que decide si un enlace se abre adentro.
func IsSupported(name string) bool {
	if _, ok := extFormat[extOf(name)]; ok {
		return true
	}
	return codeLexerName(name) != ""
}

// FormatName devuelve el nombre legible del formato ("JSON", "Go", "Markdown"…).
func FormatName(name string) string {
	if f, ok := extFormat[extOf(name)]; ok {
		return f.Name
	}
	if n, ok := nameOverride[strings.ToLower(filepath.Base(name))]; ok {
		return n
	}
	if l := codeLexerName(name); l != "" {
		if lex := lexers.Get(l); lex != nil {
			if n := lex.Config().Name; n != "" && n != "plaintext" && n != "fallback" {
				return n
			}
		}
	}
	if e := extOf(name); e != "" {
		return strings.ToUpper(strings.TrimPrefix(e, "."))
	}
	return "Texto"
}

// Nombres donde chroma se manda una macana: go.mod matchea el lexer de AMPL,
// que tambien reclama *.mod. Estos ganan por sobre la deteccion automatica.
var lexerOverride = map[string]string{
	"go.mod": "text", "go.sum": "text", "go.work": "text", "go.work.sum": "text",
	"cargo.lock": "toml", "package-lock.json": "json", "yarn.lock": "text",
}

// Nombre lindo para esos mismos archivos (chroma diria "plaintext").
var nameOverride = map[string]string{
	"go.mod": "Go module", "go.sum": "Go checksums", "go.work": "Go workspace",
	"go.work.sum": "Go checksums", "yarn.lock": "Lockfile", "cargo.lock": "Lockfile",
}

// codeLexerName: si chroma conoce el archivo por su nombre, devuelve el alias del
// lexer (por ejemplo "go", "python"). Es lo que habilita ~250 lenguajes de una.
func codeLexerName(name string) string {
	base := filepath.Base(name)
	if v, ok := lexerOverride[strings.ToLower(base)]; ok {
		return v
	}
	if extOf(name) == "" && !knownBareName[strings.ToLower(base)] {
		return "" // sin extension y sin nombre conocido: no adivinamos
	}
	lex := lexers.Match(base)
	if lex == nil {
		return ""
	}
	cfg := lex.Config()
	if len(cfg.Aliases) > 0 {
		return cfg.Aliases[0]
	}
	return strings.ToLower(cfg.Name)
}

// Archivos sin extension que igual son codigo.
var knownBareName = map[string]bool{
	"dockerfile": true, "makefile": true, "gnumakefile": true, "rakefile": true,
	"gemfile": true, "vagrantfile": true, "jenkinsfile": true, "cmakelists.txt": true,
	"license": true, "readme": true, "changelog": true, "authors": true, "todo": true,
}

// MarkdownGlob es el filtro del dialogo nativo de apertura (histórico: solo Markdown).
const MarkdownGlob = "*.md;*.markdown;*.mdown;*.mkd;*.mkdn;*.mdwn;*.mdtxt;*.mdtext;*.text;*.rmd;*.qmd;*.mdx"

// OpenGlob es el filtro "todo lo que Folio abre", armado del registro + un puñado
// de extensiones de codigo populares (chroma conoce muchas mas, pero el dialogo
// tiene que entrar en una linea).
func OpenGlob() string {
	seen := map[string]bool{}
	var globs []string
	add := func(e string) {
		if !seen[e] {
			seen[e] = true
			globs = append(globs, "*"+e)
		}
	}
	for _, f := range formats {
		for _, e := range f.Exts {
			add(e)
		}
	}
	for _, e := range popularCodeExts {
		add(e)
	}
	return strings.Join(globs, ";")
}

// Extensiones de codigo que si o si van en el dialogo y en el instalador.
var popularCodeExts = []string{
	".go", ".py", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx", ".c", ".h", ".cpp", ".hpp",
	".cc", ".cs", ".java", ".kt", ".swift", ".rs", ".rb", ".php", ".pl", ".lua", ".r",
	".sh", ".bash", ".zsh", ".ps1", ".psm1", ".bat", ".cmd", ".sql", ".vim", ".el",
	".asm", ".s", ".f90", ".pas", ".dart", ".scala", ".clj", ".ex", ".exs", ".erl",
	".hs", ".ml", ".nim", ".zig", ".v", ".jl", ".groovy", ".gradle", ".tf", ".hcl",
	".proto", ".graphql", ".css", ".scss", ".less", ".vue", ".svelte", ".astro",
}

// AllExts: todas las extensiones que Folio declara soportar (registro + codigo popular),
// ordenadas. La usan el instalador y el README.
func AllExts() []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range formats {
		for _, e := range f.Exts {
			if !seen[e] {
				seen[e] = true
				out = append(out, e)
			}
		}
	}
	for _, e := range popularCodeExts {
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	sort.Strings(out)
	return out
}

// ---- conversion ---------------------------------------------------------

// Limites de cordura: un .log de 300 MB no tiene por que voltear el visor.
const (
	maxHighlightBytes = 4 << 20  // arriba de esto no se resalta (chroma se pone lento)
	maxSourceBytes    = 48 << 20 // arriba de esto se trunca
)

// toMarkdown convierte lo que sea a Markdown. Devuelve el Markdown y el nombre del
// formato. Si el conversor falla, cae a mostrar el archivo como texto plano: es
// preferible ver algo a ver un error.
func toMarkdown(src []byte, path string) ([]byte, string) {
	name := FormatName(path)
	if len(src) > maxSourceBytes {
		src = append(src[:maxSourceBytes:maxSourceBytes],
			[]byte("\n\n… (archivo truncado)\n")...)
	}
	f, ok := extFormat[extOf(path)]
	if !binaryFormats[name] {
		src = decodeText(src) // BOM y UTF-16: en Windows aparecen todo el tiempo
	}
	if ok && f.Conv == nil {
		return src, name // ya es Markdown
	}
	var conv converter
	if ok {
		conv = f.Conv
	} else if codeLexerName(path) != "" {
		conv = convCode
	} else {
		return src, name // desconocido: que lo intente goldmark tal cual
	}
	// Un binario con extension de texto (pasa mas seguido de lo que uno cree) no
	// tiene por que pasar por un conversor de marcado: se avisa y se muestra el
	// principio en hexadecimal.
	if !binaryFormats[name] && looksBinary(src) {
		return []byte(binaryNotice(src, path)), name
	}
	out, err := conv(src, path)
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return convFallback(src, path, err), name
	}
	return out, name
}

// Formatos que SON binarios de por si: no hay que revisarlos.
var binaryFormats = map[string]bool{"Word": true, "OpenDocument": true, "EPUB": true}

// decodeText normaliza a UTF-8 sin BOM. En Windows medio archivo de texto viene
// con BOM (el Bloc de notas, PowerShell) y no son pocos los que estan en UTF-16:
// sin esto, un JSON perfecto se reporta como roto por culpa de tres bytes.
func decodeText(src []byte) []byte {
	switch {
	case len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF:
		return src[3:]
	case len(src) >= 2 && src[0] == 0xFF && src[1] == 0xFE:
		return utf16ToUTF8(src[2:], false)
	case len(src) >= 2 && src[0] == 0xFE && src[1] == 0xFF:
		return utf16ToUTF8(src[2:], true)
	}
	return src
}

func utf16ToUTF8(b []byte, bigEndian bool) []byte {
	if len(b)%2 == 1 {
		b = b[:len(b)-1]
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		if bigEndian {
			u[i] = uint16(b[i*2])<<8 | uint16(b[i*2+1])
		} else {
			u[i] = uint16(b[i*2+1])<<8 | uint16(b[i*2])
		}
	}
	return []byte(string(utf16.Decode(u)))
}

// looksBinary: si hay un byte cero en los primeros 8 KB, no es texto.
func looksBinary(src []byte) bool {
	n := len(src)
	if n > 8192 {
		n = 8192
	}
	for i := 0; i < n; i++ {
		if src[i] == 0 {
			return true
		}
	}
	return false
}

// binaryNotice arma la vista de un archivo binario: aviso + volcado hexadecimal.
func binaryNotice(src []byte, path string) string {
	var b strings.Builder
	b.WriteString(docHeader(path, fmt.Sprintf("archivo binario · %d bytes", len(src))))
	b.WriteString("> [!WARNING]\n> Esto no es texto. Van los primeros bytes, por las dudas.\n\n")
	n := len(src)
	if n > 1024 {
		n = 1024
	}
	var dump strings.Builder
	for i := 0; i < n; i += 16 {
		end := i + 16
		if end > n {
			end = n
		}
		fmt.Fprintf(&dump, "%08x  ", i)
		for j := i; j < i+16; j++ {
			if j < end {
				fmt.Fprintf(&dump, "%02x ", src[j])
			} else {
				dump.WriteString("   ")
			}
		}
		dump.WriteString(" |")
		for j := i; j < end; j++ {
			c := src[j]
			if c < 32 || c > 126 {
				c = '.'
			}
			dump.WriteByte(c)
		}
		dump.WriteString("|\n")
	}
	b.WriteString(codeBlock(dump.String(), ""))
	return b.String()
}

// convFallback: el conversor no pudo; mostramos el crudo como texto y avisamos.
func convFallback(src []byte, path string, err error) []byte {
	var b strings.Builder
	b.WriteString("# " + mdEscape(filepath.Base(path)) + "\n\n")
	if err != nil {
		b.WriteString("> [!WARNING]\n> No se pudo interpretar el archivo (" +
			mdEscape(err.Error()) + "). Va el contenido crudo.\n\n")
	}
	b.WriteString(codeBlock(string(src), ""))
	return []byte(b.String())
}

// ---- utilidades de armado de Markdown -----------------------------------

// codeBlock arma una cerca de backticks mas larga que cualquier racha que haya
// adentro, asi el contenido nunca puede cerrar el bloque antes de tiempo.
func codeBlock(code, lang string) string {
	longest := 0
	run := 0
	for _, r := range code {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", max(3, longest+1))
	if !strings.HasSuffix(code, "\n") {
		code += "\n"
	}
	return fence + lang + "\n" + code + fence + "\n\n"
}

// mdEscape protege los caracteres que Markdown se toma en serio.
func mdEscape(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`, "`", "\\`", `*`, `\*`, `_`, `\_`, `[`, `\[`, `]`, `\]`,
		`<`, `\<`, `>`, `\>`, `#`, `\#`, `|`, `\|`,
	)
	return r.Replace(s)
}

// cellEscape prepara un valor para una celda de tabla (una sola linea, sin pipes).
func cellEscape(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", "<br>")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, `|`, `\|`)
	return strings.TrimSpace(s)
}

// mdTable arma una tabla GFM. Las filas cortas se rellenan y las largas se recortan.
func mdTable(head []string, rows [][]string) string {
	if len(head) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("|")
	for _, h := range head {
		b.WriteString(" " + cellEscape(h) + " |")
	}
	b.WriteString("\n|")
	for range head {
		b.WriteString(" --- |")
	}
	b.WriteString("\n")
	for _, r := range rows {
		b.WriteString("|")
		for i := range head {
			v := ""
			if i < len(r) {
				v = r[i]
			}
			b.WriteString(" " + cellEscape(v) + " |")
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
}

// docHeader: titulo + linea de contexto que abre los documentos convertidos.
// A un documento se le saca la extension (informe.docx -> "informe"), pero a un
// archivo de codigo no: "main.go" y "go.mod" son el nombre completo o no son nada.
func docHeader(path, sub string) string {
	base := filepath.Base(path)
	title := base
	if _, known := extFormat[extOf(path)]; known {
		if stem := strings.TrimSuffix(base, filepath.Ext(base)); stem != "" {
			title = stem
		}
	}
	h := "# " + mdEscape(title) + "\n\n"
	if sub != "" {
		h += "*" + sub + "*\n\n"
	}
	return h
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
