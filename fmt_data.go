package main

// fmt_data.go — conversores de formatos de datos y de codigo fuente a Markdown.
//
// JSON / JSON Lines / CSV / TSV / YAML / TOML / INI / XML / diff / texto plano y, gracias a que
// chroma ya venia adentro, cualquier archivo de codigo que chroma reconozca (~250 lenguajes).
//
// Dos decisiones que atraviesan todo el archivo:
//
//   - El visor muestra el archivo TAL CUAL lo escribio su autor. Solo se reformatea un JSON
//     minificado (ilegible de otra forma); uno escrito a mano conserva su indentacion y, sobre
//     todo, sus comentarios: un tsconfig.json o un settings.json de VS Code sin sus comentarios
//     es otro archivo.
//   - Las tablas respetan el orden del archivo: las columnas de un JSON salen en el orden en que
//     aparecen las claves (encoding/json lo pierde al decodificar a un map), y el delimitador de
//     un CSV se deduce mirando varias lineas, no una sola.

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
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

// ---- limites de las tablas ------------------------------------------------

const (
	maxTableRows = 2000 // mas que esto no entra en pantalla ni con paciencia
	maxTableCols = 24
	maxCellChars = 120

	// Un JSON se considera minificado (y entonces SI se reformatea) cuando sus
	// renglones promedian mas de esto: ningun humano escribe lineas de 160
	// caracteres de JSON a mano, y un minificador las hace de megabytes.
	minifiedAvgLine = 160
	minifiedMinSize = 400
)

// truncRunes corta s a lo sumo en n runas, sin partir un caracter UTF-8 al medio
// (cortar por bytes deja una secuencia invalida que el navegador pinta como �).
func truncRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	i, count := 0, 0
	for i < len(s) && count < n {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		count++
	}
	return s[:i] + "…"
}

// ============================================================================
//  JSON
// ============================================================================

// jsonObject es un objeto JSON que recuerda el orden de sus claves. encoding/json
// lo pierde al decodificar a map[string]any, y en una tabla ese orden ES la
// informacion: "id, nombre, fecha" no es lo mismo que "fecha, id, nombre".
type jsonObject struct {
	keys []string
	vals map[string]any
}

// MarshalJSON serializa respetando el orden original (para las celdas que
// contienen un objeto anidado).
func (o *jsonObject) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		vb, err := json.Marshal(o.vals[k])
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// decodeOrdered lee UN valor del decoder conservando el orden de las claves de
// cada objeto. Descenso recursivo sobre el stream de tokens: O(n) en el tamaño
// del valor. Los escalares salen como string, json.Number, bool o nil.
func decodeOrdered(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		return tok, nil
	}
	switch delim {
	case '{':
		obj := &jsonObject{vals: map[string]any{}}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, _ := kt.(string)
			val, err := decodeOrdered(dec)
			if err != nil {
				return nil, err
			}
			if _, dup := obj.vals[key]; !dup {
				obj.keys = append(obj.keys, key)
			}
			obj.vals[key] = val
		}
		_, err := dec.Token() // '}'
		return obj, err
	case '[':
		arr := []any{}
		for dec.More() {
			val, err := decodeOrdered(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, val)
		}
		_, err := dec.Token() // ']'
		return arr, err
	}
	return nil, fmt.Errorf("delimitador inesperado %q", delim)
}

// jsonRecords extrae los objetos de un arreglo JSON de nivel superior, si lo es.
// Decodifica en detalle solo las primeras maxTableRows filas; del resto, que no
// entra en la tabla, alcanza con contarlas y confirmar que son objetos (van como
// RawMessage: ni un map allocado). Devuelve ok=false si no tiene forma de tabla.
func jsonRecords(src []byte) (rows []*jsonObject, total int, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(src))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return nil, 0, false
	}
	for dec.More() {
		if total < maxTableRows {
			v, err := decodeOrdered(dec)
			obj, isObj := v.(*jsonObject)
			if err != nil || !isObj {
				return nil, 0, false
			}
			rows = append(rows, obj)
		} else {
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return nil, 0, false
			}
			if t := bytes.TrimLeft(raw, " \t\r\n"); len(t) == 0 || t[0] != '{' {
				return nil, 0, false
			}
		}
		total++
	}
	return rows, total, total > 0
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
		return strconv.FormatBool(t)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprint(t)
		}
		return truncRunes(string(b), maxCellChars)
	}
}

// recordsTable arma la tabla GFM de una lista de objetos. Las columnas son la union
// de las claves EN ORDEN DE PRIMERA APARICION (un set para no repetir: O(total de
// claves)); una columna cuyos valores son todos numeros se alinea a la derecha.
func recordsTable(rows []*jsonObject, total int) string {
	var cols []string
	seen := map[string]bool{}
	for _, r := range rows {
		for _, k := range r.keys {
			if !seen[k] && len(cols) < maxTableCols {
				seen[k] = true
				cols = append(cols, k)
			}
		}
	}
	if len(cols) == 0 {
		return ""
	}
	numeric := make([]bool, len(cols))
	for j := range numeric {
		numeric[j] = true
	}
	cells := make([][]string, len(rows))
	for i, r := range rows {
		line := make([]string, len(cols))
		for j, c := range cols {
			v, present := r.vals[c]
			line[j] = jsonScalar(v)
			if _, isNum := v.(json.Number); present && v != nil && !isNum {
				numeric[j] = false
			}
		}
		cells[i] = line
	}
	align := make([]byte, len(cols))
	for j, isNum := range numeric {
		if isNum {
			align[j] = 'r'
		}
	}
	out := mdTableAligned(cols, cells, align)
	if total > len(rows) {
		out += fmt.Sprintf("*(se muestran %d de %d filas)*\n\n", len(rows), total)
	}
	if extra := countDistinctKeys(rows) - len(cols); extra > 0 {
		out += fmt.Sprintf("*(%s más no entran en la tabla: están en el JSON completo)*\n\n",
			plural(extra, "columna", "columnas"))
	}
	return out
}

func countDistinctKeys(rows []*jsonObject) int {
	seen := map[string]bool{}
	for _, r := range rows {
		for _, k := range r.keys {
			seen[k] = true
		}
	}
	return len(seen)
}

func detailsBlock(summary, body string) string {
	return "<details>\n<summary>" + summary + "</summary>\n\n" + body + "\n</details>\n\n"
}

// looksMinified: renglones promedio demasiado largos para haber sido escritos a mano.
func looksMinified(src []byte) bool {
	if len(src) < minifiedMinSize {
		return bytes.Count(src, []byte("\n")) == 0 && len(src) > 120
	}
	lines := bytes.Count(src, []byte("\n")) + 1
	return len(src)/lines > minifiedAvgLine
}

// convJSON muestra un JSON. Tres casos, en este orden:
//  1. JSON estricto valido: si esta minificado se reformatea; si no, va tal cual.
//  2. JSON con comentarios o comas colgantes (tsconfig.json, settings.json de VS Code,
//     .jsonc, .json5): se normaliza SOLO para validarlo y armar la tabla; la vista es el
//     original, resaltado con el lexer de JavaScript (el de JSON pinta los /* */ como error).
//  3. Roto: el crudo, con aviso.
//
// Si el valor es un arreglo de objetos, arriba va la tabla y el JSON queda plegado.
func convJSON(src []byte, path string) ([]byte, error) {
	trimmed := bytes.TrimSpace(src)
	strict := json.Valid(trimmed)

	data := trimmed // lo que se decodifica para la tabla
	relaxed := false
	if !strict {
		if norm := normalizeJSON5(trimmed); json.Valid(norm) {
			data, relaxed = norm, true
		}
	}

	sub := FormatName(path)
	switch {
	case relaxed:
		sub += " · con comentarios"
	case !strict:
		sub += " · con errores de sintaxis"
	}

	body, lang := string(src), "json"
	if relaxed {
		lang = "javascript"
	} else if strict && looksMinified(trimmed) {
		var pretty bytes.Buffer
		if json.Indent(&pretty, trimmed, "", "  ") == nil {
			body = pretty.String()
			sub += " · reformateado"
		}
	}
	if len(body) > maxHighlightBytes {
		lang = ""
	}

	var b strings.Builder
	b.WriteString(docHeader(path, sub))
	if strict || relaxed {
		if rows, total, ok := jsonRecords(data); ok {
			if tbl := recordsTable(rows, total); tbl != "" {
				b.WriteString(tbl)
				b.WriteString(detailsBlock("Ver el JSON completo", codeBlock(body, lang)))
				return []byte(b.String()), nil
			}
		}
	}
	b.WriteString(codeBlock(body, lang))
	return []byte(b.String()), nil
}

// convJSONL: un objeto JSON por linea (logs estructurados, datasets).
func convJSONL(src []byte, path string) ([]byte, error) {
	var rows []*jsonObject
	total, bad, nonObject := 0, 0, 0
	for _, ln := range bytes.Split(src, []byte("\n")) {
		ln = bytes.TrimSpace(ln)
		if len(ln) == 0 {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(ln))
		dec.UseNumber()
		v, err := decodeOrdered(dec)
		if err != nil {
			bad++
			continue
		}
		total++
		obj, isObj := v.(*jsonObject)
		if !isObj {
			nonObject++
			continue
		}
		if len(rows) < maxTableRows {
			rows = append(rows, obj)
		}
	}
	sub := fmt.Sprintf("%s · %s", FormatName(path), plural(total, "registro", "registros"))
	if bad > 0 {
		sub += fmt.Sprintf(" · %s no se pudieron leer", plural(bad, "línea", "líneas"))
	}
	var b strings.Builder
	b.WriteString(docHeader(path, sub))
	if nonObject == 0 && len(rows) > 0 {
		if tbl := recordsTable(rows, total); tbl != "" {
			b.WriteString(tbl)
			b.WriteString(detailsBlock("Ver el archivo completo", codeBlock(string(src), "json")))
			return []byte(b.String()), nil
		}
	}
	b.WriteString(codeBlock(string(src), "json"))
	return []byte(b.String()), nil
}

// ---- JSON5 / JSONC -> JSON ---------------------------------------------------

// normalizeJSON5 lleva JSONC y el subconjunto usual de JSON5 a JSON estricto, en UNA
// pasada con estado (O(n)): quita comentarios // y /* */, las comas colgantes (mirando
// hacia adelante por encima de espacios Y comentarios), pasa las cadenas '...' a "...",
// entrecomilla las claves sin comillas y normaliza los numeros que JSON no acepta
// (+1, .5, 5., 0x1F; Infinity y NaN pasan a cadena). Todo respetando lo que esta
// DENTRO de una cadena, que es exactamente donde fallaba la version anterior: una
// cadena con ", ]" adentro perdia la coma.
func normalizeJSON5(src []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(src) + len(src)/8)
	var last byte // ultimo byte significativo emitido (para saber si viene una clave)
	emit := func(b ...byte) {
		out.Write(b)
		for i := len(b) - 1; i >= 0; i-- {
			if !isSpaceByte(b[i]) {
				last = b[i]
				break
			}
		}
	}
	n := len(src)
	for i := 0; i < n; {
		c := src[i]
		switch {
		case isSpaceByte(c):
			out.WriteByte(c)
			i++
		case c == '/' && i+1 < n && (src[i+1] == '/' || src[i+1] == '*'):
			i = skipJSONComment(src, i)
			out.WriteByte(' ')
		case c == '"' || c == '\'':
			end := jsonStringEnd(src, i)
			emit(requoteJSONString(src[i:end])...)
			i = end
		case c == ',':
			if j := skipSpaceAndComments(src, i+1); j < n && (src[j] == ']' || src[j] == '}') {
				i++ // coma colgante: fuera
				continue
			}
			emit(',')
			i++
		case isIdentStart(c):
			j := i + 1
			for j < n && isIdentPart(src[j]) {
				j++
			}
			word := string(src[i:j])
			next := skipSpaceAndComments(src, j)
			switch {
			case (last == '{' || last == ',') && next < n && src[next] == ':':
				kb, _ := json.Marshal(word) // clave sin comillas
				emit(kb...)
			case word == "Infinity" || word == "NaN":
				emit([]byte(`"` + word + `"`)...)
			default:
				emit(src[i:j]...) // true / false / null (o basura: que la valide json.Valid)
			}
			i = j
		case c == '+' || c == '-' || c == '.' || (c >= '0' && c <= '9'):
			j := i + 1
			for j < n && (isIdentPart(src[j]) || src[j] == '.' || src[j] == '+' || src[j] == '-') {
				j++
			}
			emit([]byte(normalizeJSON5Number(string(src[i:j])))...)
			i = j
		default:
			emit(c)
			i++
		}
	}
	return out.Bytes()
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isIdentPart(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }

// skipJSONComment devuelve el indice justo despues del comentario que arranca en i.
func skipJSONComment(src []byte, i int) int {
	if src[i+1] == '/' {
		for i < len(src) && src[i] != '\n' {
			i++
		}
		return i
	}
	if end := bytes.Index(src[i+2:], []byte("*/")); end >= 0 {
		return i + 2 + end + 2
	}
	return len(src)
}

func skipSpaceAndComments(src []byte, i int) int {
	for i < len(src) {
		switch {
		case isSpaceByte(src[i]):
			i++
		case src[i] == '/' && i+1 < len(src) && (src[i+1] == '/' || src[i+1] == '*'):
			i = skipJSONComment(src, i)
		default:
			return i
		}
	}
	return i
}

// jsonStringEnd devuelve el indice justo despues de la comilla que cierra la cadena
// que abre en src[i] (respeta los escapes \" y \').
func jsonStringEnd(src []byte, i int) int {
	q := src[i]
	for j := i + 1; j < len(src); j++ {
		switch src[j] {
		case '\\':
			j++
		case q:
			return j + 1
		}
	}
	return len(src)
}

// requoteJSONString pasa una cadena '...' (o "...") a una cadena JSON valida.
func requoteJSONString(lit []byte) []byte {
	if len(lit) == 0 || lit[0] == '"' {
		return lit
	}
	var b bytes.Buffer
	b.WriteByte('"')
	body := lit[1:]
	if len(body) > 0 && body[len(body)-1] == '\'' {
		body = body[:len(body)-1]
	}
	for i := 0; i < len(body); i++ {
		switch c := body[i]; {
		case c == '\\' && i+1 < len(body) && body[i+1] == '\'':
			b.WriteByte('\'')
			i++
		case c == '\\' && i+1 < len(body):
			b.WriteByte('\\')
			b.WriteByte(body[i+1])
			i++
		case c == '"':
			b.WriteString(`\"`)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.Bytes()
}

// normalizeJSON5Number: +1 -> 1, .5 -> 0.5, 5. -> 5, 0x1F -> 31, -Infinity -> "-Infinity".
func normalizeJSON5Number(s string) string {
	neg := strings.HasPrefix(s, "-")
	body := strings.TrimLeft(s, "+-")
	if body == "Infinity" || body == "NaN" {
		return strconv.Quote(s) // JSON no los tiene: como cadena, al menos se leen
	}
	if len(body) > 2 && (body[:2] == "0x" || body[:2] == "0X") {
		if v, err := strconv.ParseInt(body[2:], 16, 64); err == nil {
			if neg {
				v = -v
			}
			return strconv.FormatInt(v, 10)
		}
		return s
	}
	if strings.HasPrefix(body, ".") {
		body = "0" + body
	}
	body = strings.TrimSuffix(body, ".")
	if neg {
		return "-" + body
	}
	return body
}

// ============================================================================
//  CSV / TSV
// ============================================================================

func convCSV(src []byte, path string) ([]byte, error) {
	r := csv.NewReader(bytes.NewReader(src))
	r.FieldsPerRecord = -1 // filas irregulares: las aceptamos igual
	r.LazyQuotes = true
	r.TrimLeadingSpace = false
	switch extOf(path) {
	case ".tsv", ".tab":
		r.Comma = '\t'
	default:
		r.Comma = sniffDelimiter(src)
	}
	recs, err := r.ReadAll()
	if err != nil && len(recs) == 0 {
		return nil, err
	}
	if len(recs) == 0 {
		return []byte(docHeader(path, "vacío")), nil
	}
	head := recs[0]
	allCols := len(head)
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
		plural(len(recs)-1, "fila", "filas"), plural(allCols, "columna", "columnas"))
	var b strings.Builder
	b.WriteString(docHeader(path, sub))
	b.WriteString(mdTableAligned(head, rows, numericColumns(len(head), rows)))
	if truncated {
		b.WriteString(fmt.Sprintf("*(se muestran %d de %d filas)*\n\n", maxTableRows, len(recs)-1))
	}
	if allCols > maxTableCols {
		b.WriteString(fmt.Sprintf("*(se muestran %d de %d columnas)*\n\n", maxTableCols, allCols))
	}
	if err != nil {
		b.WriteString("> [!WARNING]\n> El archivo tiene filas mal formadas: " +
			mdEscape(err.Error()) + "\n\n")
	}
	return []byte(b.String()), nil
}

// numericColumns marca con 'r' las columnas cuyas celdas no vacias son todas numeros
// (acepta la coma decimal del Excel en español): se alinean a la derecha, como en
// cualquier planilla. Una sola pasada por las celdas: O(filas × columnas).
func numericColumns(ncols int, rows [][]string) []byte {
	align := make([]byte, ncols)
	seenValue := make([]bool, ncols)
	for j := range align {
		align[j] = 'r'
	}
	for _, row := range rows {
		for j := 0; j < ncols && j < len(row); j++ {
			v := strings.TrimSpace(row[j])
			if v == "" || align[j] == 0 {
				continue
			}
			seenValue[j] = true
			if !looksNumeric(v) {
				align[j] = 0
			}
		}
	}
	for j := range align {
		if !seenValue[j] {
			align[j] = 0
		}
	}
	return align
}

func looksNumeric(v string) bool {
	v = strings.TrimSuffix(strings.TrimPrefix(v, "$"), "%")
	if _, err := strconv.ParseFloat(v, 64); err == nil {
		return true
	}
	// 1.234,56 (miles con punto, decimales con coma)
	v = strings.ReplaceAll(strings.ReplaceAll(v, ".", ""), ",", ".")
	_, err := strconv.ParseFloat(v, 64)
	return err == nil
}

// sniffDelimiter elige el separador de un CSV al estilo de csv.Sniffer de Python:
// para cada candidato cuenta cuantas veces aparece FUERA DE COMILLAS en cada una de
// las primeras lineas, y gana el que da una cantidad mas CONSISTENTE (la misma en mas
// lineas) y no nula. Mirar una sola linea engaña: "Apellido, Nombre" entre comillas
// tiene una coma pero el archivo va con punto y coma, como exporta el Excel en español.
// Costo: O(bytes de las primeras sniffLines lineas × candidatos).
func sniffDelimiter(src []byte) rune {
	const sniffLines = 24
	candidates := []rune{',', ';', '\t', '|'}
	counts := make([][]int, len(candidates)) // counts[c][linea]

	inQuote, lines := false, 0
	perLine := make([]int, len(candidates))
	flush := func() {
		for c := range candidates {
			counts[c] = append(counts[c], perLine[c])
			perLine[c] = 0
		}
		lines++
	}
	for i := 0; i < len(src) && lines < sniffLines; i++ {
		ch := src[i]
		switch {
		case ch == '"':
			inQuote = !inQuote
		case inQuote:
			// un salto dentro de comillas sigue siendo la misma fila
		case ch == '\n':
			flush()
		default:
			for c, d := range candidates {
				if rune(ch) == d {
					perLine[c]++
				}
			}
		}
	}
	if lines < sniffLines {
		flush() // la ultima linea, sin \n
	}

	best, bestScore := ',', -1.0
	for c, d := range candidates {
		mode, freq := modeOf(counts[c])
		if mode == 0 {
			continue
		}
		// consistencia (fraccion de lineas con la cantidad modal), desempatada por densidad
		score := float64(freq)/float64(len(counts[c])) + float64(mode)*1e-3
		if score > bestScore {
			best, bestScore = d, score
		}
	}
	return best
}

// modeOf devuelve el valor mas frecuente de xs (ignorando lineas vacias = 0 cuando hay
// otro valor) y cuantas veces aparece.
func modeOf(xs []int) (mode, freq int) {
	hist := map[int]int{}
	for _, x := range xs {
		hist[x]++
	}
	for v, f := range hist {
		if v == 0 {
			continue
		}
		if f > freq || (f == freq && v > mode) {
			mode, freq = v, f
		}
	}
	return mode, freq
}
