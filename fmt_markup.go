package main

// fmt_markup.go — otros lenguajes de marcado -> Markdown.
//
// reStructuredText, AsciiDoc, Org y MediaWiki. No son conversores completos (cada uno de esos
// formatos da para una biblioteca entera): cubren lo que uno se encuentra de verdad en un README,
// en la documentacion de un proyecto o en una pagina de wiki — encabezados, listas, enfasis,
// codigo, enlaces, imagenes, tablas (grid y simples de reST, |=== de AsciiDoc, las de Org y las
// {| de MediaWiki), notas al pie y admoniciones. Lo que no se reconoce pasa como texto, que es la
// degradacion correcta: se sigue leyendo.
//
// Todas las expresiones regulares se compilan UNA vez, a nivel de paquete: antes varias se
// compilaban adentro del bucle, una vez por renglon.

import (
	"regexp"
	"strconv"
	"strings"
)

// replaceSubmatches es ReplaceAllStringFunc pero entregando los grupos: evita el doble
// matcheo de llamar FindStringSubmatch adentro del callback. O(n).
func replaceSubmatches(re *regexp.Regexp, s string, fn func(g []string) string) string {
	idx := re.FindAllStringSubmatchIndex(s, -1)
	if idx == nil {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	last := 0
	for _, m := range idx {
		b.WriteString(s[last:m[0]])
		g := make([]string, len(m)/2)
		for k := range g {
			if m[2*k] >= 0 {
				g[k] = s[m[2*k]:m[2*k+1]]
			}
		}
		b.WriteString(fn(g))
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

// footnotes junta notas al pie mientras se convierte y las vuelca al final como [^n]: …
type footnotes struct {
	defs  []string
	named map[string]int // nota con nombre ya definida (MediaWiki <ref name>, Org [fn:x])
}

func (f *footnotes) add(text string) int {
	f.defs = append(f.defs, strings.TrimSpace(text))
	return len(f.defs)
}

func (f *footnotes) addNamed(name, text string) int {
	if f.named == nil {
		f.named = map[string]int{}
	}
	if n, ok := f.named[name]; ok {
		if text != "" && f.defs[n-1] == "" {
			f.defs[n-1] = strings.TrimSpace(text)
		}
		return n
	}
	n := f.add(text)
	f.named[name] = n
	return n
}

func (f *footnotes) ref(n int) string { return "[^" + strconv.Itoa(n) + "]" }

func (f *footnotes) render() string {
	if len(f.defs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n")
	for i, d := range f.defs {
		if d == "" {
			d = "(nota vacía)"
		}
		b.WriteString("[^" + strconv.Itoa(i+1) + "]: " + d + "\n")
	}
	return b.String()
}

// ---- HTML ----------------------------------------------------------------

func convHTML(src []byte, path string) ([]byte, error) {
	root := parseHTML(string(src)) // una sola pasada: cuerpo y <title> salen del mismo arbol
	body := htmlTreeToMarkdown(root, &htmlOptions{keepIDs: true})
	title := htmlTitle(root)
	head := ""
	if title != "" && !strings.HasPrefix(strings.TrimSpace(body), "# ") {
		head = "# " + mdEscape(title) + "\n\n"
	}
	return []byte(head + body), nil
}

// ---- reStructuredText ----------------------------------------------------

var (
	rstAdorn    = `=-~^"'` + "`" + `#*+_:.<>!$%&(),/;?@[\]{|}`
	rstDirRe    = regexp.MustCompile(`^\.\.\s+([a-zA-Z0-9_-]+)::\s*(.*)$`)
	rstFieldRe  = regexp.MustCompile(`^:([^:]+):\s*(.*)$`)
	rstLinkRe   = regexp.MustCompile("`([^`<]+?)\\s*<([^>]+)>`_+")
	rstRoleRe   = regexp.MustCompile(":[a-zA-Z:]+:`([^`]+)`")
	rstLiteral  = regexp.MustCompile("``([^`]+)``")
	rstGridRe   = regexp.MustCompile(`^\+(?:[-=]+\+)+$`)
	rstSimpleRe = regexp.MustCompile(`^=+(?:\s+=+)+$`)
	rstAdmonMap = map[string]string{
		"note": "NOTE", "tip": "TIP", "hint": "TIP", "important": "IMPORTANT",
		"warning": "WARNING", "caution": "WARNING", "attention": "WARNING",
		"danger": "CAUTION", "error": "CAUTION",
	}
)

func isAdornment(s string) bool {
	s = strings.TrimRight(s, " \t")
	if len(s) < 2 {
		return false
	}
	c := s[0]
	if !strings.ContainsRune(rstAdorn, rune(c)) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != c {
			return false
		}
	}
	return true
}

// rstInline traduce el enfasis y los enlaces de una linea de reST.
func rstInline(s string) string {
	s = rstLiteral.ReplaceAllString(s, "`$1`")
	s = rstLinkRe.ReplaceAllString(s, "[$1]($2)")
	s = rstRoleRe.ReplaceAllString(s, "`$1`")
	s = strings.ReplaceAll(s, "|", `\|`)
	return s
}

func convRST(src []byte, path string) ([]byte, error) {
	lines := splitLines(src)
	var out []string
	levels := map[byte]int{} // caracter de subrayado -> nivel de encabezado
	next := 1

	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		trimmed := strings.TrimRight(ln, " \t")
		core := strings.TrimSpace(trimmed)

		// tablas: grid (+---+) y simples (=== ===)
		if rstGridRe.MatchString(core) {
			if tbl, adv := rstGridTable(lines, i); tbl != "" {
				out = append(out, "", tbl)
				i += adv - 1
				continue
			}
		}
		if rstSimpleRe.MatchString(core) {
			if tbl, adv := rstSimpleTable(lines, i); tbl != "" {
				out = append(out, "", tbl)
				i += adv - 1
				continue
			}
		}

		// encabezado: texto con una linea de adorno abajo (y a veces arriba)
		if i+1 < len(lines) && trimmed != "" && !strings.HasPrefix(trimmed, "..") && !isAdornment(trimmed) &&
			isAdornment(lines[i+1]) && len(strings.TrimRight(lines[i+1], " \t")) >= len(trimmed)/2 {
			c := strings.TrimRight(lines[i+1], " \t")[0]
			lvl, ok := levels[c]
			if !ok {
				lvl = min(next, 6)
				levels[c] = lvl
				next++
			}
			out = append(out, "", strings.Repeat("#", lvl)+" "+rstInline(core), "")
			i++
			continue
		}
		if isAdornment(trimmed) && len(trimmed) >= 3 {
			// linea de adorno suelta: transicion horizontal, o el adorno de ARRIBA de un titulo
			if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" && !isAdornment(lines[i+1]) {
				continue
			}
			out = append(out, "", "---", "")
			continue
		}

		// directivas: .. code-block:: go / .. note:: / .. image:: x
		if m := rstDirRe.FindStringSubmatch(trimmed); m != nil {
			name, arg := strings.ToLower(m[1]), strings.TrimSpace(m[2])
			body, opts, adv := rstIndentedBlock(lines, i+1)
			switch {
			case name == "code-block" || name == "code" || name == "sourcecode":
				out = append(out, "", codeBlock(strings.Join(body, "\n"), arg))
			case name == "image" || name == "figure":
				out = append(out, "", "!["+mdEscape(opts["alt"])+"]("+arg+")", "")
				if name == "figure" && len(body) > 0 { // la leyenda de la figura
					out = append(out, "*"+rstInline(strings.TrimSpace(strings.Join(body, " ")))+"*", "")
				}
			case rstAdmonMap[name] != "":
				out = append(out, "", "> [!"+rstAdmonMap[name]+"]")
				if arg != "" {
					out = append(out, "> "+rstInline(arg))
				}
				for _, b := range body {
					out = append(out, "> "+rstInline(strings.TrimSpace(b)))
				}
				out = append(out, "")
			case name == "contents" || name == "toctree" || name == "index":
				// el indice lo arma Folio solo
			default:
				if len(body) > 0 {
					out = append(out, "", codeBlock(strings.Join(body, "\n"), ""))
				}
			}
			i += adv
			continue
		}
		// comentario suelto
		if strings.HasPrefix(trimmed, ".. ") || trimmed == ".." {
			_, _, adv := rstIndentedBlock(lines, i+1)
			i += adv
			continue
		}
		// bloque literal: parrafo que termina en ::
		if strings.HasSuffix(trimmed, "::") && core != "::" {
			out = append(out, "", rstInline(strings.TrimSuffix(trimmed, "::"))+":", "")
			body, _, adv := rstIndentedBlock(lines, i+1)
			out = append(out, codeBlock(strings.Join(body, "\n"), ""))
			i += adv
			continue
		}
		// lista de campos  :autor: fulano
		if m := rstFieldRe.FindStringSubmatch(trimmed); m != nil && !strings.HasPrefix(trimmed, "::") {
			out = append(out, "**"+mdEscape(m[1])+"**: "+rstInline(m[2]))
			continue
		}
		// enumeracion con #.
		if strings.HasPrefix(core, "#. ") {
			ind := trimmed[:len(trimmed)-len(strings.TrimLeft(trimmed, " \t"))]
			out = append(out, ind+"1. "+rstInline(core[3:]))
			continue
		}
		out = append(out, rstInline(trimmed))
	}
	return []byte(docHeaderIfNeeded(out, path) + strings.Join(out, "\n") + "\n"), nil
}

// rstIndentedBlock junta el bloque indentado que sigue a una directiva. Devuelve las
// lineas sin la indentacion, las opciones de la directiva (:alt: x) y cuantas consumir.
func rstIndentedBlock(lines []string, start int) ([]string, map[string]string, int) {
	opts := map[string]string{}
	i := start
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i >= len(lines) {
		return nil, opts, i - start
	}
	ind := indentOf(lines[i])
	if ind == 0 {
		return nil, opts, 0
	}
	var body []string
	for ; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			body = append(body, "")
			continue
		}
		if indentOf(lines[i]) < ind {
			break
		}
		body = append(body, lines[i][min(ind, len(lines[i])):])
	}
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	// opciones de directiva (:width: 50, :alt: texto) al principio
	for len(body) > 0 {
		m := rstFieldRe.FindStringSubmatch(strings.TrimSpace(body[0]))
		if m == nil {
			break
		}
		opts[strings.ToLower(strings.TrimSpace(m[1]))] = strings.TrimSpace(m[2])
		body = body[1:]
	}
	for len(body) > 0 && strings.TrimSpace(body[0]) == "" {
		body = body[1:]
	}
	return body, opts, i - start
}

func indentOf(s string) int {
	n := 0
	for _, c := range s {
		if c == ' ' {
			n++
		} else if c == '\t' {
			n += 4
		} else {
			break
		}
	}
	return n
}

// rstGridTable convierte una tabla grid de reST:
//
//	+-------+-------+
//	| a     | b     |
//	+=======+=======+
//	| 1     | 2     |
//	+-------+-------+
//
// Las columnas salen de las posiciones de los '+' del primer borde; una celda puede ocupar
// varios renglones (se juntan) y una celda que abarca columnas se detecta porque en esa
// frontera no hay '|'. Devuelve la tabla GFM y cuantos renglones consumio.
func rstGridTable(lines []string, start int) (string, int) {
	first := strings.TrimRight(lines[start], " \t")
	indent := len(first) - len(strings.TrimLeft(first, " \t"))
	var bounds []int
	for k := indent; k < len(first); k++ {
		if first[k] == '+' {
			bounds = append(bounds, k)
		}
	}
	ncols := len(bounds) - 1
	if ncols < 1 {
		return "", 0
	}
	var rows [][]string
	cur := make([][]string, ncols)
	hasContent := false
	flush := func() {
		if !hasContent {
			return
		}
		row := make([]string, ncols)
		for c := range cur {
			row[c] = rstInline(strings.Join(cur[c], " "))
			cur[c] = nil
		}
		rows = append(rows, row)
		hasContent = false
	}
	i := start
	for ; i < len(lines); i++ {
		ln := strings.TrimRight(lines[i], " \t")
		core := strings.TrimSpace(ln)
		switch {
		case rstGridRe.MatchString(core):
			flush()
		case strings.HasPrefix(core, "|"):
			// fronteras REALES de este renglon: donde hay un '|' en la posicion de un '+'
			var real []int
			for c, b := range bounds {
				if b < len(ln) && ln[b] == '|' {
					real = append(real, c)
				}
			}
			for k := 0; k+1 < len(real); k++ {
				c0, c1 := real[k], real[k+1]
				txt := strings.TrimSpace(ln[bounds[c0]+1 : min(bounds[c1], len(ln))])
				if txt != "" {
					cur[c0] = append(cur[c0], txt)
					hasContent = true
				}
			}
		default:
			flush()
			return renderRSTRows(rows), i - start
		}
	}
	flush()
	return renderRSTRows(rows), i - start
}

// rstSimpleTable convierte una tabla simple de reST (columnas marcadas con ====).
func rstSimpleTable(lines []string, start int) (string, int) {
	border := strings.TrimRight(lines[start], " \t")
	type span struct{ from, to int }
	var cols []span
	for k := 0; k < len(border); {
		if border[k] != '=' {
			k++
			continue
		}
		j := k
		for j < len(border) && border[j] == '=' {
			j++
		}
		cols = append(cols, span{k, j})
		k = j
	}
	if len(cols) < 2 {
		return "", 0
	}
	cell := func(ln string, c int) string {
		from := cols[c].from
		to := len(ln)
		if c+1 < len(cols) {
			to = min(cols[c+1].from, len(ln))
		}
		if from >= len(ln) || from >= to {
			return ""
		}
		return strings.TrimSpace(ln[from:to])
	}
	var segments [][][]string // tramos entre bordes
	var cur [][]string
	i := start + 1
	for ; i < len(lines); i++ {
		ln := strings.TrimRight(lines[i], " \t")
		if rstSimpleRe.MatchString(strings.TrimSpace(ln)) {
			segments = append(segments, cur)
			cur = nil
			// fin de la tabla: el borde va seguido de un renglon en blanco o del final
			if i+1 >= len(lines) || strings.TrimSpace(lines[i+1]) == "" {
				i++
				break
			}
			continue
		}
		if strings.TrimSpace(ln) == "" {
			continue
		}
		row := make([]string, len(cols))
		for c := range cols {
			row[c] = rstInline(cell(ln, c))
		}
		if row[0] == "" && len(cur) > 0 { // renglon de continuacion
			for c := range row {
				if row[c] != "" {
					cur[len(cur)-1][c] = strings.TrimSpace(cur[len(cur)-1][c] + " " + row[c])
				}
			}
			continue
		}
		cur = append(cur, row)
	}
	if len(cur) > 0 { // tabla sin borde de cierre (archivo cortado): lo leido no se pierde
		segments = append(segments, cur)
	}
	var rows [][]string
	for _, seg := range segments {
		rows = append(rows, seg...)
	}
	if len(rows) == 0 {
		return "", 0
	}
	return renderRSTRows(rows), i - start
}

func renderRSTRows(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	return mdTable(rows[0], rows[1:])
}

// ---- AsciiDoc ------------------------------------------------------------

var (
	adocAttrRe     = regexp.MustCompile(`^:[^:]+:\s*(.*)$`)
	adocImgRe      = regexp.MustCompile(`image::?([^\[\s]+)\[([^\]]*)\]`)
	adocLinkRe     = regexp.MustCompile(`(?:link|mailto):([^\[\s]+)\[([^\]]*)\]`)
	adocURLRe      = regexp.MustCompile(`(^|\s)(https?://[^\[\s]+)\[([^\]]*)\]`)
	adocXrefRe     = regexp.MustCompile(`<<([^,>]+)(?:,\s*([^>]+))?>>`)
	adocXrefMacro  = regexp.MustCompile(`xref:([^\[\s]+)\[([^\]]*)\]`)
	adocMonoRe     = regexp.MustCompile("`\\+?([^`]+?)\\+?`")
	adocBoldRe     = regexp.MustCompile(`(^|\W)\*([^*\n]+)\*(\W|$)`)
	adocItalRe     = regexp.MustCompile(`(^|\W)_([^_\n]+)_(\W|$)`)
	adocMarkRe     = regexp.MustCompile(`(^|\W)#([^#\n]+)#(\W|$)`)
	adocKbdRe      = regexp.MustCompile(`kbd:\[([^\]]+)\]`)
	adocFootRe     = regexp.MustCompile(`footnote:(?:([\w-]+))?\[([^\]]*)\]`)
	adocAdmonRe    = regexp.MustCompile(`^(NOTE|TIP|IMPORTANT|WARNING|CAUTION):\s*(.*)$`)
	adocHeadRe     = regexp.MustCompile(`^(=+)\s+(.*)$`)
	adocListRe     = regexp.MustCompile(`^(\*+|\.+|-)\s+(.*)$`)
	adocBlockTitle = regexp.MustCompile(`^\.([^\s.].*)$`)
	adocBlockAttr  = regexp.MustCompile(`^\[(.*)\]$`)
	adocColsRe     = regexp.MustCompile(`cols\s*=\s*"?([^",\]]*(?:,[^",\]]*)*)"?`)
	adocAnchorRe   = regexp.MustCompile(`^\[\[([^\],]+)(?:,[^\]]*)?\]\]$`)
	adocHeadMdRe   = regexp.MustCompile(`^#{1,6} `)
)

// adocInline traduce la sintaxis en linea de AsciiDoc (fn anota las notas al pie).
func adocInline(s string, fn *footnotes) string {
	// image::src[alt] — el corchete suele traer atributos (link=…, width=…) que no son el alt
	s = replaceSubmatches(adocImgRe, s, func(g []string) string {
		alt := g[2]
		if i := strings.IndexByte(alt, ','); i >= 0 {
			alt = alt[:i]
		}
		if strings.Contains(alt, "=") {
			alt = ""
		}
		return "![" + alt + "](" + g[1] + ")"
	})
	s = replaceSubmatches(adocLinkRe, s, func(g []string) string {
		text := g[2]
		if i := strings.IndexByte(text, ','); i >= 0 {
			text = text[:i]
		}
		if text == "" {
			text = g[1]
		}
		target := g[1]
		if strings.HasPrefix(g[0], "mailto:") {
			target = "mailto:" + target
		}
		return "[" + text + "](" + target + ")"
	})
	s = adocURLRe.ReplaceAllString(s, "$1[$3]($2)")
	s = replaceSubmatches(adocXrefMacro, s, func(g []string) string {
		text := g[2]
		if text == "" {
			text = g[1]
		}
		target := g[1]
		if file, frag, ok := strings.Cut(target, "#"); ok && file == "" {
			target = "#" + frag
		}
		return "[" + text + "](" + target + ")"
	})
	s = replaceSubmatches(adocXrefRe, s, func(g []string) string {
		text := strings.TrimSpace(g[2])
		if text == "" {
			text = g[1]
		}
		return "[" + text + "](#" + strings.TrimSpace(g[1]) + ")"
	})
	s = adocKbdRe.ReplaceAllString(s, "<kbd>$1</kbd>")
	if fn != nil {
		s = replaceSubmatches(adocFootRe, s, func(g []string) string {
			if g[1] != "" {
				return fn.ref(fn.addNamed(g[1], g[2]))
			}
			return fn.ref(fn.add(g[2]))
		})
	}
	s = adocMonoRe.ReplaceAllString(s, "`$1`")
	s = adocBoldRe.ReplaceAllString(s, "$1**$2**$3")
	s = adocItalRe.ReplaceAllString(s, "$1*$2*$3")
	s = adocMarkRe.ReplaceAllString(s, "$1==$2==$3")
	return s
}

// adocTable arma una tabla |=== de AsciiDoc. La cantidad de columnas sale del atributo
// cols o, como hace asciidoctor, de la cantidad de celdas del PRIMER renglon; despues las
// celdas se reparten en filas de a esa cantidad (asi funciona tanto "|a |b |c" en un
// renglon como una celda por renglon, que es el estilo mas comun).
type adocTable struct {
	cells     []string
	cols      int
	firstLine int
}

func (t *adocTable) addLine(line string, fn *footnotes) {
	const pipe = "\x00"
	line = strings.ReplaceAll(line, `\|`, pipe) // \| es un pipe literal dentro de la celda
	if !strings.HasPrefix(line, "|") {
		if n := len(t.cells); n > 0 { // continuacion de la celda anterior
			t.cells[n-1] = strings.TrimSpace(t.cells[n-1] + " " + adocInline(strings.ReplaceAll(line, pipe, "|"), fn))
		}
		return
	}
	parts := strings.Split(line[1:], "|")
	if t.firstLine == 0 {
		t.firstLine = len(parts)
	}
	for _, p := range parts {
		t.cells = append(t.cells, adocInline(strings.TrimSpace(strings.ReplaceAll(p, pipe, `\|`)), fn))
	}
}

func (t *adocTable) render() string {
	n := t.cols
	if n <= 0 {
		n = t.firstLine
	}
	if n <= 0 || len(t.cells) == 0 {
		return ""
	}
	var rows [][]string
	for i := 0; i < len(t.cells); i += n {
		rows = append(rows, t.cells[i:min(i+n, len(t.cells))])
	}
	head := append([]string(nil), rows[0]...)
	for len(head) < n {
		head = append(head, "")
	}
	return strings.TrimRight(mdTable(head, rows[1:]), "\n")
}

// adocColCount: cols="1,2,3" -> 3 ; cols="3*" -> 3 ; cols="2*,1" -> 3.
func adocColCount(spec string) int {
	n := 0
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if mult, _, ok := strings.Cut(part, "*"); ok {
			if k, err := strconv.Atoi(mult); err == nil && k > 0 {
				n += k
				continue
			}
		}
		n++
	}
	return n
}

func convAsciiDoc(src []byte, path string) ([]byte, error) {
	lines := splitLines(src)
	var out []string
	fn := &footnotes{}
	lang, admon := "", ""
	pendingCols := 0
	inCode, inQuote := false, false
	var table *adocTable
	codeFence := ""
	anchor := "" // <span id> de un [[ancla]] que espera su renglon
	emitLine := func(line string) {
		if anchor != "" && strings.TrimSpace(line) != "" {
			if m := adocHeadMdRe.FindStringIndex(line); m != nil {
				line = line[:m[1]] + anchor + line[m[1]:] // adentro del titulo, despues de los #
			} else {
				line = anchor + line
			}
			anchor = ""
		}
		out = append(out, line)
	}

	for i := 0; i < len(lines); i++ {
		ln := strings.TrimRight(lines[i], " \t")
		t := strings.TrimSpace(ln)

		// bloque de codigo: ---- o .... (y ++++, que es HTML pasante: lo mostramos como codigo)
		if inCode {
			if t == codeFence {
				out = append(out, "```", "")
				inCode = false
				continue
			}
			out = append(out, lines[i])
			continue
		}
		if isCodeFence(t) {
			out = append(out, "", "```"+lang)
			inCode, codeFence, lang = true, t, ""
			continue
		}
		// tabla |===
		if strings.HasPrefix(t, "|===") {
			if table != nil {
				if s := table.render(); s != "" {
					out = append(out, "", s, "")
				}
				table = nil
			} else {
				table = &adocTable{cols: pendingCols}
				pendingCols = 0
			}
			continue
		}
		if table != nil {
			if t != "" {
				table.addLine(t, fn)
			}
			continue
		}
		// bloques delimitados de prosa: ____ cita; ==== ejemplo, **** barra lateral y --
		// bloque abierto son transparentes. Sueltos en el Markdown, "====" subrayaba el
		// parrafo anterior como TITULO y "****" dibujaba una regla.
		if isProseFence(t) {
			if t[0] == '_' {
				inQuote = !inQuote
				out = append(out, "")
			}
			continue
		}
		if t == "'''" {
			out = append(out, "", "---", "")
			continue
		}
		// ancla de bloque [[id]]: sola seria un wikilink de Folio; se pega a lo que sigue
		if m := adocAnchorRe.FindStringSubmatch(t); m != nil {
			anchor = `<span id="` + sanitizeID(m[1]) + `"></span>`
			continue
		}
		// atributos de bloque: [source,go] [NOTE] [cols="1,2"] [%header] [quote, autor]
		if m := adocBlockAttr.FindStringSubmatch(t); m != nil && !strings.HasPrefix(t, "[[") {
			attrs := m[1]
			first := strings.TrimSpace(strings.SplitN(attrs, ",", 2)[0])
			switch low := strings.ToLower(first); {
			case low == "source" || low == "listing":
				parts := strings.Split(attrs, ",")
				if len(parts) > 1 {
					lang = strings.TrimSpace(parts[1])
				}
			case rstAdmonMap[low] != "":
				admon = rstAdmonMap[low]
			}
			if c := adocColsRe.FindStringSubmatch(attrs); c != nil {
				pendingCols = adocColCount(c[1])
			}
			continue
		}
		// comentarios y atributos del documento
		if strings.HasPrefix(t, "//") {
			continue
		}
		if strings.HasPrefix(t, ":") && adocAttrRe.MatchString(t) {
			continue
		}
		prefix := ""
		if inQuote {
			prefix = "> "
		}
		// encabezados = == ===
		if m := adocHeadRe.FindStringSubmatch(t); m != nil {
			lvl := min(len(m[1]), 6)
			out = append(out, "")
			emitLine(strings.Repeat("#", lvl) + " " + adocInline(m[2], fn))
			out = append(out, "")
			continue
		}
		// titulo de bloque: .Titulo
		if m := adocBlockTitle.FindStringSubmatch(t); m != nil {
			out = append(out, "")
			emitLine(prefix + "**" + adocInline(m[1], fn) + "**")
			continue
		}
		// admoniciones en linea
		if m := adocAdmonRe.FindStringSubmatch(t); m != nil {
			out = append(out, "", "> [!"+rstAdmonMap[strings.ToLower(m[1])]+"]", "> "+adocInline(m[2], fn), "")
			continue
		}
		if admon != "" && t != "" {
			out = append(out, "", "> [!"+admon+"]", "> "+adocInline(t, fn), "")
			admon = ""
			continue
		}
		// listas: * ** *** y . .. ... (y - suelto)
		if m := adocListRe.FindStringSubmatch(t); m != nil && len(m[1]) <= 5 {
			depth := len(m[1]) - 1
			marker := markerBullet
			if m[1][0] == '.' {
				marker = markerOrdered
			}
			// 3 espacios por nivel: alcanzan para anidar tanto bajo "- " (pide 2) como
			// bajo "1. " (pide 3), sin pasarse del limite que la volveria codigo
			emitLine(prefix + strings.Repeat(" ", depth*len(markerOrdered)) + marker + adocInline(m[2], fn))
			continue
		}
		emitLine(prefix + adocInline(ln, fn))
	}
	if inCode {
		out = append(out, "```")
	}
	if table != nil {
		if s := table.render(); s != "" {
			out = append(out, "", s)
		}
	}
	return []byte(docHeaderIfNeeded(out, path) + strings.Join(out, "\n") + "\n" + fn.render()), nil
}

// isCodeFence: ---- y .... (4 o mas) abren/cierran un bloque literal; ++++ es pasante.
func isCodeFence(t string) bool {
	if len(t) < 4 {
		return false
	}
	c := t[0]
	if c != '-' && c != '.' && c != '+' {
		return false
	}
	return strings.Count(t, string(c)) == len(t)
}

// isProseFence: ____ (cita), ==== (ejemplo), **** (barra lateral) y -- (bloque abierto).
func isProseFence(t string) bool {
	if t == "--" {
		return true
	}
	if len(t) < 4 {
		return false
	}
	c := t[0]
	if c != '_' && c != '=' && c != '*' {
		return false
	}
	return strings.Count(t, string(c)) == len(t)
}

// ---- Org-mode ------------------------------------------------------------

var (
	orgLinkRe     = regexp.MustCompile(`\[\[([^\]\[]+)\](?:\[([^\]]+)\])?\]`)
	orgBoldRe     = regexp.MustCompile(`(^|\W)\*([^*\n]+)\*(\W|$)`)
	orgItalRe     = regexp.MustCompile(`(^|\W)/([^/\n]+)/(\W|$)`)
	orgCodeRe     = regexp.MustCompile("(^|\\W)[=~]([^=~\\n]+)[=~](\\W|$)")
	orgStrikRe    = regexp.MustCompile(`(^|\W)\+([^+\n]+)\+(\W|$)`)
	orgUnderRe    = regexp.MustCompile(`(^|\W)_([^_\n]+)_(\W|$)`)
	orgHeadRe     = regexp.MustCompile(`^(\*+)\s+(.*)$`)
	orgStatusRe   = regexp.MustCompile(`^(TODO|DONE|NEXT|WAITING|CANCELLED|HOLD)\s+`)
	orgTagsRe     = regexp.MustCompile(`\s+:[\w:@]+:$`)
	orgListRe     = regexp.MustCompile(`^(\s*)([-+]|\d+[.)])\s+(.*)$`)
	orgDrawerRe   = regexp.MustCompile(`^\s*:([A-Za-z_]+):\s*$`)
	orgPlanningRe = regexp.MustCompile(`^\s*(SCHEDULED|DEADLINE|CLOSED):`)
	orgFnRefRe    = regexp.MustCompile(`\[fn:([\w-]+)\]`)
	orgFnDefRe    = regexp.MustCompile(`^\[fn:([\w-]+)\]\s*(.*)$`)
	imageExtRe    = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|svg|webp|bmp|avif)$`)
)

func orgInline(s string) string {
	s = replaceSubmatches(orgLinkRe, s, func(g []string) string {
		target, text := g[1], g[2]
		file := strings.TrimPrefix(strings.TrimPrefix(target, "file:"), "attachment:")
		switch {
		case text == "" && imageExtRe.MatchString(file):
			return "![](" + file + ")" // [[file:foto.png]] se muestra en linea en Org
		case strings.HasPrefix(target, "*"): // [[*Encabezado]]
			target = "#" + strings.TrimSpace(target[1:])
		case strings.HasPrefix(target, "#"):
		case strings.HasPrefix(target, "file:"):
			target = file
		case !strings.Contains(target, ":"): // destino interno <<asi>>
			target = "#" + target
		}
		if text == "" {
			text = strings.TrimPrefix(g[1], "*")
		}
		return "[" + text + "](" + urlEscape(target) + ")"
	})
	s = orgFnRefRe.ReplaceAllString(s, "[^$1]")
	s = orgCodeRe.ReplaceAllString(s, "$1`$2`$3")
	s = orgBoldRe.ReplaceAllString(s, "$1**$2**$3")
	s = orgItalRe.ReplaceAllString(s, "$1*$2*$3")
	s = orgStrikRe.ReplaceAllString(s, "$1~~$2~~$3")
	s = orgUnderRe.ReplaceAllString(s, "$1++$2++$3")
	return s
}

func convOrg(src []byte, path string) ([]byte, error) {
	lines := splitLines(src)
	var out []string
	title := ""
	inSrc, inQuote, inVerse, inExport, inDrawer := false, false, false, false, false
	exportHTML := false // #+BEGIN_EXPORT html pasa tal cual; latex y compania se saltean
	var table []string  // renglones de la tabla en curso (se vuelca al terminar)

	flushTable := func() {
		if len(table) == 0 {
			return
		}
		hasSep := false
		for _, r := range table {
			if strings.HasPrefix(r, "|-") {
				hasSep = true
				break
			}
		}
		for k, r := range table {
			if strings.HasPrefix(r, "|-") {
				out = append(out, orgSeparator(table, k))
				continue
			}
			out = append(out, orgInline(r))
			if !hasSep && k == 0 { // sin separador GFM no es tabla: va despues de la 1a fila
				out = append(out, orgSeparator(table, 1))
			}
		}
		table = nil
	}

	for _, raw := range lines {
		ln := strings.TrimRight(raw, " \t")
		t := strings.TrimSpace(ln)
		up := strings.ToUpper(t)

		if inSrc {
			if strings.HasPrefix(up, "#+END_SRC") || strings.HasPrefix(up, "#+END_EXAMPLE") {
				out = append(out, "```", "")
				inSrc = false
			} else {
				out = append(out, raw)
			}
			continue
		}
		if inExport {
			if strings.HasPrefix(up, "#+END_EXPORT") {
				inExport = false
				out = append(out, "")
			} else if exportHTML {
				out = append(out, raw)
			}
			continue
		}
		if inDrawer {
			if up == ":END:" {
				inDrawer = false
			}
			continue
		}
		if !strings.HasPrefix(t, "|") {
			flushTable()
		}

		switch {
		case strings.HasPrefix(up, "#+BEGIN_SRC"), strings.HasPrefix(up, "#+BEGIN_EXAMPLE"):
			lang := ""
			if f := strings.Fields(t); len(f) > 1 && strings.HasPrefix(up, "#+BEGIN_SRC") {
				lang = f[1]
			}
			out = append(out, "", "```"+lang)
			inSrc = true
			continue
		case strings.HasPrefix(up, "#+BEGIN_EXPORT"):
			// HTML pasante (el resto de los backends no aplica a un visor)
			f := strings.Fields(up)
			inExport, exportHTML = true, len(f) > 1 && f[1] == "HTML"
			out = append(out, "")
			continue
		case strings.HasPrefix(up, "#+TITLE:"):
			title = strings.TrimSpace(t[8:])
			continue
		case strings.HasPrefix(up, "#+BEGIN_QUOTE"):
			inQuote = true
			out = append(out, "")
			continue
		case strings.HasPrefix(up, "#+END_QUOTE"):
			inQuote = false
			out = append(out, "")
			continue
		case strings.HasPrefix(up, "#+BEGIN_VERSE"):
			inVerse = true
			out = append(out, "")
			continue
		case strings.HasPrefix(up, "#+END_VERSE"):
			inVerse = false
			out = append(out, "")
			continue
		case strings.HasPrefix(t, "#+"), strings.HasPrefix(t, "# ") || t == "#":
			continue // otras directivas y comentarios
		case orgDrawerRe.MatchString(t) && up != ":END:":
			inDrawer = true // :PROPERTIES: … :END: son metadatos, no texto
			continue
		case orgPlanningRe.MatchString(t):
			out = append(out, "*"+mdEscape(t)+"*", "")
			continue
		}

		prefix := ""
		if inQuote {
			prefix = "> "
		}
		// encabezados: * ** ***
		if m := orgHeadRe.FindStringSubmatch(ln); m != nil {
			lvl := min(len(m[1]), 6)
			txt := orgStatusRe.ReplaceAllString(m[2], "**$1** ") // palabras de estado
			txt = orgTagsRe.ReplaceAllString(txt, "")            // etiquetas :tag:
			out = append(out, "", strings.Repeat("#", lvl)+" "+orgInline(txt), "")
			continue
		}
		// notas al pie: [fn:1] definicion (al principio del renglon)
		if m := orgFnDefRe.FindStringSubmatch(t); m != nil {
			out = append(out, "", "[^"+m[1]+"]: "+orgInline(m[2]))
			continue
		}
		// tablas: se juntan y se vuelcan enteras (para poder agregar el separador GFM)
		if strings.HasPrefix(t, "|") {
			table = append(table, t)
			continue
		}
		// listas y casillas
		if m := orgListRe.FindStringSubmatch(ln); m != nil {
			marker := m[2]
			if marker == "+" {
				marker = "-"
			}
			body := m[3]
			if strings.HasPrefix(body, "[X]") { // GFM solo reconoce [x] en minuscula
				body = "[x]" + body[3:]
			}
			out = append(out, prefix+m[1]+marker+" "+orgInline(body))
			continue
		}
		line := prefix + orgInline(ln)
		if inVerse && t != "" {
			line += "  " // el verso respeta cada salto de linea
		}
		out = append(out, line)
	}
	flushTable()
	head := ""
	if title != "" {
		head = "# " + mdEscape(title) + "\n\n"
	}
	return []byte(head + docHeaderIfNeeded(out, path) + strings.Join(out, "\n") + "\n"), nil
}

// orgSeparator arma la fila |---|---| de GFM con tantas columnas como la fila de al lado.
func orgSeparator(table []string, near int) string {
	ref := ""
	for _, k := range []int{near - 1, near + 1, 0} {
		if k >= 0 && k < len(table) && !strings.HasPrefix(table[k], "|-") {
			ref = table[k]
			break
		}
	}
	n := strings.Count(ref, "|") - 1
	if !strings.HasSuffix(ref, "|") {
		n++
	}
	return "|" + strings.Repeat(" --- |", max(n, 1))
}

// ---- MediaWiki -----------------------------------------------------------

var (
	wikiHeadRe   = regexp.MustCompile(`^(={2,6})\s*(.*?)\s*={2,6}\s*$`)
	wikiLinkRe   = regexp.MustCompile(`\[\[([^\]|]+)(?:\|([^\]]+))?\]\]`)
	wikiExtRe    = regexp.MustCompile(`\[(https?://[^\s\]]+)(?:\s+([^\]]+))?\]`)
	wikiTmplRe   = regexp.MustCompile(`\{\{[^{}]*\}\}`)
	wikiRefRe    = regexp.MustCompile(`(?is)<ref(\s+name\s*=\s*"?([^">/]+)"?)?\s*(/>|>(.*?)</ref>)`)
	wikiMagicRe  = regexp.MustCompile(`__[A-Z]+__`)
	wikiRefsTag  = regexp.MustCompile(`(?i)<references\s*/?>`)
	wikiLinkPipe = regexp.MustCompile(`\[\[[^\]]*\]\]`)
)

func wikiInline(s string, fn *footnotes) string {
	for wikiTmplRe.MatchString(s) {
		s = wikiTmplRe.ReplaceAllString(s, "")
	}
	if fn != nil {
		s = replaceSubmatches(wikiRefRe, s, func(g []string) string {
			name, body := strings.TrimSpace(g[2]), g[4]
			if body != "" {
				body = wikiInline(body, nil)
			}
			if name != "" {
				return fn.ref(fn.addNamed(name, body))
			}
			return fn.ref(fn.add(body))
		})
	}
	s = wikiRefsTag.ReplaceAllString(s, "")
	s = wikiMagicRe.ReplaceAllString(s, "")
	s = replaceSubmatches(wikiLinkRe, s, func(g []string) string {
		target, text := g[1], g[2]
		low := strings.ToLower(target)
		if strings.HasPrefix(low, "file:") || strings.HasPrefix(low, "image:") ||
			strings.HasPrefix(low, "archivo:") || strings.HasPrefix(low, "imagen:") ||
			strings.HasPrefix(low, "category:") || strings.HasPrefix(low, "categoría:") {
			return ""
		}
		if text == "" {
			text = target
		}
		return "[[" + target + "|" + text + "]]" // wikilink nativo de Folio
	})
	s = replaceSubmatches(wikiExtRe, s, func(g []string) string {
		if g[2] == "" {
			return "<" + g[1] + ">"
		}
		return "[" + g[2] + "](" + g[1] + ")"
	})
	s = strings.ReplaceAll(s, "'''''", "***")
	s = strings.ReplaceAll(s, "'''", "**")
	s = strings.ReplaceAll(s, "''", "*")
	s = strings.ReplaceAll(s, "<nowiki>", "")
	s = strings.ReplaceAll(s, "</nowiki>", "")
	return s
}

// wikiCellContent separa los atributos de una celda (style="…" | contenido): el '|' que
// cuenta es el primero que no esta dentro de un [[enlace|texto]].
func wikiCellContent(cell string) string {
	masked := wikiLinkPipe.ReplaceAllStringFunc(cell, func(m string) string {
		return strings.Repeat("x", len(m))
	})
	if k := strings.IndexByte(masked, '|'); k >= 0 && strings.Contains(masked[:k], "=") {
		return strings.TrimSpace(cell[k+1:])
	}
	return strings.TrimSpace(cell)
}

// wikiTable convierte {| … |} a GFM. Devuelve la tabla y cuantos renglones consumio.
func wikiTable(lines []string, start int, fn *footnotes) (string, int) {
	var rows [][]string
	var row []string
	caption := ""
	pushRow := func() {
		if len(row) > 0 {
			rows = append(rows, row)
		}
		row = nil
	}
	addCells := func(line, sep string) {
		for _, c := range strings.Split(line, sep) {
			row = append(row, wikiInline(wikiCellContent(c), fn))
		}
	}
	i := start + 1
	for ; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		switch {
		case strings.HasPrefix(t, "|}"):
			pushRow()
			i++
			goto done
		case strings.HasPrefix(t, "|+"):
			caption = wikiInline(strings.TrimSpace(t[2:]), fn)
		case strings.HasPrefix(t, "|-"):
			pushRow()
		case strings.HasPrefix(t, "!"):
			addCells(t[1:], "!!")
		case strings.HasPrefix(t, "|"):
			addCells(t[1:], "||")
		case t != "" && len(row) > 0: // continuacion de la celda anterior
			row[len(row)-1] = strings.TrimSpace(row[len(row)-1] + "<br>" + wikiInline(t, fn))
		}
	}
done:
	if len(rows) == 0 {
		return "", i - start
	}
	width := 0
	for _, r := range rows {
		width = max(width, len(r))
	}
	for len(rows[0]) < width {
		rows[0] = append(rows[0], "")
	}
	out := ""
	if caption != "" {
		out = "*" + caption + "*\n\n"
	}
	return out + strings.TrimRight(mdTable(rows[0], rows[1:]), "\n"), i - start
}

func convWiki(src []byte, path string) ([]byte, error) {
	lines := splitLines(src)
	var out []string
	fn := &footnotes{}
	prevTerm := false
	for i := 0; i < len(lines); i++ {
		t := strings.TrimRight(lines[i], " \t")
		isTerm := false
		switch {
		case strings.HasPrefix(strings.TrimSpace(t), "{|"):
			tbl, adv := wikiTable(lines, i, fn)
			if tbl != "" {
				out = append(out, "", tbl, "")
			}
			i += adv - 1
		case strings.HasPrefix(strings.ToUpper(t), "#REDIRECT"):
			out = append(out, "*Redirige a* "+wikiInline(strings.TrimSpace(t[len("#REDIRECT"):]), fn))
		case wikiHeadRe.MatchString(t):
			m := wikiHeadRe.FindStringSubmatch(t)
			out = append(out, "", strings.Repeat("#", len(m[1]))+" "+wikiInline(m[2], fn), "")
		case strings.HasPrefix(t, "----"):
			out = append(out, "", "---", "")
		case strings.HasPrefix(t, "*") || strings.HasPrefix(t, "#"):
			depth := 0
			for depth < len(t) && (t[depth] == '*' || t[depth] == '#') {
				depth++
			}
			var indent strings.Builder
			for k := 0; k < depth-1; k++ { // la sangria depende de cada nivel ancestro
				if t[k] == '#' {
					indent.WriteString("   ")
				} else {
					indent.WriteString("  ")
				}
			}
			marker := markerBullet
			if t[depth-1] == '#' {
				marker = markerOrdered
			}
			out = append(out, indent.String()+marker+wikiInline(strings.TrimSpace(t[depth:]), fn))
		case strings.HasPrefix(t, ";"):
			// lista de definicion: ;termino : definicion (o la definicion en el renglon siguiente)
			term, def, hasDef := strings.Cut(t[1:], " : ")
			out = append(out, "", wikiInline(strings.TrimSpace(term), fn))
			if hasDef {
				out = append(out, ": "+wikiInline(strings.TrimSpace(def), fn))
			}
			isTerm = !hasDef
		case strings.HasPrefix(t, ":"):
			body := wikiInline(strings.TrimSpace(strings.TrimLeft(t, ":")), fn)
			if prevTerm {
				out = append(out, ": "+body)
			} else {
				out = append(out, "> "+body) // ':' suelto = sangria (en las discusiones, respuesta)
			}
		case strings.HasPrefix(t, " "):
			out = append(out, t) // linea preformateada
		default:
			out = append(out, wikiInline(t, fn))
		}
		prevTerm = isTerm
	}
	return []byte(docHeaderIfNeeded(out, path) + strings.Join(out, "\n") + "\n" + fn.render()), nil
}

// ---- utilidades ----------------------------------------------------------

func splitLines(src []byte) []string {
	s := strings.ReplaceAll(string(src), "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Split(s, "\n")
}

// docHeaderIfNeeded agrega un H1 con el nombre del archivo solo si el documento
// convertido no empieza con uno propio.
func docHeaderIfNeeded(out []string, path string) string {
	for _, l := range out {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "# ") {
			return ""
		}
		break
	}
	return docHeader(path, FormatName(path))
}
