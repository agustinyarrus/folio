package main

// webview_bg.go — pone el color de fondo por defecto de WebView2 en #08090C (el dark de Folio).
//
// Sin esto, el `about:blank` que WebView2 muestra ANTES de cargar la página es BLANCO → al abrir se
// ve un flash blanco con barra de título nativa hasta que aplica el dark/frameless y carga el doc.
// El controller (ICoreWebView2Controller2.PutDefaultBackgroundColor) NO lo expone la API de alto
// nivel de go-webview2, así que lo alcanzamos por reflexión sobre el campo no exportado `browser`
// (*edge.Chromium) del struct webview. Si la lib cambia su interno, el recover() evita romper nada.

import (
	"reflect"
	"unsafe"

	"github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/pkg/edge"
)

func setWebViewDarkBackground(w webview2.WebView) {
	defer func() {
		if r := recover(); r != nil {
			dlog("setDarkBg recover:", r)
		}
	}()

	v := reflect.ValueOf(w)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		dlog("setDarkBg: w no es ptr")
		return
	}
	f := v.Elem().FieldByName("browser")
	if !f.IsValid() || !f.CanAddr() {
		dlog("setDarkBg: sin campo browser")
		return
	}
	// leer el campo no exportado sorteando la restricción de visibilidad
	f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
	chromium, ok := f.Interface().(*edge.Chromium)
	if !ok || chromium == nil {
		dlog("setDarkBg: no es *edge.Chromium")
		return
	}
	ctrl := chromium.GetController()
	if ctrl == nil {
		dlog("setDarkBg: controller nil")
		return
	}
	ctrl2 := ctrl.GetICoreWebView2Controller2()
	if ctrl2 == nil {
		dlog("setDarkBg: controller2 nil")
		return
	}
	err := ctrl2.PutDefaultBackgroundColor(edge.COREWEBVIEW2_COLOR{A: 255, R: 0x08, G: 0x09, B: 0x0C})
	dlog("setDarkBg: aplicado, err=", err)
}
