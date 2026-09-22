package main

// fmt_docs.go — documentos "de verdad" -> Markdown: Jupyter, Word (.docx), OpenDocument (.odt)
// y EPUB. Los tres ultimos son un ZIP con XML adentro, asi que alcanza con archive/zip y
// encoding/xml de la biblioteca estandar: ni una dependencia nueva.
//
// Las imagenes que viven dentro del contenedor se incrustan como data URI, porque el documento
// convertido no tiene una carpeta al lado de donde sacarlas. Hay un tope para que un EPUB con
// 300 laminas no se coma la memoria.

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

const (
	maxEmbedImage = 768 << 10 // por imagen
	maxEmbedTotal = 24 << 20  // sumadas
	maxEmbedCount = 200
	maxZipEntry   = 32 << 20 // tope al leer una entrada del ZIP (document.xml, un capitulo…)

	markerBullet  = "- "  // vineta de Markdown
	markerOrdered = "1. " // item numerado (Markdown renumera solo)
)

// ---- ZIP -----------------------------------------------------------------

// zipIndex indexa las entradas de un ZIP por su ruta normalizada. Antes cada busqueda
// recorria el directorio entero (O(entradas) por imagen o capitulo: O(n·m) en un EPUB con
// cientos de laminas); con el mapa es O(1).
type zipIndex struct {
	zr     *zip.Reader
	byName map[string]*zip.File
}

func zipOpen(src []byte) (*zipIndex, error) {
	zr, err := zip.NewReader(bytes.NewReader(src), int64(len(src)))
	if err != nil {
		return nil, err
	}
	ix := &zipIndex{zr: zr, byName: make(map[string]*zip.File, len(zr.File))}
	for _, f := range zr.File {
		ix.byName[path.Clean(f.Name)] = f
	}
	return ix, nil
}

func (ix *zipIndex) lookup(name string) *zip.File {
	return ix.byName[path.Clean(strings.TrimPrefix(name, "/"))]
}

func (ix *zipIndex) read(name string) ([]byte, error) {
	f := ix.lookup(name)
	if f == nil {
		return nil, fmt.Errorf("falta %s", name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, maxZipEntry))
}

// embedder incrusta archivos del ZIP como data URI, con tope. Memoiza por ruta: la misma
// lamina referida desde diez capitulos se lee y se codifica una sola vez.
type embedder struct {
	ix    *zipIndex
	total int
	count int
	memo  map[string]string
}

func (e *embedder) dataURI(name string) string {
	if e == nil || e.ix == nil {
		return ""
	}
	name = path.Clean(strings.TrimPrefix(name, "/"))
	if uri, ok := e.memo[name]; ok {
		return uri
	}
	if e.count >= maxEmbedCount || e.total >= maxEmbedTotal {
		return ""
	}
	f := e.ix.lookup(name)
	if f == nil || f.UncompressedSize64 > maxEmbedImage {
		return ""
	}
	rc, err := f.Open()
	if err != nil {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(rc, maxEmbedImage))
	rc.Close()
	if err != nil || len(data) == 0 {
		return ""
	}
	e.total += len(data)
	e.count++
	uri := "data:" + mimeOf(name) + ";base64," + base64.StdEncoding.EncodeToString(data)
	if e.memo == nil {
		e.memo = map[string]string{}
	}
	e.memo[name] = uri
	return uri
}

func mimeOf(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".avif":
		return "image/avif"
	case ".emf", ".wmf":
		return "image/x-emf"
	default:
		return "application/octet-stream"
	}
}

// listIndent: sangria que necesita un item de nivel `level` para quedar DENTRO del item
// que lo contiene: la suma de los anchos de los marcadores de sus ancestros ("- " = 2,
// "1. " = 3). Con dos espacios fijos, lo anidado bajo un numerado se salia de la lista.
func listIndent(ordered func(level int) bool, level int) string {
	n := 0
	for l := 0; l < level; l++ {
		if ordered(l) {
			n += len(markerOrdered)
		} else {
			n += len(markerBullet)
		}
	}
	return strings.Repeat(" ", n)
}

// splitSpaces separa los espacios de los bordes: el formato (**, *, ~~) va sobre el
// nucleo y los espacios quedan afuera, o el Markdown no lo reconoce ("** a**").
func splitSpaces(s string) (lead, core, trail string) {
	left := strings.TrimLeftFunc(s, unicode.IsSpace)
	core = strings.TrimRightFunc(left, unicode.IsSpace)
	return s[:len(s)-len(left)], core, left[len(core):]
}

// =========================================================================
//  Jupyter (.ipynb)
// =========================================================================

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")

type nbOutput struct {
	OutputType string         `json:"output_type"`
	Name       string         `json:"name"`
	Text       any            `json:"text"`
	Data       map[string]any `json:"data"`
	Ename      string         `json:"ename"`
	Evalue     string         `json:"evalue"`
	Traceback  []string       `json:"traceback"`
}

type nbCell struct {
	CellType    string                    `json:"cell_type"`
	Source      any                       `json:"source"`
	Outputs     []nbOutput                `json:"outputs"`
	ExecCnt     *int                      `json:"execution_count"`
	Attachments map[string]map[string]any `json:"attachments"`
}

type notebook struct {
	Cells    []nbCell `json:"cells"`
	NBFormat int      `json:"nbformat"`
	Metadata struct {
		Kernelspec struct {
			Language    string `json:"language"`
			DisplayName string `json:"display_name"`
			Name        string `json:"name"`
		} `json:"kernelspec"`
		LanguageInfo struct {
			Name string `json:"name"`
		} `json:"language_info"`
	} `json:"metadata"`
	Worksheets []struct {
		Cells []nbCell `json:"cells"`
	} `json:"worksheets"`
}

// nbText acepta las dos formas en que un notebook guarda texto: una cadena o
// una lista de lineas.
func nbText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		var b strings.Builder
		for _, x := range t {
			if s, ok := x.(string); ok {
				b.WriteString(s)
			}
		}
		return b.String()
	}
	return ""
}

func convNotebook(src []byte, path string) ([]byte, error) {
	var nb notebook
	if err := json.Unmarshal(src, &nb); err != nil {
		return nil, err
	}
	cells := nb.Cells
	if len(cells) == 0 {
		for _, ws := range nb.Worksheets { // nbformat 3
			cells = append(cells, ws.Cells...)
		}
	}
	lang := nb.Metadata.LanguageInfo.Name
	if lang == "" {
		lang = nb.Metadata.Kernelspec.Language
	}
	if lang == "" {
		lang = "python"
	}
	kernel := nb.Metadata.Kernelspec.DisplayName
	if kernel == "" {
		kernel = lang
	}

	var b strings.Builder
	b.WriteString(docHeader(path, fmt.Sprintf("Jupyter · %s · %s",
		kernel, plural(len(cells), "celda", "celdas"))))

	for _, c := range cells {
		srcTxt := strings.TrimRight(nbText(c.Source), "\n")
		switch c.CellType {
		case "markdown":
			b.WriteString(resolveAttachments(srcTxt, c.Attachments) + "\n\n")
		case "raw":
			if srcTxt != "" {
				b.WriteString(codeBlock(srcTxt, ""))
			}
		default: // code
			if strings.TrimSpace(srcTxt) != "" {
				b.WriteString(codeBlock(srcTxt, lang))
			}
			for _, o := range c.Outputs {
				b.WriteString(nbRenderOutput(o))
			}
		}
	}
	return []byte(b.String()), nil
}

// resolveAttachments reemplaza las imagenes pegadas en una celda Markdown
// (![x](attachment:foto.png)): viven dentro del propio .ipynb, en base64.
func resolveAttachments(md string, att map[string]map[string]any) string {
	if len(att) == 0 || !strings.Contains(md, "attachment:") {
		return md
	}
	pairs := make([]string, 0, len(att)*2)
	for name, bundle := range att {
		for mime, data := range bundle {
			if b64 := strings.ReplaceAll(nbText(data), "\n", ""); b64 != "" {
				pairs = append(pairs, "attachment:"+name, "data:"+mime+";base64,"+b64)
				break
			}
		}
	}
	return strings.NewReplacer(pairs...).Replace(md)
}

func nbRenderOutput(o nbOutput) string {
	switch o.OutputType {
	case "stream":
		txt := strings.TrimRight(nbText(o.Text), "\n")
		if txt == "" {
			return ""
		}
		return codeBlock(ansiRe.ReplaceAllString(txt, ""), "")
	case "error":
		txt := ansiRe.ReplaceAllString(strings.Join(o.Traceback, "\n"), "")
		if txt == "" {
			txt = o.Ename + ": " + o.Evalue
		}
		return "> [!CAUTION]\n> " + mdEscape(o.Ename+": "+o.Evalue) + "\n\n" + codeBlock(txt, "")
	}
	// execute_result / display_data: el mejor formato disponible
	if o.Data == nil {
		return ""
	}
	if v, ok := o.Data["text/markdown"]; ok {
		return nbText(v) + "\n\n"
	}
	for _, mime := range []string{"image/png", "image/jpeg", "image/gif", "image/webp"} {
		if v, ok := o.Data[mime]; ok {
			if b64 := strings.ReplaceAll(nbText(v), "\n", ""); b64 != "" {
				return "![salida](data:" + mime + ";base64," + b64 + ")\n\n"
			}
		}
	}
	if v, ok := o.Data["image/svg+xml"]; ok {
		if svg := nbText(v); svg != "" {
			return "![salida](data:image/svg+xml;base64," +
				base64.StdEncoding.EncodeToString([]byte(svg)) + ")\n\n"
		}
	}
	// sympy y compañia: la salida matematica ya viene en TeX y Folio tiene KaTeX
	if v, ok := o.Data["text/latex"]; ok {
		if tex := strings.TrimSpace(nbText(v)); tex != "" {
			if !strings.Contains(tex, "$") {
				tex = "$$\n" + tex + "\n$$"
			}
			return tex + "\n\n"
		}
	}
	if v, ok := o.Data["text/html"]; ok {
		if md := htmlToMarkdown(nbText(v), nil); strings.TrimSpace(md) != "" {
			return md + "\n\n"
		}
	}
	if v, ok := o.Data["application/json"]; ok {
		if b, err := json.MarshalIndent(v, "", "  "); err == nil {
			return codeBlock(string(b), "json")
		}
	}
	if v, ok := o.Data["text/plain"]; ok {
		if txt := strings.TrimRight(nbText(v), "\n"); txt != "" {
			return codeBlock(ansiRe.ReplaceAllString(txt, ""), "")
		}
	}
	return ""
}

// =========================================================================
//  Word (.docx)
// =========================================================================

// docxRels: id -> destino (URL de un hipervinculo o ruta de una imagen).
func docxRels(ix *zipIndex, name string) map[string]string {
	out := map[string]string{}
	data, err := ix.read(name)
	if err != nil {
		return out
	}
	var rels struct {
		Rel []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	if xml.Unmarshal(data, &rels) != nil {
		return out
	}
	for _, r := range rels.Rel {
		out[r.ID] = r.Target
	}
	return out
}

var (
	docxHeadRe = regexp.MustCompile(`^(?i)heading\s*([1-9])$`)
	// campo HYPERLINK "url" (\l "marca" = ancla interna, que no tiene destino afuera)
	docxHyperlinkRe = regexp.MustCompile(`(?i)^\s*HYPERLINK\s+(?:\\[a-z]\s+)*"([^"]+)"`)
)

// docxCtx es lo que necesita el conversor de parrafos: destinos de los enlaces,
// imagenes incrustables, el mapa de numeracion (para saber si una lista va con numeros
// o con vinetas, que eso vive en numbering.xml y no en el parrafo) y las notas al pie.
type docxCtx struct {
	rels  map[string]string
	emb   *embedder
	num   map[string]bool // "numId:ilvl" -> es numerada
	notes []string        // definiciones [^n]: … que van al final
	noteN map[string]int  // "f:2" / "e:1" -> numero de nota ya asignado
}

// docxNumbering arma el mapa numId:ilvl -> numerada leyendo numbering.xml.
func docxNumbering(ix *zipIndex) map[string]bool {
	out := map[string]bool{}
	data, err := ix.read("word/numbering.xml")
	if err != nil {
		return out
	}
	var doc struct {
		Abstract []struct {
			ID  string `xml:"abstractNumId,attr"`
			Lvl []struct {
				Ilvl   string `xml:"ilvl,attr"`
				NumFmt struct {
					Val string `xml:"val,attr"`
				} `xml:"numFmt"`
			} `xml:"lvl"`
		} `xml:"abstractNum"`
		Num []struct {
			NumID    string `xml:"numId,attr"`
			Abstract struct {
				Val string `xml:"val,attr"`
			} `xml:"abstractNumId"`
		} `xml:"num"`
	}
	if xml.Unmarshal(data, &doc) != nil {
		return out
	}
	byAbstract := map[string]map[string]bool{}
	for _, a := range doc.Abstract {
		m := map[string]bool{}
		for _, l := range a.Lvl {
			m[l.Ilvl] = l.NumFmt.Val != "" && l.NumFmt.Val != "bullet" && l.NumFmt.Val != "none"
		}
		byAbstract[a.ID] = m
	}
	for _, n := range doc.Num {
		for ilvl, ordered := range byAbstract[n.Abstract.Val] {
			out[n.NumID+":"+ilvl] = ordered
		}
	}
	return out
}

// docxNotes lee footnotes.xml / endnotes.xml: id -> texto de la nota (los ids 0 y -1
// son los separadores que Word guarda como notas "especiales").
func docxNotes(ix *zipIndex, name string, ctx *docxCtx) map[string]string {
	out := map[string]string{}
	data, err := ix.read(name)
	if err != nil {
		return out
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	id := ""
	var body strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "footnote", "endnote":
				id = attrVal(t, "id")
				body.Reset()
			case "p":
				if para := strings.TrimSpace(docxParagraph(dec, ctx)); para != "" {
					if body.Len() > 0 {
						body.WriteByte(' ')
					}
					body.WriteString(strings.ReplaceAll(para, "\n", " "))
				}
			}
		case xml.EndElement:
			if (t.Name.Local == "footnote" || t.Name.Local == "endnote") && id != "" {
				if txt := strings.TrimSpace(body.String()); txt != "" {
					out[id] = txt
				}
				id = ""
			}
		}
	}
	return out
}

func convDocx(src []byte, docPath string) ([]byte, error) {
	ix, err := zipOpen(src)
	if err != nil {
		return nil, err
	}
	body, err := ix.read("word/document.xml")
	if err != nil {
		return nil, err
	}
	ctx := &docxCtx{
		rels:  docxRels(ix, "word/_rels/document.xml.rels"),
		emb:   &embedder{ix: ix},
		num:   docxNumbering(ix),
		noteN: map[string]int{},
	}
	// las notas se convierten con su propio mapa de relaciones (sus enlaces e imagenes)
	noteCtx := &docxCtx{rels: docxRels(ix, "word/_rels/footnotes.xml.rels"), emb: ctx.emb, num: ctx.num, noteN: map[string]int{}}
	footnotes := docxNotes(ix, "word/footnotes.xml", noteCtx)
	noteCtx.rels = docxRels(ix, "word/_rels/endnotes.xml.rels")
	endnotes := docxNotes(ix, "word/endnotes.xml", noteCtx)

	var out strings.Builder
	dec := xml.NewDecoder(bytes.NewReader(body))
	prevList := false // el bloque anterior fue un item de lista
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "p":
			md, isList := docxParagraphBlock(dec, ctx)
			if strings.TrimSpace(md) == "" {
				out.WriteString(md)
				continue
			}
			// un parrafo pegado a una lista, sin renglon en blanco, se vuelve continuacion
			// perezosa del ultimo item: se sumaba al item en vez de quedar suelto
			if prevList && !isList {
				out.WriteString("\n")
			}
			out.WriteString(md)
			prevList = isList
		case "tbl":
			out.WriteString(docxTable(dec, ctx))
			prevList = false
		}
	}
	md := strings.TrimSpace(out.String())
	if md == "" {
		return nil, fmt.Errorf("el documento no tiene texto")
	}
	// definiciones de las notas, en el orden en que aparecieron
	var notes strings.Builder
	for _, key := range ctx.notes {
		kind, id, _ := strings.Cut(key, ":")
		text := footnotes[id]
		if kind == "e" {
			text = endnotes[id]
		}
		if text == "" {
			text = "(nota vacía)"
		}
		fmt.Fprintf(&notes, "[^%d]: %s\n", ctx.noteN[key], text)
	}
	head := ""
	if !strings.HasPrefix(md, "# ") {
		head = docHeader(docPath, "Word")
	}
	res := head + md + "\n"
	if notes.Len() > 0 {
		res += "\n" + notes.String()
	}
	return []byte(res), nil
}

// docxParagraph consume un <w:p> y devuelve su Markdown (con el salto de parrafo).
func docxParagraph(dec *xml.Decoder, ctx *docxCtx) string {
	md, _ := docxParagraphBlock(dec, ctx)
	return md
}

// docxParagraphBlock es docxParagraph diciendo ademas si el parrafo es un item de lista.
func docxParagraphBlock(dec *xml.Decoder, ctx *docxCtx) (string, bool) {
	var text bytes.Buffer
	style, numID, ilvl := "", "", "0"
	isList := false
	// formato del run en curso
	var bold, ital, strike, code bool
	depth := 1

	// Word parte el texto en runs por cualquier motivo (un corrector, un cambio de
	// idioma), asi que hay que juntar los runs consecutivos con el mismo formato:
	// si no, "**a****b**" queda como una negrita vacia en el medio.
	var pend bytes.Buffer
	var pb, pi, ps, pc bool
	emit := func() {
		s := pend.String()
		pend.Reset()
		if s == "" {
			return
		}
		lead, core, trail := splitSpaces(s)
		if core == "" {
			text.WriteString(s)
			return
		}
		var esc string
		if pc {
			esc = codeSpan(core)
		} else {
			esc = escapeInline(core)
			if ps {
				esc = "~~" + esc + "~~"
			}
			if pb {
				esc = "**" + esc + "**"
			}
			if pi {
				esc = "*" + esc + "*"
			}
		}
		text.WriteString(lead + esc + trail)
	}
	flush := func(s string) {
		if s == "" {
			return
		}
		s = strings.ReplaceAll(s, "\r", "")
		if pend.Len() > 0 && (pb != bold || pi != ital || ps != strike || pc != code) {
			emit()
		}
		if pend.Len() == 0 {
			pb, pi, ps, pc = bold, ital, strike, code
		}
		pend.WriteString(s)
	}
	onOff := func(t xml.StartElement) bool {
		v := attrVal(t, "val")
		return v != "0" && v != "false" && v != "none"
	}

	// Enlaces: <w:hyperlink>, <w:fldSimple w:instr="HYPERLINK …"> y los campos complejos
	// (fldChar begin / instrText / separate / end). Pila, porque los campos anidan.
	type linkFrame struct {
		at  int    // posicion en text donde arranca el texto del enlace
		url string // destino ("" = sin destino externo: queda solo el texto)
	}
	var links []linkFrame
	openLink := func(url string) {
		emit()
		links = append(links, linkFrame{at: text.Len(), url: url})
	}
	closeLink := func() {
		if len(links) == 0 {
			return
		}
		emit()
		lf := links[len(links)-1]
		links = links[:len(links)-1]
		inner := strings.TrimSpace(string(text.Bytes()[lf.at:]))
		text.Truncate(lf.at)
		switch {
		case inner == "":
		case lf.url != "":
			text.WriteString("[" + inner + "](" + urlEscape(lf.url) + ")")
		default:
			text.WriteString(inner)
		}
	}
	// campos complejos: por cada begin, una entrada; el instr se junta hasta el separate
	type fieldFrame struct {
		instr  strings.Builder
		inInst bool
		isLink bool
	}
	var fields []*fieldFrame

	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "pStyle":
				style = attrVal(t, "val")
			case "numPr":
				isList = true
			case "ilvl":
				ilvl = attrVal(t, "val")
			case "numId":
				numID = attrVal(t, "val")
			case "r":
				bold, ital, strike, code = false, false, false, false
			case "b":
				bold = onOff(t)
			case "i":
				ital = onOff(t)
			case "strike", "dstrike":
				strike = onOff(t)
			case "rFonts":
				f := strings.ToLower(attrVal(t, "ascii"))
				code = strings.Contains(f, "mono") || strings.Contains(f, "consol") ||
					strings.Contains(f, "courier")
			case "hyperlink":
				url := ""
				if id := attrVal(t, "id"); id != "" {
					url = ctx.rels[id]
				}
				openLink(url)
			case "fldSimple":
				url := ""
				if m := docxHyperlinkRe.FindStringSubmatch(attrVal(t, "instr")); m != nil {
					url = m[1]
				}
				openLink(url)
			case "fldChar":
				switch attrVal(t, "fldCharType") {
				case "begin":
					fields = append(fields, &fieldFrame{inInst: true})
				case "separate":
					if n := len(fields); n > 0 {
						f := fields[n-1]
						f.inInst = false
						if m := docxHyperlinkRe.FindStringSubmatch(f.instr.String()); m != nil {
							f.isLink = true
							openLink(m[1])
						}
					}
				case "end":
					if n := len(fields); n > 0 {
						if fields[n-1].isLink {
							closeLink()
						}
						fields = fields[:n-1]
					}
				}
			case "instrText":
				var s string
				if dec.DecodeElement(&s, &t) == nil && len(fields) > 0 && fields[len(fields)-1].inInst {
					fields[len(fields)-1].instr.WriteString(s)
				}
				depth--
			case "br":
				emit()
				text.WriteString("  \n")
			case "tab":
				emit()
				text.WriteString("  ")
			case "blip":
				emit()
				if id := attrVal(t, "embed"); id != "" {
					if tgt := ctx.rels[id]; tgt != "" {
						if uri := ctx.emb.dataURI("word/" + strings.TrimPrefix(tgt, "/")); uri != "" {
							text.WriteString("![](" + uri + ")")
						} else {
							text.WriteString("*[imagen]*")
						}
					}
				}
			case "footnoteReference", "endnoteReference":
				emit()
				kind := "f"
				if t.Name.Local == "endnoteReference" {
					kind = "e"
				}
				key := kind + ":" + attrVal(t, "id")
				n, seen := ctx.noteN[key]
				if !seen {
					n = len(ctx.notes) + 1
					ctx.noteN[key] = n
					ctx.notes = append(ctx.notes, key)
				}
				text.WriteString("[^" + strconv.Itoa(n) + "]")
			case "t":
				var s string
				if dec.DecodeElement(&s, &t) == nil {
					flush(s)
				}
				depth--
			}
		case xml.EndElement:
			depth--
			if t.Name.Local == "hyperlink" || t.Name.Local == "fldSimple" {
				closeLink()
			}
		}
	}
	emit()
	for len(links) > 0 { // un campo sin cerrar no puede comerse el texto
		closeLink()
	}

	s := strings.TrimRight(text.String(), " ")
	if strings.TrimSpace(s) == "" {
		return "\n", false
	}
	if m := docxHeadRe.FindStringSubmatch(style); m != nil {
		n, _ := strconv.Atoi(m[1])
		return "\n" + strings.Repeat("#", min(n, 6)) + " " + strings.TrimSpace(s) + "\n\n", false
	}
	switch strings.ToLower(style) {
	case "title":
		return "\n# " + strings.TrimSpace(s) + "\n\n", false
	case "subtitle":
		return "\n*" + strings.TrimSpace(s) + "*\n\n", false
	case "quote", "intensequote":
		return "\n> " + strings.TrimSpace(s) + "\n\n", false
	case "code", "sourcecode", "html-pre":
		return "\n" + codeBlock(s, ""), false
	}
	if isList {
		lvl, _ := strconv.Atoi(ilvl)
		ordered := func(l int) bool { return ctx.num[numID+":"+strconv.Itoa(l)] }
		marker := markerBullet
		if ordered(lvl) {
			marker = markerOrdered
		}
		return listIndent(ordered, lvl) + marker + strings.TrimSpace(s) + "\n", true
	}
	return s + "\n\n", false
}

// docxTable consume un <w:tbl> y lo pasa a tabla GFM. Las celdas combinadas a lo ancho
// (gridSpan) se rellenan para que las columnas no se corran, y una tabla ANIDADA se
// aplana dentro de su celda (GFM no anida tablas) en vez de romper la fila de afuera.
func docxTable(dec *xml.Decoder, ctx *docxCtx) string {
	rows := docxTableRows(dec, ctx)
	if len(rows) == 0 {
		return ""
	}
	width := 0
	for _, r := range rows {
		width = max(width, len(r))
	}
	for len(rows[0]) < width {
		rows[0] = append(rows[0], "")
	}
	return "\n" + mdTable(rows[0], rows[1:])
}

func docxTableRows(dec *xml.Decoder, ctx *docxCtx) [][]string {
	var rows [][]string
	var row []string
	span := 1
	depth := 1
	appendToCell := func(s string) {
		if len(row) == 0 || s == "" {
			return
		}
		if row[len(row)-1] != "" {
			row[len(row)-1] += " "
		}
		row[len(row)-1] += s
	}
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "tr":
				row = nil
			case "tc":
				row = append(row, "")
				span = 1
			case "gridSpan":
				if n, err := strconv.Atoi(attrVal(t, "val")); err == nil && n > 1 {
					span = n
				}
			case "p":
				appendToCell(strings.TrimSpace(strings.ReplaceAll(docxParagraph(dec, ctx), "\n", " ")))
				depth--
			case "tbl":
				nested := docxTableRows(dec, ctx)
				depth--
				lines := make([]string, 0, len(nested))
				for _, nr := range nested {
					lines = append(lines, strings.Join(nr, " · "))
				}
				appendToCell(strings.Join(lines, "<br>"))
			}
		case xml.EndElement:
			depth--
			switch t.Name.Local {
			case "tc":
				for ; span > 1; span-- {
					row = append(row, "")
				}
			case "tr":
				if len(row) > 0 {
					rows = append(rows, row)
				}
				row = nil
			}
		}
	}
	return rows
}

func attrVal(se xml.StartElement, name string) string {
	for _, a := range se.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// =========================================================================
//  OpenDocument (.odt)
// =========================================================================

// odtStyle: lo que importa de un estilo de texto, con herencia (parent-style-name).
type odtStyle struct {
	bold, ital, set bool
	parent          string
}

// odtStyleSheet resuelve estilos con herencia y memoiza el resultado: un estilo
// automatico ("T3") suele heredar de uno con nombre ("Strong_20_Emphasis").
type odtStyleSheet struct {
	raw      map[string]odtStyle
	resolved map[string]odtStyle
}

func (ss *odtStyleSheet) get(name string) odtStyle {
	if st, ok := ss.resolved[name]; ok {
		return st
	}
	st := ss.raw[name]
	for hops, p := 0, st.parent; p != "" && hops < 16; hops++ { // tope: ciclos en un archivo roto
		ps := ss.raw[p]
		if !st.set {
			st.bold, st.ital, st.set = ps.bold, ps.ital, ps.set
		}
		p = ps.parent
	}
	ss.resolved[name] = st
	return st
}

// odtReadStyles junta los estilos de texto y de lista de un XML de ODF (content.xml
// o styles.xml): negrita/cursiva por estilo y, por lista, si el primer nivel es numerado.
func odtReadStyles(content []byte, ss *odtStyleSheet, ordered map[string]bool) {
	dec := xml.NewDecoder(bytes.NewReader(content))
	cur, curList := "", ""
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "style":
				cur = attrVal(t, "name")
				st := ss.raw[cur]
				st.parent = attrVal(t, "parent-style-name")
				ss.raw[cur] = st
			case "text-properties":
				if cur == "" {
					break
				}
				st := ss.raw[cur]
				if w := strings.ToLower(attrVal(t, "font-weight")); w != "" {
					st.bold, st.set = strings.Contains(w, "bold") || w >= "600", true
				}
				if strings.Contains(strings.ToLower(attrVal(t, "font-style")), "italic") {
					st.ital, st.set = true, true
				}
				ss.raw[cur] = st
			case "list-style":
				curList = attrVal(t, "name")
			case "list-level-style-number":
				if _, seen := ordered[curList]; curList != "" && !seen {
					ordered[curList] = true
				}
			case "list-level-style-bullet", "list-level-style-image":
				if _, seen := ordered[curList]; curList != "" && !seen {
					ordered[curList] = false
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "style":
				cur = ""
			case "list-style":
				curList = ""
			}
		}
	}
}

// odtParaState: un parrafo a medio armar, guardado mientras se lee una nota al pie.
type odtParaState struct {
	text    string
	inPara  bool
	heading int
	style   odtStyle
	spans   []odtStyle
}

// odtTable: una tabla en construccion (pueden anidar).
type odtTable struct {
	rows [][]string
	row  []string
}

func convODT(src []byte, docPath string) ([]byte, error) {
	ix, err := zipOpen(src)
	if err != nil {
		return nil, err
	}
	content, err := ix.read("content.xml")
	if err != nil {
		return nil, err
	}
	emb := &embedder{ix: ix}
	ss := &odtStyleSheet{raw: map[string]odtStyle{}, resolved: map[string]odtStyle{}}
	ordered := map[string]bool{}
	if st, err := ix.read("styles.xml"); err == nil {
		odtReadStyles(st, ss, ordered)
	}
	odtReadStyles(content, ss, ordered) // los automaticos del documento pisan a los comunes

	var out strings.Builder
	dec := xml.NewDecoder(bytes.NewReader(content))

	var (
		para       strings.Builder
		inPara     bool
		heading    int
		paraStyle  odtStyle
		lists      []bool     // pila: cada nivel abierto, numerado o no
		spans      []odtStyle // pila de formato de <text:span>
		links      []int      // pila de posiciones donde arranca el texto de un <text:a>
		linkURLs   []string
		tables     []*odtTable
		notes      []string // definiciones de notas al pie, en orden
		inNote     bool
		inCitation bool
		noteBody   strings.Builder
		// la nota vive ADENTRO del parrafo que la cita: al entrar se guarda el parrafo
		// a medio armar y al salir se retoma, con la marca [^n] donde estaba la nota
		saved *odtParaState
		// el bloque anterior fue un item de lista: lo que no lo sea necesita un renglon en
		// blanco antes, o Markdown lo pega al ultimo item (continuacion perezosa)
		lastList bool
	)
	cur := func() odtStyle { // formato efectivo: el del span mas interno, o el del parrafo
		if n := len(spans); n > 0 {
			return spans[n-1]
		}
		return paraStyle
	}
	emit := func() {
		s := strings.TrimRight(para.String(), " ")
		para.Reset()
		if strings.TrimSpace(s) == "" {
			return
		}
		if inNote {
			if noteBody.Len() > 0 {
				noteBody.WriteByte(' ')
			}
			noteBody.WriteString(strings.TrimSpace(s))
			return
		}
		if n := len(tables); n > 0 { // dentro de una celda: el texto va a la celda
			t := tables[n-1]
			if len(t.row) > 0 {
				if t.row[len(t.row)-1] != "" {
					t.row[len(t.row)-1] += " "
				}
				t.row[len(t.row)-1] += strings.TrimSpace(s)
			}
			return
		}
		switch {
		case heading > 0:
			out.WriteString("\n" + strings.Repeat("#", min(heading, 6)) + " " + strings.TrimSpace(s) + "\n\n")
			lastList = false
		case len(lists) > 0:
			lvl := len(lists) - 1
			marker := markerBullet
			if lists[lvl] {
				marker = markerOrdered
			}
			out.WriteString(listIndent(func(l int) bool { return lists[l] }, lvl) + marker + strings.TrimSpace(s) + "\n")
			lastList = true
		default:
			if lastList {
				out.WriteString("\n")
			}
			out.WriteString(s + "\n\n")
			lastList = false
		}
	}

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "h":
				inPara, heading = true, 1
				if v, err := strconv.Atoi(attrVal(t, "outline-level")); err == nil && v >= 1 {
					heading = v
				}
				paraStyle = odtStyle{}
			case "p":
				inPara, heading = true, 0
				paraStyle = ss.get(attrVal(t, "style-name"))
			case "list":
				name := attrVal(t, "style-name")
				isOrdered, known := ordered[name]
				if !known {
					if n := len(lists); n > 0 {
						isOrdered = lists[n-1] // una sublista sin estilo propio hereda
					} else {
						isOrdered = strings.Contains(strings.ToLower(name), "number")
					}
				}
				lists = append(lists, isOrdered)
			case "span":
				spans = append(spans, ss.get(attrVal(t, "style-name")))
			case "a":
				links = append(links, para.Len())
				linkURLs = append(linkURLs, attrVal(t, "href"))
			case "line-break":
				para.WriteString("  \n")
			case "tab":
				para.WriteString("  ")
			case "s":
				n := 1
				if c, err := strconv.Atoi(attrVal(t, "c")); err == nil && c > 1 {
					n = c
				}
				para.WriteString(strings.Repeat(" ", n))
			case "image":
				if href := attrVal(t, "href"); href != "" {
					if uri := emb.dataURI(strings.TrimPrefix(href, "./")); uri != "" {
						para.WriteString("![](" + uri + ")")
					} else {
						para.WriteString("*[imagen]*")
					}
				}
			case "table":
				emit()
				tables = append(tables, &odtTable{})
			case "table-row":
				if n := len(tables); n > 0 {
					tables[n-1].row = nil
				}
			case "table-cell", "covered-table-cell":
				if n := len(tables); n > 0 {
					reps := 1
					if r, err := strconv.Atoi(attrVal(t, "number-columns-repeated")); err == nil && r > 1 {
						reps = min(r, maxTableCols) // las planillas repiten celdas vacias hasta 1024
					}
					for ; reps > 0; reps-- {
						tables[n-1].row = append(tables[n-1].row, "")
					}
				}
			case "note":
				saved = &odtParaState{para.String(), inPara, heading, paraStyle, spans}
				para.Reset()
				spans = nil
				inNote = true
				noteBody.Reset()
			case "note-citation":
				inCitation = true
			}
		case xml.CharData:
			if inCitation || !inPara {
				break
			}
			s := escapeInline(string(t))
			if strings.TrimSpace(s) == "" {
				para.WriteString(s)
				break
			}
			lead, core, trail := splitSpaces(s)
			if st := cur(); st.bold || st.ital {
				if st.bold {
					core = "**" + core + "**"
				}
				if st.ital {
					core = "*" + core + "*"
				}
			}
			para.WriteString(lead + core + trail)
		case xml.EndElement:
			switch t.Name.Local {
			case "h", "p":
				if inPara {
					emit()
					inPara, heading = false, 0
				}
			case "list":
				if n := len(lists); n > 0 {
					lists = lists[:n-1]
				}
			case "span":
				if n := len(spans); n > 0 {
					spans = spans[:n-1]
				}
			case "a":
				if n := len(links); n > 0 {
					at, href := links[n-1], linkURLs[n-1]
					links, linkURLs = links[:n-1], linkURLs[:n-1]
					inner := strings.TrimSpace(para.String()[min(at, para.Len()):])
					if inner != "" && href != "" {
						rest := para.String()[:min(at, para.Len())]
						para.Reset()
						para.WriteString(rest + "[" + inner + "](" + urlEscape(href) + ")")
					}
				}
			case "table-row":
				if n := len(tables); n > 0 && len(tables[n-1].row) > 0 {
					t := tables[n-1]
					t.rows = append(t.rows, t.row)
					t.row = nil
				}
			case "table":
				if n := len(tables); n > 0 {
					t := tables[n-1]
					tables = tables[:n-1]
					rendered := odtRenderTable(t.rows)
					if len(tables) > 0 { // anidada: se aplana dentro de la celda de afuera
						parent := tables[len(tables)-1]
						if len(parent.row) > 0 {
							flat := make([]string, 0, len(t.rows))
							for _, r := range t.rows {
								flat = append(flat, strings.Join(r, " · "))
							}
							parent.row[len(parent.row)-1] += strings.Join(flat, "<br>")
						}
					} else {
						out.WriteString(rendered)
						lastList = false
					}
				}
			case "note-citation":
				inCitation = false
			case "note":
				emit() // el ultimo parrafo de la nota, a noteBody
				inNote = false
				notes = append(notes, strings.TrimSpace(noteBody.String()))
				para.Reset()
				if saved != nil {
					para.WriteString(saved.text)
					inPara, heading, paraStyle, spans = saved.inPara, saved.heading, saved.style, saved.spans
					saved = nil
				}
				para.WriteString("[^" + strconv.Itoa(len(notes)) + "]")
			}
		}
	}
	md := strings.TrimSpace(out.String())
	if md == "" {
		return nil, fmt.Errorf("el documento no tiene texto")
	}
	head := ""
	if !strings.HasPrefix(md, "# ") {
		head = docHeader(docPath, "OpenDocument")
	}
	res := head + md + "\n"
	for i, n := range notes {
		if i == 0 {
			res += "\n"
		}
		if n == "" {
			n = "(nota vacía)"
		}
		res += "[^" + strconv.Itoa(i+1) + "]: " + n + "\n"
	}
	return []byte(res), nil
}

// odtRenderTable: filas de una tabla de ODF a GFM, recortando la cola de celdas vacias
// que las planillas arrastran.
func odtRenderTable(rows [][]string) string {
	width := 0
	for _, r := range rows {
		last := len(r)
		for last > 0 && r[last-1] == "" {
			last--
		}
		width = max(width, last)
	}
	if width == 0 {
		return ""
	}
	for i, r := range rows {
		if len(r) > width {
			rows[i] = r[:width]
		}
	}
	for len(rows[0]) < width {
		rows[0] = append(rows[0], "")
	}
	return "\n" + mdTable(rows[0], rows[1:]) + "\n"
}

// =========================================================================
//  EPUB
// =========================================================================

var urlSchemeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

func convEPUB(src []byte, docPath string) ([]byte, error) {
	ix, err := zipOpen(src)
	if err != nil {
		return nil, err
	}
	// container.xml dice donde esta el OPF
	cdata, err := ix.read("META-INF/container.xml")
	if err != nil {
		return nil, err
	}
	var container struct {
		Rootfiles []struct {
			Path string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(cdata, &container); err != nil || len(container.Rootfiles) == 0 {
		return nil, fmt.Errorf("EPUB sin rootfile")
	}
	opfPath := container.Rootfiles[0].Path
	opfData, err := ix.read(opfPath)
	if err != nil {
		return nil, err
	}
	var opf struct {
		Metadata struct {
			Title   []string `xml:"title"`
			Creator []string `xml:"creator"`
			Lang    []string `xml:"language"`
		} `xml:"metadata"`
		Manifest []struct {
			ID   string `xml:"id,attr"`
			Href string `xml:"href,attr"`
			Type string `xml:"media-type,attr"`
		} `xml:"manifest>item"`
		Spine []struct {
			IDRef  string `xml:"idref,attr"`
			Linear string `xml:"linear,attr"`
		} `xml:"spine>itemref"`
	}
	if err := xml.Unmarshal(opfData, &opf); err != nil {
		return nil, err
	}
	base := path.Dir(opfPath)
	type item struct{ href, mediaType string }
	byID := make(map[string]item, len(opf.Manifest))
	for _, it := range opf.Manifest {
		byID[it.ID] = item{it.Href, it.Type}
	}

	// 1) los capitulos de lectura, en orden, y el numero de cada uno por ruta: con eso un
	//    enlace a "cap07.xhtml#nota3" se vuelve un salto DENTRO del documento convertido.
	var chapters []string
	chapterNo := map[string]int{}
	for _, ref := range opf.Spine {
		it, ok := byID[ref.IDRef]
		if !ok || it.href == "" || strings.EqualFold(ref.Linear, "no") {
			continue
		}
		if it.mediaType != "" && !strings.Contains(it.mediaType, "html") {
			continue
		}
		full := path.Clean(path.Join(base, unescapePath(it.href)))
		if _, dup := chapterNo[full]; dup {
			continue
		}
		chapterNo[full] = len(chapters) + 1
		chapters = append(chapters, full)
	}

	emb := &embedder{ix: ix}
	var body strings.Builder
	read := 0
	for _, full := range chapters {
		data, err := ix.read(full)
		if err != nil {
			continue
		}
		dir := path.Dir(full)
		n := chapterNo[full]
		opts := &htmlOptions{
			keepIDs:  true,
			idPrefix: fmt.Sprintf("c%d-", n),
			resolveImage: func(u string) string {
				if u == "" || urlSchemeRe.MatchString(u) {
					return u
				}
				file, _, _ := strings.Cut(u, "#")
				file, _, _ = strings.Cut(file, "?")
				if file == "" {
					return ""
				}
				return emb.dataURI(path.Join(dir, unescapePath(file)))
			},
			resolveLink: func(u string) string {
				if u == "" || urlSchemeRe.MatchString(u) {
					return u // http(s), mailto…: afuera
				}
				file, frag, _ := strings.Cut(u, "#")
				target := full
				if file != "" {
					target = path.Clean(path.Join(dir, unescapePath(file)))
				}
				k, ok := chapterNo[target]
				switch {
				case !ok:
					return "" // un recurso que no es un capitulo: queda el texto
				case frag == "":
					return "#c" + strconv.Itoa(k)
				default:
					return "#c" + strconv.Itoa(k) + "-" + sanitizeID(frag)
				}
			},
		}
		root := parseHTML(string(data))
		w := &mdWriter{opts: opts, pending: []string{"c" + strconv.Itoa(n)}} // ancla del capitulo
		if b := findNode(root, "body"); b != nil {
			root = b
		}
		w.walkKids(root)
		md := strings.TrimSpace(collapseBlankLines(w.b.String()))
		if md == "" {
			continue
		}
		if read > 0 {
			body.WriteString("\n---\n\n")
		}
		body.WriteString(md + "\n")
		read++
	}
	if read == 0 {
		return nil, fmt.Errorf("no se pudo leer ningún capítulo")
	}

	var b strings.Builder
	title := firstNonEmpty(opf.Metadata.Title)
	author := firstNonEmpty(opf.Metadata.Creator)
	if title == "" {
		// filepath (no path): en Windows la ruta viene con contrabarras y path.Base
		// devolvia la ruta entera como titulo
		title = strings.TrimSuffix(filepath.Base(docPath), filepath.Ext(docPath))
	}
	b.WriteString("# " + mdEscape(title) + "\n\n")
	if author != "" {
		b.WriteString("*" + mdEscape(author) + "*\n\n")
	}
	b.WriteString(fmt.Sprintf("*EPUB · %s*\n\n", plural(read, "capítulo", "capítulos")))
	b.WriteString(body.String())
	return []byte(b.String()), nil
}

// unescapePath deshace el %20 y compañia de un href (los capitulos se llaman como quieran).
func unescapePath(p string) string {
	if u, err := url.PathUnescape(p); err == nil {
		return u
	}
	return p
}

func firstNonEmpty(ss []string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
