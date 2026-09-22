package main

// wikilink.go — enlaces wiki estilo Obsidian / Foam:
//
//   [[Página]]            -> enlaza a Página.md (en la carpeta del documento)
//   [[Página|alias]]      -> mismo destino, texto visible "alias"
//   [[Página#sección]]    -> Página.md#sección (la sección se busca por su TEXTO, como Obsidian)
//   [[#sección]]          -> ancla dentro del documento actual
//   [[archivo.png]]       -> respeta la extensión si ya la trae
//   ![[foto.png]]         -> imagen insertada;  ![[foto.png|300]] / |300x200 le fija el tamaño
//   ![[Otra nota]]        -> por ahora, un enlace a la nota (no se transcluye)
//
// No creamos un nodo propio: emitimos un *ast.Link / *ast.Image normal con el destino ya armado,
// así el docTransformer existente lo clasifica (doc/anchor/open) y el renderer lo dibuja igual
// que cualquier otro (un solo camino de render). Llevan data-wiki para que la resolución del
// destino sea la de Obsidian: si no está al lado de la nota, se busca en la bóveda (findInVault).

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

const wikiAttr = "data-wiki" // marca de los nodos que vinieron de [[…]]

var (
	wikiImageExtRe = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|svg|webp|bmp|avif|ico)$`)
	wikiSizeRe     = regexp.MustCompile(`^(\d{1,5})(?:x(\d{1,5}))?$`)
)

type wikilinkParser struct{}

// Trigger: '[' para [[…]] y '!' para ![[…]]. Con prioridad 1 se ve antes que el parser de
// enlaces nativo; si no es un wikilink devuelve nil y el nativo sigue como siempre.
func (p *wikilinkParser) Trigger() []byte { return []byte{'[', '!'} }

func (p *wikilinkParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, _ := block.PeekLine()
	embed := len(line) > 0 && line[0] == '!'
	if embed {
		line = line[1:]
	}
	if len(line) < 5 || line[0] != '[' || line[1] != '[' { // mínimo [[x]]
		return nil
	}
	rest := line[2:]
	end := bytes.Index(rest, []byte("]]"))
	if end < 0 {
		return nil
	}
	inner := string(rest[:end])
	if strings.TrimSpace(inner) == "" || strings.ContainsAny(inner, "\n\r[") {
		return nil
	}
	consumed := 2 + end + 2
	if embed {
		consumed++
	}
	block.Advance(consumed)

	target, display, _ := strings.Cut(inner, "|")
	target = strings.TrimSpace(target)
	display = strings.TrimSpace(display)

	if embed && wikiImageExtRe.MatchString(target) {
		img := ast.NewImage(ast.NewLink())
		img.Destination = []byte(filepath.ToSlash(target))
		img.SetAttributeString(wikiAttr, []byte("1"))
		alt := filepath.Base(target)
		if m := wikiSizeRe.FindStringSubmatch(display); m != nil { // |300 o |300x200: tamaño
			img.SetAttributeString("width", []byte(m[1]))
			if m[2] != "" {
				img.SetAttributeString("height", []byte(m[2]))
			}
		} else if display != "" {
			alt = display
		}
		img.AppendChild(img, ast.NewString([]byte(alt)))
		return img
	}

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
	link.SetAttributeString(wikiAttr, []byte("1"))
	link.AppendChild(link, ast.NewString([]byte(display)))
	return link
}

type wikilinkExtension struct{}

func (e *wikilinkExtension) Extend(m goldmark.Markdown) {
	// prioridad ALTA (1) para ganarle al parser de enlaces nativo en los triggers `[` y `!`
	m.Parser().AddOptions(parser.WithInlineParsers(
		util.Prioritized(&wikilinkParser{}, 1),
	))
}

// ---- resolución estilo Obsidian ---------------------------------------------

// Carpetas donde Obsidian (y compañía) suelen guardar los adjuntos.
var vaultAttachmentDirs = []string{"", "attachments", "Attachments", "assets", "images",
	"img", "media", "_resources", "files"}

const (
	vaultMaxUp   = 3   // niveles hacia arriba que se prueban si no hay .obsidian a la vista
	vaultMaxDirs = 256 // tope de carpetas en la búsqueda dentro de la bóveda
)

// vaultResolver resuelve destinos de wikilinks con memoria por render: la misma imagen o
// nota citada diez veces se busca una sola vez.
type vaultResolver struct {
	docDir string
	memo   map[string]string
	root   string // raíz de la bóveda (carpeta con .obsidian); "" si no hay
	index  map[string]string
}

func newVaultResolver(docDir string) *vaultResolver {
	return &vaultResolver{docDir: docDir, memo: map[string]string{}}
}

// find devuelve la ruta absoluta de rel (tal como la escribió el autor) o "" si no existe.
// Orden: al lado de la nota, en las carpetas de adjuntos, subiendo carpetas hasta la raíz
// de la bóveda y, por último, en cualquier carpeta de la bóveda (por nombre, como Obsidian).
func (v *vaultResolver) find(rel string) string {
	if got, ok := v.memo[rel]; ok {
		return got
	}
	got := v.lookup(filepath.FromSlash(rel))
	v.memo[rel] = got
	return got
}

func (v *vaultResolver) lookup(rel string) string {
	if filepath.IsAbs(rel) {
		if fileExists(rel) {
			return rel
		}
		return ""
	}
	dir := v.docDir
	for up := 0; up <= vaultMaxUp && dir != ""; up++ {
		for _, sub := range vaultAttachmentDirs {
			if p := filepath.Join(dir, sub, rel); fileExists(p) {
				return p
			}
		}
		if dirExists(filepath.Join(dir, ".obsidian")) {
			v.root = dir
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if v.root == "" {
		return ""
	}
	if v.index == nil {
		v.index = indexVault(v.root)
	}
	return v.index[strings.ToLower(filepath.Base(rel))]
}

// indexVault recorre la bóveda (BFS, acotado a vaultMaxDirs carpetas y sin entrar a las
// ocultas) y arma nombre -> ruta. La primera aparición gana: la más cercana a la raíz.
func indexVault(root string) map[string]string {
	idx := map[string]string{}
	queue := []string{root}
	for visited := 0; len(queue) > 0 && visited < vaultMaxDirs; visited++ {
		dir := queue[0]
		queue = queue[1:]
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			full := filepath.Join(dir, name)
			if e.IsDir() {
				queue = append(queue, full)
				continue
			}
			if key := strings.ToLower(name); idx[key] == "" {
				idx[key] = full
			}
		}
	}
	return idx
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
