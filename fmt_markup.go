package main

// fmt_markup.go — otros lenguajes de marcado -> Markdown.
//
// reStructuredText, AsciiDoc, Org y MediaWiki. No son conversores completos (cada uno de esos
// formatos da para una biblioteca entera): cubren lo que uno se encuentra de verdad en un README
// o en la documentacion de un proyecto — encabezados, listas, enfasis, codigo, enlaces, imagenes,
// tablas y admoniciones. Lo que no se reconoce pasa como texto, que es la degradacion correcta:
// se sigue leyendo.

import (
	"regexp"
	"strings"
)

// ---- HTML ----------------------------------------------------------------

func convHTML(src []byte, path string) ([]byte, error) {
	s := string(src)
	body := htmlToMarkdown(s, nil)
	title := HTMLTitle(s)
	head := ""
	if title != "" && !strings.HasPrefix(strings.TrimSpace(body), "# ") {
		head = "# " + mdEscape(title) + "\n\n"
	}
	return []byte(head + body), nil
}

// ---- reStructuredText ----------------------------------------------------

var (
	rstAdorn    = `=-~^"'` + "`" + `#*+_:.<>!$%&(),/;?@[\]{|}`
	rstDirRe    = regexp.MustCompile(`^\.\.\s+([a-zA-Z0-9_-]+)::\s*(.*)$`)
	rstFieldRe  = regexp.MustCompile(`^:([^:]+):\s*(.*)$`)
	rstLinkRe   = regexp.MustCompile("`([^`<]+?)\\s*<([^>]+)>`_+")
	rstRoleRe   = regexp.MustCompile(":[a-zA-Z:]+:`([^`]+)`")
	rstLiteral  = regexp.MustCompile("``([^`]+)``")
	rstAdmonMap = map[string]string{
		"note": "NOTE", "tip": "TIP", "hint": "TIP", "important": "IMPORTANT",
		"warning": "WARNING", "caution": "WARNING", "attention": "WARNING",
		"danger": "CAUTION", "error": "CAUTION",
	}
)

func isAdornment(s string) bool {
	s = strings.TrimRight(s, " \t")
	if len(s) < 2 {
		return false
	}
	c := s[0]
	if !strings.ContainsRune(rstAdorn, rune(c)) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != c {
			return false
		}
	}
	return true
}

// rstInline traduce el enfasis y los enlaces de una linea de reST.
func rstInline(s string) string {
	s = rstLiteral.ReplaceAllString(s, "`$1`")
	s = rstLinkRe.ReplaceAllString(s, "[$1]($2)")
	s = rstRoleRe.ReplaceAllString(s, "`$1`")
	s = strings.ReplaceAll(s, "|", `\|`)
	return s
}

func convRST(src []byte, path string) ([]byte, error) {
	lines := splitLines(src)
	var out []string
	levels := map[byte]int{} // caracter de subrayado -> nivel de encabezado
	next := 1

	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		trimmed := strings.TrimRight(ln, " \t")

		// encabezado: texto con una linea de adorno abajo (y a veces arriba)
		if i+1 < len(lines) && trimmed != "" && !strings.HasPrefix(trimmed, "..") &&
			isAdornment(lines[i+1]) && len(strings.TrimRight(lines[i+1], " \t")) >= len(trimmed)/2 {
			c := strings.TrimRight(lines[i+1], " \t")[0]
			lvl, ok := levels[c]
			if !ok {
				lvl = next
				if lvl > 6 {
					lvl = 6
				}
				levels[c] = lvl
				next++
			}
			out = append(out, "", strings.Repeat("#", lvl)+" "+rstInline(strings.TrimSpace(trimmed)), "")
			i++
			// adorno de arriba: saltarlo tambien
			continue
		}
		if isAdornment(trimmed) && len(trimmed) >= 3 {
			// linea de adorno suelta: transicion horizontal
			if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" && !isAdornment(lines[i+1]) {
				continue // era el adorno superior de un titulo
			}
			out = append(out, "", "---", "")
			continue
		}

		// directivas: .. code-block:: go / .. note:: / .. image:: x
		if m := rstDirRe.FindStringSubmatch(trimmed); m != nil {
			name, arg := strings.ToLower(m[1]), strings.TrimSpace(m[2])
			body, adv := rstIndentedBlock(lines, i+1)
			switch {
			case name == "code-block" || name == "code" || name == "sourcecode":
				out = append(out, "", codeBlock(strings.Join(body, "\n"), arg))
				i += adv
			case name == "image" || name == "figure":
				out = append(out, "", "!["+"]("+arg+")", "")
				i += adv
			case rstAdmonMap[name] != "":
				out = append(out, "", "> [!"+rstAdmonMap[name]+"]")
				if arg != "" {
					out = append(out, "> "+rstInline(arg))
				}
				for _, b := range body {
					out = append(out, "> "+rstInline(strings.TrimSpace(b)))
				}
				out = append(out, "")
				i += adv
			case name == "contents" || name == "toctree" || name == "index":
				i += adv // el indice lo arma Folio solo
			default:
				if len(body) > 0 {
					out = append(out, "", codeBlock(strings.Join(body, "\n"), ""))
					i += adv
				}
			}
			continue
		}
		// comentario suelto
		if strings.HasPrefix(trimmed, ".. ") || trimmed == ".." {
			_, adv := rstIndentedBlock(lines, i+1)
			i += adv
			continue
		}
		// bloque literal: parrafo que termina en ::
		if strings.HasSuffix(trimmed, "::") && strings.TrimSpace(trimmed) != "::" {
			out = append(out, "", rstInline(strings.TrimSuffix(trimmed, "::"))+":", "")
			body, adv := rstIndentedBlock(lines, i+1)
			out = append(out, codeBlock(strings.Join(body, "\n"), ""))
			i += adv
			continue
		}
		// lista de campos  :autor: fulano
		if m := rstFieldRe.FindStringSubmatch(trimmed); m != nil && !strings.HasPrefix(trimmed, "::") {
			out = append(out, "**"+mdEscape(m[1])+"**: "+rstInline(m[2]))
			continue
		}
		// enumeracion con #.
		if strings.HasPrefix(strings.TrimSpace(trimmed), "#. ") {
			ind := trimmed[:len(trimmed)-len(strings.TrimLeft(trimmed, " \t"))]
			out = append(out, ind+"1. "+rstInline(strings.TrimSpace(trimmed)[3:]))
			continue
		}
		out = append(out, rstInline(trimmed))
	}
	return []byte(docHeaderIfNeeded(out, path) + strings.Join(out, "\n") + "\n"), nil
}

// rstIndentedBlock junta el bloque indentado que sigue a una directiva.
// Devuelve las lineas sin la indentacion y cuantas consumir.
func rstIndentedBlock(lines []string, start int) ([]string, int) {
	i := start
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i >= len(lines) {
		return nil, i - start
	}
	ind := indentOf(lines[i])
	if ind == 0 {
		return nil, 0
	}
	var body []string
	for ; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			body = append(body, "")
			continue
		}
		if indentOf(lines[i]) < ind {
			break
		}
		body = append(body, lines[i][min(ind, len(lines[i])):])
	}
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	// opciones de directiva (:width: 50) al principio: fuera
	for len(body) > 0 && rstFieldRe.MatchString(strings.TrimSpace(body[0])) {
		body = body[1:]
	}
	return body, i - start
}

func indentOf(s string) int {
	n := 0
	for _, c := range s {
		if c == ' ' {
			n++
		} else if c == '\t' {
			n += 4
		} else {
			break
		}
	}
	return n
}

// ---- AsciiDoc ------------------------------------------------------------

var (
	adocAttrRe  = regexp.MustCompile(`^:[^:]+:\s*(.*)$`)
	adocImgRe   = regexp.MustCompile(`image::?([^\[\s]+)\[([^\]]*)\]`)
	adocLinkRe  = regexp.MustCompile(`link:([^\[\s]+)\[([^\]]*)\]`)
	adocURLRe   = regexp.MustCompile(`(^|\s)(https?://[^\[\s]+)\[([^\]]*)\]`)
	adocXrefRe  = regexp.MustCompile(`<<([^,>]+)(?:,\s*([^>]+))?>>`)
	adocMonoRe  = regexp.MustCompile("`\\+?([^`]+?)\\+?`")
	adocBoldRe  = regexp.MustCompile(`(^|\W)\*([^*\n]+)\*(\W|$)`)
	adocItalRe  = regexp.MustCompile(`(^|\W)_([^_\n]+)_(\W|$)`)
	adocAdmonRe = regexp.MustCompile(`^(NOTE|TIP|IMPORTANT|WARNING|CAUTION):\s*(.*)$`)
	adocBlkAttr = regexp.MustCompile(`^\[([a-zA-Z]+)[,\]]`)
	adocSrcAttr = regexp.MustCompile(`^\[source\s*,?\s*([a-zA-Z0-9_+-]*)`)
)

func adocInline(s string) string {
	// image::src[alt] — el corchete suele traer atributos (link=…, width=…) que no son el alt
	s = adocImgRe.ReplaceAllStringFunc(s, func(m string) string {
		g := adocImgRe.FindStringSubmatch(m)
		alt := g[2]
		if strings.Contains(alt, "=") {
			alt = ""
		}
		if i := strings.IndexByte(alt, ','); i >= 0 {
			alt = alt[:i]
		}
		return "![" + alt + "](" + g[1] + ")"
	})
	s = adocLinkRe.ReplaceAllStringFunc(s, func(m string) string {
		g := adocLinkRe.FindStringSubmatch(m)
		text := g[2]
		if text == "" {
			text = g[1]
		}
		if i := strings.IndexByte(text, ','); i >= 0 {
			text = text[:i]
		}
		return "[" + text + "](" + g[1] + ")"
	})
	s = adocURLRe.ReplaceAllString(s, "$1[$3]($2)")
	s = adocXrefRe.ReplaceAllString(s, "$2")
	s = adocMonoRe.ReplaceAllString(s, "`$1`")
	s = adocBoldRe.ReplaceAllString(s, "$1**$2**$3")
	s = adocItalRe.ReplaceAllString(s, "$1*$2*$3")
	return s
}

func convAsciiDoc(src []byte, path string) ([]byte, error) {
	lines := splitLines(src)
	var out []string
	lang, admon := "", ""
	inCode, inTable := false, false
	var tbl [][]string

	for i := 0; i < len(lines); i++ {
		ln := strings.TrimRight(lines[i], " \t")
		t := strings.TrimSpace(ln)

		// bloque delimitado: ---- ==== .... ++++
		if isFence(t) {
			if inCode {
				out = append(out, "```", "")
				inCode = false
			} else {
				out = append(out, "", "```"+lang)
				inCode = true
				lang = ""
			}
			continue
		}
		if inCode {
			out = append(out, lines[i])
			continue
		}
		// tabla |===
		if strings.HasPrefix(t, "|===") {
			if inTable {
				inTable = false
				if len(tbl) > 0 {
					out = append(out, "", strings.TrimRight(mdTable(tbl[0], tbl[1:]), "\n"))
				}
				tbl = nil
			} else {
				inTable = true
			}
			continue
		}
		if inTable {
			if t == "" {
				continue
			}
			cells := strings.Split(strings.TrimPrefix(t, "|"), "|")
			for j := range cells {
				cells[j] = adocInline(strings.TrimSpace(cells[j]))
			}
			tbl = append(tbl, cells)
			continue
		}
		// atributos de bloque: [source,go] / [NOTE]
		if m := adocSrcAttr.FindStringSubmatch(t); m != nil {
			lang = m[1]
			continue
		}
		if m := adocBlkAttr.FindStringSubmatch(t); m != nil {
			if a := strings.ToUpper(m[1]); rstAdmonMap[strings.ToLower(a)] != "" {
				admon = rstAdmonMap[strings.ToLower(a)]
			}
			continue
		}
		// comentarios y atributos del documento
		if strings.HasPrefix(t, "//") {
			continue
		}
		if adocAttrRe.MatchString(t) && strings.HasPrefix(t, ":") {
			continue
		}
		// encabezados = == ===
		if m := regexp.MustCompile(`^(=+)\s+(.*)$`).FindStringSubmatch(t); m != nil {
			lvl := len(m[1])
			if lvl > 6 {
				lvl = 6
			}
			out = append(out, "", strings.Repeat("#", lvl)+" "+adocInline(m[2]), "")
			continue
		}
		// admoniciones en linea
		if m := adocAdmonRe.FindStringSubmatch(t); m != nil {
			out = append(out, "", "> [!"+rstAdmonMap[strings.ToLower(m[1])]+"]", "> "+adocInline(m[2]), "")
			continue
		}
		if admon != "" && t != "" {
			out = append(out, "", "> [!"+admon+"]", "> "+adocInline(t), "")
			admon = ""
			continue
		}
		// listas: * ** *** y . .. ...
		if m := regexp.MustCompile(`^(\*+|\.+)\s+(.*)$`).FindStringSubmatch(t); m != nil && len(m[1]) <= 5 {
			depth := len(m[1]) - 1
			marker := "- "
			if m[1][0] == '.' {
				marker = "1. "
			}
			out = append(out, strings.Repeat("  ", depth)+marker+adocInline(m[2]))
			continue
		}
		out = append(out, adocInline(ln))
	}
	if inCode {
		out = append(out, "```")
	}
	return []byte(docHeaderIfNeeded(out, path) + strings.Join(out, "\n") + "\n"), nil
}

func isFence(t string) bool {
	if len(t) < 4 {
		return false
	}
	c := t[0]
	if c != '-' && c != '=' && c != '.' && c != '+' && c != '*' && c != '_' {
		return false
	}
	for i := 0; i < len(t); i++ {
		if t[i] != c {
			return false
		}
	}
	return c == '-' || c == '.' || c == '+'
}

// ---- Org-mode ------------------------------------------------------------

var (
	orgLinkRe  = regexp.MustCompile(`\[\[([^\]\[]+)\](?:\[([^\]]+)\])?\]`)
	orgBoldRe  = regexp.MustCompile(`(^|\W)\*([^*\n]+)\*(\W|$)`)
	orgItalRe  = regexp.MustCompile(`(^|\W)/([^/\n]+)/(\W|$)`)
	orgCodeRe  = regexp.MustCompile("(^|\\W)[=~]([^=~\\n]+)[=~](\\W|$)")
	orgStrikRe = regexp.MustCompile(`(^|\W)\+([^+\n]+)\+(\W|$)`)
)

func orgInline(s string) string {
	s = orgLinkRe.ReplaceAllStringFunc(s, func(m string) string {
		g := orgLinkRe.FindStringSubmatch(m)
		if g[2] != "" {
			return "[" + g[2] + "](" + g[1] + ")"
		}
		return "<" + g[1] + ">"
	})
	s = orgCodeRe.ReplaceAllString(s, "$1`$2`$3")
	s = orgBoldRe.ReplaceAllString(s, "$1**$2**$3")
	s = orgItalRe.ReplaceAllString(s, "$1*$2*$3")
	s = orgStrikRe.ReplaceAllString(s, "$1~~$2~~$3")
	return s
}

func convOrg(src []byte, path string) ([]byte, error) {
	lines := splitLines(src)
	var out []string
	title := ""
	inSrc := false

	for _, raw := range lines {
		ln := strings.TrimRight(raw, " \t")
		t := strings.TrimSpace(ln)
		up := strings.ToUpper(t)

		switch {
		case strings.HasPrefix(up, "#+BEGIN_SRC"), strings.HasPrefix(up, "#+BEGIN_EXAMPLE"):
			lang := ""
			if f := strings.Fields(t); len(f) > 1 && strings.HasPrefix(up, "#+BEGIN_SRC") {
				lang = f[1]
			}
			out = append(out, "", "```"+lang)
			inSrc = true
			continue
		case strings.HasPrefix(up, "#+END_SRC"), strings.HasPrefix(up, "#+END_EXAMPLE"):
			out = append(out, "```", "")
			inSrc = false
			continue
		case inSrc:
			out = append(out, raw)
			continue
		case strings.HasPrefix(up, "#+TITLE:"):
			title = strings.TrimSpace(t[8:])
			continue
		case strings.HasPrefix(up, "#+BEGIN_QUOTE"):
			out = append(out, "")
			continue
		case strings.HasPrefix(up, "#+END_QUOTE"):
			out = append(out, "")
			continue
		case strings.HasPrefix(t, "#+"), strings.HasPrefix(t, "# "):
			continue // otras directivas y comentarios
		}

		// encabezados: * ** ***
		if m := regexp.MustCompile(`^(\*+)\s+(.*)$`).FindStringSubmatch(ln); m != nil {
			lvl := len(m[1])
			if lvl > 6 {
				lvl = 6
			}
			txt := m[2]
			// palabras de estado y etiquetas :tag:
			txt = regexp.MustCompile(`^(TODO|DONE|NEXT|WAITING)\s+`).ReplaceAllString(txt, "**$1** ")
			txt = regexp.MustCompile(`\s+:[\w:@]+:$`).ReplaceAllString(txt, "")
			out = append(out, "", strings.Repeat("#", lvl)+" "+orgInline(txt), "")
			continue
		}
		// tablas: la fila separadora |---+---| pasa a GFM
		if strings.HasPrefix(t, "|") {
			if strings.HasPrefix(t, "|-") {
				prev := ""
				if len(out) > 0 {
					prev = out[len(out)-1]
				}
				n := strings.Count(prev, "|") - 1
				if n < 1 {
					n = 1
				}
				out = append(out, "|"+strings.Repeat(" --- |", n))
				continue
			}
			out = append(out, orgInline(t))
			continue
		}
		// listas y casillas
		if m := regexp.MustCompile(`^(\s*)([-+]|\d+[.)])\s+(.*)$`).FindStringSubmatch(ln); m != nil {
			marker := m[2]
			if marker == "+" {
				marker = "-"
			}
			body := strings.Replace(m[3], "[ ]", "[ ]", 1)
			out = append(out, m[1]+marker+" "+orgInline(body))
			continue
		}
		out = append(out, orgInline(ln))
	}
	head := ""
	if title != "" {
		head = "# " + mdEscape(title) + "\n\n"
	}
	return []byte(head + docHeaderIfNeeded(out, path) + strings.Join(out, "\n") + "\n"), nil
}

// ---- MediaWiki -----------------------------------------------------------

var (
	wikiHeadRe = regexp.MustCompile(`^(={2,6})\s*(.*?)\s*={2,6}\s*$`)
	wikiLinkRe = regexp.MustCompile(`\[\[([^\]|]+)(?:\|([^\]]+))?\]\]`)
	wikiExtRe  = regexp.MustCompile(`\[(https?://[^\s\]]+)(?:\s+([^\]]+))?\]`)
	wikiTmplRe = regexp.MustCompile(`\{\{[^{}]*\}\}`)
)

func wikiInline(s string) string {
	for wikiTmplRe.MatchString(s) {
		s = wikiTmplRe.ReplaceAllString(s, "")
	}
	s = wikiLinkRe.ReplaceAllStringFunc(s, func(m string) string {
		g := wikiLinkRe.FindStringSubmatch(m)
		target, text := g[1], g[2]
		if strings.HasPrefix(strings.ToLower(target), "file:") ||
			strings.HasPrefix(strings.ToLower(target), "image:") {
			return ""
		}
		if text == "" {
			text = target
		}
		return "[[" + target + "|" + text + "]]" // wikilink nativo de Folio
	})
	s = wikiExtRe.ReplaceAllString(s, "[$2]($1)")
	s = strings.ReplaceAll(s, "'''''", "***")
	s = strings.ReplaceAll(s, "'''", "**")
	s = strings.ReplaceAll(s, "''", "*")
	s = strings.ReplaceAll(s, "<nowiki>", "")
	s = strings.ReplaceAll(s, "</nowiki>", "")
	return s
}

func convWiki(src []byte, path string) ([]byte, error) {
	lines := splitLines(src)
	var out []string
	for _, raw := range lines {
		t := strings.TrimRight(raw, " \t")
		switch {
		case wikiHeadRe.MatchString(t):
			m := wikiHeadRe.FindStringSubmatch(t)
			lvl := len(m[1])
			out = append(out, "", strings.Repeat("#", lvl)+" "+wikiInline(m[2]), "")
		case strings.HasPrefix(t, "----"):
			out = append(out, "", "---", "")
		case strings.HasPrefix(t, "*") || strings.HasPrefix(t, "#"):
			depth := 0
			for depth < len(t) && (t[depth] == '*' || t[depth] == '#') {
				depth++
			}
			marker := "- "
			if t[depth-1] == '#' {
				marker = "1. "
			}
			out = append(out, strings.Repeat("  ", depth-1)+marker+wikiInline(strings.TrimSpace(t[depth:])))
		case strings.HasPrefix(t, ";") || strings.HasPrefix(t, ":"):
			out = append(out, wikiInline(strings.TrimSpace(t[1:])))
		case strings.HasPrefix(t, " "):
			out = append(out, t) // linea preformateada
		default:
			out = append(out, wikiInline(t))
		}
	}
	return []byte(docHeaderIfNeeded(out, path) + strings.Join(out, "\n") + "\n"), nil
}

// ---- utilidades ----------------------------------------------------------

func splitLines(src []byte) []string {
	s := strings.ReplaceAll(string(src), "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Split(s, "\n")
}

// docHeaderIfNeeded agrega un H1 con el nombre del archivo solo si el documento
// convertido no empieza con uno propio.
func docHeaderIfNeeded(out []string, path string) string {
	for _, l := range out {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "# ") {
			return ""
		}
		break
	}
	return docHeader(path, FormatName(path))
}
