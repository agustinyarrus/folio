package main

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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

func lacks(t *testing.T, got string, name string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if strings.Contains(got, s) {
			t.Errorf("%s: sobra %q en la salida", name, s)
		}
	}
}

// before verifica que a aparezca antes que b (orden de columnas, de filas…).
func before(t *testing.T, got, name, a, b string) {
	t.Helper()
	ia, ib := strings.Index(got, a), strings.Index(got, b)
	if ia < 0 || ib < 0 || ia > ib {
		t.Errorf("%s: esperaba %q antes que %q (posiciones %d y %d)", name, a, b, ia, ib)
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

// ---- JSON ------------------------------------------------------------------

func TestJSON(t *testing.T) {
	// objeto suelto: se ve el JSON
	res := mustRender(t, `{"a":1,"b":[true,null]}`, "cfg.json")
	wants(t, res.HTML, "json objeto", `data-lang="json"`, "&#34;a&#34;")
	if res.Format != "JSON" {
		t.Errorf("formato = %q", res.Format)
	}

	// arreglo de objetos: tabla (numeros a la derecha) + el JSON plegado
	res = mustRender(t, `[{"id":1,"nombre":"ana"},{"id":2,"nombre":"beto"}]`, "gente.json")
	wants(t, res.HTML, "json tabla", "<table>", `<th style="text-align:right">id</th>`,
		"<th>nombre</th>", "<td>ana</td>", "<details>")

	// las columnas respetan el ORDEN DEL ARCHIVO (antes: alfabetico)
	res = mustRender(t, `[{"zeta":1,"alfa":2},{"medio":3,"zeta":4}]`, "orden.json")
	before(t, res.HTML, "orden de columnas", ">zeta</th>", ">alfa</th>")
	before(t, res.HTML, "union de claves", ">alfa</th>", ">medio</th>")

	// roto: no explota, avisa y muestra el crudo
	res = mustRender(t, `{roto`, "malo.json")
	wants(t, res.HTML, "json roto", "roto", "errores de sintaxis")
}

func TestJSONWithComments(t *testing.T) {
	// .jsonc: se ve EL ORIGINAL, comentarios incluidos (antes se reformateaba y se perdian)
	res := mustRender(t, "{\n // hola\n \"x\": 1,\n}", "cfg.jsonc")
	wants(t, res.HTML, "jsonc", "&#34;x&#34;", "hola", "con comentarios")
	lacks(t, res.HTML, "jsonc", "errores de sintaxis")

	// tsconfig.json: extension .json pero con comentarios y comas colgantes (lo normal)
	res = mustRender(t, "{\n  \"compilerOptions\": {\n    /* estricto */\n    \"strict\": true,\n  },\n}\n", "tsconfig.json")
	wants(t, res.HTML, "tsconfig", "estricto", "con comentarios")
	lacks(t, res.HTML, "tsconfig", "errores de sintaxis")

	// una cadena con ", ]" adentro: la limpieza de comas NO puede tocarla
	res = mustRender(t, `[{"t": "a, ]", "u": 1,},]`, "raro.jsonc")
	wants(t, res.HTML, "coma dentro de cadena", "<td>a, ]</td>")

	// JSON5: comillas simples, claves sin comillas, hexadecimal, Infinity
	res = mustRender(t, "[{nombre: 'ana \"la\" grande', edad: 0x1F, max: Infinity,},]", "datos.json5")
	wants(t, res.HTML, "json5", "<table>", ">nombre</th>", "ana &#34;la&#34; grande", ">31</td>", "Infinity")
}

func TestJSONMinifiedVsHandwritten(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < 40; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"k":"valor largo para inflar la linea","n":12345}`)
	}
	b.WriteString(`]}`)
	res := mustRender(t, b.String(), "min.json")
	wants(t, res.HTML, "minificado", "reformateado")

	hand := "{\n    \"a\": 1,\n    \"b\": 2\n}\n" // indentado a 4: se respeta
	res = mustRender(t, hand, "mano.json")
	lacks(t, res.HTML, "escrito a mano", "reformateado")
}

func TestJSONCellTruncationIsUTF8Safe(t *testing.T) {
	long := strings.Repeat("ñ", 300)
	res := mustRender(t, `[{"x":{"t":"`+long+`"}}]`, "ene.json")
	if !utf8.ValidString(res.HTML) {
		t.Fatal("el corte de celda partio un caracter UTF-8 al medio")
	}
	wants(t, res.HTML, "truncado", "…")
}

func TestJSONL(t *testing.T) {
	res := mustRender(t, "{\"a\":1}\n{\"a\":2,\"b\":\"x\"}\n", "log.jsonl")
	wants(t, res.HTML, "jsonl", "<table>", `>a</th>`, ">b</th>", "2 registros")

	// mezcla de objetos y escalares: no es tabla, pero se ve igual
	res = mustRender(t, "{\"a\":1}\n42\nroto{\n", "mezcla.jsonl")
	lacks(t, res.HTML, "jsonl mezcla", "<table>")
	wants(t, res.HTML, "jsonl mezcla", "no se pudieron leer")
}

// ---- CSV -------------------------------------------------------------------

func TestCSV(t *testing.T) {
	res := mustRender(t, "nombre,edad\nana,33\nbeto,41\n", "gente.csv")
	wants(t, res.HTML, "csv", "<table>", "<th>nombre</th>", `<th style="text-align:right">edad</th>`,
		"<td>beto</td>", "2 filas")

	// punto y coma (el Excel en español) y pipes en el contenido
	res = mustRender(t, "a;b\nuno|dos;tres\n", "puntoycoma.csv")
	wants(t, res.HTML, "csv ;", "<table>", "uno|dos")

	// TSV
	res = mustRender(t, "x\ty\n1\t2\n", "t.tsv")
	wants(t, res.HTML, "tsv", "<table>", ">x</th>")
}

func TestCSVSniffer(t *testing.T) {
	// la primera linea tiene UNA coma (entre comillas) y un punto y coma: gana el ';'
	src := "\"Apellido, Nombre\";Edad\n\"Perez, Ana\";33\n\"Gomez, Beto\";41\n"
	if d := sniffDelimiter([]byte(src)); d != ';' {
		t.Errorf("sniffDelimiter = %q, esperaba ';'", d)
	}
	res := mustRender(t, src, "excel.csv")
	wants(t, res.HTML, "sniffer", "<th>Apellido, Nombre</th>", "<td>Perez, Ana</td>")

	// casos borde del olfateador
	for _, c := range []struct {
		src  string
		want rune
	}{
		{"", ','},
		{"una sola columna\notra\n", ','},
		{"a|b|c\n1|2|3\n", '|'},
		{"a\tb\n1\t2\n", '\t'},
		{"a,b;c\n1,2;3\n4,5;6\n", ','}, // empate de consistencia: gana el primer candidato
	} {
		if got := sniffDelimiter([]byte(c.src)); got != c.want {
			t.Errorf("sniffDelimiter(%q) = %q, esperaba %q", c.src, got, c.want)
		}
	}
}

func TestCSVNumericAndWide(t *testing.T) {
	// numeros con coma decimal y separador de miles del Excel en español
	res := mustRender(t, "item;monto\nuno;1.234,56\ndos;7,5\n", "montos.csv")
	wants(t, res.HTML, "monto a la derecha", `<th style="text-align:right">monto</th>`)

	// mas columnas de las que entran: se avisa
	head := make([]string, 30)
	row := make([]string, 30)
	for i := range head {
		head[i] = "c" + string(rune('A'+i%26)) + string(rune('0'+i/26))
		row[i] = "v"
	}
	res = mustRender(t, strings.Join(head, ",")+"\n"+strings.Join(row, ",")+"\n", "ancho.csv")
	wants(t, res.HTML, "columnas de mas", "se muestran 24 de 30 columnas", "30 columnas")
}

// ---- codigo y texto ------------------------------------------------------------

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

func TestRSTTablesAndFigures(t *testing.T) {
	grid := "+--------+-------+\n" +
		"| Nombre | Edad  |\n" +
		"+========+=======+\n" +
		"| Ana    | 33    |\n" +
		"| (sigue)|       |\n" +
		"+--------+-------+\n" +
		"| Beto   | 41    |\n" +
		"+--------+-------+\n"
	res := mustRender(t, "Datos\n=====\n\n"+grid, "grid.rst")
	wants(t, res.HTML, "grid", "<table>", "<th>Nombre</th>", "<td>Ana (sigue)</td>", "<td>41</td>")

	simple := "=====  =====\nA      B\n=====  =====\n1      2\n3      4\n=====  =====\n\nfin\n"
	res = mustRender(t, simple, "simple.rst")
	wants(t, res.HTML, "simple", "<table>", "<th>A</th>", "<td>3</td>", "<td>4</td>", "fin")

	fig := ".. figure:: plano.png\n   :alt: el plano\n   :width: 300\n\n   Leyenda de la figura.\n"
	res = mustRender(t, fig, "fig.rst")
	wants(t, res.HTML, "figura", `alt="el plano"`, "<em>Leyenda de la figura.</em>")
	lacks(t, res.HTML, "figura", ":width:")

	// titulo con adorno arriba y abajo
	res = mustRender(t, "======\nArriba\n======\n\ntexto\n", "over.rst")
	wants(t, res.HTML, "overline", "<h1", "Arriba")
}

func TestAsciiDoc(t *testing.T) {
	src := "= Titulo\n\n== Seccion\n\nTexto con *fuerte* y _cursiva_.\n\nNOTE: cuidado.\n\n" +
		"[source,go]\n----\nfunc main() {}\n----\n\n* uno\n* dos\n"
	res := mustRender(t, src, "doc.adoc")
	wants(t, res.HTML, "adoc",
		"<h1", "Titulo", "<h2", "Seccion", "<strong>fuerte</strong>", "<em>cursiva</em>",
		"alert-note", `data-lang="go"`, "<li>uno</li>")
}

func TestAsciiDocBlocksAndTables(t *testing.T) {
	// una celda por renglon, con cols: 2 columnas
	tbl := "[cols=\"1,2\"]\n|===\n|Nombre\n|Rol\n\n|Ana\n|Diseño\n\n|Beto\n|Backend\n|===\n"
	res := mustRender(t, "= Doc\n\n"+tbl, "tabla.adoc")
	wants(t, res.HTML, "tabla adoc", "<table>", "<th>Nombre</th>", "<th>Rol</th>", "<td>Backend</td>")
	lacks(t, res.HTML, "tabla adoc", "cols=")

	// ==== (ejemplo) NO puede convertir el parrafo anterior en titulo
	res = mustRender(t, "= Doc\n\nun parrafo\n====\nadentro del ejemplo\n====\n", "ejemplo.adoc")
	if strings.Count(res.HTML, "<h1") != 1 || strings.Contains(res.HTML, "<h1 id=\"un-parrafo\"") {
		t.Errorf("==== volvio titulo al parrafo anterior:\n%s", res.HTML)
	}
	wants(t, res.HTML, "ejemplo", "adentro del ejemplo")

	// notas al pie, titulo de bloque, ancla y referencia cruzada
	src := "= Doc\n\n[[intro]]\n== Intro\n\n.Un titulo de bloque\nTexto footnote:[la nota].\n\nVer <<intro,la intro>>.\n"
	res = mustRender(t, src, "notas.adoc")
	wants(t, res.HTML, "notas adoc", `class="footnotes"`, "la nota", "<strong>Un titulo de bloque</strong>",
		`href="#intro"`, `id="intro"`)
	lacks(t, res.HTML, "notas adoc", "intro.md", "footnote:[")
}

func TestOrg(t *testing.T) {
	src := "#+TITLE: Mi doc\n\n* Uno\n** Dos\n\nTexto con *fuerte* y /cursiva/ y =codigo=.\n\n" +
		"#+BEGIN_SRC go\nfunc main() {}\n#+END_SRC\n\n- a\n- b\n\n[[https://ejemplo.com][sitio]]\n"
	res := mustRender(t, src, "doc.org")
	wants(t, res.HTML, "org",
		"Mi doc", "<h1", "Uno", "<h2", "Dos", "<strong>fuerte</strong>", "<em>cursiva</em>",
		"<code>codigo</code>", `data-lang="go"`, `href="https://ejemplo.com"`)
}

func TestOrgExtras(t *testing.T) {
	src := "* Tarea\n:PROPERTIES:\n:ID: 1234\n:END:\n" +
		"#+BEGIN_QUOTE\nuna cita\n#+END_QUOTE\n\n" +
		"[[file:foto.png]]\n\n- [X] hecho\n- [ ] pendiente\n\n" +
		"| a | b |\n| 1 | 2 |\n\n" +
		"Con nota[fn:1] y _subrayado_.\n\n[fn:1] La nota.\n\n" +
		"#+BEGIN_EXPORT latex\n\\secreto\n#+END_EXPORT\n"
	res := mustRender(t, src, "extras.org")
	wants(t, res.HTML, "org extras", "<blockquote>", "una cita", "<img", "foto.png",
		`checked=""`, "<table>", "<th>a</th>", `class="footnotes"`, "La nota", "<ins>subrayado</ins>")
	lacks(t, res.HTML, "org extras", ":PROPERTIES:", "1234", "secreto", "[X]")
}

func TestMediaWiki(t *testing.T) {
	src := "== Titulo ==\n\nTexto con '''negrita''' y una ref<ref>La fuente.</ref> y otra<ref name=\"a\">Nombrada</ref> y repetida<ref name=\"a\" />.\n\n" +
		"{|\n! Col 1 !! Col 2\n|-\n| uno || style=\"color:red\" | dos\n|}\n\n" +
		";Término : su definición\n\n* a\n*# a1\n"
	res := mustRender(t, src, "pagina.wiki")
	wants(t, res.HTML, "wiki", "<h2", "<strong>negrita</strong>", `class="footnotes"`, "La fuente.",
		"Nombrada", "<table>", "<th>Col 1</th>", "<td>dos</td>", "<dl>", "<dt>Término</dt>", "su definición")
	lacks(t, res.HTML, "wiki", "style=", "<ref")
	if n := strings.Count(res.HTML, `class="footnote-ref"`); n != 3 {
		t.Errorf("esperaba 3 llamadas a nota (una repetida), hay %d", n)
	}
	if n := strings.Count(res.HTML, "<li id=\"fn:"); n != 2 {
		t.Errorf("esperaba 2 notas distintas, hay %d", n)
	}
}

// ---- HTML -----------------------------------------------------------------------

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

func TestHTMLParserRobustness(t *testing.T) {
	// cierres implicitos: filas y celdas sin cerrar, <p> cortado por una lista
	src := `<table><tr><td>a<td>b<tr><td>c<td>d</table><p>antes<ul><li>x</ul>`
	md := htmlToMarkdown(src, nil)
	wants(t, md, "implicitos", "| a | b |", "| c | d |", "- x")

	// un '<' que no abre etiqueta es texto
	md = htmlToMarkdown(`<p>si a < b y b > c</p>`, nil)
	wants(t, md, "menor suelto", `a \< b y b > c`)

	// texto crudo: la busqueda del cierre no se desfasa con caracteres que cambian de largo
	md = htmlToMarkdown(`<script>var s = "İİİİİİİİ";</script><p>después</p>`, nil)
	wants(t, md, "script unicode", "después")
	lacks(t, md, "script unicode", "var s")

	// lista anidada DENTRO de un item numerado: 3 espacios, no 2
	md = htmlToMarkdown(`<ol><li>uno<ul><li>anidado</li></ul></li><li>dos</li></ol>`, nil)
	wants(t, md, "anidada", "1. uno\n   - anidado")
	res := mustRender(t, `<ol><li>uno<ul><li>anidado</li></ul></li><li>dos</li></ol>`, "lista.html")
	if !regexpMatch(`(?s)<ol>\s*<li>uno\s*<ul>\s*<li>anidado</li>`, res.HTML) {
		t.Errorf("la sublista no quedo dentro del item numerado:\n%s", res.HTML)
	}

	// las lineas en blanco de un bloque de codigo son del autor
	md = htmlToMarkdown("<pre>a\n\n\n\nb</pre>", nil)
	wants(t, md, "codigo con blancos", "a\n\n\n\nb")

	// sup/sub con espacios y kbd: HTML crudo
	md = htmlToMarkdown(`<p>marca<sup>tm registrada</sup> y <kbd>Ctrl</kbd></p>`, nil)
	wants(t, md, "sup", "<sup>tm registrada</sup>", "<kbd>Ctrl</kbd>")

	// un texto que arranca como sintaxis de Markdown se escapa
	md = htmlToMarkdown(`<p># no es titulo</p><p>1. no es lista</p><p>- tampoco</p>`, nil)
	wants(t, md, "escapes de inicio", `\# no es titulo`, `1\. no es lista`, `\- tampoco`)

	// ids: se conservan como anclas (encabezados con {#id}, el resto con <span id>)
	res = mustRender(t, `<h2 id="sec">Sec</h2><p id="p1">texto</p><a href="#p1">ir</a>`, "ids.html")
	wants(t, res.HTML, "ids", `id="sec"`, `<span id="p1"></span>`, `href="#p1"`)
}

// ---- documentos ------------------------------------------------------------

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

func TestNotebookLatexAndAttachments(t *testing.T) {
	src := `{"nbformat":4,"cells":[
	 {"cell_type":"markdown","source":"![graf](attachment:g.png)","attachments":{"g.png":{"image/png":"iVBORw0KGgo="}}},
	 {"cell_type":"code","source":"x","outputs":[{"output_type":"execute_result","data":{"text/latex":["\\frac{a}{b}"],"text/plain":["a/b"]}}]}]}`
	res := mustRender(t, src, "mate.ipynb")
	wants(t, res.HTML, "ipynb extras", "data:image/png;base64,iVBORw0KGgo=", `class="math math-display"`, `\frac{a}{b}`)
	lacks(t, res.HTML, "ipynb extras", "attachment:")
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

const wordNS = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` +
	`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`

func TestDocx(t *testing.T) {
	doc := `<?xml version="1.0"?>
<w:document ` + wordNS + `>
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

func TestDocxExtras(t *testing.T) {
	doc := `<?xml version="1.0"?>
<w:document ` + wordNS + `>
 <w:body>
  <w:p><w:r><w:t xml:space="preserve">` + "\u00a0" + `¿Anda?</w:t></w:r><w:r><w:rPr><w:b/></w:rPr><w:t>sí</w:t></w:r></w:p>
  <w:p><w:r><w:t>Con nota</w:t></w:r><w:r><w:footnoteReference w:id="2"/></w:r><w:r><w:t xml:space="preserve"> y fin.</w:t></w:r></w:p>
  <w:p><w:fldSimple w:instr=" HYPERLINK &quot;https://simple.com&quot; "><w:r><w:t>campo simple</w:t></w:r></w:fldSimple></w:p>
  <w:p><w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText xml:space="preserve"> HYPERLINK "https://complejo.com" </w:instrText></w:r>
       <w:r><w:fldChar w:fldCharType="separate"/></w:r><w:r><w:t>campo complejo</w:t></w:r><w:r><w:fldChar w:fldCharType="end"/></w:r></w:p>
  <w:p><w:r><w:rPr><w:rFonts w:ascii="Consolas"/></w:rPr><w:t>a` + "`" + `b</w:t></w:r></w:p>
  <w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>primero</w:t></w:r></w:p>
  <w:p><w:pPr><w:numPr><w:ilvl w:val="1"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>vineta anidada</w:t></w:r></w:p>
  <w:tbl>
   <w:tr><w:tc><w:tcPr><w:gridSpan w:val="2"/></w:tcPr><w:p><w:r><w:t>ancha</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>c</w:t></w:r></w:p></w:tc></w:tr>
   <w:tr><w:tc><w:p><w:r><w:t>x</w:t></w:r></w:p>
         <w:tbl><w:tr><w:tc><w:p><w:r><w:t>in1</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>in2</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:tc>
        <w:tc><w:p><w:r><w:t>y</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>z</w:t></w:r></w:p></w:tc></w:tr>
  </w:tbl>
 </w:body></w:document>`
	num := `<?xml version="1.0"?><w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
	 <w:abstractNum w:abstractNumId="0"><w:lvl w:ilvl="0"><w:numFmt w:val="decimal"/></w:lvl><w:lvl w:ilvl="1"><w:numFmt w:val="bullet"/></w:lvl></w:abstractNum>
	 <w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num></w:numbering>`
	notes := `<?xml version="1.0"?><w:footnotes xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
	 <w:footnote w:id="0"><w:p><w:r><w:separator/></w:r></w:p></w:footnote>
	 <w:footnote w:id="2"><w:p><w:r><w:footnoteRef/></w:r><w:r><w:t xml:space="preserve"> El texto de la nota.</w:t></w:r></w:p></w:footnote></w:footnotes>`
	z := zipBytes(t, map[string]string{
		"word/document.xml":  doc,
		"word/numbering.xml": num,
		"word/footnotes.xml": notes,
	})
	res, err := Render(z, "extras.docx")
	if err != nil {
		t.Fatal(err)
	}
	wants(t, res.HTML, "docx extras",
		"¿Anda?", "<strong>sí</strong>", // el NBSP + "¿" rompia el corte de espacios
		`class="footnotes"`, "El texto de la nota.",
		`href="https://simple.com"`, "campo simple", `href="https://complejo.com"`, "campo complejo",
		"<code>a`b</code>")
	if !regexpMatch(`(?s)<ol>\s*<li>primero\s*<ul>\s*<li>vineta anidada</li>`, res.HTML) {
		t.Errorf("la vineta de nivel 1 no quedo dentro del item numerado:\n%s", res.HTML)
	}
	// gridSpan relleno + tabla anidada aplanada en su celda: la fila de afuera sigue entera
	wants(t, res.HTML, "tablas", "<th>ancha</th>", "in1 · in2", "<td>z</td>")
	lacks(t, res.HTML, "docx extras", "HYPERLINK", "¿¿")
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

func TestODTExtras(t *testing.T) {
	styles := `<?xml version="1.0"?><office:document-styles
  xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"
  xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0"
  xmlns:fo="urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0"><office:styles>
  <style:style style:name="Strong_20_Emphasis"><style:text-properties fo:font-weight="bold"/></style:style>
 </office:styles></office:document-styles>`
	content := `<?xml version="1.0"?>
<office:document-content
  xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"
  xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0"
  xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0"
  xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0"
  xmlns:xlink="http://www.w3.org/1999/xlink">
 <office:automatic-styles>
  <style:style style:name="T9" style:parent-style-name="Strong_20_Emphasis"/>
  <text:list-style style:name="LN"><text:list-level-style-number text:level="1"/></text:list-style>
  <text:list-style style:name="LB"><text:list-level-style-bullet text:level="1"/></text:list-style>
 </office:automatic-styles>
 <office:body><office:text>
  <text:p>Heredado: <text:span text:style-name="T9">fuerte</text:span>.</text:p>
  <text:list text:style-name="LN">
   <text:list-item><text:p>uno</text:p>
    <text:list text:style-name="LB"><text:list-item><text:p>vineta</text:p></text:list-item></text:list>
   </text:list-item>
   <text:list-item><text:p>dos</text:p></text:list-item>
  </text:list>
  <text:p>Antes<text:note text:note-class="footnote"><text:note-citation>1</text:note-citation><text:note-body><text:p>La nota.</text:p></text:note-body></text:note> después.</text:p>
  <table:table><table:table-row><table:table-cell><text:p>h1</text:p></table:table-cell><table:table-cell><text:p>h2</text:p></table:table-cell></table:table-row>
   <table:table-row><table:table-cell><text:p>v1</text:p></table:table-cell><table:table-cell><text:p>v2</text:p></table:table-cell><table:table-cell table:number-columns-repeated="1000"/></table:table-row></table:table>
 </office:text></office:body></office:document-content>`
	z := zipBytes(t, map[string]string{"content.xml": content, "styles.xml": styles})
	res, err := Render(z, "extras.odt")
	if err != nil {
		t.Fatal(err)
	}
	wants(t, res.HTML, "odt extras", "<strong>fuerte</strong>", "<table>", "<th>h1</th>", "<td>v2</td>",
		`class="footnotes"`, "La nota.")
	// la sublista (vinetas) va DENTRO del item numerado y "dos" sigue siendo numerado
	if !regexpMatch(`(?s)<ol>\s*<li>uno\s*<ul>\s*<li>vineta</li>\s*</ul>\s*</li>\s*<li>dos</li>\s*</ol>`, res.HTML) {
		t.Errorf("listas anidadas mal:\n%s", res.HTML)
	}
	// la nota no parte el parrafo: "Antes" y "después" siguen juntos
	if !regexpMatch(`<p>Antes<sup[^>]*>.*?</sup> después\.</p>`, res.HTML) {
		t.Errorf("la nota partio el parrafo:\n%s", res.HTML)
	}
}

func TestEPUB(t *testing.T) {
	container := `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
	 <rootfiles><rootfile full-path="OEBPS/book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`
	opf := `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0">
	 <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
	   <dc:title>El libro</dc:title><dc:creator>Alguien</dc:creator></metadata>
	 <manifest><item id="c1" href="cap1.xhtml" media-type="application/xhtml+xml"/>
	           <item id="c2" href="cap%202.xhtml" media-type="application/xhtml+xml"/></manifest>
	 <spine><itemref idref="c1"/><itemref idref="c2"/></spine></package>`
	cap1 := `<html><body><svg><image xlink:href="img/tapa.png"/></svg><h1>Capitulo uno</h1><p>Habia una vez.</p>
	         <p>Ver la <a href="cap%202.xhtml#nota">nota</a> y el <a href="cap%202.xhtml">capitulo dos</a>.</p></body></html>`
	cap2 := `<html><body><h1>Capitulo dos</h1><p id="nota">Y colorin.</p></body></html>`
	z := zipBytes(t, map[string]string{
		"META-INF/container.xml": container,
		"OEBPS/book.opf":         opf,
		"OEBPS/cap1.xhtml":       cap1,
		"OEBPS/cap 2.xhtml":      cap2,
		"OEBPS/img/tapa.png":     "\x89PNG\r\n\x1a\nfalso",
	})
	res, err := Render(z, "libro.epub")
	if err != nil {
		t.Fatal(err)
	}
	wants(t, res.HTML, "epub",
		"El libro", "Alguien", "Capitulo uno", "Habia una vez", "Capitulo dos", "2 capítulos",
		`href="#c2-nota"`, `href="#c2"`, `id="c2-nota"`, `id="c2"`, "data:image/png;base64,")
	// un enlace a otro capitulo NO puede incrustar ese capitulo como data URI
	lacks(t, res.HTML, "epub", "application/octet-stream", "cap%202.xhtml")
	if len(res.Toc) < 3 {
		t.Errorf("epub: el indice quedo con %d entradas", len(res.Toc))
	}
}

func TestEPUBTitleFallbackOnWindowsPath(t *testing.T) {
	container := `<?xml version="1.0"?><container><rootfiles><rootfile full-path="b.opf"/></rootfiles></container>`
	opf := `<?xml version="1.0"?><package><metadata/><manifest><item id="a" href="a.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="a"/></spine></package>`
	z := zipBytes(t, map[string]string{"META-INF/container.xml": container, "b.opf": opf,
		"a.xhtml": "<html><body><p>texto</p></body></html>"})
	res, err := Render(z, `C:\libros\sin titulo.epub`)
	if err != nil {
		t.Fatal(err)
	}
	wants(t, res.HTML, "titulo", ">sin titulo</h1>")
	lacks(t, res.HTML, "titulo", `libros`)
}

// ---- Markdown: abreviaturas y wikilinks ---------------------------------------------

func TestAbbreviationsUnicodeAndPunctuation(t *testing.T) {
	src := "*[C++]: el lenguaje\n*[.NET]: la plataforma\n*[AÑO]: periodo\n*[HTML]: marcado\n*[HTML5]: la version 5\n\n" +
		"Usamos C++ y .NET cada AÑO, con HTML5 y HTML. Pero no HTMLX ni AÑOS.\n"
	res := mustRender(t, src, "abbr.md")
	wants(t, res.HTML, "abbr",
		`<abbr title="el lenguaje">C++</abbr>`, `<abbr title="la plataforma">.NET</abbr>`,
		`<abbr title="periodo">AÑO</abbr>`, `<abbr title="la version 5">HTML5</abbr>`,
		`<abbr title="marcado">HTML</abbr>.`)
	lacks(t, res.HTML, "abbr", `>HTMLX`, `AÑO</abbr>S`)
}

func TestWikiEmbedsAndVault(t *testing.T) {
	vault := t.TempDir()
	notes := filepath.Join(vault, "notas", "sub")
	must(t, os.MkdirAll(notes, 0o755))
	must(t, os.MkdirAll(filepath.Join(vault, ".obsidian"), 0o755))
	must(t, os.MkdirAll(filepath.Join(vault, "adjuntos"), 0o755))
	must(t, os.WriteFile(filepath.Join(vault, "adjuntos", "foto.png"), []byte("x"), 0o644))
	must(t, os.WriteFile(filepath.Join(vault, "Otra Nota.md"), []byte("# otra"), 0o644))

	doc := filepath.Join(notes, "nota.md")
	res, err := Render([]byte("![[foto.png|300]]\n\n![[falta.png]]\n\n[[Otra Nota#Sección 1]]\n"), doc)
	if err != nil {
		t.Fatal(err)
	}
	wants(t, res.HTML, "embeds", `<img src="/asset?path=`, `width="300"`, "adjuntos", "foto.png",
		`data-doc="`+filepath.Join(vault, "Otra Nota.md"), `data-frag="Sección 1"`)
	lacks(t, res.HTML, "embeds", "![[")
}

func regexpMatch(pattern, s string) bool { return regexp.MustCompile(pattern).MatchString(s) }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// ---- robustez ------------------------------------------------------------

// Ningun conversor puede entrar en panico ni colgarse con basura.
func TestConvertersSurviveGarbage(t *testing.T) {
	junk := make([]byte, 4096)
	for i := range junk {
		junk[i] = byte(i*37 + 11)
	}
	names := []string{"x.json", "x.jsonc", "x.json5", "x.jsonl", "x.csv", "x.tsv", "x.yaml", "x.xml",
		"x.txt", "x.rst", "x.adoc", "x.org", "x.wiki", "x.html", "x.ipynb", "x.docx", "x.odt",
		"x.epub", "x.go", "x.md"}
	inputs := [][]byte{junk, {}, []byte("\x00\x00"), []byte("<a"), []byte("{"), []byte("[{"),
		[]byte("|===\n|a"), []byte("+--+\n| a"), []byte("{|\n| a"), []byte("'''"), []byte("<<<<"),
		[]byte("[[x"), []byte("![["), []byte("*[x]: y\nx x x")}
	for _, n := range names {
		for _, src := range inputs {
			res, err := Render(src, n)
			if err != nil {
				t.Errorf("%s: error %v", n, err)
			}
			if !utf8.ValidString(res.HTML) && utf8.Valid(src) {
				t.Errorf("%s: salida UTF-8 invalida", n)
			}
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

// ---- servidor ---------------------------------------------------------------------

func TestOnlyLocalHost(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := onlyLocalHost("127.0.0.1:5555", ok)
	for host, want := range map[string]int{
		"127.0.0.1:5555":         204,
		"localhost:5555":         204,
		"LOCALHOST:5555":         204,
		"evil.example.com:5555":  403, // DNS rebinding: el dominio del atacante
		"127.0.0.1:6666":         403,
		"":                       403,
		"127.0.0.1.nip.io:5555":  403,
		"[::1]:5555":             403,
		"attacker.com":           403,
		"127.0.0.1:5555.evil.io": 403,
	} {
		req := httptest.NewRequest("GET", "/render?path=C:/secreto.txt", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Host %q: %d, esperaba %d", host, rec.Code, want)
		}
	}
}

func TestSettleWaitsForTheLastWrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "doc.md")
	must(t, os.WriteFile(p, []byte("a"), 0o644))
	first, _ := stampOf(p)
	go func() { // un guardado en dos pasos: truncar y escribir
		time.Sleep(20 * time.Millisecond)
		_ = os.WriteFile(p, []byte(""), 0o644)
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(p, []byte("contenido final"), 0o644)
	}()
	got := settle(context.Background(), p, first)
	final, _ := stampOf(p)
	if !got.same(final) || got.size != int64(len("contenido final")) {
		t.Errorf("settle devolvio %+v, el archivo quedo en %+v", got, final)
	}
}

func TestReadDocumentIsBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("escribe un archivo de ~49 MB")
	}
	p := filepath.Join(t.TempDir(), "enorme.log")
	f, err := os.Create(p)
	must(t, err)
	chunk := bytes.Repeat([]byte("linea de log\n"), 1<<16)
	for written := 0; written <= maxSourceBytes+len(chunk); written += len(chunk) {
		_, err = f.Write(chunk)
		must(t, err)
	}
	must(t, f.Close())
	data, err := readDocument(p)
	must(t, err)
	if len(data) != maxSourceBytes+1 {
		t.Fatalf("leyo %d bytes, esperaba el tope %d", len(data), maxSourceBytes+1)
	}
	out, _ := toMarkdown(data, p)
	if !strings.Contains(string(out[len(out)-200:]), "truncado") {
		t.Error("no aviso que el archivo se trunco")
	}
}

// ---- ids de encabezado ----------------------------------------------------------

func TestHeadingIDsLikeGitHub(t *testing.T) {
	src := "# Configuración del índice\n\n## Tablas\n\n## Tablas\n\n## Ver [la doc](https://ejemplo.com/x) ahora\n\n" +
		"## Uno {#dos}\n\n## Dos\n\n## ¿Qué pasa? ¡Nada!\n\n## `código` y *énfasis*\n\n## ¡!\n"
	res := mustRender(t, src, "ids.md")
	wants(t, res.HTML, "slugs",
		`id="configuración-del-índice"`, // antes: configuracin-del-ndice
		`id="tablas"`, `id="tablas-1"`,  // duplicados como GitHub
		`id="ver-la-doc-ahora"`,  // la URL del enlace NO entra al id
		`id="dos"`, `id="dos-1"`, // el explicito se reserva primero
		`id="qué-pasa-nada"`, `id="código-y-énfasis"`, `id="seccion"`)
	if len(res.Toc) != 9 || res.Toc[0].ID != "configuración-del-índice" {
		t.Errorf("toc = %+v", res.Toc)
	}
	// el conteo de palabras es de lectura: la sintaxis no suma
	res = mustRender(t, "# Hola mundo\n\n| a | b |\n|---|---|\n| uno | dos |\n\n```\ncodigo no cuenta\n```\n", "w.md")
	if res.Words != 6 {
		t.Errorf("palabras = %d, esperaba 6 (hola, mundo, a, b, uno, dos)", res.Words)
	}
}
