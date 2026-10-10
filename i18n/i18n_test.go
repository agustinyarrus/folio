package i18n

import (
	"errors"
	"strings"
	"testing"
)

// swapCatalog instala un catálogo de prueba y devuelve cómo restaurar el real. Las pruebas de este
// archivo no dependen de lo que traduzca cada app.
func swapCatalog(t *testing.T, m map[string]string) {
	t.Helper()
	old, oldLang := en, Current()
	en = m
	t.Cleanup(func() { en = old; Set(oldLang) })
}

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Lang
		ok   bool
	}{
		{"es", ES, true}, {"ES", ES, true}, {" es ", ES, true}, {"es-AR", ES, true}, {"es-419", ES, true},
		{"es_419.UTF-8", ES, true}, {"es_ES@euro", ES, true}, {"spa", ES, true}, {"Español", ES, true},
		{"espanol", ES, true}, {"castellano", ES, true}, {"Spanish", ES, true},
		{"en", EN, true}, {"EN", EN, true}, {"en-US", EN, true}, {"en_GB.utf8", EN, true}, {"eng", EN, true},
		{"English", EN, true}, {"inglés", EN, true}, {"ingles", EN, true},
		{"", ES, false}, {"   ", ES, false}, {"auto", ES, false}, {"AUTO", ES, false}, {"C", ES, false},
		{"POSIX", ES, false}, {"C.UTF-8", ES, false}, {"fr", ES, false}, {"pt-BR", ES, false},
		{"esperanto", ES, false}, {"e", ES, false}, {"-", ES, false},
	}
	for _, c := range cases {
		got, ok := Parse(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("Parse(%q) = %v,%v; quería %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// entorno arma un LookupEnv falso a partir de pares clave=valor.
func entorno(kv ...string) func(string) (string, bool) {
	m := map[string]string{}
	for _, s := range kv {
		k, v, _ := strings.Cut(s, "=")
		m[k] = v
	}
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func sistema(ui, format string) func() (string, string) {
	return func() (string, string) { return ui, format }
}

func TestResolveOrden(t *testing.T) {
	type caso struct {
		nombre string
		c      Choice
		env    []string
		ui     string
		format string
		want   Lang
		src    Source
	}
	app := "APP_LANG"
	cases := []caso{
		{"flag gana a todo", Choice{Flag: "en", Setting: "es", AppEnv: app}, []string{"APP_LANG=es", "LC_ALL=es_AR"}, "es-ES", "es-AR", EN, FromFlag},
		{"flag auto no elige", Choice{Flag: "auto", Setting: "en"}, nil, "es-ES", "", EN, FromSetting},
		{"ajuste gana a la variable", Choice{Setting: "es", AppEnv: app}, []string{"APP_LANG=en"}, "en-US", "en-US", ES, FromSetting},
		{"ajuste auto cae a la variable", Choice{Setting: "auto", AppEnv: app}, []string{"APP_LANG=en"}, "es-ES", "", EN, FromAppEnv},
		{"variable inválida no elige", Choice{AppEnv: app}, []string{"APP_LANG=klingon"}, "es-419", "", ES, FromOS},
		{"variable vacía no elige", Choice{AppEnv: app}, []string{"APP_LANG="}, "en-US", "en-US", EN, FromOS},
		{"sin nombre de variable no la busca", Choice{}, []string{"=en"}, "es-ES", "", ES, FromOS},
	}
	for _, k := range localeVars {
		cases = append(cases,
			caso{"locale " + k + " es", Choice{}, []string{k + "=es_AR.UTF-8"}, "en-US", "en-US", ES, FromLocaleEnv},
			caso{"locale " + k + " C = inglés", Choice{}, []string{k + "=C"}, "es-ES", "es-ES", EN, FromLocaleEnv},
			caso{"locale " + k + " francés = inglés", Choice{}, []string{k + "=fr_FR.UTF-8"}, "es-ES", "es-ES", EN, FromLocaleEnv},
			caso{"locale " + k + " en blanco no cuenta", Choice{}, []string{k + "=  "}, "es-ES", "", ES, FromOS},
		)
	}
	if len(localeVars) >= 2 {
		a, b := localeVars[0], localeVars[1]
		cases = append(cases, caso{"manda la primera variable con valor", Choice{}, []string{a + "=en_US", b + "=es_AR"}, "es-ES", "", EN, FromLocaleEnv})
	}
	cases = append(cases,
		// El caso que motivó la regla: Windows en inglés con formato regional latinoamericano.
		caso{"interfaz en inglés + formato es-419", Choice{}, nil, "en-US", "es-419", ES, FromOS},
		caso{"interfaz en castellano + formato inglés", Choice{}, nil, "es-ES", "en-US", ES, FromOS},
		caso{"todo inglés", Choice{}, nil, "en-US", "en-GB", EN, FromOS},
		caso{"portugués va a inglés", Choice{}, nil, "pt-BR", "pt-BR", EN, FromOS},
		caso{"sin datos del sistema", Choice{}, nil, "", "", EN, FromDefault},
		caso{"sólo formato", Choice{}, nil, "", "es-MX", ES, FromOS},
	)
	for _, c := range cases {
		got, src := resolve(c.c, entorno(c.env...), sistema(c.ui, c.format))
		if got != c.want || src != c.src {
			t.Errorf("%s: resolve = %v (%v); quería %v (%v)", c.nombre, got, src, c.want, c.src)
		}
	}
}

func TestLANGSoloFueraDeWindows(t *testing.T) {
	tieneLANG := false
	for _, k := range localeVars {
		tieneLANG = tieneLANG || k == "LANG"
	}
	got, src := resolve(Choice{}, entorno("LANG=en_US.UTF-8"), sistema("en-US", "es-419"))
	if tieneLANG && (got != EN || src != FromLocaleEnv) {
		t.Errorf("fuera de Windows LANG manda: %v %v", got, src)
	}
	if !tieneLANG && (got != ES || src != FromOS) {
		t.Errorf("en Windows LANG (la pone Git Bash sola) no debe mandar: %v %v", got, src)
	}
}

func TestSetCurrent(t *testing.T) {
	swapCatalog(t, map[string]string{})
	Set(EN)
	if Current() != EN {
		t.Fatal("Set(EN)")
	}
	Set(Lang(77)) // cualquier valor raro vuelve al idioma fuente
	if Current() != ES {
		t.Fatal("Set(desconocido) debería quedar en ES")
	}
}

func TestTraduccion(t *testing.T) {
	swapCatalog(t, map[string]string{
		"Abrir":               "Open",
		"línea":               "line",
		"líneas":              "lines",
		"%d línea":            "%d line",
		"%d líneas":           "%d lines",
		"No se pudo abrir %s": "Could not open %s",
		"%[2]s de %[1]s":      "%[1]s's %[2]s",
	})
	Set(ES)
	if T("Abrir") != "Abrir" || F("No se pudo abrir %s", "x") != "No se pudo abrir x" {
		t.Error("en castellano T/F devuelven la fuente")
	}
	Set(EN)
	checks := []struct{ got, want string }{
		{T("Abrir"), "Open"},
		{T("sin traducción"), "sin traducción"}, // falta -> la fuente, nunca un hueco
		{F("No se pudo abrir %s", "a.md"), "Could not open a.md"},
		{N(1, "línea", "líneas"), "line"},
		{N(0, "línea", "líneas"), "lines"},
		{N(2, "línea", "líneas"), "lines"},
		{N(-1, "línea", "líneas"), "lines"},
		{Nf(1, "%d línea", "%d líneas", 1), "1 line"},
		{Nf(12, "%d línea", "%d líneas", 12), "12 lines"},
		{F("%[2]s de %[1]s", "Folio", "menú"), "Folio's menú"},
		{In(ES, "Abrir"), "Abrir"},
		{In(EN, "Abrir"), "Open"},
		{M("Abrir"), "Abrir"},
	}
	for i, c := range checks {
		if c.got != c.want {
			t.Errorf("caso %d: %q, quería %q", i, c.got, c.want)
		}
	}
}

func TestErrores(t *testing.T) {
	swapCatalog(t, map[string]string{
		"el portapapeles no tiene texto": "the clipboard has no text",
		"no pude leer %s: %w":            "could not read %s: %w",
	})
	centinela := Error("el portapapeles no tiene texto") // nace en castellano, antes de resolver
	Set(EN)
	if centinela.Error() != "the clipboard has no text" {
		t.Errorf("Error se traduce al mostrarse: %q", centinela.Error())
	}
	var err error = centinela
	if !errors.Is(err, Error("el portapapeles no tiene texto")) {
		t.Error("errors.Is compara por valor")
	}
	base := errors.New("permiso denegado")
	envuelto := Errorf("no pude leer %s: %w", "a.txt", base)
	if envuelto.Error() != "could not read a.txt: permiso denegado" || !errors.Is(envuelto, base) {
		t.Errorf("Errorf traduce y conserva %%w: %q", envuelto)
	}
}

func TestNumeros(t *testing.T) {
	swapCatalog(t, map[string]string{})
	cases := []struct {
		l    Lang
		got  func() string
		want string
	}{
		{ES, func() string { return Int(0) }, "0"},
		{ES, func() string { return Int(999) }, "999"},
		{ES, func() string { return Int(1000) }, "1.000"},
		{ES, func() string { return Int(1234567) }, "1.234.567"},
		{ES, func() string { return Int(-1234) }, "-1.234"},
		{EN, func() string { return Int(1234567) }, "1,234,567"},
		{EN, func() string { return Int(-100) }, "-100"},
		{ES, func() string { return Dec(51.84, 1) }, "51,8"},
		{EN, func() string { return Dec(51.84, 1) }, "51.8"},
		{ES, func() string { return Dec(12345.678, 2) }, "12.345,68"},
		{EN, func() string { return Dec(-12345.5, 0) }, "-12,346"},
		{ES, func() string { return Dec(0.5, 3) }, "0,500"},
	}
	for i, c := range cases {
		Set(c.l)
		if g := c.got(); g != c.want {
			t.Errorf("caso %d (%v): %q, quería %q", i, c.l, g, c.want)
		}
	}
}

func TestNombres(t *testing.T) {
	if ES.String() != "es" || EN.String() != "en" || ES.Name() != "Español" || EN.Name() != "English" {
		t.Error("String/Name")
	}
	if ES.Tag() != "es-AR" || EN.Tag() != "en-US" {
		t.Error("Tag")
	}
	if FromOS.String() != "os" || Source(200).String() != "source(200)" {
		t.Error("Source.String")
	}
}

func TestCatalogoRotoExplota(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("un en.json roto tiene que explotar al cargar")
		}
	}()
	mustCatalog([]byte(`{"a": 1}`))
}

// TestSistemaNoExplota corre la consulta real al sistema: sólo exige que no falle y que, si Windows
// contesta, conteste etiquetas con forma de idioma.
func TestSistemaNoExplota(t *testing.T) {
	ui, format := osLanguages()
	for _, tag := range []string{ui, format} {
		if tag != "" && (len(tag) < 2 || strings.ContainsAny(tag, " \x00")) {
			t.Errorf("etiqueta rara del sistema: %q", tag)
		}
	}
	t.Logf("sistema: interfaz=%q formato=%q -> %v", ui, format, OSLang())
}
