package main

// htmlmd.go — HTML -> Markdown, sin dependencias.
//
// Lo usan tres formatos: los .html/.xhtml sueltos, los capitulos de un EPUB y las salidas HTML
// de un notebook. Convertir a Markdown (en vez de pasar el HTML crudo) tiene una ventaja
// concreta: los encabezados entran al indice, la busqueda funciona y el resultado se ve con la
// misma tipografia que el resto de Folio.
//
// El parser es chico pero sigue las reglas del estandar que importan para LEER: los elementos
// vacios, los cierres implicitos (un <li> cierra al <li> abierto de su lista, un <tr> cierra la
// celda y la fila abiertas, un bloque cierra un <p>), el texto crudo de <script>/<style>/<title>
// y un '<' suelto como texto ("a < b" no se come nada). No arma el arbol conforme al 100 %, pero
// aguanta el HTML del mundo real que uno abre para leer.

import (
	"html"
	"regexp"
	"strconv"
	"strings"
)

// ---- arbol ---------------------------------------------------------------

type hNode struct {
	Name string // "" para los nodos de texto
	Text string
	Attr map[string]string
	Kids []*hNode
}

func (n *hNode) attr(k string) string {
	if n.Attr == nil {
		return ""
	}
	return n.Attr[k]
}

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

var (
	voidTags = set("area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta",
		"param", "source", "track", "wbr", "image")

	// Contenido que no se parsea como HTML (texto crudo / RCDATA).
	rawTextTags = set("script", "style", "textarea", "title", "xmp", "noembed", "noframes", "iframe")

	// Elementos de bloque que, al abrirse, cierran un <p> abierto (regla "close a p element
	// in button scope" del estandar).
	closesParagraph = set("address", "article", "aside", "blockquote", "center", "details",
		"dialog", "dir", "div", "dl", "fieldset", "figcaption", "figure", "footer", "form",
		"h1", "h2", "h3", "h4", "h5", "h6", "header", "hgroup", "hr", "main", "menu", "nav",
		"ol", "p", "pre", "section", "summary", "table", "ul", "li", "dd", "dt")

	// Limites de alcance: una busqueda de "elemento abierto" no sigue de largo por estos.
	scopeButton  = set("button", "table", "td", "th", "caption", "marquee", "object", "template", "html")
	scopeList    = set("ul", "ol", "menu", "table", "td", "th", "template")
	scopeDefList = set("dl", "table", "td", "th", "template")
	scopeRow     = set("table", "tbody", "thead", "tfoot", "template")
	scopeCell    = set("tr", "table", "template")
	scopeSection = set("table", "template")

	headingTags = set("h1", "h2", "h3", "h4", "h5", "h6")

	// Conjuntos de cierre, armados una vez (impliedEnds corre por cada etiqueta que abre).
	onlyP        = set("p")
	onlyLi       = set("li")
	dtOrDd       = set("dt", "dd")
	onlyTr       = set("tr")
	tdOrTh       = set("td", "th")
	tableSection = set("tbody", "thead", "tfoot")
	onlyOption   = set("option")
	scopeSelect  = set("select", "datalist")
)

// htmlParser mantiene la pila de elementos abiertos mientras recorre el documento.
type htmlParser struct {
	root  *hNode
	stack []*hNode
}

func (p *htmlParser) top() *hNode { return p.stack[len(p.stack)-1] }

func (p *htmlParser) appendKid(n *hNode) { p.top().Kids = append(p.top().Kids, n) }

func (p *htmlParser) push(n *hNode) {
	p.appendKid(n)
	p.stack = append(p.stack, n)
}

// closeInScope cierra (con todo lo que tenga encima) el elemento abierto mas cercano cuyo
// nombre este en names, siempre que aparezca ANTES de un limite de alcance. O(profundidad).
func (p *htmlParser) closeInScope(names, boundary map[string]bool) {
	for i := len(p.stack) - 1; i > 0; i-- {
		n := p.stack[i].Name
		if names[n] {
			p.stack = p.stack[:i]
			return
		}
		if boundary[n] {
			return
		}
	}
}

// closeTag atiende una etiqueta de cierre: cierra hasta el elemento con ese nombre, si esta
// abierto; una etiqueta de cierre huerfana no rompe nada.
func (p *htmlParser) closeTag(name string) {
	for i := len(p.stack) - 1; i > 0; i-- {
		if p.stack[i].Name == name {
			p.stack = p.stack[:i]
			return
		}
	}
}

// impliedEnds aplica los cierres implicitos que dispara abrir `name`.
func (p *htmlParser) impliedEnds(name string) {
	if closesParagraph[name] {
		p.closeInScope(onlyP, scopeButton)
	}
	switch {
	case name == "li":
		p.closeInScope(onlyLi, scopeList)
	case name == "dt" || name == "dd":
		p.closeInScope(dtOrDd, scopeDefList)
	case name == "tr":
		p.closeInScope(onlyTr, scopeRow)
	case name == "td" || name == "th":
		p.closeInScope(tdOrTh, scopeCell)
	case tableSection[name]:
		p.closeInScope(tableSection, scopeSection)
	case name == "option":
		p.closeInScope(onlyOption, scopeSelect)
	case headingTags[name]:
		if headingTags[p.top().Name] { // <h2> dentro de un <h1> abierto: el estandar lo cierra
			p.stack = p.stack[:len(p.stack)-1]
		}
	}
}

// parseHTML arma el arbol. Una sola pasada sobre src: O(n) salvo la busqueda de cierre de
// los elementos de texto crudo, que tambien es lineal (indexFoldASCII).
func parseHTML(src string) *hNode {
	p := &htmlParser{root: &hNode{Name: "#root"}}
	p.stack = []*hNode{p.root}
	addText := func(s string) {
		if s != "" {
			p.appendKid(&hNode{Text: html.UnescapeString(s)})
		}
	}

	i := 0
	for i < len(src) {
		lt := strings.IndexByte(src[i:], '<')
		if lt < 0 {
			addText(src[i:])
			break
		}
		addText(src[i : i+lt])
		i += lt
		rest := src[i:]
		switch {
		case strings.HasPrefix(rest, "<!--"):
			if end := strings.Index(rest[4:], "-->"); end >= 0 {
				i += 4 + end + 3
			} else {
				i = len(src)
			}
			continue
		case strings.HasPrefix(rest, "<![CDATA["):
			if end := strings.Index(rest[9:], "]]>"); end >= 0 {
				p.appendKid(&hNode{Text: rest[9 : 9+end]})
				i += 9 + end + 3
			} else {
				i = len(src)
			}
			continue
		case strings.HasPrefix(rest, "<!") || strings.HasPrefix(rest, "<?"):
			if end := strings.IndexByte(rest, '>'); end >= 0 {
				i += end + 1
			} else {
				i = len(src)
			}
			continue
		case len(rest) < 2 || !(isASCIILetter(rest[1]) || rest[1] == '/'):
			// "a < b", "x <= y": un '<' que no abre etiqueta es texto (asi lo lee el navegador)
			addText("<")
			i++
			continue
		}
		gt := strings.IndexByte(rest, '>')
		if gt < 0 {
			addText(rest)
			break
		}
		tag := rest[1:gt]
		i += gt + 1
		if strings.HasPrefix(tag, "/") {
			p.closeTag(strings.ToLower(strings.TrimSpace(tag[1:])))
			continue
		}
		selfClose := strings.HasSuffix(tag, "/")
		name, attrs := parseTag(strings.TrimSuffix(tag, "/"))
		if name == "" {
			continue
		}
		p.impliedEnds(name)
		n := &hNode{Name: name, Attr: attrs}
		if voidTags[name] || selfClose {
			p.appendKid(n)
			continue
		}
		if rawTextTags[name] {
			closeIdx := indexFoldASCII(src[i:], "</"+name)
			body := src[i:]
			if closeIdx >= 0 {
				body = src[i : i+closeIdx]
			}
			if name == "title" || name == "textarea" {
				body = html.UnescapeString(body) // RCDATA: las entidades SI cuentan
			}
			n.Kids = append(n.Kids, &hNode{Text: body})
			p.appendKid(n)
			if closeIdx < 0 {
				i = len(src)
			} else {
				i += closeIdx
				if end := strings.IndexByte(src[i:], '>'); end >= 0 {
					i += end + 1
				}
			}
			continue
		}
		p.push(n)
	}
	return p.root
}

func isASCIILetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// indexFoldASCII busca needle (ASCII, en minusculas) en s sin distinguir mayusculas y
// devuelve un indice de BYTES de s. La version anterior hacia strings.ToLower(s): O(n)
// por cada <script> (O(n²) en un HTML con muchos) y, peor, ToLower puede cambiar el largo
// en bytes de algunos caracteres Unicode, con lo que el indice ya no correspondia a s.
func indexFoldASCII(s, needle string) int {
	n := len(needle)
	for i := 0; i+n <= len(s); i++ {
		j := 0
		for j < n {
			c := s[i+j]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != needle[j] {
				break
			}
			j++
		}
		if j == n {
			return i
		}
	}
	return -1
}

// parseTag separa "div class='x' id=y" en el nombre y sus atributos.
func parseTag(tag string) (string, map[string]string) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return "", nil
	}
	j := 0
	for j < len(tag) && !isSpaceByte(tag[j]) {
		j++
	}
	name := strings.ToLower(tag[:j])
	attrs := map[string]string{}
	rest := tag[j:]
	for {
		rest = strings.TrimLeft(rest, " \t\r\n")
		if rest == "" {
			break
		}
		eq := 0
		for eq < len(rest) && rest[eq] != '=' && !isSpaceByte(rest[eq]) {
			eq++
		}
		key := strings.ToLower(rest[:eq])
		rest = strings.TrimLeft(rest[eq:], " \t\r\n")
		val := ""
		if strings.HasPrefix(rest, "=") {
			rest = strings.TrimLeft(rest[1:], " \t\r\n")
			if rest != "" && (rest[0] == '"' || rest[0] == '\'') {
				q := rest[0]
				end := strings.IndexByte(rest[1:], q)
				if end < 0 {
					val, rest = rest[1:], ""
				} else {
					val, rest = rest[1:1+end], rest[1+end+1:]
				}
			} else {
				end := 0
				for end < len(rest) && !isSpaceByte(rest[end]) {
					end++
				}
				val, rest = rest[:end], rest[end:]
			}
		}
		if key != "" {
			attrs[key] = html.UnescapeString(val)
		}
	}
	return name, attrs
}

// ---- conversion a Markdown ----------------------------------------------

// htmlOptions ajusta la conversion segun de donde venga el HTML.
type htmlOptions struct {
	resolveImage func(string) string // reescribe el src de las imagenes ("" = descartarla)
	resolveLink  func(string) string // reescribe el href de los enlaces ("" = solo el texto)
	keepIDs      bool                // conservar los id del documento como anclas
	idPrefix     string              // espacio de nombres de esos id (capitulos de un EPUB)
}

type mdWriter struct {
	b       strings.Builder
	opts    *htmlOptions
	listStk []listState
	quote   int
	tail    [2]byte  // los dos ultimos bytes escritos (para saber si estamos en un renglon nuevo)
	fresh   bool     // renglon nuevo tras un <br>, con la sangria ya escrita
	inline  bool     // sub-escritor de una sola linea (celdas, enlaces): <br> sale como <br>
	pending []string // anclas de contenedores que esperan al primer texto (ver flushAnchors)
}

// listState: un nivel de lista abierta. width es el ancho del marcador del item en curso
// ("- " = 2, "1. " = 3, "10. " = 4): es la sangria que necesita su contenido para quedar
// DENTRO del item. Con dos espacios fijos, lo anidado bajo un "1. " se escapaba de la lista.
type listState struct {
	ordered bool
	index   int
	width   int
}

// htmlToMarkdown convierte un fragmento (o documento) HTML a Markdown.
func htmlToMarkdown(src string, opts *htmlOptions) string {
	return htmlTreeToMarkdown(parseHTML(src), opts)
}

// htmlTreeToMarkdown convierte un arbol ya parseado (asi el que ademas necesita el
// <title> no parsea dos veces).
func htmlTreeToMarkdown(root *hNode, opts *htmlOptions) string {
	if opts == nil {
		opts = &htmlOptions{}
	}
	if body := findNode(root, "body"); body != nil {
		root = body
	}
	w := &mdWriter{opts: opts}
	w.walkKids(root)
	return strings.TrimSpace(collapseBlankLines(w.b.String())) + "\n"
}

// HTMLTitle saca el <title> de un documento (o, si no tiene, su primer <h1>).
func HTMLTitle(src string) string { return htmlTitle(parseHTML(src)) }

func htmlTitle(root *hNode) string {
	if t := findNode(root, "title"); t != nil {
		if s := strings.TrimSpace(collapseSpaces(collectText(t))); s != "" {
			return s
		}
	}
	if h := findNode(root, "h1"); h != nil {
		return strings.TrimSpace(collapseSpaces(collectText(h)))
	}
	return ""
}

func findNode(n *hNode, name string) *hNode {
	for _, k := range n.Kids {
		if k.Name == name {
			return k
		}
		if f := findNode(k, name); f != nil {
			return f
		}
	}
	return nil
}

func collectText(n *hNode) string {
	var b strings.Builder
	var rec func(*hNode)
	rec = func(x *hNode) {
		if x.Name == "" {
			b.WriteString(x.Text)
			return
		}
		for _, k := range x.Kids {
			rec(k)
		}
	}
	rec(n)
	return b.String()
}

func (w *mdWriter) write(s string) {
	if s == "" {
		return
	}
	w.fresh = false
	w.b.WriteString(s)
	if len(s) >= 2 {
		w.tail = [2]byte{s[len(s)-2], s[len(s)-1]}
	} else {
		w.tail = [2]byte{w.tail[1], s[0]}
	}
}

func (w *mdWriter) atLineStart() bool { return w.b.Len() == 0 || w.tail[1] == '\n' }

// nl asegura que estamos al principio de una linea.
func (w *mdWriter) nl() {
	if !w.atLineStart() {
		w.write("\n")
	}
}

// blank abre un parrafo nuevo (linea en blanco de por medio).
func (w *mdWriter) blank() {
	w.nl()
	if w.b.Len() > 0 && !(w.tail[0] == '\n' && w.tail[1] == '\n') {
		w.write("\n")
	}
}

// quotePrefix / contentIndent / markerIndent: lo que va al principio de cada renglon.
// El contenido de un item va sangrado por el ancho de TODOS los marcadores que lo
// contienen; el marcador del item, por todos menos el suyo.
func (w *mdWriter) quotePrefix() string { return strings.Repeat("> ", w.quote) }

func (w *mdWriter) listIndent(levels int) string {
	n := 0
	for i := 0; i < levels && i < len(w.listStk); i++ {
		n += w.listStk[i].width
	}
	return strings.Repeat(" ", n)
}

func (w *mdWriter) contentIndent() string { return w.quotePrefix() + w.listIndent(len(w.listStk)) }
func (w *mdWriter) markerIndent() string  { return w.quotePrefix() + w.listIndent(len(w.listStk)-1) }

func (w *mdWriter) walkKids(n *hNode) {
	for _, k := range n.Kids {
		w.walk(k)
	}
}

// anchor devuelve el id (ya con el prefijo del documento) de un elemento, si se conservan.
func (w *mdWriter) anchor(n *hNode) string {
	if !w.opts.keepIDs {
		return ""
	}
	id := n.attr("id")
	if id == "" && n.Name == "a" {
		id = n.attr("name")
	}
	if id == "" {
		return ""
	}
	return w.opts.idPrefix + sanitizeID(id)
}

// sanitizeID deja un id apto para el atributo {#id} de goldmark y para un id de HTML.
func sanitizeID(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r == '-' || r == '_' || r == ':' || r == '.' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r > 127:
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// Las anclas van como <span id="…"></span>: un <a> dentro del texto de un enlace
// anidaria enlaces (el navegador los parte), un span no molesta en ningun lado.
func (w *mdWriter) anchorTag(id string) string {
	return `<span id="` + html.EscapeString(id) + `"></span>`
}

// deferAnchor guarda el ancla de un CONTENEDOR (div, section, un marcador de pagina vacio)
// para pegarla al primer texto que venga: escrita sola en su renglon seria un parrafo vacio,
// y un EPUB con un id por parrafo quedaba lleno de huecos.
func (w *mdWriter) deferAnchor(id string) {
	if id != "" {
		w.pending = append(w.pending, id)
	}
}

// flushAnchors escribe las anclas pendientes justo antes del contenido que las recibe.
func (w *mdWriter) flushAnchors() {
	for _, id := range w.pending {
		w.write(w.anchorTag(id))
	}
	w.pending = w.pending[:0]
}

// flushAnchorsAsLine: antes de un bloque que no admite anclas en linea (codigo, tabla,
// regla), quedan en un renglon propio.
func (w *mdWriter) flushAnchorsAsLine() {
	if len(w.pending) == 0 {
		return
	}
	w.blank()
	w.write(w.contentIndent())
	w.flushAnchors()
	w.blank()
}

func (w *mdWriter) walk(n *hNode) {
	if n.Name == "" {
		w.text(n.Text)
		return
	}

	switch n.Name {
	case "script", "style", "head", "meta", "link", "noscript", "iframe", "form", "button",
		"title", "template", "select", "textarea", "object", "noembed", "noframes":
		return

	case "svg":
		w.svg(n)

	case "h1", "h2", "h3", "h4", "h5", "h6":
		lvl := int(n.Name[1] - '0')
		w.blank()
		w.write(w.contentIndent() + strings.Repeat("#", lvl) + " ")
		w.flushAnchors()
		w.write(strings.TrimSpace(inlineOf(n, w.opts)))
		if id := w.anchor(n); id != "" {
			w.write(" {#" + id + "}")
		}
		w.nl()
		w.write("\n")

	case "p", "div", "section", "article", "main", "header", "footer", "figcaption", "address",
		"aside", "nav", "center", "hgroup", "fieldset", "dialog":
		w.blank()
		w.deferAnchor(w.anchor(n))
		w.walkKids(n)
		w.blank()

	case "br":
		if w.inline {
			w.write("<br>")
		} else {
			w.write("  \n" + w.contentIndent())
			w.fresh = true
		}

	case "hr":
		w.flushAnchorsAsLine()
		w.blank()
		w.write(w.contentIndent() + "---\n\n")

	case "strong", "b":
		w.wrapInline(n, "**", "**")
	case "em", "i", "cite", "var", "dfn":
		w.wrapInline(n, "*", "*")
	case "del", "s", "strike":
		w.wrapInline(n, "~~", "~~")
	case "mark":
		w.wrapInline(n, "==", "==")
	case "ins", "u":
		w.wrapInline(n, "++", "++")
	// sup/sub/kbd van como HTML crudo: la sintaxis ^x^ / ~x~ no admite espacios adentro
	// ("^tm registrada^" quedaba con los circunflejos a la vista) y <kbd> tiene su estilo.
	case "sup":
		w.wrapInline(n, "<sup>", "</sup>")
	case "sub":
		w.wrapInline(n, "<sub>", "</sub>")
	case "kbd":
		w.wrapInline(n, "<kbd>", "</kbd>")
	case "samp", "tt":
		w.flushAnchors()
		w.write(codeSpan(collapseSpaces(collectText(n))))

	case "code":
		w.flushAnchors()
		w.write(codeSpan(collapseSpaces(collectText(n))))

	case "pre":
		w.flushAnchorsAsLine()
		w.blank()
		lang := langFromClass(n)
		if lang == "" {
			if c := firstChild(n, "code"); c != nil {
				lang = langFromClass(c)
			}
		}
		txt := strings.Trim(collectText(n), "\n")
		block := codeBlock(txt, lang)
		if ind := w.contentIndent(); ind != "" {
			for _, ln := range strings.Split(strings.TrimRight(block, "\n"), "\n") {
				w.write(ind + ln + "\n")
			}
			w.write("\n")
		} else {
			w.write(block)
		}

	case "blockquote":
		w.blank()
		w.quote++
		w.walkKids(n)
		w.quote--
		w.blank()

	case "ul", "ol", "menu", "dir":
		if len(w.listStk) == 0 {
			w.blank()
		} else {
			w.nl()
		}
		w.listStk = append(w.listStk, listState{ordered: n.Name == "ol", index: startIndex(n)})
		w.walkKids(n)
		w.listStk = w.listStk[:len(w.listStk)-1]
		if len(w.listStk) == 0 {
			w.blank()
		}

	case "li":
		if len(w.listStk) == 0 { // <li> suelto, sin lista: lo tratamos como vineta
			w.listStk = append(w.listStk, listState{})
			defer func() { w.listStk = w.listStk[:len(w.listStk)-1] }()
		}
		st := &w.listStk[len(w.listStk)-1]
		w.nl()
		marker := "- "
		if st.ordered {
			marker = strconv.Itoa(st.index) + ". "
			st.index++
		}
		st.width = len(marker)
		w.write(w.markerIndent() + marker)
		w.deferAnchor(w.anchor(n))
		w.walkKids(n)
		w.nl()

	case "dl":
		w.blank()
		w.walkKids(n)
		w.blank()
	case "dt":
		w.nl()
		w.write(w.contentIndent() + strings.TrimSpace(inlineOf(n, w.opts)))
		w.nl()
	case "dd":
		w.nl()
		w.write(w.contentIndent() + ": " + strings.TrimSpace(inlineOf(n, w.opts)))
		w.nl()

	case "a":
		w.link(n)

	case "img":
		src := n.attr("src")
		if src == "" {
			src = firstSrcsetURL(n.attr("srcset"))
		}
		w.image(src, n.attr("alt"))

	case "table":
		w.flushAnchorsAsLine()
		w.blank()
		w.table(n)
		w.blank()

	default: // span, font, small, figure, details, summary, abbr, time, label, picture…
		w.deferAnchor(w.anchor(n))
		w.walkKids(n)
	}
}

// text escribe un nodo de texto: espacios colapsados como en el navegador, y escapado lo
// justo para que el texto no se convierta en sintaxis de Markdown.
func (w *mdWriter) text(raw string) {
	txt := collapseSpaces(raw)
	if txt == "" {
		return
	}
	switch {
	case w.atLineStart(), w.fresh:
		txt = strings.TrimLeft(txt, " ")
		if txt == "" {
			return
		}
		if !w.fresh {
			w.write(w.contentIndent())
		}
		w.flushAnchors()
		w.write(escapeLineStart(escapeInline(txt)))
	default:
		if txt[0] == ' ' && w.tail[1] == ' ' { // "a " + " b": un solo espacio, como el navegador
			txt = txt[1:]
			if txt == "" {
				return
			}
		}
		w.flushAnchors()
		w.write(escapeInline(txt))
	}
}

// link: <a href> con texto; si no hay destino util queda solo el texto (o el ancla).
func (w *mdWriter) link(n *hNode) {
	id := w.anchor(n)
	href := n.attr("href")
	if href != "" && w.opts.resolveLink != nil {
		href = w.opts.resolveLink(href)
	}
	raw := inlineOf(n, w.opts)
	w.deferAnchor(id)
	if href == "" || strings.HasPrefix(strings.ToLower(href), "javascript:") {
		w.inlineSpan(raw, func(inner string) string { return inner })
		return // un <a id> sin texto deja su ancla pendiente: la recibe lo que sigue
	}
	if strings.Trim(raw, " ") == "" {
		raw = escapeInline(href)
	}
	w.inlineSpan(raw, func(inner string) string { return "[" + inner + "](" + urlEscape(href) + ")" })
}

func (w *mdWriter) image(src, alt string) {
	if src != "" && w.opts.resolveImage != nil {
		src = w.opts.resolveImage(src)
	}
	if src == "" {
		return
	}
	if w.atLineStart() {
		w.write(w.contentIndent())
	}
	w.flushAnchors()
	w.write("![" + escapeInline(strings.TrimSpace(alt)) + "](" + urlEscape(src) + ")")
}

// svg: el SVG en linea no se puede mostrar como Markdown, pero en un EPUB la tapa suele
// venir como <svg><image xlink:href="tapa.jpg"/></svg>: esa imagen si se rescata.
func (w *mdWriter) svg(n *hNode) {
	var rec func(*hNode)
	rec = func(x *hNode) {
		for _, k := range x.Kids {
			if k.Name == "image" {
				src := k.attr("href")
				if src == "" {
					src = k.attr("xlink:href")
				}
				w.blank()
				w.image(src, n.attr("aria-label"))
				w.blank()
				continue
			}
			rec(k)
		}
	}
	rec(n)
}

func (w *mdWriter) wrapInline(n *hNode, open, close string) {
	w.inlineSpan(inlineOf(n, w.opts), func(inner string) string { return open + inner + close })
}

// inlineSpan escribe un elemento en linea cuyo contenido ya viene colapsado. Los espacios de
// sus bordes van AFUERA de la marca ("<b> hola </b>" -> " **hola** "): adentro Markdown no
// reconoce el enfasis, y tirarlos pegaba la palabra con la de al lado.
func (w *mdWriter) inlineSpan(raw string, mark func(inner string) string) {
	inner := strings.Trim(raw, " ")
	if inner == "" {
		if raw != "" && !w.atLineStart() && w.tail[1] != ' ' {
			w.write(" ")
		}
		return
	}
	if w.atLineStart() {
		w.write(w.contentIndent())
	} else if raw[0] == ' ' && w.tail[1] != ' ' {
		w.write(" ")
	}
	w.flushAnchors()
	w.write(mark(inner))
	if raw[len(raw)-1] == ' ' {
		w.write(" ")
	}
}

// inlineOf convierte un subarbol a Markdown en una sola linea.
func inlineOf(n *hNode, opts *htmlOptions) string {
	sub := &mdWriter{opts: opts, inline: true}
	sub.walkKids(n)
	return collapseSpaces(strings.ReplaceAll(sub.b.String(), "\n", " "))
}

func (w *mdWriter) table(n *hNode) {
	var rows [][]string
	var rec func(*hNode)
	rec = func(x *hNode) {
		for _, k := range x.Kids {
			switch k.Name {
			case "tr":
				var row []string
				for _, c := range k.Kids {
					if c.Name == "td" || c.Name == "th" {
						row = append(row, inlineOf(c, w.opts))
					}
				}
				if len(row) > 0 {
					rows = append(rows, row)
				}
			case "table": // tabla anidada: no se aplana dentro de la de afuera
			default:
				rec(k)
			}
		}
	}
	rec(n)
	if len(rows) == 0 {
		return
	}
	width := 0
	for _, r := range rows {
		width = max(width, len(r))
	}
	head := rows[0]
	for len(head) < width { // una primera fila con celdas combinadas no puede recortar la tabla
		head = append(head, "")
	}
	ind := w.contentIndent()
	for _, ln := range strings.Split(strings.TrimRight(mdTable(head, rows[1:]), "\n"), "\n") {
		w.write(ind + ln + "\n")
	}
}

func firstChild(n *hNode, name string) *hNode {
	for _, k := range n.Kids {
		if k.Name == name {
			return k
		}
	}
	return nil
}

// firstSrcsetURL: "a.jpg 1x, b.jpg 2x" -> "a.jpg".
func firstSrcsetURL(srcset string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(srcset), ",")
	if f := strings.Fields(first); len(f) > 0 {
		return f[0]
	}
	return ""
}

// langFromClass saca "go" de class="language-go" / "lang-go" / "highlight-go" / "brush: go".
func langFromClass(n *hNode) string {
	for _, cls := range strings.Fields(n.attr("class")) {
		for _, pre := range []string{"language-", "lang-", "highlight-", "brush:"} {
			if strings.HasPrefix(cls, pre) {
				return strings.TrimPrefix(cls, pre)
			}
		}
	}
	return ""
}

func startIndex(n *hNode) int {
	if v, err := strconv.Atoi(strings.TrimSpace(n.attr("start"))); err == nil && v >= 0 {
		return v
	}
	return 1
}

// codeSpan arma un `codigo` en linea que aguanta backticks adentro: la cerca es una
// racha mas larga que la mas larga del contenido, con espacio si el borde es un backtick.
func codeSpan(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	longest, run := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", longest+1)
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		s = " " + s + " "
	}
	return fence + s + fence
}

// collapseSpaces normaliza los espacios como haria un navegador: cada racha de blancos
// vale UN espacio, tambien en los bordes. Ese espacio del borde es el que separa las
// palabras de dos nodos vecinos: la version anterior lo tiraba al principio del nodo y
// "<b>negrita</b> texto" salia "**negrita**texto", con las palabras pegadas. El espacio
// duro (U+00A0) NO se colapsa: el autor lo puso justamente para que no se parta.
func collapseSpaces(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r', '\f':
			space = true
		default:
			if space {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		}
	}
	if space {
		b.WriteByte(' ')
	}
	return b.String()
}

// inlineEscaper protege lo minimo indispensable: si escapamos de mas, el texto del
// documento se llena de contrabarras. Se arma una sola vez (ver mdEscaper).
var inlineEscaper = strings.NewReplacer(`\`, `\\`, "`", "\\`", `*`, `\*`, `_`, `\_`,
	`[`, `\[`, `]`, `\]`, `<`, `\<`)

func escapeInline(s string) string { return inlineEscaper.Replace(s) }

// lineStartSyntax: lo que, al PRINCIPIO de un renglon, Markdown leeria como estructura
// ("# " titulo, "> " cita, "- " / "+ " vineta, "12. " / "3) " lista numerada).
var lineStartSyntax = regexp.MustCompile(`^(#{1,6}\s|>|[-+]\s|\d{1,9}[.)]\s)`)

func escapeLineStart(s string) string {
	if m := lineStartSyntax.FindStringIndex(s); m != nil {
		// la barra va justo antes del signo que dispara la sintaxis
		if s[0] >= '0' && s[0] <= '9' {
			k := strings.IndexAny(s, ".)")
			return s[:k] + `\` + s[k:]
		}
		return `\` + s
	}
	return s
}

// urlEscape deja la URL utilizable dentro de (…) de Markdown.
func urlEscape(u string) string {
	if strings.ContainsAny(u, " ()<>") {
		return "<" + strings.NewReplacer("<", "%3C", ">", "%3E").Replace(u) + ">"
	}
	return u
}

// collapseBlankLines deja como maximo una linea en blanco seguida, en UNA pasada, y sin
// tocar el interior de los bloques de codigo: ahi las lineas en blanco son del autor (antes
// se colapsaban tambien, y un programa con dos renglones vacios cambiaba).
func collapseBlankLines(md string) string {
	var b strings.Builder
	b.Grow(len(md))
	blanks := 0
	fence := ""
	for _, line := range strings.SplitAfter(md, "\n") {
		core := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "> "))
		if fence != "" {
			if strings.HasPrefix(core, fence) && strings.Trim(core, fence[:1]) == "" {
				fence = ""
			}
			b.WriteString(line)
			continue
		}
		if core == "" && strings.TrimSpace(line) == "" {
			blanks++
			if blanks > 1 {
				continue
			}
		} else {
			blanks = 0
		}
		if f := fenceOf(core); f != "" {
			fence = f
		}
		b.WriteString(line)
	}
	return b.String()
}

// fenceOf devuelve la cerca (``` o ~~~, con su largo) si el renglon abre un bloque.
func fenceOf(core string) string {
	for _, c := range []byte{'`', '~'} {
		n := 0
		for n < len(core) && core[n] == c {
			n++
		}
		if n >= 3 {
			return core[:n]
		}
	}
	return ""
}
