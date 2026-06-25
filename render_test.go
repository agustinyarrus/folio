package main

import (
	"os"
	"strings"
	"testing"
)

// TestRenderTorture corre el pipeline completo sobre testdocs/torture.md y verifica que cada
// "formato" produzca el HTML esperado. Escribe el resultado a testdocs/torture.out.html.
func TestRenderTorture(t *testing.T) {
	src, err := os.ReadFile("testdocs/torture.md")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Render(src, "testdocs/torture.md")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile("testdocs/torture.out.html", []byte(res.HTML), 0o644)

	checks := map[string]string{
		"math display":   `class="math math-display"`,
		"math inline":    `class="math math-inline"`,
		"mermaid":        `<pre class="mermaid">`,
		"chroma code":    `class="chroma"`,
		"codeblock wrap": `class="codeblock"`,
		"lang go":        `data-lang="go"`,
		"table":          `<table>`,
		"footnotes":      `class="footnotes"`,
		"def list":       `<dl>`,
		"task checkbox":  `type="checkbox"`,
		"strikethrough":  `<del>`,
		"external link":  `data-external="1"`,
		"anchor link":    `href="#tablas"`,
		"heading id":     `id="tablas"`,
		// --- formatos universales nuevos ---
		"alert note":      `alert alert-note`,
		"alert tip":       `alert alert-tip`,
		"alert important": `alert alert-important`,
		"alert warning":   `alert alert-warning`,
		"alert caution":   `alert alert-caution`,
		"alert title":     `class="alert-title"`,
		"alert icon":      `class="alert-ico"`,
		"emoji tada":      "\U0001F389", // :tada: -> 🎉
		"emoji rocket":    "\U0001F680", // :rocket: -> 🚀
		"emoji thumbsup":  "\U0001F44D", // :+1: -> 👍
		"mark":            `<mark>`,
		"ins":             `<ins>`,
		"superscript md":  `<sup>10</sup>`, // de e^10^ (valor único, no del HTML)
		"subscript md":    `<sub>`,
		"wikilink doc":    `Arquitectura.md`,
		"wikilink alias":  `ver el diseño`,
		"wikilink anchor": `href="#emoji"`,
		"heading custom id": `id="mi-ancla"`,
		"details":         `<details>`,
		"summary":         `<summary>`,
		"abbr":            `<abbr title=`,
		"kbd":             `<kbd>`,
		// --- abreviaturas automáticas, contenedores, link#frag ---
		"abbr auto":          `<abbr title="Application Programming Interface">API</abbr>`,
		"container tip":      `callout callout-tip`,
		"container warning":  `callout callout-warning`,
		"container danger":   `callout callout-danger`,
		"container title":    `class="callout-title"`,
		"container custom title": `Atención especial`,
		"container raw class": `class="nota-propia"`,
		"container raw id":     `id="seccion-x"`,
		"doc link fragment":   `data-frag="tablas"`,
		"wikilink fragment":   `data-frag="encabezado"`,
		// --- bloque ```markdown renderizado ---
		"md embed box":     `class="md-embed"`,
		"md embed heading": `<h1>Título embebido</h1>`,
		"md embed bold":    `<strong>resaltado-embebido</strong>`,
	}
	for name, want := range checks {
		if !strings.Contains(res.HTML, want) {
			t.Errorf("falta [%s]: no se encontro %q", name, want)
		}
	}

	// estos literales deben haber DESAPARECIDO (prueba de que se convirtieron, no que pasaron crudos)
	absent := map[string]string{
		"mark sin convertir":     `==resaltado==`,
		"wikilink sin convertir": `[[Arquitectura]]`,
		"emoji sin convertir":    `:tada:`,
		"alerta sin convertir":   `[!NOTE]`,
		"superscript sin conv":   `e^10^`,
		"abbr def sin convertir": `*[API]:`,
		"contenedor sin conv":    `::: tip`,
		"md embed sin render":    `**resaltado-embebido**`,
	}
	for name, bad := range absent {
		if strings.Contains(res.HTML, bad) {
			t.Errorf("[%s]: el literal %q quedó sin convertir", name, bad)
		}
	}
	if res.Title != "Folio · Prueba de fuego" {
		t.Errorf("title = %q (esperaba frontmatter)", res.Title)
	}
	if len(res.Toc) < 10 {
		t.Errorf("toc tiene %d items (esperaba >=10)", len(res.Toc))
	}
	t.Logf("OK · title=%q · toc=%d · words=%d · htmlLen=%d", res.Title, len(res.Toc), res.Words, len(res.HTML))
}
