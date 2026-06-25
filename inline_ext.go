package main

// inline_ext.go — marcas inline extra, populares en Pandoc / markdown-it / Obsidian:
//
//   ==resaltado==   -> <mark>      (procesador de delimitadores: admite formato anidado)
//   ++insertado++   -> <ins>       (idem)
//   ^superíndice^   -> <sup>       (un solo renglón, sin espacios; estilo Pandoc x^2^)
//   ~subíndice~     -> <sub>       (idem; convive con ~~tachado~~ de GFM mirando el doble ~)
//
// mark/ins van por el mismo mecanismo de delimitadores que el tachado de goldmark (ScanDelimiter +
// PushDelimiter) para soportar anidado. sup/sub son escaneos de un renglón (el contenido no lleva
// espacios), con prioridad MAYOR que el tachado para que `~x~` sea subíndice y `~~x~~` siga siendo
// tachado.

import (
	"html"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// ---- nodos --------------------------------------------------------------
var (
	KindMark = ast.NewNodeKind("Mark")
	KindIns  = ast.NewNodeKind("Ins")
	KindSup  = ast.NewNodeKind("Sup")
	KindSub  = ast.NewNodeKind("Sub")
)

type Mark struct{ ast.BaseInline }

func (n *Mark) Kind() ast.NodeKind   { return KindMark }
func (n *Mark) Dump(s []byte, l int) { ast.DumpHelper(n, s, l, nil, nil) }

type Ins struct{ ast.BaseInline }

func (n *Ins) Kind() ast.NodeKind   { return KindIns }
func (n *Ins) Dump(s []byte, l int) { ast.DumpHelper(n, s, l, nil, nil) }

type Sup struct {
	ast.BaseInline
	Value []byte
}

func (n *Sup) Kind() ast.NodeKind   { return KindSup }
func (n *Sup) Dump(s []byte, l int) { ast.DumpHelper(n, s, l, nil, nil) }

type Sub struct {
	ast.BaseInline
	Value []byte
}

func (n *Sub) Kind() ast.NodeKind   { return KindSub }
func (n *Sub) Dump(s []byte, l int) { ast.DumpHelper(n, s, l, nil, nil) }

// ---- delimitadores: mark (==) e ins (++) --------------------------------
type pairDelimProc struct {
	char byte
	make func() ast.Node
}

func (p *pairDelimProc) IsDelimiter(b byte) bool                   { return b == p.char }
func (p *pairDelimProc) CanOpenCloser(o, c *parser.Delimiter) bool { return o.Char == c.Char }
func (p *pairDelimProc) OnMatch(consumes int) ast.Node             { return p.make() }

var markDelim = &pairDelimProc{char: '=', make: func() ast.Node { return &Mark{} }}
var insDelim = &pairDelimProc{char: '+', make: func() ast.Node { return &Ins{} }}

type pairParser struct {
	char  byte
	delim *pairDelimProc
}

func (s *pairParser) Trigger() []byte { return []byte{s.char} }

func (s *pairParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	before := block.PrecendingCharacter()
	line, segment := block.PeekLine()
	node := parser.ScanDelimiter(line, before, 2, s.delim) // requiere == / ++
	if node == nil || node.OriginalLength != 2 {
		return nil
	}
	node.Segment = segment.WithStop(segment.Start + node.OriginalLength)
	block.Advance(node.OriginalLength)
	pc.PushDelimiter(node)
	return node
}

func (s *pairParser) CloseBlock(parent ast.Node, pc parser.Context) {}

// ---- escaneo de un renglón: sup (^) y sub (~) ---------------------------
type scriptParser struct {
	char      byte
	deferTwin bool // si true, `xx` (doble) se cede a otro parser (tachado para ~)
	make      func(val []byte) ast.Node
}

func (p *scriptParser) Trigger() []byte { return []byte{p.char} }

func (p *scriptParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, _ := block.PeekLine()
	if len(line) < 2 || line[0] != p.char {
		return nil
	}
	if p.deferTwin && line[1] == p.char { // `~~...` -> dejar al tachado
		return nil
	}
	for i := 1; i < len(line); i++ {
		c := line[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' { // sin espacios (regla Pandoc)
			return nil
		}
		if c == p.char {
			if i == 1 { // vacío (`^^` / `~~`)
				return nil
			}
			val := append([]byte(nil), line[1:i]...)
			block.Advance(i + 1)
			return p.make(val)
		}
	}
	return nil
}

func (p *scriptParser) CloseBlock(parent ast.Node, pc parser.Context) {}

// ---- renderer -----------------------------------------------------------
type inlineExtRenderer struct{}

func (r *inlineExtRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(KindMark, wrapTag("mark"))
	reg.Register(KindIns, wrapTag("ins"))
	reg.Register(KindSup, r.renderScript("sup"))
	reg.Register(KindSub, r.renderScript("sub"))
}

func wrapTag(tag string) renderer.NodeRendererFunc {
	open, close := "<"+tag+">", "</"+tag+">"
	return func(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			w.WriteString(open)
		} else {
			w.WriteString(close)
		}
		return ast.WalkContinue, nil
	}
}

func (r *inlineExtRenderer) renderScript(tag string) renderer.NodeRendererFunc {
	return func(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var val []byte
		switch t := n.(type) {
		case *Sup:
			val = t.Value
		case *Sub:
			val = t.Value
		}
		w.WriteString("<" + tag + ">")
		w.WriteString(html.EscapeString(string(val)))
		w.WriteString("</" + tag + ">")
		return ast.WalkSkipChildren, nil
	}
}

// ---- extensión ----------------------------------------------------------
type inlineExtensions struct{}

func (e *inlineExtensions) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithInlineParsers(
		// sup/sub ANTES del tachado (500) para ganar el trigger `~`/`^`
		util.Prioritized(&scriptParser{char: '^', make: func(v []byte) ast.Node { return &Sup{Value: v} }}, 100),
		util.Prioritized(&scriptParser{char: '~', deferTwin: true, make: func(v []byte) ast.Node { return &Sub{Value: v} }}, 100),
		util.Prioritized(&pairParser{char: '=', delim: markDelim}, 500),
		util.Prioritized(&pairParser{char: '+', delim: insDelim}, 500),
	))
	m.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(&inlineExtRenderer{}, 500),
	))
}
