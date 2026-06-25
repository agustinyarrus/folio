package main

// alerts.go — admoniciones estilo GitHub ("alerts"):
//
//   > [!NOTE]
//   > Texto informativo.
//
// Tipos: NOTE, TIP, IMPORTANT, WARNING, CAUTION. Un AST-transformer detecta el blockquote cuyo
// primer renglón es exactamente `[!TIPO]`, lo marca con el atributo `data-alert` y borra ese
// renglón; el renderer de blockquote (más abajo) dibuja la caja con su ícono y color. Robusto a
// cómo goldmark segmente el `[!TIPO]` en nodos Text (reconstruye el primer renglón desde el source).

import (
	"regexp"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var alertRe = regexp.MustCompile(`(?i)^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]$`)

type alertMeta struct {
	label string
	icon  string // SVG octicon (16x16), fill=currentColor
}

// Octicons de GitHub (los mismos glifos que usa GitHub para cada alerta).
var alertInfo = map[string]alertMeta{
	"note":      {"Note", svgIcon(`<path d="M0 8a8 8 0 1 1 16 0A8 8 0 0 1 0 8Zm8-6.5a6.5 6.5 0 1 0 0 13 6.5 6.5 0 0 0 0-13ZM6.5 7.75A.75.75 0 0 1 7.25 7h1a.75.75 0 0 1 .75.75v2.75h.25a.75.75 0 0 1 0 1.5h-2a.75.75 0 0 1 0-1.5h.25v-2h-.25a.75.75 0 0 1-.75-.75ZM8 6a1 1 0 1 1 0-2 1 1 0 0 1 0 2Z"/>`)},
	"tip":       {"Tip", svgIcon(`<path d="M8 1.5c-2.363 0-4 1.69-4 3.75 0 .984.424 1.625.984 2.304l.214.253c.223.264.47.556.673.848.284.411.537.896.621 1.49a.75.75 0 0 1-1.484.211c-.04-.282-.163-.547-.37-.847a8.456 8.456 0 0 0-.542-.68c-.084-.1-.173-.205-.268-.32C3.201 7.75 2.5 6.766 2.5 5.25 2.5 2.31 4.863 0 8 0s5.5 2.31 5.5 5.25c0 1.516-.701 2.5-1.328 3.262-.095.115-.184.22-.268.319-.207.245-.383.453-.541.681-.208.3-.33.565-.37.847a.751.751 0 0 1-1.485-.212c.084-.593.337-1.078.621-1.489.203-.292.45-.584.673-.848.075-.088.147-.173.214-.253.56-.679.984-1.32.984-2.304 0-2.06-1.637-3.75-4-3.75ZM5.75 12h4.5a.75.75 0 0 1 0 1.5h-4.5a.75.75 0 0 1 0-1.5ZM6 15.25a.75.75 0 0 1 .75-.75h2.5a.75.75 0 0 1 0 1.5h-2.5a.75.75 0 0 1-.75-.75Z"/>`)},
	"important": {"Important", svgIcon(`<path d="M0 1.75C0 .784.784 0 1.75 0h12.5C15.216 0 16 .784 16 1.75v9.5A1.75 1.75 0 0 1 14.25 13H8.06l-2.573 2.573A1.458 1.458 0 0 1 3 14.543V13H1.75A1.75 1.75 0 0 1 0 11.25Zm1.75-.25a.25.25 0 0 0-.25.25v9.5c0 .138.112.25.25.25h2a.75.75 0 0 1 .75.75v2.19l2.72-2.72a.749.749 0 0 1 .53-.22h6.5a.25.25 0 0 0 .25-.25v-9.5a.25.25 0 0 0-.25-.25Zm7 2.25v2.5a.75.75 0 0 1-1.5 0v-2.5a.75.75 0 0 1 1.5 0ZM9 9a1 1 0 1 1-2 0 1 1 0 0 1 2 0Z"/>`)},
	"warning":   {"Warning", svgIcon(`<path d="M6.457 1.047c.659-1.234 2.427-1.234 3.086 0l6.082 11.378A1.75 1.75 0 0 1 14.082 15H1.918a1.75 1.75 0 0 1-1.543-2.575Zm1.763.707a.25.25 0 0 0-.44 0L1.698 13.132a.25.25 0 0 0 .22.368h12.164a.25.25 0 0 0 .22-.368Zm.53 3.996v2.5a.75.75 0 0 1-1.5 0v-2.5a.75.75 0 0 1 1.5 0ZM9 11a1 1 0 1 1-2 0 1 1 0 0 1 2 0Z"/>`)},
	"caution":   {"Caution", svgIcon(`<path d="M4.47.22A.749.749 0 0 1 5 0h6c.199 0 .389.079.53.22l4.25 4.25c.141.141.22.331.22.53v6a.749.749 0 0 1-.22.53l-4.25 4.25A.749.749 0 0 1 11 16H5a.749.749 0 0 1-.53-.22L.22 11.53A.749.749 0 0 1 0 11V5c0-.199.079-.389.22-.53Zm.84 1.28L1.5 5.31v5.38l3.81 3.81h5.38l3.81-3.81V5.31L10.69 1.5ZM8 4a.75.75 0 0 1 .75.75v3.5a.75.75 0 0 1-1.5 0v-3.5A.75.75 0 0 1 8 4Zm0 8a1 1 0 1 1 0-2 1 1 0 0 1 0 2Z"/>`)},
}

func svgIcon(path string) string {
	return `<svg class="alert-ico" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">` + path + `</svg>`
}

// ---- transformer: detecta y marca las alertas --------------------------
type alertTransformer struct{}

func (t *alertTransformer) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	source := reader.Source()
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if bq, ok := n.(*ast.Blockquote); ok {
			detectAlert(bq, source)
		}
		return ast.WalkContinue, nil
	})
}

// detectAlert: si el primer renglón del blockquote es exactamente `[!TIPO]`, lo marca y lo borra.
func detectAlert(bq *ast.Blockquote, source []byte) {
	para, ok := bq.FirstChild().(*ast.Paragraph)
	if !ok || para.FirstChild() == nil {
		return
	}
	// reconstruir el primer renglón desde sus nodos Text (hasta el primer salto de línea)
	var firstLine []ast.Node
	var b strings.Builder
	for c := para.FirstChild(); c != nil; c = c.NextSibling() {
		tn, ok := c.(*ast.Text)
		if !ok { // un renglón de marcador es texto puro
			return
		}
		firstLine = append(firstLine, c)
		b.Write(tn.Segment.Value(source))
		if tn.SoftLineBreak() || tn.HardLineBreak() {
			break
		}
	}
	m := alertRe.FindStringSubmatch(strings.TrimSpace(b.String()))
	if m == nil {
		return
	}
	bq.SetAttributeString("data-alert", []byte(strings.ToLower(m[1])))
	for _, nd := range firstLine {
		para.RemoveChild(para, nd)
	}
	if para.FirstChild() == nil { // el marcador era su propio párrafo: borrarlo
		bq.RemoveChild(bq, para)
	}
}

// ---- renderer de blockquote (alerta o normal) --------------------------
type blockRenderer struct{}

func (r *blockRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindBlockquote, r.renderBlockquote)
}

func (r *blockRenderer) renderBlockquote(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	typ := attrString(n, "data-alert")
	if typ == "" { // blockquote normal: igual que el default de goldmark
		if entering {
			w.WriteString("<blockquote>\n")
		} else {
			w.WriteString("</blockquote>\n")
		}
		return ast.WalkContinue, nil
	}
	if entering {
		meta := alertInfo[typ]
		w.WriteString(`<div class="alert alert-` + typ + `">`)
		w.WriteString(`<p class="alert-title">` + meta.icon + `<span>` + meta.label + `</span></p>`)
	} else {
		w.WriteString("</div>\n")
	}
	return ast.WalkContinue, nil
}
