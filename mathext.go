package main

// mathext.go — extension propia de goldmark para matematica TeX.
//
// Protege el contenido matematico de la interpretacion Markdown (que destrozaria \frac, _, *, etc.)
// y lo emite verbatim dentro de <span>/<div class="math">. KaTeX lo renderiza en el cliente.
//
//   $...$        matematica en linea
//   $$...$$      matematica destacada (en una linea o en bloque multilinea)
//
// El parser inline cubre $...$ y $$...$$ de una sola linea; el parser de bloque cubre el caso
// multilinea ($$ sola, contenido en lineas siguientes, cierre con $$).

import (
	"bytes"
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
	KindMathInline = ast.NewNodeKind("MathInline")
	KindMathBlock  = ast.NewNodeKind("MathBlock")
)

type MathInline struct {
	ast.BaseInline
	Value   []byte
	Display bool // $$...$$ en una linea -> displayMode pero dentro de un <span>
}

func (n *MathInline) Kind() ast.NodeKind { return KindMathInline }
func (n *MathInline) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

type MathBlock struct {
	ast.BaseBlock
	Value []byte
}

func (n *MathBlock) Kind() ast.NodeKind { return KindMathBlock }
func (n *MathBlock) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

// ---- parser inline ------------------------------------------------------
type mathInlineParser struct{}

func (p *mathInlineParser) Trigger() []byte { return []byte{'$'} }

func (p *mathInlineParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, _ := block.PeekLine()
	if len(line) < 2 || line[0] != '$' {
		return nil
	}

	// $$ ... $$  destacada en una linea
	if line[1] == '$' {
		rest := line[2:]
		end := bytes.Index(rest, []byte("$$"))
		if end < 0 {
			return nil
		}
		val := append([]byte(nil), rest[:end]...)
		block.Advance(2 + end + 2)
		return &MathInline{Value: bytes.TrimSpace(val), Display: true}
	}

	// $ ... $  en linea. Heuristica anti-falsos-positivos (precios, etc.):
	//   - el caracter tras el $ de apertura no es espacio
	//   - el caracter antes del $ de cierre no es espacio
	//   - el caracter tras el $ de cierre no es un digito
	if isSpaceByte(line[1]) {
		return nil
	}
	i := 1
	for i < len(line) {
		switch line[i] {
		case '\\':
			i += 2
			continue
		case '$':
			content := line[1:i]
			if len(content) == 0 || isSpaceByte(line[i-1]) {
				return nil
			}
			if i+1 < len(line) && line[i+1] >= '0' && line[i+1] <= '9' {
				return nil
			}
			val := append([]byte(nil), content...)
			block.Advance(i + 1)
			return &MathInline{Value: val, Display: false}
		}
		i++
	}
	return nil
}

// ---- parser de bloque ($$ multilinea) -----------------------------------
type mathBlockParser struct{}

func (p *mathBlockParser) Trigger() []byte { return []byte{'$'} }

// goldmark consume la linea de apertura por su cuenta (reader.AdvanceLine tras un Open exitoso),
// asi que Open NO debe avanzar; Continue usa AdvanceToEOL y deja el salto de linea al motor
// (mismo patron que el parser de fenced-code de goldmark).
func (p *mathBlockParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, _ := reader.PeekLine()
	trimmed := bytes.TrimLeft(line, " \t")
	if !bytes.HasPrefix(trimmed, []byte("$$")) {
		return nil, parser.NoChildren
	}
	after := bytes.TrimRight(trimmed[2:], " \t\r\n")
	// si cierra en la misma linea ($$ ... $$) lo maneja el parser inline (queda como <span>)
	if bytes.Contains(after, []byte("$$")) {
		return nil, parser.NoChildren
	}
	node := &MathBlock{}
	if len(after) > 0 { // contenido tras $$ en la linea de apertura (raro pero valido)
		node.Value = append(node.Value, after...)
		node.Value = append(node.Value, '\n')
	}
	return node, parser.NoChildren
}

func (p *mathBlockParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	mb := node.(*MathBlock)
	line, _ := reader.PeekLine()
	raw := bytes.TrimRight(line, "\r\n")
	tt := bytes.TrimRight(raw, " \t")

	if bytes.HasSuffix(tt, []byte("$$")) { // linea de cierre, con o sin contenido antes del $$
		before := bytes.TrimSpace(tt[:len(tt)-2])
		if len(before) > 0 {
			mb.Value = append(mb.Value, before...)
			mb.Value = append(mb.Value, '\n')
		}
		reader.AdvanceToEOL()
		return parser.Close
	}
	mb.Value = append(mb.Value, raw...)
	mb.Value = append(mb.Value, '\n')
	reader.AdvanceToEOL()
	return parser.Continue
}

func (p *mathBlockParser) Close(node ast.Node, reader text.Reader, pc parser.Context) {
	mb := node.(*MathBlock)
	mb.Value = bytes.TrimSpace(mb.Value)
}

func (p *mathBlockParser) CanInterruptParagraph() bool { return true }
func (p *mathBlockParser) CanAcceptIndentedLine() bool { return false }

// ---- renderer -----------------------------------------------------------
type mathRenderer struct{}

func newMathRenderer() *mathRenderer { return &mathRenderer{} }

func (r *mathRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(KindMathInline, r.renderInline)
	reg.Register(KindMathBlock, r.renderBlock)
}

func (r *mathRenderer) renderInline(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	node := n.(*MathInline)
	cls := "math math-inline"
	if node.Display {
		cls = "math math-display"
	}
	w.WriteString(`<span class="` + cls + `">`)
	w.WriteString(html.EscapeString(string(node.Value)))
	w.WriteString(`</span>`)
	return ast.WalkSkipChildren, nil
}

func (r *mathRenderer) renderBlock(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	node := n.(*MathBlock)
	w.WriteString(`<div class="math math-display">`)
	w.WriteString(html.EscapeString(string(node.Value)))
	w.WriteString("</div>\n")
	return ast.WalkSkipChildren, nil
}

// ---- extension ----------------------------------------------------------
type mathExtension struct{}

func (e *mathExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithBlockParsers(util.Prioritized(&mathBlockParser{}, 701)),
		parser.WithInlineParsers(util.Prioritized(&mathInlineParser{}, 501)),
	)
	m.Renderer().AddOptions(
		renderer.WithNodeRenderers(util.Prioritized(newMathRenderer(), 99)),
	)
}
