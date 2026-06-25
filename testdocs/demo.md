---
title: Folio
---

# Folio

Un lector de **Markdown** dark, frameless y ultraminimalista. Todo el render vive en Go
(goldmark + chroma); la matemática y los diagramas van vendorizados. Funciona 100% offline.

> [!NOTE]
> Soporta CommonMark, GFM, alertas de GitHub, emoji, contenedores, wikilinks y mucho más.

## Código con resaltado

```go
func saludar(nombre string) string {
    return fmt.Sprintf("¡Hola, %s! 👋", nombre)
}
```

## Tabla y matemática

| Formato   | Soportado | Motor   |
|:----------|:---------:|:--------|
| GFM       |     ✓     | goldmark |
| Matemática |     ✓     | KaTeX   |
| Diagramas |     ✓     | mermaid |

La identidad de Euler, en línea: $e^{i\pi} + 1 = 0$.

## Lista de tareas

- [x] Render server-side en Go
- [x] Índice con scroll-spy
- [ ] Conquistar el mundo
