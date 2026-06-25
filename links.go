package main

// links.go — renderer de enlaces. El docTransformer ya clasifico cada enlace y dejo en el nodo
// los atributos data-kind / data-path; aca los volcamos como data-* en el <a> para que el cliente
// decida que hacer al hacer clic (abrir en el navegador del sistema, cargar otro .md en Folio,
// abrir un archivo local, o saltar a un ancla interna).

import (
	"html"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

type linkRenderer struct{}

func (r *linkRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindLink, r.renderLink)
}

func (r *linkRenderer) renderLink(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	node := n.(*ast.Link)
	if !entering {
		w.WriteString("</a>")
		return ast.WalkContinue, nil
	}
	kind := attrString(node, "data-kind")
	path := attrString(node, "data-path")
	w.WriteString("<a")
	switch kind {
	case "external":
		w.WriteString(` href="` + html.EscapeString(string(node.Destination)) + `" data-external="1" rel="noreferrer"`)
	case "doc":
		w.WriteString(` href="#" data-doc="` + html.EscapeString(path) + `"`)
		if frag := attrString(node, "data-frag"); frag != "" {
			w.WriteString(` data-frag="` + html.EscapeString(frag) + `"`)
		}
	case "open":
		w.WriteString(` href="#" data-open="` + html.EscapeString(path) + `"`)
	default: // ancla interna (#...) o enlace normal
		w.WriteString(` href="` + html.EscapeString(string(node.Destination)) + `"`)
	}
	if node.Title != nil {
		w.WriteString(` title="` + html.EscapeString(string(node.Title)) + `"`)
	}
	w.WriteString(">")
	return ast.WalkContinue, nil
}

func attrString(n ast.Node, name string) string {
	if v, ok := n.AttributeString(name); ok {
		switch t := v.(type) {
		case []byte:
			return string(t)
		case string:
			return t
		}
	}
	return ""
}
