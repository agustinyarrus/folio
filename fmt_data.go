package main

// fmt_data.go — conversores de formatos de datos y de codigo fuente a Markdown.
//
// JSON / JSON Lines / CSV / TSV / YAML / TOML / INI / XML / diff / texto plano y, gracias a que
// chroma ya venia adentro, cualquier archivo de codigo que chroma reconozca (~250 lenguajes).

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
)

// ---- codigo y texto ------------------------------------------------------

// convCode: un archivo de codigo se muestra como un bloque resaltado con el
// lenguaje que chroma deduce del nombre del archivo.
func convCode(src []byte, path string) ([]byte, error) {
	lang := codeLexerName(path)
	if len(src) > maxHighlightBytes {
		lang = "" // demasiado grande para tokenizar: va plano
	}
	sub := FormatName(path) + " · " + plural(countLines(src), "línea", "líneas")
	return []byte(docHeader(path, sub) + codeBlock(string(src), lang)), nil
}

// convText: texto plano. Sin resaltado, pero respetando el formato original.
func convText(src []byte, path string) ([]byte, error) {
	sub := plural(countLines(src), "línea", "líneas")
	return []byte(docHeader(path, sub) + codeBlock(string(src), "")), nil
}

// convHighlight: el archivo va tal cual, resaltado con el lexer que corresponda
// (YAML, TOML, INI, diff…). Es lo mismo que convCode pero sin adivinar nada raro.
func convHighlight(src []byte, path string) ([]byte, error) {
	lang := codeLexerName(path)
	if lang == "" {
		lang = strings.TrimPrefix(extOf(path), ".")
	}
	if len(src) > maxHighlightBytes {
		lang = ""
	}
	sub := FormatName(path) + " · " + plural(countLines(src), "línea", "líneas")
	return []byte(docHeader(path, sub) + codeBlock(string(src), lang)), nil
}

// convXML: fuerza el lexer de XML, que cubre tambien plist, csproj, resx y demas.
func convXML(src []byte, path string) ([]byte, error) {
	lang := "xml"
	if len(src) > maxHighlightBytes {
		lang = ""
	}
	sub := FormatName(path) + " · " + plural(countLines(src), "línea", "líneas")
	return []byte(docHeader(path, sub) + codeBlock(string(src), lang)), nil
}

func countLines(src []byte) int {
	if len(src) == 0 {
		return 0
	}
	n := bytes.Count(src, []byte("\n"))
	if !bytes.HasSuffix(src, []byte("\n")) {
		n++
	}
	return n
}

// ---- JSON ----------------------------------------------------------------

const (
	maxTableRows = 2000 // mas que esto no entra en pantalla ni con paciencia
	maxTableCols = 24
	maxCellChars = 120
)

// stripJSONComments saca // y /* */ de un .jsonc / .json5 sin tocar lo que este
// adentro de una cadena. Tambien limpia las comas colgantes, que json5 permite.
func stripJSONComments(src []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(src))
	inStr, esc := false, false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if inStr {
			out.WriteByte(c)
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch {
		case c == '"':
			inStr = true
			out.WriteByte(c)
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			out.WriteByte('\n')
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i++
		default:
			out.WriteByte(c)
		}
	}
	// comas colgantes: ,] o ,}
	b := out.Bytes()
	var fin bytes.Buffer
	fin.Grow(len(b))
	for i := 0; i < len(b); i++ {
		if b[i] == ',' {
			j := i + 1
			for j < len(b) && (b[j] == ' ' || b[j] == '\t' || b[j] == '\n' || b[j] == '\r') {
				j++
			}
			if j < len(b) && (b[j] == ']' || b[j] == '}') {
				continue
			}
		}
		fin.WriteByte(b[i])
	}
	return fin.Bytes()
}

// jsonScalar pasa un valor de JSON a texto de celda.
func jsonScalar(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		return fmt.Sprintf("%t", t)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprint(t)
		}
		s := string(b)
		if len(s) > maxCellChars {
			s = s[:maxCellChars] + "…"
		}
		return s
	}
}

// jsonTable arma una tabla si el valor es un arreglo de objetos planos.
// Devuelve "" si no tiene esa forma (y entonces se muestra solo el JSON).
func jsonTable(v any) string {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return ""
	}
	var cols []string
	seen := map[string]bool{}
	objs := 0
	for _, it := range arr {
		m, ok := it.(map[string]any)
		if !ok {
			return "" // arreglo mixto: no es una tabla
		}
		objs++
		for _, k := range sortedKeys(m) {
			if !seen[k] && len(cols) < maxTableCols {
				seen[k] = true
				cols = append(cols, k)
			}
		}
	}
	if objs == 0 || len(cols) == 0 {
		return ""
	}
	rows := make([][]string, 0, len(arr))
	for i, it := range arr {
		if i >= maxTableRows {
			break
		}
		m := it.(map[string]any)
		r := make([]string, len(cols))
		for j, c := range cols {
			r[j] = jsonScalar(m[c])
		}
		rows = append(rows, r)
	}
	out := mdTable(cols, rows)
	if len(arr) > maxTableRows {
		out += fmt.Sprintf("*(se muestran %d de %d filas)*\n\n", maxTableRows, len(arr))
	}
	return out
}

// sortedKeys mantiene el orden estable sin depender del mapa.
func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// orden alfabetico: los mapas de Go no conservan el del archivo
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func detailsBlock(summary, body string) string {
	return "<details>\n<summary>" + summary + "</summary>\n\n" + body + "\n</details>\n\n"
}

func convJSON(src []byte, path string) ([]byte, error) {
	clean := src
	if e := extOf(path); e == ".jsonc" || e == ".json5" {
		clean = stripJSONComments(src)
	}
	var pretty bytes.Buffer
	valid := json.Indent(&pretty, bytes.TrimSpace(clean), "", "  ") == nil

	var v any
	dec := json.NewDecoder(bytes.NewReader(clean))
	dec.UseNumber()
	_ = dec.Decode(&v)

	sub := FormatName(path)
	if !valid {
		sub += " · con errores de sintaxis"
	}
	var b strings.Builder
	b.WriteString(docHeader(path, sub))

	body := string(src)
	if valid {
		body = pretty.String()
	}
	lang := "json"
	if len(body) > maxHighlightBytes {
		lang = ""
	}

	// arreglo de objetos: primero la tabla, el JSON completo queda plegado
	if tbl := jsonTable(v); tbl != "" {
		b.WriteString(tbl)
		b.WriteString(detailsBlock("Ver el JSON completo", codeBlock(body, lang)))
		return []byte(b.String()), nil
	}
	b.WriteString(codeBlock(body, lang))
	return []byte(b.String()), nil
}

func convJSONL(src []byte, path string) ([]byte, error) {
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	var items []any
	bad := 0
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		var v any
		dec := json.NewDecoder(strings.NewReader(ln))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			bad++
			continue
		}
		items = append(items, v)
	}
	sub := fmt.Sprintf("%s · %s", FormatName(path), plural(len(items), "registro", "registros"))
	if bad > 0 {
		sub += fmt.Sprintf(" · %s no se pudieron leer", plural(bad, "línea", "líneas"))
	}
	var b strings.Builder
	b.WriteString(docHeader(path, sub))
	if tbl := jsonTable(items); tbl != "" {
		b.WriteString(tbl)
		b.WriteString(detailsBlock("Ver el archivo completo", codeBlock(string(src), "json")))
		return []byte(b.String()), nil
	}
	b.WriteString(codeBlock(string(src), "json"))
	return []byte(b.String()), nil
}

// ---- CSV / TSV -----------------------------------------------------------

func convCSV(src []byte, path string) ([]byte, error) {
	r := csv.NewReader(bytes.NewReader(src))
	r.FieldsPerRecord = -1 // filas irregulares: las aceptamos igual
	r.LazyQuotes = true
	r.TrimLeadingSpace = false
	switch extOf(path) {
	case ".tsv", ".tab":
		r.Comma = '\t'
	default:
		r.Comma = detectDelimiter(src)
	}
	recs, err := r.ReadAll()
	if err != nil && len(recs) == 0 {
		return nil, err
	}
	if len(recs) == 0 {
		return []byte(docHeader(path, "vacío")), nil
	}
	head := recs[0]
	if len(head) > maxTableCols {
		head = head[:maxTableCols]
	}
	rows := recs[1:]
	truncated := false
	if len(rows) > maxTableRows {
		rows = rows[:maxTableRows]
		truncated = true
	}
	sub := fmt.Sprintf("%s · %s × %s", FormatName(path),
		plural(len(recs)-1, "fila", "filas"), plural(len(recs[0]), "columna", "columnas"))
	var b strings.Builder
	b.WriteString(docHeader(path, sub))
	b.WriteString(mdTable(head, rows))
	if truncated {
		b.WriteString(fmt.Sprintf("*(se muestran %d de %d filas)*\n\n", maxTableRows, len(recs)-1))
	}
	if err != nil {
		b.WriteString("> [!WARNING]\n> El archivo tiene filas mal formadas: " +
			mdEscape(err.Error()) + "\n\n")
	}
	return []byte(b.String()), nil
}

// detectDelimiter mira la primera linea y elige entre coma, punto y coma o pipe:
// los CSV que exporta Excel en español van con punto y coma.
func detectDelimiter(src []byte) rune {
	line := src
	if i := bytes.IndexByte(src, '\n'); i >= 0 {
		line = src[:i]
	}
	best, bestN := ',', 0
	for _, d := range []rune{',', ';', '\t', '|'} {
		if n := bytes.Count(line, []byte(string(d))); n > bestN {
			best, bestN = d, n
		}
	}
	return best
}
