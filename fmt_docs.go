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
	"path"
	"regexp"
	"strconv"
	"strings"
)

const (
	maxEmbedImage = 768 << 10 // por imagen
	maxEmbedTotal = 24 << 20  // sumadas
	maxEmbedCount = 200
	maxZipEntry   = 32 << 20 // tope al leer una entrada del ZIP (document.xml, un capitulo…)
)

// embedder incrusta archivos del ZIP como data URI, con tope.
type embedder struct {
	zr    *zip.Reader
	total int
	count int
}

func (e *embedder) dataURI(name string) string {
	if e == nil || e.zr == nil || e.count >= maxEmbedCount || e.total >= maxEmbedTotal {
		return ""
	}
	name = path.Clean(strings.TrimPrefix(name, "/"))
	for _, f := range e.zr.File {
		if path.Clean(f.Name) != name {
			continue
		}
		if f.UncompressedSize64 > maxEmbedImage {
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
		return "data:" + mimeOf(name) + ";base64," + base64.StdEncoding.EncodeToString(data)
	}
	return ""
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
	case ".emf", ".wmf":
		return "image/x-emf"
	default:
		return "application/octet-stream"
	}
}

func zipOpen(src []byte) (*zip.Reader, error) {
	return zip.NewReader(bytes.NewReader(src), int64(len(src)))
}

func zipFile(zr *zip.Reader, name string) ([]byte, error) {
	name = path.Clean(name)
	for _, f := range zr.File {
		if path.Clean(f.Name) == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, maxZipEntry))
		}
	}
	return nil, fmt.Errorf("falta %s", name)
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
	CellType string     `json:"cell_type"`
	Source   any        `json:"source"`
	Outputs  []nbOutput `json:"outputs"`
	ExecCnt  *int       `json:"execution_count"`
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
			b.WriteString(srcTxt + "\n\n")
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
	for _, mime := range []string{"image/png", "image/jpeg", "image/gif"} {
		if v, ok := o.Data[mime]; ok {
			b64 := strings.ReplaceAll(nbText(v), "\n", "")
			if b64 != "" {
				return "![salida](data:" + mime + ";base64," + b64 + ")\n\n"
			}
		}
	}
	if v, ok := o.Data["image/svg+xml"]; ok {
		svg := nbText(v)
		if svg != "" {
			return "![salida](data:image/svg+xml;base64," +
				base64.StdEncoding.EncodeToString([]byte(svg)) + ")\n\n"
		}
	}
	if v, ok := o.Data["text/html"]; ok {
		if md := htmlToMarkdown(nbText(v), nil); strings.TrimSpace(md) != "" {
			return md + "\n\n"
		}
	}
	if v, ok := o.Data["text/plain"]; ok {
		txt := strings.TrimRight(nbText(v), "\n")
		if txt != "" {
			return codeBlock(ansiRe.ReplaceAllString(txt, ""), "")
		}
	}
	return ""
}

// =========================================================================
//  Word (.docx)
// =========================================================================

// docxRels: id -> destino (URL de un hipervinculo o ruta de una imagen).
func docxRels(zr *zip.Reader, name string) map[string]string {
	out := map[string]string{}
	data, err := zipFile(zr, name)
	if err != nil {
		return out
	}
	var rels struct {
		Rel []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
			Mode   string `xml:"TargetMode,attr"`
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

var docxHeadRe = regexp.MustCompile(`^(?i)heading\s*([1-9])$`)

// docxCtx es lo que necesita el conversor de parrafos: destinos de los enlaces,
// imagenes incrustables y el mapa de numeracion (para saber si una lista va con
// numeros o con vinetas, que eso vive en numbering.xml y no en el parrafo).
type docxCtx struct {
	rels map[string]string
	emb  *embedder
	num  map[string]bool // "numId:ilvl" -> es numerada
}

// docxNumbering arma el mapa numId:ilvl -> numerada leyendo numbering.xml.
func docxNumbering(zr *zip.Reader) map[string]bool {
	out := map[string]bool{}
	data, err := zipFile(zr, "word/numbering.xml")
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

func convDocx(src []byte, docPath string) ([]byte, error) {
	zr, err := zipOpen(src)
	if err != nil {
		return nil, err
	}
	body, err := zipFile(zr, "word/document.xml")
	if err != nil {
		return nil, err
	}
	ctx := &docxCtx{
		rels: docxRels(zr, "word/_rels/document.xml.rels"),
		emb:  &embedder{zr: zr},
		num:  docxNumbering(zr),
	}

	var out strings.Builder
	dec := xml.NewDecoder(bytes.NewReader(body))
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
			out.WriteString(docxParagraph(dec, ctx))
		case "tbl":
			out.WriteString(docxTable(dec, ctx))
		}
	}
	md := strings.TrimSpace(out.String())
	if md == "" {
		return nil, fmt.Errorf("el documento no tiene texto")
	}
	head := ""
	if !strings.HasPrefix(md, "# ") {
		head = docHeader(docPath, "Word")
	}
	return []byte(head + md + "\n"), nil
}

// docxParagraph consume un <w:p> y devuelve su Markdown (con el salto de parrafo).
func docxParagraph(dec *xml.Decoder, ctx *docxCtx) string {
	var text bytes.Buffer
	style, numID, ilvl := "", "", "0"
	isList := false
	// formato del run en curso
	var bold, ital, strike, code bool
	linkAt, linkURL := -1, ""
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
		core := strings.TrimSpace(s)
		if core == "" {
			text.WriteString(s)
			return
		}
		// los espacios de los bordes van AFUERA de los asteriscos
		lead := s[:strings.Index(s, core[:1])]
		trail := s[len(lead)+len(core):]
		esc := escapeInline(core)
		if pc {
			esc = "`" + core + "`"
		} else {
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
			case "strike":
				strike = onOff(t)
			case "rFonts":
				f := strings.ToLower(attrVal(t, "ascii"))
				code = strings.Contains(f, "mono") || strings.Contains(f, "consol") ||
					strings.Contains(f, "courier")
			case "hyperlink":
				emit()
				linkAt = text.Len()
				linkURL = ""
				if id := attrVal(t, "id"); id != "" {
					linkURL = ctx.rels[id]
				}
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
			case "t":
				var s string
				if dec.DecodeElement(&s, &t) == nil {
					flush(s)
				}
				depth--
			}
		case xml.EndElement:
			depth--
			if t.Name.Local == "hyperlink" && linkAt >= 0 {
				emit()
				inner := strings.TrimSpace(string(text.Bytes()[linkAt:]))
				text.Truncate(linkAt)
				if inner != "" {
					if linkURL != "" {
						text.WriteString("[" + inner + "](" + urlEscape(linkURL) + ")")
					} else {
						text.WriteString(inner)
					}
				}
				linkAt, linkURL = -1, ""
			}
		}
	}

	emit()
	s := strings.TrimRight(text.String(), " ")
	if strings.TrimSpace(s) == "" {
		return "\n"
	}
	if m := docxHeadRe.FindStringSubmatch(style); m != nil {
		n, _ := strconv.Atoi(m[1])
		return "\n" + strings.Repeat("#", n) + " " + strings.TrimSpace(s) + "\n\n"
	}
	switch strings.ToLower(style) {
	case "title":
		return "\n# " + strings.TrimSpace(s) + "\n\n"
	case "subtitle":
		return "\n*" + strings.TrimSpace(s) + "*\n\n"
	case "quote", "intensequote":
		return "\n> " + strings.TrimSpace(s) + "\n\n"
	case "code", "sourcecode", "html-pre":
		return "\n" + codeBlock(s, "")
	}
	if isList {
		marker := "- "
		if ctx.num[numID+":"+ilvl] {
			marker = "1. "
		}
		lvl, _ := strconv.Atoi(ilvl)
		return strings.Repeat("  ", lvl) + marker + strings.TrimSpace(s) + "\n"
	}
	return s + "\n\n"
}

// docxTable consume un <w:tbl> y lo pasa a tabla GFM.
func docxTable(dec *xml.Decoder, ctx *docxCtx) string {
	var rows [][]string
	var row []string
	depth := 1
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
			case "p":
				cell := strings.TrimSpace(strings.ReplaceAll(docxParagraph(dec, ctx), "\n", " "))
				depth--
				if len(row) > 0 && cell != "" {
					if row[len(row)-1] != "" {
						row[len(row)-1] += " "
					}
					row[len(row)-1] += cell
				}
			}
		case xml.EndElement:
			depth--
			if t.Name.Local == "tr" && len(row) > 0 {
				rows = append(rows, row)
				row = nil
			}
		}
	}
	if len(rows) == 0 {
		return ""
	}
	return "\n" + mdTable(rows[0], rows[1:])
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

func convODT(src []byte, docPath string) ([]byte, error) {
	zr, err := zipOpen(src)
	if err != nil {
		return nil, err
	}
	content, err := zipFile(zr, "content.xml")
	if err != nil {
		return nil, err
	}
	emb := &embedder{zr: zr}
	styles := odtStyles(content) // nombre de estilo -> negrita/cursiva
	ordered := odtListStyles(content)
	if st, err := zipFile(zr, "styles.xml"); err == nil {
		for k, v := range odtListStyles(st) {
			ordered[k] = v
		}
	}

	var out strings.Builder
	dec := xml.NewDecoder(bytes.NewReader(content))
	listDepth, listOrdered := 0, false
	var para strings.Builder
	var bold, ital bool
	inPara, heading := false, 0
	pendingLink := ""

	emit := func() {
		s := strings.TrimRight(para.String(), " ")
		para.Reset()
		if strings.TrimSpace(s) == "" {
			return
		}
		switch {
		case heading > 0:
			if heading > 6 {
				heading = 6
			}
			out.WriteString("\n" + strings.Repeat("#", heading) + " " + strings.TrimSpace(s) + "\n\n")
		case listDepth > 0:
			marker := "- "
			if listOrdered {
				marker = "1. "
			}
			out.WriteString(strings.Repeat("  ", listDepth-1) + marker + strings.TrimSpace(s) + "\n")
		default:
			out.WriteString(s + "\n\n")
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
				if v := attrVal(t, "outline-level"); v != "" {
					heading, _ = strconv.Atoi(v)
					if heading < 1 {
						heading = 1
					}
				}
			case "p":
				inPara, heading = true, 0
			case "list":
				listDepth++
				name := attrVal(t, "style-name")
				if v, ok := ordered[name]; ok {
					listOrdered = v
				} else {
					listOrdered = strings.Contains(strings.ToLower(name), "number")
				}
			case "span":
				st := styles[attrVal(t, "style-name")]
				bold, ital = st.bold, st.ital
			case "a":
				pendingLink = attrVal(t, "href")
			case "line-break":
				para.WriteString("  \n")
			case "tab":
				para.WriteString("  ")
			case "s":
				para.WriteString(" ")
			case "image":
				if href := attrVal(t, "href"); href != "" {
					if uri := emb.dataURI(strings.TrimPrefix(href, "./")); uri != "" {
						para.WriteString("![](" + uri + ")")
					} else {
						para.WriteString("*[imagen]*")
					}
				}
			}
		case xml.CharData:
			if inPara {
				s := escapeInline(string(t))
				if s == "" {
					break
				}
				if bold {
					s = "**" + s + "**"
				}
				if ital {
					s = "*" + s + "*"
				}
				if pendingLink != "" {
					s = "[" + s + "](" + urlEscape(pendingLink) + ")"
					pendingLink = ""
				}
				para.WriteString(s)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "h", "p":
				if inPara {
					emit()
					inPara, heading = false, 0
				}
			case "list":
				if listDepth > 0 {
					listDepth--
				}
			case "span":
				bold, ital = false, false
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
	return []byte(head + md + "\n"), nil
}

// odtListStyles: nombre de la lista -> si es numerada. Lo dice el primer nivel
// (text:list-level-style-number vs …-bullet).
func odtListStyles(content []byte) map[string]bool {
	out := map[string]bool{}
	dec := xml.NewDecoder(bytes.NewReader(content))
	cur := ""
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "list-style":
				cur = attrVal(t, "name")
			case "list-level-style-number":
				if cur != "" {
					if _, seen := out[cur]; !seen {
						out[cur] = true
					}
				}
			case "list-level-style-bullet", "list-level-style-image":
				if cur != "" {
					if _, seen := out[cur]; !seen {
						out[cur] = false
					}
				}
			}
		case xml.EndElement:
			if t.Name.Local == "list-style" {
				cur = ""
			}
		}
	}
	return out
}

type odtStyle struct{ bold, ital bool }

// odtStyles lee los estilos automaticos para saber que span va en negrita o cursiva.
func odtStyles(content []byte) map[string]odtStyle {
	out := map[string]odtStyle{}
	dec := xml.NewDecoder(bytes.NewReader(content))
	cur := ""
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
			case "text-properties":
				if cur == "" {
					break
				}
				st := out[cur]
				if strings.Contains(strings.ToLower(attrVal(t, "font-weight")), "bold") {
					st.bold = true
				}
				if strings.Contains(strings.ToLower(attrVal(t, "font-style")), "italic") {
					st.ital = true
				}
				out[cur] = st
			}
		case xml.EndElement:
			if t.Name.Local == "style" {
				cur = ""
			}
		}
	}
	return out
}

// =========================================================================
//  EPUB
// =========================================================================

func convEPUB(src []byte, docPath string) ([]byte, error) {
	zr, err := zipOpen(src)
	if err != nil {
		return nil, err
	}
	// container.xml dice donde esta el OPF
	cdata, err := zipFile(zr, "META-INF/container.xml")
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
	opfData, err := zipFile(zr, opfPath)
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
	byID := map[string]string{}
	typeByID := map[string]string{}
	for _, it := range opf.Manifest {
		byID[it.ID] = it.Href
		typeByID[it.ID] = it.Type
	}

	emb := &embedder{zr: zr}
	var b strings.Builder
	title := firstNonEmpty(opf.Metadata.Title)
	author := firstNonEmpty(opf.Metadata.Creator)
	if title == "" {
		title = strings.TrimSuffix(path.Base(docPath), path.Ext(docPath))
	}
	b.WriteString("# " + mdEscape(title) + "\n\n")
	if author != "" {
		b.WriteString("*" + mdEscape(author) + "*\n\n")
	}
	b.WriteString(fmt.Sprintf("*EPUB · %s*\n\n", plural(len(opf.Spine), "capítulo", "capítulos")))

	chapters := 0
	for _, ref := range opf.Spine {
		if strings.EqualFold(ref.Linear, "no") {
			continue
		}
		href := byID[ref.IDRef]
		if href == "" {
			continue
		}
		mt := typeByID[ref.IDRef]
		if mt != "" && !strings.Contains(mt, "html") {
			continue
		}
		full := path.Join(base, href)
		data, err := zipFile(zr, full)
		if err != nil {
			continue
		}
		dir := path.Dir(full)
		md := htmlToMarkdown(string(data), func(u string) string {
			if u == "" || strings.Contains(u, "://") || strings.HasPrefix(u, "data:") {
				return u
			}
			clean := u
			if i := strings.IndexAny(clean, "#?"); i >= 0 {
				clean = clean[:i]
			}
			if clean == "" {
				return u
			}
			if uri := emb.dataURI(path.Join(dir, clean)); uri != "" {
				return uri
			}
			return ""
		})
		if strings.TrimSpace(md) == "" {
			continue
		}
		if chapters > 0 {
			b.WriteString("\n---\n\n")
		}
		b.WriteString(md + "\n")
		chapters++
	}
	if chapters == 0 {
		return nil, fmt.Errorf("no se pudo leer ningún capítulo")
	}
	return []byte(b.String()), nil
}

func firstNonEmpty(ss []string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
