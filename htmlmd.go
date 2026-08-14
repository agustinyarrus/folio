package main

// htmlmd.go — HTML -> Markdown, sin dependencias.
//
// Lo usan tres formatos: los .html/.xhtml sueltos, los capitulos de un EPUB y las salidas HTML
// de un notebook. Convertir a Markdown (en vez de pasar el HTML crudo) tiene una ventaja
// concreta: los encabezados entran al indice, la busqueda funciona y el resultado se ve con la
// misma tipografia que el resto de Folio.
//
// El parser es deliberadamente chico y tolerante: cierra solo los elementos vacios, deja que un
// <li> cierre al <li> anterior y que una etiqueta de cierre huerfana no rompa el arbol. No es un
// parser conforme al estandar, pero aguanta el HTML del mundo real que uno abre para leer.

import (
	"html"
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

var voidTags = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true,
	"img": true, "input": true, "link": true, "meta": true, "param": true,
	"source": true, "track": true, "wbr": true,
}

// Etiquetas que, al abrirse, cierran a una hermana del mismo tipo todavia abierta.
var closesSelf = map[string]bool{
	"li": true, "p": true, "td": true, "th": true, "tr": true, "dt": true, "dd": true, "option": true,
}

// Contenido que no se parsea como HTML.
var rawTextTags = map[string]bool{"script": true, "style": true, "textarea": true}

func parseHTML(src string) *hNode {
	root := &hNode{Name: "#root"}
	stack := []*hNode{root}
	top := func() *hNode { return stack[len(stack)-1] }
	push := func(n *hNode) { top().Kids = append(top().Kids, n); stack = append(stack, n) }
	addText := func(s string) {
		if s == "" {
			return
		}
		top().Kids = append(top().Kids, &hNode{Text: html.UnescapeString(s)})
	}
	// cierra hasta (e incluyendo) la etiqueta name; si no esta abierta, no hace nada
	closeTag := func(name string) {
		for i := len(stack) - 1; i > 0; i-- {
			if stack[i].Name == name {
				stack = stack[:i]
				return
			}
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
		// comentarios, doctype, CDATA
		if strings.HasPrefix(src[i:], "<!--") {
			if end := strings.Index(src[i+4:], "-->"); end >= 0 {
				i += 4 + end + 3
			} else {
				i = len(src)
			}
			continue
		}
		if strings.HasPrefix(src[i:], "<![CDATA[") {
			if end := strings.Index(src[i+9:], "]]>"); end >= 0 {
				top().Kids = append(top().Kids, &hNode{Text: src[i+9 : i+9+end]})
				i += 9 + end + 3
			} else {
				i = len(src)
			}
			continue
		}
		if strings.HasPrefix(src[i:], "<!") || strings.HasPrefix(src[i:], "<?") {
			if end := strings.IndexByte(src[i:], '>'); end >= 0 {
				i += end + 1
			} else {
				i = len(src)
			}
			continue
		}
		gt := strings.IndexByte(src[i:], '>')
		if gt < 0 {
			addText(src[i:])
			break
		}
		tag := src[i+1 : i+gt]
		i += gt + 1
		if strings.HasPrefix(tag, "/") {
			closeTag(strings.ToLower(strings.TrimSpace(tag[1:])))
			continue
		}
		selfClose := strings.HasSuffix(tag, "/")
		tag = strings.TrimSuffix(tag, "/")
		name, attrs := parseTag(tag)
		if name == "" {
			continue
		}
		if closesSelf[name] && top().Name == name {
			closeTag(name) // <li> nuevo cierra al <li> anterior, y asi
		}
		n := &hNode{Name: name, Attr: attrs}
		if voidTags[name] || selfClose {
			top().Kids = append(top().Kids, n)
			continue
		}
		if rawTextTags[name] {
			closeIdx := strings.Index(strings.ToLower(src[i:]), "</"+name)
			if closeIdx < 0 {
				i = len(src)
			} else {
				n.Kids = append(n.Kids, &hNode{Text: src[i : i+closeIdx]})
				top().Kids = append(top().Kids, n)
				i += closeIdx
				if end := strings.IndexByte(src[i:], '>'); end >= 0 {
					i += end + 1
				}
			}
			continue
		}
		push(n)
	}
	return root
}

// parseTag separa "div class='x' id=y" en el nombre y sus atributos.
func parseTag(tag string) (string, map[string]string) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return "", nil
	}
	// nombre
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
		rest = rest[eq:]
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

type mdWriter struct {
	b        strings.Builder
	resolve  func(string) string // reescribe src/href (EPUB: data URI)
	listStk  []listState
	quote    int
	lastCh   byte
	inPre    bool
	skipRest bool
}

type listState struct {
	ordered bool
	index   int
}

// htmlToMarkdown convierte un fragmento (o documento) HTML a Markdown.
// resolve puede ser nil; si no lo es, se le pasa cada src/href para reescribirlo.
func htmlToMarkdown(src string, resolve func(string) string) string {
	root := parseHTML(src)
	if body := findNode(root, "body"); body != nil {
		root = body
	}
	w := &mdWriter{resolve: resolve}
	w.walkKids(root)
	out := w.b.String()
	// como maximo una linea en blanco seguida
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(out) + "\n"
}

// HTMLTitle saca el <title> de un documento (para el nombre de la ventana).
func HTMLTitle(src string) string {
	root := parseHTML(src)
	if t := findNode(root, "title"); t != nil {
		return strings.TrimSpace(collectText(t))
	}
	if h := findNode(root, "h1"); h != nil {
		return strings.TrimSpace(collectText(h))
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
	w.b.WriteString(s)
	w.lastCh = s[len(s)-1]
}

// nl asegura que estamos al principio de una linea.
func (w *mdWriter) nl() {
	if w.b.Len() > 0 && w.lastCh != '\n' {
		w.write("\n")
	}
}

// blank abre un parrafo nuevo (linea en blanco de por medio).
func (w *mdWriter) blank() {
	w.nl()
	s := w.b.String()
	if len(s) > 0 && !strings.HasSuffix(s, "\n\n") {
		w.write("\n")
	}
}

func (w *mdWriter) prefix() string {
	p := strings.Repeat("> ", w.quote)
	if n := len(w.listStk); n > 0 {
		p += strings.Repeat("  ", n-1)
	}
	return p
}

func (w *mdWriter) walkKids(n *hNode) {
	for _, k := range n.Kids {
		w.walk(k)
	}
}

func (w *mdWriter) walk(n *hNode) {
	if n.Name == "" {
		txt := n.Text
		if !w.inPre {
			txt = collapseSpaces(txt)
			if txt == "" {
				return
			}
			// no arrancar una linea con espacio suelto
			if w.lastCh == '\n' || w.b.Len() == 0 {
				txt = strings.TrimLeft(txt, " ")
				if txt == "" {
					return
				}
				w.write(w.prefix())
			}
			w.write(escapeInline(txt))
			return
		}
		w.write(txt)
		return
	}

	switch n.Name {
	case "script", "style", "head", "meta", "link", "noscript", "svg", "iframe", "form", "button":
		return
	case "title":
		return // ya lo toma HTMLTitle

	case "h1", "h2", "h3", "h4", "h5", "h6":
		lvl := int(n.Name[1] - '0')
		w.blank()
		w.write(w.prefix() + strings.Repeat("#", lvl) + " ")
		w.inline(n)
		w.nl()
		w.write("\n")

	case "p", "div", "section", "article", "main", "header", "footer", "figcaption", "address":
		w.blank()
		w.walkKids(n)
		w.blank()

	case "br":
		w.write("  \n" + w.prefix())

	case "hr":
		w.blank()
		w.write(w.prefix() + "---\n\n")

	case "strong", "b":
		w.wrapInline(n, "**")
	case "em", "i", "cite", "var":
		w.wrapInline(n, "*")
	case "del", "s", "strike":
		w.wrapInline(n, "~~")
	case "mark":
		w.wrapInline(n, "==")
	case "ins", "u":
		w.wrapInline(n, "++")
	case "sup":
		w.wrapInline(n, "^")
	case "sub":
		w.wrapInline(n, "~")
	case "kbd", "samp", "tt":
		w.wrapInline(n, "`")

	case "code":
		if w.inPre {
			w.walkKids(n)
			return
		}
		txt := collectText(n)
		fence := "`"
		if strings.Contains(txt, "`") {
			fence = "``"
		}
		w.write(fence + strings.TrimSpace(txt) + fence)

	case "pre":
		w.blank()
		lang := langFromClass(n)
		if lang == "" {
			if c := firstChild(n, "code"); c != nil {
				lang = langFromClass(c)
			}
		}
		w.inPre = true
		txt := collectText(n)
		w.inPre = false
		txt = strings.Trim(txt, "\n")
		block := codeBlock(txt, lang)
		if w.quote > 0 || len(w.listStk) > 0 {
			for _, ln := range strings.Split(strings.TrimRight(block, "\n"), "\n") {
				w.write(w.prefix() + ln + "\n")
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

	case "ul", "ol":
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
		if len(w.listStk) == 0 {
			w.listStk = append(w.listStk, listState{})
			defer func() { w.listStk = w.listStk[:len(w.listStk)-1] }()
		}
		st := &w.listStk[len(w.listStk)-1]
		w.nl()
		marker := "- "
		if st.ordered {
			marker = itoa(st.index) + ". "
			st.index++
		}
		w.write(w.prefix() + marker)
		w.walkKids(n)
		w.nl()

	case "dl":
		w.blank()
		w.walkKids(n)
		w.blank()
	case "dt":
		w.nl()
		w.inline(n)
		w.nl()
	case "dd":
		w.nl()
		w.write(": ")
		w.inline(n)
		w.nl()

	case "a":
		href := n.attr("href")
		if w.resolve != nil && href != "" {
			href = w.resolve(href)
		}
		text := strings.TrimSpace(inlineOf(n, w.resolve))
		if text == "" {
			text = href
		}
		if href == "" {
			w.write(text)
			return
		}
		w.write("[" + text + "](" + urlEscape(href) + ")")

	case "img":
		src := n.attr("src")
		if w.resolve != nil && src != "" {
			src = w.resolve(src)
		}
		if src == "" {
			return
		}
		alt := strings.TrimSpace(n.attr("alt"))
		w.write("![" + escapeInline(alt) + "](" + urlEscape(src) + ")")

	case "table":
		w.blank()
		w.table(n)
		w.blank()

	case "figure", "details", "summary", "span", "font", "small", "big", "abbr", "time", "label":
		w.walkKids(n)

	default:
		w.walkKids(n)
	}
}

// inline escribe el contenido de un nodo en la misma linea.
func (w *mdWriter) inline(n *hNode) {
	w.write(strings.TrimSpace(inlineOf(n, w.resolve)))
}

func (w *mdWriter) wrapInline(n *hNode, mark string) {
	inner := strings.TrimSpace(inlineOf(n, w.resolve))
	if inner == "" {
		return
	}
	w.write(mark + inner + mark)
}

// inlineOf convierte un subarbol a Markdown en una sola linea.
func inlineOf(n *hNode, resolve func(string) string) string {
	sub := &mdWriter{resolve: resolve}
	sub.walkKids(n)
	s := sub.b.String()
	s = strings.ReplaceAll(s, "\n", " ")
	return collapseSpaces(s)
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
						row = append(row, inlineOf(c, w.resolve))
					}
				}
				if len(row) > 0 {
					rows = append(rows, row)
				}
			default:
				rec(k)
			}
		}
	}
	rec(n)
	if len(rows) == 0 {
		return
	}
	head := rows[0]
	body := rows[1:]
	for _, ln := range strings.Split(strings.TrimRight(mdTable(head, body), "\n"), "\n") {
		w.write(w.prefix() + ln + "\n")
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

// langFromClass saca "go" de class="language-go" / "lang-go" / "highlight-go".
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
	if s := n.attr("start"); s != "" {
		v := 0
		for _, c := range s {
			if c < '0' || c > '9' {
				return 1
			}
			v = v*10 + int(c-'0')
		}
		if v > 0 {
			return v
		}
	}
	return 1
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// collapseSpaces normaliza los espacios como haria un navegador.
func collapseSpaces(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r', ' ':
			space = true
		default:
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		}
	}
	if space && b.Len() > 0 {
		b.WriteByte(' ')
	}
	return b.String()
}

// escapeInline protege lo minimo indispensable: si escapamos de mas, el texto
// del documento se llena de contrabarras.
func escapeInline(s string) string {
	r := strings.NewReplacer(`\`, `\\`, "`", "\\`", `*`, `\*`, `_`, `\_`,
		`[`, `\[`, `]`, `\]`, `<`, `\<`)
	return r.Replace(s)
}

// urlEscape deja la URL utilizable dentro de (…) de Markdown.
func urlEscape(u string) string {
	if strings.ContainsAny(u, " ()") {
		return "<" + strings.ReplaceAll(u, ">", "%3E") + ">"
	}
	return u
}
