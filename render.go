package main

// render.go — motor de render Markdown -> HTML.
//
// Todo el trabajo pesado vive en Go (goldmark + chroma), compilado dentro del .exe: sin CDN, sin
// JS de parsing que vendorizar. El cliente solo agrega KaTeX (matematica) y mermaid (diagramas)
// sobre el HTML ya generado, y sanea con DOMPurify.
//
// Soporta "todos los formatos" de Markdown: CommonMark + GFM (tablas, tachado, autolinks,
// listas de tareas) + footnotes + listas de definicion + tipografia + frontmatter YAML +
// matematica $...$ / $$...$$ (extension propia) + resaltado de codigo por lenguaje (chroma) +
// bloques ```markdown que se RENDERIZAN formateados (mdEmbed, recursion acotada) +
// bloques ```mermaid + alertas estilo GitHub (> [!NOTE], ver alerts.go) + emoji (:tada:) +
// marcas inline ==mark== / ++ins++ / ^sup^ / ~sub~ (inline_ext.go) + wikilinks [[Pagina]]
// (wikilink.go) + abreviaturas *[CLAVE]: (abbr.go) + contenedores ::: nombre (containers.go) +
// ids de encabezado propios {#id}. Resuelve imagenes/enlaces relativos a la carpeta del documento
// (incluido el salto a otro.md#seccion) y arma el indice (TOC) a partir de los encabezados.

import (
	"bytes"
	"html"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/yuin/goldmark"
	emoji "github.com/yuin/goldmark-emoji"
	meta "github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// ---- extensiones de archivo reconocidas como Markdown -------------------
var markdownExts = map[string]bool{
	".md": true, ".markdown": true, ".mdown": true, ".mkd": true, ".mkdn": true,
	".mdwn": true, ".mdtxt": true, ".mdtext": true, ".text": true, ".rmd": true,
	".qmd": true, ".mdx": true, ".litcoffee": false,
}

// MarkdownGlob es el filtro para el dialogo nativo de apertura.
const MarkdownGlob = "*.md;*.markdown;*.mdown;*.mkd;*.mkdn;*.mdwn;*.mdtxt;*.mdtext;*.text;*.rmd;*.qmd;*.mdx"

func IsMarkdown(name string) bool {
	return markdownExts[strings.ToLower(filepath.Ext(name))]
}

// ---- resultado de render ------------------------------------------------
type TocItem struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
	ID    string `json:"id"`
}

type RenderResult struct {
	HTML  string    `json:"html"`
	Title string    `json:"title"`
	Toc   []TocItem `json:"toc"`
	Words int       `json:"words"`
}

// ---- claves de contexto -------------------------------------------------
var (
	docDirKey  = parser.NewContextKey()
	tocKey     = parser.NewContextKey()
	firstH1Key = parser.NewContextKey()
)

// ---- singletons goldmark ------------------------------------------------
// md     : instancia principal (un bloque ```markdown se RENDERIZA como Markdown formateado).
// mdEmbed: instancia para ese render anidado; ahí ```markdown vuelve a ser código -> recursión
//
//	acotada a UN nivel (sin bucle infinito) y sin auto-id de encabezados (evita colisiones).
var md, mdEmbed goldmark.Markdown

func init() {
	mdEmbed = buildGoldmark(true)
	md = buildGoldmark(false)
}

func buildGoldmark(forEmbed bool) goldmark.Markdown {
	exts := []goldmark.Extender{
		extension.GFM,            // tablas + tachado + autolinks + listas de tareas
		extension.Footnote,       // notas al pie
		extension.DefinitionList, // listas de definicion
		extension.Typographer,    // comillas/guiones tipograficos
		meta.Meta,                // frontmatter YAML (lo oculta, también en el embebido)
		emoji.New(emoji.WithRenderingMethod(emoji.Unicode)), // :shortcodes: -> emoji real (offline)
		&mathExtension{},      // $...$  /  $$...$$
		&inlineExtensions{},   // ==marca== ++ins++ ^sup^ ~sub~
		&wikilinkExtension{},  // [[Pagina]] / [[Pagina|alias]] / [[Pagina#seccion]]
		&abbrExtension{},      // *[HTML]: ... -> <abbr>
		&containerExtension{}, // ::: warning ... :::  (fenced divs)
	}
	parserOpts := []parser.Option{
		parser.WithAttribute(), // # Encabezado {#id-propio .clase}
		parser.WithASTTransformers(
			util.Prioritized(&alertTransformer{}, 100), // marca alertas antes del docTransformer
			util.Prioritized(&docTransformer{}, 999),
		),
	}
	if !forEmbed {
		parserOpts = append(parserOpts, parser.WithAutoHeadingID()) // ids sólo en el doc externo
	}
	return goldmark.New(
		goldmark.WithExtensions(exts...),
		goldmark.WithParserOptions(parserOpts...),
		goldmark.WithRendererOptions(
			gmhtml.WithUnsafe(), // dejamos pasar HTML crudo; el cliente lo sanea con DOMPurify
			renderer.WithNodeRenderers(
				util.Prioritized(&codeRenderer{allowEmbed: !forEmbed}, 100),
				util.Prioritized(&linkRenderer{}, 100),
				util.Prioritized(&blockRenderer{}, 100), // blockquote normal + alertas GitHub
			),
		),
	)
}

// markdownLangs: lenguajes de cerca que se renderizan como Markdown formateado en vez de código.
var markdownLangs = map[string]bool{"markdown": true, "md": true, "mdc": true, "mdx": true, "mkd": true}

// renderEmbed convierte un bloque de Markdown anidado a HTML (vía mdEmbed, recursión acotada).
func renderEmbed(src string) (string, bool) {
	var buf bytes.Buffer
	if err := mdEmbed.Convert([]byte(src), &buf, parser.WithContext(parser.NewContext())); err != nil {
		return "", false
	}
	return buf.String(), true
}

// Render convierte el Markdown de docPath en HTML + metadatos.
func Render(src []byte, docPath string) (RenderResult, error) {
	docDir := filepath.Dir(docPath)
	pc := parser.NewContext()
	pc.Set(docDirKey, docDir)

	var buf bytes.Buffer
	if err := md.Convert(src, &buf, parser.WithContext(pc)); err != nil {
		return RenderResult{}, err
	}

	res := RenderResult{HTML: buf.String(), Words: countWords(src)}
	if v, ok := pc.Get(tocKey).([]TocItem); ok {
		res.Toc = v
	}
	res.Title = resolveTitle(pc, docPath)
	return res, nil
}

func resolveTitle(pc parser.Context, docPath string) string {
	if m := meta.Get(pc); m != nil {
		for _, k := range []string{"title", "Title"} {
			if t, ok := m[k].(string); ok && strings.TrimSpace(t) != "" {
				return strings.TrimSpace(t)
			}
		}
	}
	if h1, ok := pc.Get(firstH1Key).(string); ok && h1 != "" {
		return h1
	}
	base := filepath.Base(docPath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func countWords(src []byte) int {
	return len(strings.Fields(string(src)))
}

// =========================================================================
// docTransformer: corre sobre el AST ya parseado. Arma el TOC, capta el primer
// H1 y reescribe destinos de imagenes/enlaces relativos a la carpeta del doc.
// =========================================================================
type docTransformer struct{}

func (t *docTransformer) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	source := reader.Source()
	docDir, _ := pc.Get(docDirKey).(string)
	var toc []TocItem
	firstH1 := ""

	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := n.(type) {
		case *ast.Heading:
			txt := nodeText(node, source)
			id := ""
			if v, ok := node.AttributeString("id"); ok {
				if b, ok := v.([]byte); ok {
					id = string(b)
				} else if s, ok := v.(string); ok {
					id = s
				}
			}
			toc = append(toc, TocItem{Level: node.Level, Text: txt, ID: id})
			if firstH1 == "" && node.Level == 1 {
				firstH1 = txt
			}
		case *ast.Image:
			node.Destination = []byte(resolveAsset(docDir, string(node.Destination)))
		case *ast.Link:
			kind, resolved, frag := classifyLink(docDir, string(node.Destination))
			node.SetAttributeString("data-kind", []byte(kind))
			if resolved != "" {
				node.SetAttributeString("data-path", []byte(resolved))
			}
			if frag != "" {
				node.SetAttributeString("data-frag", []byte(frag))
			}
		}
		return ast.WalkContinue, nil
	})

	pc.Set(tocKey, toc)
	pc.Set(firstH1Key, firstH1)
}

// nodeText extrae el texto plano de un nodo (para titulos del TOC).
func nodeText(n ast.Node, source []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(source))
		case *ast.String:
			b.Write(t.Value)
		case *ast.AutoLink:
			b.Write(t.URL(source))
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(b.String())
}

// classifyLink decide como tratar un enlace y resuelve su ruta absoluta si es local.
//
//	anchor   -> #fragmento dentro del documento
//	external -> http(s)/mailto/tel: abre en el navegador del sistema
//	doc      -> otro archivo .md: lo abre dentro de Folio
//	open     -> otro archivo local: lo abre con la app del sistema
func classifyLink(docDir, dest string) (kind, resolved, frag string) {
	if dest == "" {
		return "normal", "", ""
	}
	if strings.HasPrefix(dest, "#") {
		return "anchor", "", ""
	}
	low := strings.ToLower(dest)
	for _, pre := range []string{"http://", "https://", "mailto:", "tel:", "ftp://"} {
		if strings.HasPrefix(low, pre) {
			return "external", "", ""
		}
	}
	p := dest
	if i := strings.IndexByte(p, '#'); i >= 0 { // preservar #seccion para saltar al abrir otro .md
		frag = p[i+1:]
		p = p[:i]
	}
	if j := strings.IndexByte(p, '?'); j >= 0 {
		p = p[:j]
	}
	if p == "" {
		return "anchor", "", ""
	}
	abs := filepath.FromSlash(p)
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(docDir, abs)
	}
	if IsMarkdown(abs) {
		return "doc", abs, frag
	}
	return "open", abs, "" // archivo no-md: el fragmento no aplica
}

// resolveAsset reescribe el src de una imagen relativa hacia el endpoint /asset del server.
func resolveAsset(docDir, src string) string {
	if src == "" {
		return src
	}
	low := strings.ToLower(src)
	for _, pre := range []string{"http://", "https://", "data:"} {
		if strings.HasPrefix(low, pre) {
			return src
		}
	}
	abs := filepath.FromSlash(src)
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(docDir, abs)
	}
	return "/asset?path=" + url.QueryEscape(abs)
}

// =========================================================================
// codeRenderer: bloques de codigo. ```mermaid pasa crudo; el resto lo resalta
// chroma con el estilo Tokyo Night. El codigo indentado va plano.
// =========================================================================
type codeRenderer struct{ allowEmbed bool } // allowEmbed: ```markdown se renderiza (no se resalta)

func (r *codeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, r.renderFenced)
	reg.Register(ast.KindCodeBlock, r.renderIndented)
}

func (r *codeRenderer) renderFenced(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	node := n.(*ast.FencedCodeBlock)
	lang := ""
	if l := node.Language(source); l != nil {
		lang = string(l)
	}
	code := codeText(n, source)

	if strings.EqualFold(lang, "mermaid") {
		w.WriteString(`<pre class="mermaid">`)
		w.WriteString(html.EscapeString(code))
		w.WriteString("</pre>\n")
		return ast.WalkSkipChildren, nil
	}

	// ```markdown / ```md / ```mdc ...  -> renderizar el Markdown adentro (caja con contenido formateado)
	if r.allowEmbed && markdownLangs[strings.ToLower(lang)] {
		if inner, ok := renderEmbed(code); ok {
			w.WriteString(`<div class="md-embed">` + "\n")
			w.WriteString(inner)
			w.WriteString("</div>\n")
			return ast.WalkSkipChildren, nil
		}
		// si el render anidado falla, cae al resaltado normal de abajo
	}

	w.WriteString(`<div class="codeblock"`)
	if lang != "" {
		w.WriteString(` data-lang="` + html.EscapeString(lang) + `"`)
	}
	w.WriteString(">")
	writeHighlighted(w, code, lang)
	w.WriteString("</div>\n")
	return ast.WalkSkipChildren, nil
}

func (r *codeRenderer) renderIndented(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	code := codeText(n, source)
	w.WriteString(`<div class="codeblock"><pre class="chroma"><code>`)
	w.WriteString(html.EscapeString(code))
	w.WriteString("</code></pre></div>\n")
	return ast.WalkSkipChildren, nil
}

func codeText(n ast.Node, source []byte) string {
	var b strings.Builder
	l := n.Lines()
	for i := 0; i < l.Len(); i++ {
		seg := l.At(i)
		b.Write(seg.Value(source))
	}
	return b.String()
}

// ---- chroma -------------------------------------------------------------
var (
	chromaFormatter = chromahtml.New(chromahtml.WithClasses(true), chromahtml.TabWidth(4))
	chromaCSSOnce   sync.Once
	chromaCSS       string
)

func writeHighlighted(w util.BufWriter, code, lang string) {
	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Analyse(code)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)
	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		w.WriteString(`<pre class="chroma"><code>` + html.EscapeString(code) + `</code></pre>`)
		return
	}
	if err := chromaFormatter.Format(w, folioCodeStyle, it); err != nil {
		w.WriteString(`<pre class="chroma"><code>` + html.EscapeString(code) + `</code></pre>`)
	}
}

// ChromaCSS devuelve (y cachea) el CSS de los tokens de codigo para el estilo Folio.
func ChromaCSS() string {
	chromaCSSOnce.Do(func() {
		var b bytes.Buffer
		_ = chromaFormatter.WriteCSS(&b, folioCodeStyle)
		chromaCSS = b.String()
	})
	return chromaCSS
}

// folioCodeStyle: paleta Tokyo Night alineada al esquema Nocturne del sistema.
var folioCodeStyle = chroma.MustNewStyle("folio", chroma.StyleEntries{
	chroma.Background:            "#c0caf5 bg:#0c0e15",
	chroma.LineHighlight:         "bg:#1b2030",
	chroma.Comment:               "italic #565f89",
	chroma.CommentHashbang:       "italic #565f89",
	chroma.CommentMultiline:      "italic #565f89",
	chroma.CommentPreproc:        "#7aa2f7",
	chroma.Keyword:               "#bb9af7",
	chroma.KeywordConstant:       "#ff9e64",
	chroma.KeywordDeclaration:    "#bb9af7",
	chroma.KeywordNamespace:      "#bb9af7",
	chroma.KeywordType:           "#2ac3de",
	chroma.Operator:              "#89ddff",
	chroma.OperatorWord:          "#bb9af7",
	chroma.Punctuation:           "#a9b1d6",
	chroma.Name:                  "#c0caf5",
	chroma.NameAttribute:         "#bb9af7",
	chroma.NameBuiltin:           "#2ac3de",
	chroma.NameBuiltinPseudo:     "#2ac3de",
	chroma.NameClass:             "#c0caf5",
	chroma.NameConstant:          "#ff9e64",
	chroma.NameDecorator:         "#7aa2f7",
	chroma.NameException:         "#f7768e",
	chroma.NameFunction:          "#7aa2f7",
	chroma.NameLabel:             "#2ac3de",
	chroma.NameNamespace:         "#c0caf5",
	chroma.NameTag:               "#f7768e",
	chroma.NameVariable:          "#c0caf5",
	chroma.NameVariableInstance:  "#e0af68",
	chroma.LiteralString:         "#9ece6a",
	chroma.LiteralStringEscape:   "#89ddff",
	chroma.LiteralStringInterpol: "#89ddff",
	chroma.LiteralStringRegex:    "#b4f9f8",
	chroma.LiteralStringSymbol:   "#9ece6a",
	chroma.LiteralNumber:         "#ff9e64",
	chroma.GenericHeading:        "bold #7aa2f7",
	chroma.GenericSubheading:     "bold #7aa2f7",
	chroma.GenericDeleted:        "#f7768e bg:#2d202a",
	chroma.GenericInserted:       "#9ece6a bg:#1f2a24",
	chroma.GenericEmph:           "italic",
	chroma.GenericStrong:         "bold",
	chroma.Error:                 "#f7768e",
})
