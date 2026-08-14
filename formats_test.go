package main

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// mustRender corre el pipeline entero (conversor + goldmark) y devuelve el HTML.
func mustRender(t *testing.T, src, name string) RenderResult {
	t.Helper()
	res, err := Render([]byte(src), name)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func wants(t *testing.T, got string, name string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(got, s) {
			t.Errorf("%s: falta %q en la salida", name, s)
		}
	}
}

// ---- deteccion de formatos ----------------------------------------------

func TestFormatDetection(t *testing.T) {
	cases := map[string]string{
		"a.md": "Markdown", "a.json": "JSON", "a.csv": "CSV", "a.yaml": "YAML",
		"a.rst": "reStructuredText", "a.adoc": "AsciiDoc", "a.org": "Org",
		"a.ipynb": "Jupyter", "a.docx": "Word", "a.epub": "EPUB", "a.html": "HTML",
	}
	for name, want := range cases {
		if got := FormatName(name); got != want {
			t.Errorf("FormatName(%s) = %q, esperaba %q", name, got, want)
		}
		if !IsSupported(name) {
			t.Errorf("IsSupported(%s) = false", name)
		}
	}
	// codigo: lo reconoce chroma, no una lista nuestra
	for _, f := range []string{"main.go", "x.py", "y.rs", "Dockerfile"} {
		if !IsSupported(f) {
			t.Errorf("IsSupported(%s) = false (chroma deberia reconocerlo)", f)
		}
	}
	if IsSupported("foto.jpg") {
		t.Error("IsSupported(foto.jpg) deberia ser false")
	}
	if !IsMarkdown("a.md") || IsMarkdown("a.json") {
		t.Error("IsMarkdown no distingue Markdown de lo demas")
	}
}

// ---- datos ---------------------------------------------------------------

func TestJSON(t *testing.T) {
	// objeto suelto: se ve el JSON formateado
	res := mustRender(t, `{"a":1,"b":[true,null]}`, "cfg.json")
	wants(t, res.HTML, "json objeto", `data-lang="json"`, "&#34;a&#34;")
	if res.Format != "JSON" {
		t.Errorf("formato = %q", res.Format)
	}

	// arreglo de objetos: ademas de la fuente, sale una tabla
	res = mustRender(t, `[{"id":1,"nombre":"ana"},{"id":2,"nombre":"beto"}]`, "gente.json")
	wants(t, res.HTML, "json tabla", "<table>", "<th>id</th>", "<td>ana</td>", "<details>")

	// jsonc con comentarios y coma colgante: se limpia y se muestra igual
	res = mustRender(t, "{\n // hola\n \"x\": 1,\n}", "cfg.jsonc")
	wants(t, res.HTML, "jsonc", "&#34;x&#34;")

	// json roto: no explota, avisa y muestra el crudo
	res = mustRender(t, `{roto`, "malo.json")
	wants(t, res.HTML, "json roto", "roto")
}

func TestJSONL(t *testing.T) {
	res := mustRender(t, "{\"a\":1}\n{\"a\":2}\n", "log.jsonl")
	wants(t, res.HTML, "jsonl", "<table>", "<th>a</th>")
}

func TestCSV(t *testing.T) {
	res := mustRender(t, "nombre,edad\nana,33\nbeto,41\n", "gente.csv")
	wants(t, res.HTML, "csv", "<table>", "<th>nombre</th>", "<td>beto</td>", "2 filas")

	// punto y coma (el Excel en español) y pipes en el contenido
	res = mustRender(t, "a;b\nuno|dos;tres\n", "puntoycoma.csv")
	wants(t, res.HTML, "csv ;", "<table>", "uno|dos")

	// TSV
	res = mustRender(t, "x\ty\n1\t2\n", "t.tsv")
	wants(t, res.HTML, "tsv", "<table>", "<th>x</th>")
}

func TestCodeAndText(t *testing.T) {
	res := mustRender(t, "package main\n\nfunc main() {}\n", "main.go")
	wants(t, res.HTML, "go", `data-lang="go"`, "class=\"chroma\"")
	if res.Format != "Go" {
		t.Errorf("formato = %q, esperaba Go", res.Format)
	}

	res = mustRender(t, "linea 1\nlinea 2\n", "notas.txt")
	wants(t, res.HTML, "txt", "linea 1", "2 líneas")

	// el contenido con backticks no puede romper la cerca
	res = mustRender(t, "esto tiene ``` adentro\n", "raro.txt")
	wants(t, res.HTML, "cerca", "adentro")

	res = mustRender(t, "clave: valor\nlista:\n  - a\n", "conf.yaml")
	wants(t, res.HTML, "yaml", `data-lang="yaml"`)

	res = mustRender(t, "--- a/x\n+++ b/x\n-viejo\n+nuevo\n", "cambio.diff")
	wants(t, res.HTML, "diff", `data-lang="diff"`)
}

// ---- marcado -------------------------------------------------------------

func TestRST(t *testing.T) {
	src := "Titulo\n======\n\n" +
		"Un parrafo con ``literal`` y *enfasis*.\n\n" +
		".. note::\n   Ojo con esto.\n\n" +
		".. code-block:: python\n\n   print(\"hola\")\n\n" +
		"- item uno\n- item dos\n\n" +
		"Ver `el sitio <https://ejemplo.com>`_.\n"
	res := mustRender(t, src, "doc.rst")
	wants(t, res.HTML, "rst",
		"<h1", "Titulo", "<code>literal</code>", "alert-note", "Ojo con esto",
		`data-lang="python"`, "<li>item uno</li>", `href="https://ejemplo.com"`)
}

func TestAsciiDoc(t *testing.T) {
	src := "= Titulo\n\n== Seccion\n\nTexto con *fuerte* y _cursiva_.\n\nNOTE: cuidado.\n\n" +
		"[source,go]\n----\nfunc main() {}\n----\n\n* uno\n* dos\n"
	res := mustRender(t, src, "doc.adoc")
	wants(t, res.HTML, "adoc",
		"<h1", "Titulo", "<h2", "Seccion", "<strong>fuerte</strong>", "<em>cursiva</em>",
		"alert-note", `data-lang="go"`, "<li>uno</li>")
}

func TestOrg(t *testing.T) {
	src := "#+TITLE: Mi doc\n\n* Uno\n** Dos\n\nTexto con *fuerte* y /cursiva/ y =codigo=.\n\n" +
		"#+BEGIN_SRC go\nfunc main() {}\n#+END_SRC\n\n- a\n- b\n\n[[https://ejemplo.com][sitio]]\n"
	res := mustRender(t, src, "doc.org")
	wants(t, res.HTML, "org",
		"Mi doc", "<h1", "Uno", "<h2", "Dos", "<strong>fuerte</strong>", "<em>cursiva</em>",
		"<code>codigo</code>", `data-lang="go"`, `href="https://ejemplo.com"`)
}

func TestHTMLConv(t *testing.T) {
	src := `<html><head><title>Pagina</title><style>b{}</style></head><body>
<h1>Hola</h1><p>Un <b>parrafo</b> con <a href="https://x.com">enlace</a>.</p>
<ul><li>uno<li>dos</ul>
<pre><code class="language-go">func main() {}</code></pre>
<table><tr><th>a</th><th>b</th></tr><tr><td>1</td><td>2</td></tr></table>
</body></html>`
	res := mustRender(t, src, "pagina.html")
	wants(t, res.HTML, "html",
		"<h1", "Hola", "<strong>parrafo</strong>", `href="https://x.com"`,
		"<li>uno</li>", "<li>dos</li>", `data-lang="go"`, "<table>", "<th>a</th>")
	if strings.Contains(res.HTML, "b{}") {
		t.Error("html: el <style> no deberia pasar")
	}
}

// ---- documentos ----------------------------------------------------------

func TestNotebook(t *testing.T) {
	src := `{"nbformat":4,"metadata":{"language_info":{"name":"python"}},"cells":[
	 {"cell_type":"markdown","source":["# Titulo\n","texto\n"]},
	 {"cell_type":"code","source":"print('hola')","outputs":[
	   {"output_type":"stream","name":"stdout","text":["hola\n"]},
	   {"output_type":"execute_result","data":{"text/plain":["42"]}}]},
	 {"cell_type":"code","source":"1/0","outputs":[
	   {"output_type":"error","ename":"ZeroDivisionError","evalue":"division by zero",
	    "traceback":["\u001b[0;31mZeroDivisionError\u001b[0m: division by zero"]}]}]}`
	res := mustRender(t, src, "cuaderno.ipynb")
	wants(t, res.HTML, "ipynb",
		"Titulo", `data-lang="python"`, "hola", "42", "alert-caution", "ZeroDivisionError")
	if strings.Contains(res.HTML, "\x1b[") {
		t.Error("ipynb: quedaron codigos ANSI en la salida")
	}
}

// zipBytes arma un ZIP en memoria para las pruebas de docx/odt/epub.
func zipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDocx(t *testing.T) {
	doc := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
 <w:body>
  <w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Titulo</w:t></w:r></w:p>
  <w:p><w:r><w:t xml:space="preserve">Texto </w:t></w:r>
       <w:r><w:rPr><w:b/></w:rPr><w:t>fuerte</w:t></w:r>
       <w:r><w:t xml:space="preserve"> y normal</w:t></w:r></w:p>
  <w:p><w:hyperlink r:id="rId1"><w:r><w:t>un enlace</w:t></w:r></w:hyperlink></w:p>
  <w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>
       <w:r><w:t>item de lista</w:t></w:r></w:p>
  <w:tbl>
   <w:tr><w:tc><w:p><w:r><w:t>a</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>b</w:t></w:r></w:p></w:tc></w:tr>
   <w:tr><w:tc><w:p><w:r><w:t>1</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>2</w:t></w:r></w:p></w:tc></w:tr>
  </w:tbl>
 </w:body></w:document>`
	rels := `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
	 <Relationship Id="rId1" Target="https://ejemplo.com" TargetMode="External"/></Relationships>`
	num := `<?xml version="1.0"?><w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
	 <w:abstractNum w:abstractNumId="0"><w:lvl w:ilvl="0"><w:numFmt w:val="decimal"/></w:lvl></w:abstractNum>
	 <w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num></w:numbering>`
	z := zipBytes(t, map[string]string{
		"word/document.xml":            doc,
		"word/_rels/document.xml.rels": rels,
		"word/numbering.xml":           num,
	})
	res, err := Render(z, "carta.docx")
	if err != nil {
		t.Fatal(err)
	}
	wants(t, res.HTML, "docx",
		"<h1", "Titulo", "<strong>fuerte</strong>", `href="https://ejemplo.com"`,
		"<ol>", "item de lista", "<table>", "<th>a</th>", "<td>2</td>")
	if res.Format != "Word" {
		t.Errorf("formato = %q", res.Format)
	}
}

func TestODT(t *testing.T) {
	content := `<?xml version="1.0"?>
<office:document-content
  xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"
  xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0"
  xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0"
  xmlns:fo="urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0"
  xmlns:xlink="http://www.w3.org/1999/xlink">
 <office:automatic-styles>
  <style:style style:name="T1"><style:text-properties fo:font-weight="bold"/></style:style>
  <text:list-style style:name="L1"><text:list-level-style-number text:level="1"/></text:list-style>
 </office:automatic-styles>
 <office:body><office:text>
  <text:h text:outline-level="1">Titulo</text:h>
  <text:p>Texto con <text:span text:style-name="T1">negrita</text:span>.</text:p>
  <text:list text:style-name="L1"><text:list-item><text:p>primero</text:p></text:list-item></text:list>
  <text:p><text:a xlink:href="https://ejemplo.com">enlace</text:a></text:p>
 </office:text></office:body></office:document-content>`
	z := zipBytes(t, map[string]string{"content.xml": content})
	res, err := Render(z, "doc.odt")
	if err != nil {
		t.Fatal(err)
	}
	wants(t, res.HTML, "odt",
		"<h1", "Titulo", "<strong>negrita</strong>", "<ol>", "primero", `href="https://ejemplo.com"`)
}

func TestEPUB(t *testing.T) {
	container := `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
	 <rootfiles><rootfile full-path="OEBPS/book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`
	opf := `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0">
	 <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
	   <dc:title>El libro</dc:title><dc:creator>Alguien</dc:creator></metadata>
	 <manifest><item id="c1" href="cap1.xhtml" media-type="application/xhtml+xml"/>
	           <item id="c2" href="cap2.xhtml" media-type="application/xhtml+xml"/></manifest>
	 <spine><itemref idref="c1"/><itemref idref="c2"/></spine></package>`
	cap1 := `<html><body><h1>Capitulo uno</h1><p>Habia una vez.</p></body></html>`
	cap2 := `<html><body><h1>Capitulo dos</h1><p>Y colorin.</p></body></html>`
	z := zipBytes(t, map[string]string{
		"META-INF/container.xml": container,
		"OEBPS/book.opf":         opf,
		"OEBPS/cap1.xhtml":       cap1,
		"OEBPS/cap2.xhtml":       cap2,
	})
	res, err := Render(z, "libro.epub")
	if err != nil {
		t.Fatal(err)
	}
	wants(t, res.HTML, "epub",
		"El libro", "Alguien", "Capitulo uno", "Habia una vez", "Capitulo dos", "2 capítulos")
	if len(res.Toc) < 3 {
		t.Errorf("epub: el indice quedo con %d entradas", len(res.Toc))
	}
}

// ---- robustez ------------------------------------------------------------

// Ningun conversor puede entrar en panico ni colgarse con basura.
func TestConvertersSurviveGarbage(t *testing.T) {
	junk := make([]byte, 4096)
	for i := range junk {
		junk[i] = byte(i*37 + 11)
	}
	names := []string{"x.json", "x.jsonl", "x.csv", "x.tsv", "x.yaml", "x.xml", "x.txt",
		"x.rst", "x.adoc", "x.org", "x.wiki", "x.html", "x.ipynb", "x.docx", "x.odt",
		"x.epub", "x.go", "x.md"}
	for _, n := range names {
		for _, src := range [][]byte{junk, {}, []byte("\x00\x00"), []byte("<a"), []byte("{")} {
			res, err := Render(src, n)
			if err != nil {
				t.Errorf("%s: error %v", n, err)
			}
			_ = res.HTML
		}
	}
}

// En Windows los archivos de texto vienen con BOM o directamente en UTF-16.
func TestBOMyUTF16(t *testing.T) {
	res := mustRender(t, "\ufeff{\"a\":1}", "cfg.json")
	if strings.Contains(res.HTML, "errores de sintaxis") {
		t.Error("un JSON con BOM se reporto como roto")
	}
	// UTF-16 LE con BOM
	le := []byte{0xFF, 0xFE}
	for _, r := range "clave: valor\n" {
		le = append(le, byte(r), 0)
	}
	out, _ := toMarkdown(le, "x.yaml")
	if !strings.Contains(string(out), "clave: valor") {
		t.Errorf("UTF-16 LE mal decodificado: %q", string(out))
	}
	// UTF-16 BE con BOM
	be := []byte{0xFE, 0xFF}
	for _, r := range "hola" {
		be = append(be, 0, byte(r))
	}
	out, _ = toMarkdown(be, "x.txt")
	if !strings.Contains(string(out), "hola") {
		t.Errorf("UTF-16 BE mal decodificado: %q", string(out))
	}
}
