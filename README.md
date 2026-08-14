<div align="center">

# Folio

**Un lector de documentos dark, frameless y ultraminimalista para Windows.**

Markdown de nacimiento, y **126 extensiones más**: JSON, CSV, YAML, reStructuredText, AsciiDoc, Org,
Jupyter, Word, EPUB y todo el código que chroma sepa resaltar. Todo el render vive en Go — sin
Electron, sin navegador, sin CDN. Un solo `.exe` portable.

![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)
![Windows](https://img.shields.io/badge/Windows-10%20%2F%2011-0078D6?logo=windows&logoColor=white)
![WebView2](https://img.shields.io/badge/WebView2-frameless-7AA2F7)
![Formatos](https://img.shields.io/badge/formatos-126%20extensiones-7AA2F7)
![License](https://img.shields.io/badge/License-MIT-9ECE6A)

[![Descargar](https://img.shields.io/badge/Descargar-Setup_%2B_Portable-7AA2F7?style=for-the-badge&logo=github&logoColor=white)](https://github.com/agustinyarrus/folio/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/agustinyarrus/folio/total?style=for-the-badge&color=9ECE6A&label=descargas)](https://github.com/agustinyarrus/folio/releases)

<img src="docs/screenshot.png" alt="Folio" width="820">

</div>

---

## ✨ Qué es

**Folio** es un visor de documentos minimalista: lo abrís, lee hermoso, te corrés. Una sola ventana
**WebView2 sin marco del sistema** — la barra de título y los botones los dibuja la propia página.
El Markdown lo convierte a HTML **Go** (goldmark + chroma) compilado dentro del `.exe`; la matemática
(KaTeX) y los diagramas (mermaid) van vendorizados. **Funciona 100% offline** y arranca en ~0.5 s.

## 📚 Formatos

Folio no lee solo Markdown. Todo lo demás **se convierte a Markdown** y pasa por el mismo motor,
así que hereda el índice, la búsqueda, el resaltado y la tipografía sin ningún caso especial.

| Grupo | Formatos | Qué hace |
|---|---|---|
| **Markdown** | `md` `markdown` `mdown` `mkd` `mdx` `rmd` `qmd` … | el camino nativo (ver abajo) |
| **Datos** | `json` `jsonc` `json5` `jsonl` `ndjson` `csv` `tsv` `yaml` `toml` `ini` `env` `xml` `plist` | el JSON se formatea; un arreglo de objetos y un CSV salen como **tabla de verdad** |
| **Marcado** | `rst` `adoc` `asciidoc` `org` `wiki` `mediawiki` `html` `htm` `xhtml` | encabezados, listas, énfasis, código, enlaces, imágenes, tablas y admoniciones (`.. note::` → alerta) |
| **Documentos** | `docx` `odt` `epub` `ipynb` | Word y OpenDocument con negritas, enlaces, listas y tablas; el EPUB se arma capítulo por capítulo; el notebook con sus salidas, imágenes y errores |
| **Código** | `go` `py` `js` `ts` `c` `cpp` `rs` `java` `cs` `rb` `php` `sh` `ps1` `sql` … | resaltado por lenguaje: **todo lo que chroma reconozca**, que son ~250 |
| **Texto** | `txt` `log` `nfo` `diff` `patch` | tal cual, con formato preservado (y el `diff` coloreado) |

Los `.docx`, `.odt` y `.epub` son un ZIP con XML adentro: se leen con `archive/zip` y `encoding/xml`
de la biblioteca estándar. Las imágenes que viven dentro del documento se incrustan como `data:`
URI. **Ni una dependencia nueva** para todo esto.

<sub>Detalles: un archivo con BOM o en UTF-16 (el pan de cada día en Windows) se decodifica solo; un
binario con extensión de texto no cuelga nada, muestra un volcado hexadecimal; y si un conversor
falla, se ve el contenido crudo en vez de un error.</sub>

## 🧩 Y en Markdown, "todos los formatos"

- **CommonMark** completo + **GFM**: tablas, tachado, autolinks, listas de tareas.
- **Alertas estilo GitHub**: `> [!NOTE]`, `[!TIP]`, `[!IMPORTANT]`, `[!WARNING]`, `[!CAUTION]` con ícono y color.
- **Emoji** por atajo: `:tada:` → 🎉 (unicode real, offline).
- **Marcas inline** (Pandoc / markdown-it): `==resaltado==`, `++insertado++`, `x^2^`, `H~2~O`.
- **Wikilinks** (Obsidian): `[[Página]]`, `[[Página|alias]]`, `[[Página#sección]]`.
- **Contenedores** (Pandoc / VuePress): `::: warning … :::`, anidables, con título propio.
- **Abreviaturas** (Markdown Extra): `*[HTML]: HyperText Markup Language`.
- **Markdown embebido**: un bloque ` ```markdown ` se renderiza formateado dentro de su caja.
- **IDs de encabezado propios** `{#mi-ancla}`, footnotes, listas de definición, tipografía, frontmatter YAML.
- **Resaltado de código** por lenguaje (chroma, paleta Tokyo Night) con botón de copiar.
- **Matemática** `$...$` / `$$...$$` (KaTeX) y **diagramas** ```mermaid```.

## 🎛️ Características

- **Índice (TOC)** lateral autogenerado, de **ancho arrastrable**, con resaltado de la sección activa.
- **Recarga en vivo**: editás el `.md` en cualquier editor y la vista se actualiza sola (SSE), conservando el scroll.
- **Búsqueda** in-page (Ctrl F) con resaltado vía CSS Custom Highlight API.
- **Zoom de lectura** (Ctrl ±) recordado entre sesiones.
- **Pantalla completa**, **instancia única** (la 2ª apertura reusa la ventana viva), arrastrar-y-soltar.
- Esquinas redondeadas y borde oscuro nativos de Windows 11.

## ⌨️ Atajos

| Tecla                | Acción                          |
|----------------------|---------------------------------|
| `Ctrl O`             | Abrir documento                 |
| `Ctrl F`             | Buscar (`Enter` / `Shift+Enter`)|
| `T`                  | Mostrar/ocultar índice          |
| `Ctrl ±` / `Ctrl+rueda` | Tamaño de letra (se recuerda)|
| `F` / `F11`          | Pantalla completa               |
| `g` / `G`            | Ir arriba / abajo               |
| `Espacio` / `PgUp/Dn`| Desplazar                       |

## 📦 Instalación

> Requiere [**Go**](https://go.dev) 1.24+ para compilar y el **runtime de WebView2** (viene de fábrica en Windows 11).

```powershell
git clone https://github.com/agustinyarrus/folio.git
cd folio
.\build.ps1      # compila folio.exe (release, sin consola, con icono)
.\install.ps1    # instala en Program Files + Menú de Inicio y lo asocia a .md (UAC)
```

El instalador se queda como **predeterminado sólo de los `.md`** y sus variantes. Todo el resto
(`.json`, `.csv`, `.docx`, `.go`…) se agrega únicamente al menú **"Abrir con"**: Folio no le roba
las asociaciones a tu editor ni a Word.

Los iconos son assets curados del repo: **`folio.ico`** (icono de la app, embebido en el `.exe` al
compilar) y **`folio-file.ico`** (icono de los documentos `.md`, vía el ProgID `Folio.Document`). El
script opcional `.\gen-icon.ps1` regenera el glifo original y **sobreescribe `folio.ico`**.

`.\install.ps1 -Uninstall` revierte la instalación. O simplemente corré el `.exe`:

```powershell
folio.exe ruta\al\documento.md
```

## 🏗️ Arquitectura

| Archivo          | Rol                                                                    |
|------------------|------------------------------------------------------------------------|
| `main.go`        | Host frameless + servidor HTTP local + instancia única + recarga viva  |
| `render.go`      | Pipeline goldmark: TOC, código (chroma), enlaces, frontmatter          |
| `formats.go`     | Catálogo de formatos, detección y conversión a Markdown                |
| `fmt_data.go`    | JSON, JSON Lines, CSV/TSV, YAML/TOML/INI, XML, código y texto plano    |
| `fmt_markup.go`  | reStructuredText, AsciiDoc, Org, MediaWiki, HTML                       |
| `fmt_docs.go`    | Jupyter, Word (.docx), OpenDocument (.odt) y EPUB                      |
| `htmlmd.go`      | Parser de HTML propio (sin dependencias) y su conversión a Markdown    |
| `alerts.go`      | Alertas estilo GitHub (`> [!NOTE]`…)                                   |
| `inline_ext.go`  | Marcas inline `==mark==`, `++ins++`, `^sup^`, `~sub~`                  |
| `wikilink.go`    | Enlaces wiki `[[Página]]`                                              |
| `abbr.go`        | Abreviaturas `*[CLAVE]: …` → `<abbr>`                                  |
| `containers.go`  | Contenedores `::: nombre … :::`                                        |
| `mathext.go`     | Extensión propia `$...$` / `$$...$$`                                   |
| `winapi.go`      | Diálogo nativo de apertura, pantalla completa                          |
| `ui/`            | HTML + CSS + JS embebidos (`//go:embed`) y librerías vendorizadas      |

## 📄 Licencia

MIT © Agustín Yarrus — ver [LICENSE](LICENSE).
