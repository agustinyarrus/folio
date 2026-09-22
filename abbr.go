package main

// abbr.go — abreviaturas estilo PHP Markdown Extra / Pandoc:
//
//   La <abbr> HTML y la CSS son estándares.
//
//   *[HTML]: HyperText Markup Language
//   *[CSS]:  Cascading Style Sheets
//
// Las líneas `*[CLAVE]: definición` se capturan con un block-parser (no renderizan nada) y guardan
// la definición en el contexto. Después, un transformer recorre los nodos Text y envuelve cada
// ocurrencia de palabra completa de una clave en <abbr title="definición">CLAVE</abbr>. Se saltea el
// contenido de código en línea. Corre DESPUÉS del docTransformer para no ensuciar el texto del TOC.
//
// La búsqueda es un autómata de Aho–Corasick sobre todas las claves a la vez (una pasada por
// texto, O(n + m + z)) con bordes de palabra Unicode. La versión anterior usaba \b de RE2, que
// sólo entiende ASCII: una clave con Ñ o acento en un borde ("AÑO") o que empieza o termina en
// puntuación ("C++", ".NET", "I/O") no matcheaba nunca.

import (
	"bytes"
	"html"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var (
	abbrsKey    = parser.NewContextKey()
	abbrDefRe   = regexp.MustCompile(`^\*\[([^\]]+)\]:[ \t]*(.+?)[ \t]*$`)
	KindAbbrDef = ast.NewNodeKind("AbbrDef")
	KindAbbr    = ast.NewNodeKind("Abbr")
)

func getAbbrs(pc parser.Context) map[string]string {
	if v, ok := pc.Get(abbrsKey).(map[string]string); ok {
		return v
	}
	m := map[string]string{}
	pc.Set(abbrsKey, m)
	return m
}

// ---- nodos --------------------------------------------------------------
type abbrDefBlock struct{ ast.BaseBlock } // sólo marcador; no renderiza

func (n *abbrDefBlock) Kind() ast.NodeKind   { return KindAbbrDef }
func (n *abbrDefBlock) Dump(s []byte, l int) { ast.DumpHelper(n, s, l, nil, nil) }

type Abbreviation struct {
	ast.BaseInline
	Title string
	Seg   text.Segment
}

func (n *Abbreviation) Kind() ast.NodeKind   { return KindAbbr }
func (n *Abbreviation) Dump(s []byte, l int) { ast.DumpHelper(n, s, l, nil, nil) }

// ---- block-parser de definiciones ---------------------------------------
type abbrDefParser struct{}

func (p *abbrDefParser) Trigger() []byte { return []byte{'*'} }

func (p *abbrDefParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, _ := reader.PeekLine()
	m := abbrDefRe.FindSubmatch(bytes.TrimRight(line, "\r\n"))
	if m == nil {
		return nil, parser.NoChildren
	}
	key := strings.TrimSpace(string(m[1]))
	title := strings.TrimSpace(string(m[2]))
	if key == "" || title == "" {
		return nil, parser.NoChildren
	}
	getAbbrs(pc)[key] = title
	return &abbrDefBlock{}, parser.NoChildren
}

func (p *abbrDefParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	return parser.Close // bloque de una sola línea
}
func (p *abbrDefParser) Close(node ast.Node, reader text.Reader, pc parser.Context) {}
func (p *abbrDefParser) CanInterruptParagraph() bool                                { return false }
func (p *abbrDefParser) CanAcceptIndentedLine() bool                                { return false }

// ---- Aho–Corasick -------------------------------------------------------

// acMatcher es el automata de Aho–Corasick de un conjunto de claves (sobre sus bytes UTF-8,
// que es seguro: UTF-8 se autosincroniza y una clave valida no puede matchear arrancando en
// la mitad de otro caracter). Transiciones en tabla densa de 256 por nodo (1 KB c/u: las
// claves de un documento suman pocos cientos de nodos y el paso queda en O(1)), fallas por
// BFS y la salida de cada nodo ya incluye la de su cadena de fallas.
type acMatcher struct {
	next [][256]int32 // transiciones; 0 = sin arista (el nodo 0 es la raiz)
	fail []int32
	out  [][]int32 // indices de las claves que terminan en cada nodo
	keys []string
}

func newACMatcher(keys []string) *acMatcher {
	m := &acMatcher{next: make([][256]int32, 1), fail: []int32{0}, out: [][]int32{nil}, keys: keys}
	for ki, k := range keys {
		node := int32(0)
		for i := 0; i < len(k); i++ {
			c := k[i]
			if m.next[node][c] == 0 {
				m.next = append(m.next, [256]int32{})
				m.fail = append(m.fail, 0)
				m.out = append(m.out, nil)
				m.next[node][c] = int32(len(m.next) - 1)
			}
			node = m.next[node][c]
		}
		m.out[node] = append(m.out[node], int32(ki))
	}
	// BFS: la falla de v (hijo de u por c) es el nodo alcanzado siguiendo c desde la falla de u
	queue := make([]int32, 0, len(m.next))
	for c := 0; c < 256; c++ {
		if v := m.next[0][c]; v != 0 {
			queue = append(queue, v)
		}
	}
	for head := 0; head < len(queue); head++ {
		u := queue[head]
		for c := 0; c < 256; c++ {
			v := m.next[u][c]
			if v == 0 {
				continue
			}
			queue = append(queue, v)
			f := m.fail[u]
			for f != 0 && m.next[f][c] == 0 {
				f = m.fail[f]
			}
			if nx := m.next[f][c]; nx != 0 && nx != v {
				m.fail[v] = nx
			}
			m.out[v] = append(m.out[v], m.out[m.fail[v]]...)
		}
	}
	return m
}

// abbrMatch: una ocurrencia [start,end) de la clave key en el texto.
type abbrMatch struct{ start, end, key int }

// findAll devuelve todas las ocurrencias (solapadas incluidas) en una pasada. O(n + z).
func (m *acMatcher) findAll(txt []byte) []abbrMatch {
	var res []abbrMatch
	node := int32(0)
	for i := 0; i < len(txt); i++ {
		c := txt[i]
		for node != 0 && m.next[node][c] == 0 {
			node = m.fail[node]
		}
		node = m.next[node][c]
		for _, k := range m.out[node] {
			res = append(res, abbrMatch{i + 1 - len(m.keys[k]), i + 1, int(k)})
		}
	}
	return res
}

func isWordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// wholeWord: si la clave empieza (termina) con letra o digito, el caracter de al lado no
// puede serlo; si empieza con puntuación (".NET") ese borde no se exige.
func wholeWord(txt []byte, mt abbrMatch, key string) bool {
	first, _ := utf8.DecodeRuneInString(key)
	last, _ := utf8.DecodeLastRuneInString(key)
	if mt.start > 0 && isWordRune(first) {
		if prev, _ := utf8.DecodeLastRune(txt[:mt.start]); isWordRune(prev) {
			return false
		}
	}
	if mt.end < len(txt) && isWordRune(last) {
		if next, _ := utf8.DecodeRune(txt[mt.end:]); isWordRune(next) {
			return false
		}
	}
	return true
}

// pickMatches se queda con las ocurrencias validas que no se pisan, prefiriendo la que
// arranca antes y, a igual comienzo, la mas larga ("HTML5" le gana a "HTML"). O(z log z).
func pickMatches(txt []byte, all []abbrMatch, keys []string) []abbrMatch {
	sort.Slice(all, func(i, j int) bool {
		if all[i].start != all[j].start {
			return all[i].start < all[j].start
		}
		return all[i].end > all[j].end
	})
	var picked []abbrMatch
	cursor := 0
	for _, mt := range all {
		if mt.start < cursor || !wholeWord(txt, mt, keys[mt.key]) {
			continue
		}
		picked = append(picked, mt)
		cursor = mt.end
	}
	return picked
}

// ---- transformer: envuelve ocurrencias ----------------------------------
type abbrTransformer struct{}

func (t *abbrTransformer) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	abbrs := getAbbrs(pc)
	if len(abbrs) == 0 {
		return
	}
	keys := make([]string, 0, len(abbrs))
	for k := range abbrs {
		keys = append(keys, k)
	}
	sort.Strings(keys) // determinista
	matcher := newACMatcher(keys)
	source := reader.Source()

	// recolectar primero (no mutar durante el Walk)
	var texts []*ast.Text
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if tn, ok := n.(*ast.Text); ok && !insideCode(tn) {
				texts = append(texts, tn)
			}
		}
		return ast.WalkContinue, nil
	})
	for _, tn := range texts {
		wrapAbbrInText(tn, source, matcher, abbrs)
	}
}

func insideCode(n ast.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.Kind() == ast.KindCodeSpan {
			return true
		}
	}
	return false
}

func wrapAbbrInText(tn *ast.Text, source []byte, m *acMatcher, abbrs map[string]string) {
	parent := tn.Parent()
	if parent == nil {
		return
	}
	seg := tn.Segment
	val := seg.Value(source)
	found := m.findAll(val)
	if len(found) == 0 {
		return
	}
	matches := pickMatches(val, found, m.keys)
	if len(matches) == 0 {
		return
	}
	var nodes []ast.Node
	prev := 0
	for _, mt := range matches {
		if mt.start > prev {
			nodes = append(nodes, ast.NewTextSegment(text.NewSegment(seg.Start+prev, seg.Start+mt.start)))
		}
		nodes = append(nodes, &Abbreviation{
			Title: abbrs[m.keys[mt.key]],
			Seg:   text.NewSegment(seg.Start+mt.start, seg.Start+mt.end),
		})
		prev = mt.end
	}
	if prev < len(val) {
		nodes = append(nodes, ast.NewTextSegment(text.NewSegment(seg.Start+prev, seg.Start+len(val))))
	}
	// trasladar el salto de línea del original al último nodo
	last := nodes[len(nodes)-1]
	if lt, ok := last.(*ast.Text); ok {
		lt.SetSoftLineBreak(tn.SoftLineBreak())
		lt.SetHardLineBreak(tn.HardLineBreak())
	} else if tn.SoftLineBreak() || tn.HardLineBreak() {
		brk := ast.NewTextSegment(text.NewSegment(seg.Stop, seg.Stop))
		brk.SetSoftLineBreak(tn.SoftLineBreak())
		brk.SetHardLineBreak(tn.HardLineBreak())
		nodes = append(nodes, brk)
	}
	for _, nn := range nodes {
		parent.InsertBefore(parent, tn, nn)
	}
	parent.RemoveChild(parent, tn)
}

// ---- renderer -----------------------------------------------------------
type abbrRenderer struct{}

func (r *abbrRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(KindAbbrDef, r.renderNothing)
	reg.Register(KindAbbr, r.renderAbbr)
}

func (r *abbrRenderer) renderNothing(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	return ast.WalkSkipChildren, nil
}

func (r *abbrRenderer) renderAbbr(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	a := n.(*Abbreviation)
	w.WriteString(`<abbr title="` + html.EscapeString(a.Title) + `">`)
	w.WriteString(html.EscapeString(string(a.Seg.Value(source))))
	w.WriteString(`</abbr>`)
	return ast.WalkSkipChildren, nil
}

// ---- extensión ----------------------------------------------------------
type abbrExtension struct{}

func (e *abbrExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithBlockParsers(util.Prioritized(&abbrDefParser{}, 99)),
		// el transformer corre DESPUÉS del docTransformer (999) para no tocar el texto del TOC
		parser.WithASTTransformers(util.Prioritized(&abbrTransformer{}, 1000)),
	)
	m.Renderer().AddOptions(
		renderer.WithNodeRenderers(util.Prioritized(&abbrRenderer{}, 100)),
	)
}
