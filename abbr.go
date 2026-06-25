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

import (
	"bytes"
	"html"
	"regexp"
	"sort"
	"strings"

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

// ---- transformer: envuelve ocurrencias ----------------------------------
type abbrTransformer struct{}

func (t *abbrTransformer) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	abbrs := getAbbrs(pc)
	if len(abbrs) == 0 {
		return
	}
	// regex: \b(CLAVE1|CLAVE2|...)\b con las claves más largas primero
	keys := make([]string, 0, len(abbrs))
	for k := range abbrs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	quoted := make([]string, len(keys))
	for i, k := range keys {
		quoted[i] = regexp.QuoteMeta(k)
	}
	re, err := regexp.Compile(`\b(?:` + strings.Join(quoted, "|") + `)\b`)
	if err != nil {
		return
	}
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
		wrapAbbrInText(tn, source, re, abbrs)
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

func wrapAbbrInText(tn *ast.Text, source []byte, re *regexp.Regexp, abbrs map[string]string) {
	parent := tn.Parent()
	if parent == nil {
		return
	}
	seg := tn.Segment
	val := seg.Value(source)
	locs := re.FindAllIndex(val, -1)
	if len(locs) == 0 {
		return
	}
	var nodes []ast.Node
	prev := 0
	for _, loc := range locs {
		s, e := loc[0], loc[1]
		title, ok := abbrs[string(val[s:e])]
		if !ok {
			continue
		}
		if s > prev {
			nodes = append(nodes, ast.NewTextSegment(text.NewSegment(seg.Start+prev, seg.Start+s)))
		}
		ab := &Abbreviation{Title: title, Seg: text.NewSegment(seg.Start+s, seg.Start+e)}
		nodes = append(nodes, ab)
		prev = e
	}
	if len(nodes) == 0 {
		return
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
