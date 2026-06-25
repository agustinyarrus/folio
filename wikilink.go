package main

// wikilink.go — enlaces wiki estilo Obsidian / Foam:
//
//   [[Página]]            -> enlaza a Página.md (en la carpeta del documento)
//   [[Página|alias]]      -> mismo destino, texto visible "alias"
//   [[Página#sección]]    -> Página.md#sección
//   [[#sección]]          -> ancla dentro del documento actual
//   [[archivo.png]]       -> respeta la extensión si ya la trae
//
// No creamos un nodo propio: emitimos un *ast.Link normal con el destino ya armado, así el
// docTransformer existente lo clasifica (doc/anchor/open) y el linkRenderer lo dibuja igual que
// cualquier otro enlace (un solo camino de render).

import (
	"bytes"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

type wikilinkParser struct{}

func (p *wikilinkParser) Trigger() []byte { return []byte{'['} }

func (p *wikilinkParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, _ := block.PeekLine()
	if len(line) < 5 || line[0] != '[' || line[1] != '[' { // mínimo [[x]]
		return nil
	}
	rest := line[2:]
	end := bytes.Index(rest, []byte("]]"))
	if end < 0 {
		return nil
	}
	inner := string(rest[:end])
	if strings.TrimSpace(inner) == "" {
		return nil
	}
	block.Advance(2 + end + 2)

	target := inner
	display := ""
	if i := strings.Index(inner, "|"); i >= 0 {
		target, display = inner[:i], inner[i+1:]
	}
	target = strings.TrimSpace(target)
	display = strings.TrimSpace(display)
	if display == "" {
		display = target // texto visible: lo escrito (incluye #sección si la hay)
	}

	section := ""
	if i := strings.Index(target, "#"); i >= 0 {
		section, target = target[i:], target[:i]
	}
	dest := section
	if target != "" {
		if filepath.Ext(target) == "" {
			target += ".md"
		}
		dest = filepath.ToSlash(target) + section
	}

	link := ast.NewLink()
	link.Destination = []byte(dest)
	link.AppendChild(link, ast.NewString([]byte(display)))
	return link
}

type wikilinkExtension struct{}

func (e *wikilinkExtension) Extend(m goldmark.Markdown) {
	// prioridad ALTA (1) para ganarle al parser de enlaces nativo en el trigger `[`
	m.Parser().AddOptions(parser.WithInlineParsers(
		util.Prioritized(&wikilinkParser{}, 1),
	))
}
