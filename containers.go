package main

// containers.go — contenedores con cercas `:::` (Pandoc fenced divs / VuePress / Docusaurus):
//
//   ::: warning
//   Contenido con **Markdown** adentro.
//   :::
//
//   ::: tip Título personalizado
//   ...
//   :::
//
//   ::: {.clase-propia #mi-id}
//   ...
//   :::
//
// Apertura: `:::` (3 o más) seguido de info NO vacía. El contenido se parsea como Markdown (bloques
// hijos). Cierre: una línea de sólo `:` (>= la cantidad de apertura, así anidan poniendo más `:`
// afuera). Render: `<div class="callout callout-TIPO">` con un título, o un `<div>` con clase/id
// crudos si la info viene entre `{...}`.

import (
	"bytes"
	"html"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var KindContainer = ast.NewNodeKind("Container")

type containerBlock struct {
	ast.BaseBlock
	callout bool   // true: `::: nombre` ; false: `::: {.clase #id}`
	typ     string // nombre del callout (note/tip/warning/…)
	title   string // título a mostrar (callout)
	classes string // clases crudas (forma con llaves)
	id      string // id crudo (forma con llaves)
	fence   int    // cantidad de `:` de apertura (para anidar)
}

func (n *containerBlock) Kind() ast.NodeKind   { return KindContainer }
func (n *containerBlock) Dump(s []byte, l int) { ast.DumpHelper(n, s, l, nil, nil) }

// ---- parser -------------------------------------------------------------
type containerParser struct{}

func (p *containerParser) Trigger() []byte { return []byte{':'} }

func colonRun(b []byte) int {
	i := 0
	for i < len(b) && b[i] == ':' {
		i++
	}
	return i
}

func (p *containerParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, _ := reader.PeekLine()
	t := bytes.TrimLeft(bytes.TrimRight(line, "\r\n"), " ")
	n := colonRun(t)
	if n < 3 {
		return nil, parser.NoChildren
	}
	info := strings.TrimSpace(string(t[n:]))
	if info == "" { // `:::` solo es un cierre, no abre (evita contenedores fantasma)
		return nil, parser.NoChildren
	}
	cb := parseContainerInfo(info)
	cb.fence = n
	reader.AdvanceLine() // consumir la línea de apertura (HasChildren no la avanza solo)
	return cb, parser.HasChildren
}

func (p *containerParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	cb := node.(*containerBlock)
	line, _ := reader.PeekLine()
	t := bytes.TrimLeft(bytes.TrimRight(line, "\r\n"), " ")
	if n := colonRun(t); n >= cb.fence && len(bytes.TrimSpace(t[n:])) == 0 {
		reader.Advance(len(line)) // consumir la línea de cierre
		return parser.Close
	}
	return parser.Continue | parser.HasChildren // el contenedor sigue y acepta bloques hijos
}

func (p *containerParser) Close(node ast.Node, reader text.Reader, pc parser.Context) {}
func (p *containerParser) CanInterruptParagraph() bool                                { return true }
func (p *containerParser) CanAcceptIndentedLine() bool                                { return false }

func parseContainerInfo(info string) *containerBlock {
	cb := &containerBlock{}
	if strings.HasPrefix(info, "{") { // {.clase .otra #id}
		inner := strings.TrimSpace(strings.Trim(info, "{}"))
		var classes []string
		for _, tok := range strings.Fields(inner) {
			switch {
			case strings.HasPrefix(tok, "."):
				classes = append(classes, tok[1:])
			case strings.HasPrefix(tok, "#"):
				cb.id = tok[1:]
			default:
				classes = append(classes, tok)
			}
		}
		cb.classes = strings.Join(classes, " ")
		return cb
	}
	// `nombre [título personalizado]`
	cb.callout = true
	parts := strings.SplitN(info, " ", 2)
	cb.typ = strings.ToLower(strings.TrimSpace(parts[0]))
	if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
		cb.title = strings.TrimSpace(parts[1])
	} else {
		cb.title = capitalizeWord(cb.typ)
	}
	return cb
}

func capitalizeWord(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// ---- renderer -----------------------------------------------------------
type containerRenderer struct{}

func (r *containerRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(KindContainer, r.render)
}

func (r *containerRenderer) render(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	cb := n.(*containerBlock)
	if !entering {
		w.WriteString("</div>\n")
		return ast.WalkContinue, nil
	}
	if cb.callout {
		w.WriteString(`<div class="callout`)
		if cb.typ != "" {
			w.WriteString(` callout-` + html.EscapeString(cb.typ))
		}
		w.WriteString(`">`)
		if cb.title != "" {
			w.WriteString(`<p class="callout-title">` + html.EscapeString(cb.title) + `</p>`)
		}
	} else {
		w.WriteString(`<div`)
		if cb.classes != "" {
			w.WriteString(` class="` + html.EscapeString(cb.classes) + `"`)
		}
		if cb.id != "" {
			w.WriteString(` id="` + html.EscapeString(cb.id) + `"`)
		}
		w.WriteString(`>`)
	}
	w.WriteString("\n")
	return ast.WalkContinue, nil
}

// ---- extensión ----------------------------------------------------------
type containerExtension struct{}

func (e *containerExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithBlockParsers(util.Prioritized(&containerParser{}, 99)),
	)
	m.Renderer().AddOptions(
		renderer.WithNodeRenderers(util.Prioritized(&containerRenderer{}, 100)),
	)
}
