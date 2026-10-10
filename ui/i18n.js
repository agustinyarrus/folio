'use strict';

// i18n.js — castellano (fuente) e inglés para la UI.
//
// Las frases en castellano SON las claves del catálogo inglés (estilo gettext): el HTML y el JS se
// siguen leyendo igual que antes, y una frase sin traducir sale en castellano en vez de un hueco
// (la prueba de cobertura de cada app no deja publicar ninguna).
//
//   t('Abrir')                      -> 'Open' en inglés
//   t('Abrí {n} archivos', {n: 3})  -> con llaves
//   tn(n, 'línea', 'líneas')        -> singular o plural, traducido, sin el número
//   tm('Texto')                     -> marca la frase para el catálogo sin traducirla (tablas que se
//                                      traducen recién al mostrarse, con t(fila.nombre))
//   I18N.apply(raíz)                -> traduce lo marcado con data-i18n: sus textos DIRECTOS y sus
//                                      atributos title, placeholder, aria-label y alt. Recuerda el
//                                      castellano original, así ida y vuelta no pierde nada.
//                                      data-i18n="attr": sólo los atributos (el texto lo pone el
//                                      código en vivo, p. ej. una pill de la barra de estado)
//   I18N.set('en')                  -> cambia, re-traduce la página y avisa con el evento 'i18n'
//   I18N.lang · I18N.locale         -> 'es' | 'en' · 'es-AR' | 'en-US'
//   I18N.num(1234.5, 1)             -> '1.234,5' | '1,234.5'
//
// Lo que el código cambia en vivo (un toast, un contador, un título que depende del documento) NO
// lleva data-i18n: se arma con t() y se vuelve a armar al escuchar el evento 'i18n'.
//
// El catálogo y el idioma inicial los pone quien sirve la página: window.__I18N_EN__ (objeto
// castellano -> inglés) y window.__I18N_LANG__ ('es' | 'en'). Este archivo es idéntico en todas
// las apps.
(() => {
  const EN = window.__I18N_EN__ || {};
  const ATTRS = ['title', 'placeholder', 'aria-label', 'alt'];
  const LOCALES = { es: 'es-AR', en: 'en-US' };
  const origText = new WeakMap(); // nodo de texto -> su castellano original, con sus blancos
  const origAttr = new WeakMap(); // elemento -> { atributo: castellano }
  const hasOwn = Object.prototype.hasOwnProperty;
  let lang = window.__I18N_LANG__ === 'en' ? 'en' : 'es';

  // la misma normalización que usa la prueba de cobertura para armar las claves del HTML
  const norm = (s) => s.replace(/\s+/g, ' ').trim();
  const fill = (s, p) => (p ? s.replace(/\{(\w+)\}/g, (m, k) => (hasOwn.call(p, k) ? String(p[k]) : m)) : s);

  function t(es, params) {
    const s = lang === 'en' && hasOwn.call(EN, es) ? EN[es] : es;
    return fill(s, params);
  }
  const tn = (n, one, many, params) => t(n === 1 ? one : many, params);
  const tm = (es) => es;

  function translateElement(el) {
    const soloAtributos = el.getAttribute('data-i18n') === 'attr';
    for (const node of soloAtributos ? [] : el.childNodes) {
      if (node.nodeType !== 3) continue;
      let src = origText.get(node);
      if (src === undefined) {
        if (!node.nodeValue.trim()) continue;
        src = node.nodeValue;
        origText.set(node, src);
      }
      const lead = src.match(/^\s*/)[0];
      const trail = src.slice(lead.length).match(/\s*$/)[0];
      node.nodeValue = lead + t(norm(src)) + trail;
    }
    let attrs = origAttr.get(el);
    if (!attrs) {
      attrs = {};
      for (const a of ATTRS) if (el.hasAttribute(a) && el.getAttribute(a).trim()) attrs[a] = el.getAttribute(a);
      origAttr.set(el, attrs);
    }
    for (const a in attrs) el.setAttribute(a, t(norm(attrs[a])));
  }

  // O(elementos marcados): una sola consulta por raíz.
  function apply(root = document) {
    if (root.nodeType === 1 && root.hasAttribute('data-i18n')) translateElement(root);
    for (const el of root.querySelectorAll('[data-i18n]')) translateElement(el);
  }

  function set(next) {
    next = next === 'en' ? 'en' : 'es';
    if (next === lang) return false;
    lang = next;
    document.documentElement.lang = lang;
    apply();
    window.dispatchEvent(new CustomEvent('i18n', { detail: { lang } }));
    return true;
  }

  // Intl.NumberFormat es caro de crear: uno por idioma y cantidad de decimales.
  const formatters = new Map();
  function num(n, digits) {
    const key = lang + '|' + (digits == null ? '' : digits);
    let f = formatters.get(key);
    if (!f) {
      f = new Intl.NumberFormat(LOCALES[lang], digits == null ? undefined
        : { minimumFractionDigits: digits, maximumFractionDigits: digits });
      formatters.set(key, f);
    }
    return f.format(n);
  }

  window.I18N = Object.freeze({
    t, tn, tm, apply, set, num,
    get lang() { return lang; },
    get locale() { return LOCALES[lang]; },
  });
  window.t = t;
  window.tn = tn;
  window.tm = tm;

  // Todo lo que está ANTES de este <script> ya existe: se traduce ya, así no se ve el castellano
  // ni un cuadro. Lo que venga después se traduce al terminar de cargar (apply es idempotente).
  document.documentElement.lang = lang;
  apply();
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', () => apply(), { once: true });
})();
