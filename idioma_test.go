package main

import (
	"testing"

	"folio/i18n"
)

// enIngles corre la prueba con la interfaz en inglés y la deja en castellano al terminar (el resto
// de las pruebas asume el idioma fuente).
func enIngles(t *testing.T) {
	t.Helper()
	i18n.Set(i18n.EN)
	t.Cleanup(func() { i18n.Set(i18n.ES) })
}

// Lo que arma el Go al convertir formatos sale en el idioma de la interfaz: subtítulos, avisos,
// notas de tabla. Castellano ya lo cubren las pruebas de siempre; acá, el mismo material en inglés
// y la garantía de que no se cuela ni una palabra del otro idioma.
func TestFormatosEnIngles(t *testing.T) {
	enIngles(t)

	res := mustRender(t, "nombre,edad\nana,33\nbeto,41\n", "gente.csv")
	wants(t, res.HTML, "csv en", "CSV · 2 rows × 2 columns")
	lacks(t, res.HTML, "csv en", "filas", "columnas")

	res = mustRender(t, "{\"a\":1}\n{\"a\":2,\"b\":\"x\"}\n", "log.jsonl")
	wants(t, res.HTML, "jsonl en", "2 records", "View the full file")
	lacks(t, res.HTML, "jsonl en", "registros", "Ver el archivo")

	res = mustRender(t, "{\"a\":1}\n42\nroto{\n", "mezcla.jsonl")
	wants(t, res.HTML, "jsonl roto en", "1 line couldn&rsquo;t be read") // el tipógrafo curva el apóstrofo

	res = mustRender(t, "texto\x00binario", "raro.txt")
	wants(t, res.HTML, "binario en", "binary file · ", "This isn&rsquo;t text")
	lacks(t, res.HTML, "binario en", "Esto no es texto")

	res = mustRender(t, "uno\ndos\n", "notas.txt")
	wants(t, res.HTML, "texto en", "2 lines")
	if res.Format != "Text" {
		t.Errorf("formato mostrado = %q, quería %q", res.Format, "Text")
	}

	nb := `{"cells":[{"cell_type":"code","source":"print(1)","outputs":[]}],"metadata":{"kernelspec":{"display_name":"Python 3","language":"python"}},"nbformat":4}`
	res = mustRender(t, nb, "cuaderno.ipynb")
	wants(t, res.HTML, "jupyter en", "Jupyter · Python 3 · 1 cell")
}

// El nombre del formato sigue siendo el canónico para la lógica aunque la interfaz esté en inglés:
// binaryFormats compara contra él (si se tradujera, un .docx dejaría de leerse como ZIP).
func TestNombreCanonicoNoSeTraduce(t *testing.T) {
	enIngles(t)
	if FormatName("x.reg") != "Registro de Windows" || formatLabel("x.reg") != "Windows Registry" {
		t.Errorf("FormatName=%q formatLabel=%q", FormatName("x.reg"), formatLabel("x.reg"))
	}
	if !binaryFormats[FormatName("informe.docx")] {
		t.Error("un .docx tiene que seguir siendo binario con la UI en inglés")
	}
}
