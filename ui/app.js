'use strict';

// Folio — logica de la UI. WebView2 no soporta -webkit-app-region, asi que el arrastre y el
// redimensionado de la ventana frameless se piden al host (folioDrag/folioResize). El resto es
// lector: render del Markdown ya convertido por el server, realce de matematica (KaTeX) y
// diagramas (mermaid), indice con scroll-spy, busqueda, zoom de lectura y recarga en vivo.

const $ = (id) => document.getElementById(id);
const body = document.body;
const content = $('content');
const reader = $('reader');
const toc = $('toc');
const tocInner = $('tocInner');

window.__log = (m) => { if (!window.__FOLIO_DEBUG__) return; try { fetch('/log?m=' + encodeURIComponent(m)); } catch (e) {} };
window.addEventListener('error', (e) => window.__log('ERR ' + e.message + ' @' + (e.filename || '') + ':' + e.lineno));
window.addEventListener('unhandledrejection', (e) => window.__log('REJECT ' + (e.reason && (e.reason.message || e.reason))));

function bridge(name, ...args) {
  try { if (typeof window[name] === 'function') return window[name](...args); }
  catch (e) { /* dev en navegador: sin host */ }
}

// ---- estado -------------------------------------------------------------
const current = { path: null, name: '', dir: '' };
let es = null;               // EventSource de recarga en vivo
let headings = [];           // encabezados con id, para scroll-spy
let mermaidReady = false;
let tocOpen = window.__FOLIO_TOC__ !== false;   // inyectado por el host desde el config

// =========================================================================
// Apertura / render
// =========================================================================
async function openPath(path, opts = {}) {
  try {
    const r = await fetch('/render?path=' + encodeURIComponent(path));
    const j = await r.json();
    if (!j.ok) { toast(j.error || 'No se pudo abrir'); return; }
    current.path = j.path; current.name = j.name; current.dir = j.dir;
    paintText(j, opts);
    watch(j.path);
    body.classList.add('live-on');
    await renderMath();   // KaTeX (lazy): lo esperamos para no revelar TeX crudo un instante
    if (opts.frag) scrollToAnchor(opts.frag); // saltar a #sección si vino de un enlace doc#sec
    renderMermaid();      // mermaid (lazy + pesado): que aparezca después, sin bloquear
  } catch (e) { window.__log('open ' + e); toast('Error al abrir'); }
}
window.__folioOpen = (p) => openPath(p);   // el host lo llama por Eval

// Arrastrar-y-soltar: render de texto crudo (sin ruta -> sin assets relativos ni recarga viva).
async function renderRawText(text, name) {
  try {
    const r = await fetch('/render-text?name=' + encodeURIComponent(name || 'documento.md'), {
      method: 'POST', headers: { 'Content-Type': 'text/plain; charset=utf-8' }, body: text,
    });
    const j = await r.json();
    if (!j.ok) { toast('No se pudo abrir'); return; }
    current.path = null; current.name = j.name; current.dir = '';
    if (es) { es.close(); es = null; }
    body.classList.remove('live-on');
    paintText(j, {});
    await renderMath();
    renderMermaid();
  } catch (e) { toast('Error al abrir'); }
}

// paintText inyecta el HTML y los realces SÍNCRONOS (instantáneos). KaTeX/mermaid van aparte
// (asíncronos, bajo demanda) para no demorar el primer pintado.
function paintText(j, opts) {
  const clean = DOMPurify.sanitize(j.html, { ADD_ATTR: ['target'], ALLOW_DATA_ATTR: true });
  content.innerHTML = clean;
  body.classList.add('has-doc');
  body.classList.remove('no-doc', 'empty');
  $('capName').textContent = j.title || j.name || '';
  addCopyButtons();
  addAnchors();
  collectHeadings();
  buildToc(j.toc || []);
  if (!opts.silent) { reader.scrollTop = 0; updateProgress(); }
  window.__log('painted ' + (j.name || ''));
}

// ---- carga perezosa de librerías pesadas --------------------------------
let katexP = null, mermaidP = null;
function loadScript(src) {
  return new Promise((resolve, reject) => {
    const s = document.createElement('script');
    s.src = src; s.async = true;
    s.onload = () => resolve();
    s.onerror = () => reject(new Error('no se pudo cargar ' + src));
    document.head.appendChild(s);
  });
}

// ---- KaTeX (bajo demanda) -----------------------------------------------
async function renderMath() {
  const els = content.querySelectorAll('.math:not([data-done])');
  if (!els.length) return;
  try { katexP = katexP || loadScript('/vendor/katex/katex.min.js'); await katexP; }
  catch (e) { window.__log('katex ' + e); return; }
  els.forEach((el) => {
    const display = el.classList.contains('math-display');
    try {
      katex.render(el.textContent, el, {
        displayMode: display, throwOnError: false, errorColor: '#f7768e', strict: 'ignore',
      });
      el.dataset.done = '1';
    } catch (e) { el.classList.add('katex-error'); }
  });
}

// ---- mermaid ------------------------------------------------------------
function initMermaid() {
  if (mermaidReady || !window.mermaid) return;
  mermaid.initialize({
    startOnLoad: false, securityLevel: 'loose', theme: 'base',
    fontFamily: '"Cascadia Code","Cascadia Mono",monospace',
    themeVariables: {
      darkMode: true, background: '#0c0e15',
      primaryColor: '#11151f', primaryBorderColor: '#7aa2f7', primaryTextColor: '#c8cdd9',
      secondaryColor: '#161b27', tertiaryColor: '#0c0e15',
      lineColor: '#565f89', textColor: '#c8cdd9', titleColor: '#e9ecf3',
      nodeBorder: '#7aa2f7', clusterBkg: '#0b0e14', clusterBorder: '#2a3147',
      edgeLabelBackground: '#11151f', fontSize: '14px',
      // notas / secuencia: que combinen con el dark (mermaid las pinta crema por defecto)
      noteBkgColor: '#1a2030', noteTextColor: '#c8cdd9', noteBorderColor: '#2a3147',
      actorBkg: '#11151f', actorBorder: '#7aa2f7', actorTextColor: '#c8cdd9', actorLineColor: '#3b4150',
      signalColor: '#8a92a6', signalTextColor: '#c8cdd9',
      labelBoxBkgColor: '#11151f', labelBoxBorderColor: '#2a3147', labelTextColor: '#c8cdd9',
      loopTextColor: '#c8cdd9', sequenceNumberColor: '#08090c',
    },
  });
  mermaidReady = true;
}
async function renderMermaid() {
  const nodes = [...content.querySelectorAll('pre.mermaid:not(.done)')];
  if (!nodes.length) return; // sin diagramas no cargamos los 3.2 MB de mermaid
  try { mermaidP = mermaidP || loadScript('/vendor/mermaid/mermaid.min.js'); await mermaidP; }
  catch (e) { window.__log('mermaid load ' + e); return; }
  initMermaid();
  try { await mermaid.run({ nodes, suppressErrors: true }); }
  catch (e) { window.__log('mermaid ' + e); }
  nodes.forEach((n) => n.classList.add('done'));
}

// ---- botones de copiar --------------------------------------------------
function addCopyButtons() {
  content.querySelectorAll('.codeblock').forEach((block) => {
    if (block.querySelector('.copy-btn')) return;
    const pre = block.querySelector('pre'); if (!pre) return;
    const btn = document.createElement('button');
    btn.className = 'copy-btn'; btn.tabIndex = -1;
    btn.innerHTML = '<svg width="11" height="11" viewBox="0 0 16 16" style="stroke:currentColor;stroke-width:1.4;fill:none;stroke-linejoin:round"><rect x="5" y="5" width="8.5" height="9.5" rx="1.6"/><path d="M11 5V3.6A1.6 1.6 0 0 0 9.4 2H4A1.6 1.6 0 0 0 2.5 3.6V11"/></svg><span>Copiar</span>';
    btn.addEventListener('click', async () => {
      const text = pre.innerText;
      try { await navigator.clipboard.writeText(text); }
      catch (e) {
        const r = document.createRange(); r.selectNodeContents(pre);
        const s = getSelection(); s.removeAllRanges(); s.addRange(r);
        try { document.execCommand('copy'); } catch (_) {}
        s.removeAllRanges();
      }
      btn.classList.add('done'); btn.querySelector('span').textContent = 'Copiado';
      setTimeout(() => { btn.classList.remove('done'); btn.querySelector('span').textContent = 'Copiar'; }, 1400);
    });
    block.appendChild(btn);
  });
}

// ---- anclas en encabezados ---------------------------------------------
function addAnchors() {
  content.querySelectorAll('h1[id],h2[id],h3[id],h4[id],h5[id],h6[id]').forEach((h) => {
    if (h.querySelector('.anchor')) return;
    const a = document.createElement('a');
    a.className = 'anchor'; a.href = '#' + h.id; a.textContent = '#';
    a.tabIndex = -1; a.setAttribute('aria-hidden', 'true');
    h.insertBefore(a, h.firstChild);
  });
}

// =========================================================================
// Indice (TOC) + scroll-spy
// =========================================================================
function buildToc(items) {
  tocInner.innerHTML = '';
  const usable = items.filter((it) => it.id);
  usable.forEach((it) => {
    const a = document.createElement('a');
    a.className = 'toc-link lvl-' + it.level;
    a.textContent = it.text || '—';
    a.href = '#' + it.id;
    a.dataset.target = it.id;
    a.tabIndex = -1;
    a.addEventListener('click', (e) => { e.preventDefault(); scrollToAnchor(it.id); });
    tocInner.appendChild(a);
  });
  const hasToc = usable.length > 0;
  $('btnOutline').classList.toggle('on', hasToc && tocOpen);
  $('btnOutline').style.opacity = hasToc ? '' : '.35';
  body.classList.toggle('no-toc', !tocOpen || !hasToc);
}

function collectHeadings() {
  headings = [...content.querySelectorAll('h1[id],h2[id],h3[id],h4[id],h5[id],h6[id]')];
}

let spyScheduled = false;
function scrollSpy() {
  if (spyScheduled) return;
  spyScheduled = true;
  requestAnimationFrame(() => {
    spyScheduled = false;
    if (!headings.length) return;
    const top = reader.scrollTop + 90;
    let activeId = headings[0].id;
    for (const h of headings) {
      if (h.offsetTop <= top) activeId = h.id; else break;
    }
    const links = tocInner.querySelectorAll('.toc-link');
    links.forEach((l) => {
      const on = l.dataset.target === activeId;
      l.classList.toggle('active', on);
      if (on) keepTocVisible(l);
    });
  });
}
function keepTocVisible(el) {
  const r = el.getBoundingClientRect(), t = toc.getBoundingClientRect();
  if (r.top < t.top + 40) toc.scrollTop -= (t.top + 40 - r.top);
  else if (r.bottom > t.bottom - 12) toc.scrollTop += (r.bottom - (t.bottom - 12));
}

function scrollToAnchor(id) {
  const el = document.getElementById(id);
  if (el) el.scrollIntoView({ block: 'start', behavior: 'smooth' });
}

// =========================================================================
// Enlaces (delegacion)
// =========================================================================
content.addEventListener('click', (e) => {
  const a = e.target.closest('a'); if (!a) return;
  const href = a.getAttribute('href') || '';
  if (a.dataset.external !== undefined || /^(https?:|mailto:|tel:|ftp:)/i.test(href)) {
    e.preventDefault(); bridge('folioOpenExternal', a.href || href); return;
  }
  if (a.dataset.doc) { e.preventDefault(); openPath(a.dataset.doc, { frag: a.dataset.frag || '' }); return; }
  if (a.dataset.open) { e.preventDefault(); bridge('folioOpenPath', a.dataset.open); return; }
  if (href.startsWith('#')) { e.preventDefault(); scrollToAnchor(href.slice(1)); }
});

// =========================================================================
// Recarga en vivo (SSE)
// =========================================================================
function watch(path) {
  if (es) { es.close(); es = null; }
  if (!path) return;
  try {
    es = new EventSource('/events?path=' + encodeURIComponent(path));
    es.onmessage = (ev) => { if (ev.data === 'reload') liveReload(); };
    es.onerror = () => {};
  } catch (e) {}
}
async function liveReload() {
  if (!current.path) return;
  const denom = Math.max(1, reader.scrollHeight - reader.clientHeight);
  const keep = reader.scrollTop / denom;
  try {
    const r = await fetch('/render?path=' + encodeURIComponent(current.path));
    const j = await r.json();
    if (!j.ok) return;
    paintText(j, { silent: true });
    await renderMath();
    renderMermaid();
    requestAnimationFrame(() => {
      reader.scrollTop = keep * Math.max(1, reader.scrollHeight - reader.clientHeight);
      updateProgress();
    });
    pulseLive();
  } catch (e) {}
}
function pulseLive() {
  const d = $('capLive'); d.classList.remove('pulse'); void d.offsetWidth; d.classList.add('pulse');
}

// =========================================================================
// Progreso de lectura
// =========================================================================
function updateProgress() {
  const denom = Math.max(1, reader.scrollHeight - reader.clientHeight);
  const p = Math.min(1, Math.max(0, reader.scrollTop / denom));
  $('progressBar').style.width = (p * 100) + '%';
}
reader.addEventListener('scroll', () => { updateProgress(); scrollSpy(); }, { passive: true });

// =========================================================================
// Busqueda (CSS Custom Highlight API)
// =========================================================================
const find = { matches: [], idx: -1 };
const supportsHL = !!(window.CSS && CSS.highlights && window.Highlight);

function openFind() {
  body.classList.add('find-open');
  const inp = $('findInput'); inp.focus(); inp.select();
  if (inp.value) runFind(inp.value);
}
function closeFind() {
  body.classList.remove('find-open');
  if (supportsHL) { CSS.highlights.delete('folio-find'); CSS.highlights.delete('folio-find-current'); }
  find.matches = []; find.idx = -1;
  $('findInput').blur();
}
function runFind(term) {
  if (!supportsHL) return;
  CSS.highlights.delete('folio-find'); CSS.highlights.delete('folio-find-current');
  find.matches = []; find.idx = -1;
  const q = term.trim().toLowerCase();
  if (!q) { updateFindCount(); return; }

  const walker = document.createTreeWalker(content, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      if (!node.nodeValue || !node.nodeValue.trim()) return NodeFilter.FILTER_REJECT;
      let p = node.parentElement;
      while (p && p !== content) {
        const tag = p.tagName;
        if (tag === 'STYLE' || tag === 'SCRIPT' || tag === 'svg' ||
            (p.classList && p.classList.contains('katex'))) return NodeFilter.FILTER_REJECT;
        p = p.parentElement;
      }
      return NodeFilter.FILTER_ACCEPT;
    },
  });
  const ranges = [];
  let node;
  while ((node = walker.nextNode())) {
    const hay = node.nodeValue.toLowerCase();
    let i = hay.indexOf(q);
    while (i >= 0) {
      const r = document.createRange();
      r.setStart(node, i); r.setEnd(node, i + q.length);
      ranges.push(r);
      i = hay.indexOf(q, i + q.length);
    }
  }
  find.matches = ranges;
  if (ranges.length) {
    const hl = new Highlight(...ranges); hl.priority = 1;
    CSS.highlights.set('folio-find', hl);
    find.idx = 0; markCurrent();
  }
  updateFindCount();
}
function markCurrent() {
  if (!supportsHL) return;
  CSS.highlights.delete('folio-find-current');
  const r = find.matches[find.idx];
  if (!r) return;
  const cur = new Highlight(r); cur.priority = 2;
  CSS.highlights.set('folio-find-current', cur);
  const el = r.startContainer.parentElement;
  if (el) el.scrollIntoView({ block: 'center', behavior: 'smooth' });
  updateFindCount();
}
function findStep(dir) {
  if (!find.matches.length) return;
  find.idx = (find.idx + dir + find.matches.length) % find.matches.length;
  markCurrent();
}
function updateFindCount() {
  const c = $('findCount');
  c.textContent = find.matches.length ? (find.idx + 1) + '/' + find.matches.length : (($('findInput').value ? '0/0' : ''));
}
$('findInput').addEventListener('input', (e) => runFind(e.target.value));
$('findInput').addEventListener('keydown', (e) => {
  if (e.key === 'Enter') { e.preventDefault(); findStep(e.shiftKey ? -1 : 1); }
  else if (e.key === 'Escape') { e.preventDefault(); closeFind(); }
});
$('findPrev').addEventListener('click', () => findStep(-1));
$('findNext').addEventListener('click', () => findStep(1));
$('findClose').addEventListener('click', closeFind);

// =========================================================================
// Zoom de lectura + persistencia (server-side; el puerto efímero rompe localStorage, ver config.go)
// =========================================================================
let rscale = (typeof window.__FOLIO_RSCALE__ === 'number' && window.__FOLIO_RSCALE__ > 0) ? window.__FOLIO_RSCALE__ : 1;

// ---- ancho del índice (TOC): arrastrable y persistido por el mismo canal que rscale/tocOpen ----
const TOC_DEFAULT = 268, TOC_MIN = 150;
const tocMax = () => Math.min(640, Math.max(TOC_MIN, window.innerWidth - 320)); // dejar aire al lector
const clampTocW = (w) => Math.round(Math.min(tocMax(), Math.max(TOC_MIN, w)));
let tocWidth = (typeof window.__FOLIO_TOCW__ === 'number' && window.__FOLIO_TOCW__ > 0) ? window.__FOLIO_TOCW__ : TOC_DEFAULT;
function applyTocWidth() { document.documentElement.style.setProperty('--toc-w', clampTocW(tocWidth) + 'px'); }
applyTocWidth();

// guardado con debounce: al hacer zoom rápido se mandan muchos cambios; sólo persistimos el valor
// final (evita una carrera de POSTs que dejaba un valor viejo). Flush en pagehide por las dudas.
let saveTimer = null;
function postSettings() {
  try {
    fetch('/api/settings', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ rscale, tocOpen, tocWidth }),
    });
  } catch (e) {}
}
function saveSettings() {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(() => { saveTimer = null; postSettings(); }, 180);
}
window.addEventListener('pagehide', () => {
  if (!saveTimer) return;
  clearTimeout(saveTimer); saveTimer = null;
  try { navigator.sendBeacon('/api/settings', JSON.stringify({ rscale, tocOpen, tocWidth })); } catch (e) { postSettings(); }
});
function applyScale() {
  rscale = Math.min(1.9, Math.max(0.7, rscale));
  document.documentElement.style.setProperty('--rscale', rscale.toFixed(3));
}
applyScale();

// =========================================================================
// Indice / pantalla completa
// =========================================================================
function toggleToc() {
  tocOpen = !tocOpen;
  saveSettings();
  const hasToc = tocInner.querySelector('.toc-link');
  body.classList.toggle('no-toc', !tocOpen || !hasToc);
  $('btnOutline').classList.toggle('on', tocOpen && !!hasToc);
}
let isFs = false;
function setFullscreen(on) {
  isFs = on; body.classList.toggle('fullscreen', on);
  bridge('folioFullscreen', on);
}

// =========================================================================
// Controles de ventana + arrastre/redimension frameless
// =========================================================================
$('btnMin').addEventListener('click', () => bridge('folioMin'));
$('btnClose').addEventListener('click', () => bridge('folioClose'));
$('btnMax').addEventListener('click', () => { bridge('folioMaxToggle'); body.classList.toggle('maximized'); });
$('btnOutline').addEventListener('click', toggleToc);
$('btnFind').addEventListener('click', openFind);
$('btnOpen').addEventListener('click', () => bridge('folioPick'));
$('emptyOpen').addEventListener('click', () => bridge('folioPick'));

let lastTbDown = 0;
$('titlebar').addEventListener('pointerdown', (e) => {
  if (e.button !== 0 || e.target.closest('.winbtn') || e.target.closest('.tbtn')) return;
  const now = Date.now();
  if (now - lastTbDown < 300) { lastTbDown = 0; bridge('folioMaxToggle'); body.classList.toggle('maximized'); return; }
  lastTbDown = now;
  bridge('folioDrag');
});
document.querySelectorAll('.rsz').forEach((el) => {
  el.addEventListener('pointerdown', (e) => { if (e.button === 0) bridge('folioResize', el.dataset.dir); });
});

// separador arrastrable del índice: ajusta --toc-w en vivo y persiste al soltar; doble clic restablece
(() => {
  const rz = $('tocResizer');
  if (!rz) return;
  let sx = 0, sw = 0, on = false;
  rz.addEventListener('pointerdown', (e) => {
    if (e.button !== 0) return;
    e.preventDefault();
    on = true; sx = e.clientX; sw = clampTocW(tocWidth);
    body.classList.add('toc-resizing');
    try { rz.setPointerCapture(e.pointerId); } catch (_) {}
  });
  rz.addEventListener('pointermove', (e) => {
    if (!on) return;
    tocWidth = clampTocW(sw + (e.clientX - sx));
    applyTocWidth();
  });
  const end = (e) => {
    if (!on) return;
    on = false; body.classList.remove('toc-resizing');
    try { rz.releasePointerCapture(e.pointerId); } catch (_) {}
    saveSettings();
  };
  rz.addEventListener('pointerup', end);
  rz.addEventListener('pointercancel', end);
  rz.addEventListener('dblclick', (e) => { e.preventDefault(); tocWidth = TOC_DEFAULT; applyTocWidth(); saveSettings(); });
})();

// =========================================================================
// Teclado
// =========================================================================
function typing() {
  const a = document.activeElement;
  return a && (a.tagName === 'INPUT' || a.tagName === 'TEXTAREA' || a.isContentEditable);
}
window.addEventListener('keydown', (e) => {
  if (e.ctrlKey || e.metaKey) {
    const k = e.key.toLowerCase();
    if (k === 'o') { e.preventDefault(); bridge('folioPick'); return; }
    if (k === 'f') { e.preventDefault(); openFind(); return; }
    if (k === '=' || k === '+') { e.preventDefault(); rscale += 0.08; applyScale(); saveSettings(); return; }
    if (k === '-' || k === '_') { e.preventDefault(); rscale -= 0.08; applyScale(); saveSettings(); return; }
    if (k === '0') { e.preventDefault(); rscale = 1; applyScale(); saveSettings(); return; }
    return;
  }
  if (typing()) return;

  switch (e.key) {
    case 't': case 'T': e.preventDefault(); toggleToc(); break;
    case 'f': case 'F': case 'F11': e.preventDefault(); setFullscreen(!isFs); break;
    case 'Escape': if (isFs) { e.preventDefault(); setFullscreen(false); } break;
    case 'g': e.preventDefault(); reader.scrollTo({ top: 0, behavior: 'smooth' }); break;
    case 'G': e.preventDefault(); reader.scrollTo({ top: reader.scrollHeight, behavior: 'smooth' }); break;
    case 'Home': e.preventDefault(); reader.scrollTo({ top: 0, behavior: 'smooth' }); break;
    case 'End': e.preventDefault(); reader.scrollTo({ top: reader.scrollHeight, behavior: 'smooth' }); break;
    case ' ': case 'PageDown': e.preventDefault(); reader.scrollBy({ top: reader.clientHeight * 0.86, behavior: 'smooth' }); break;
    case 'PageUp': e.preventDefault(); reader.scrollBy({ top: -reader.clientHeight * 0.86, behavior: 'smooth' }); break;
    case 'j': reader.scrollBy({ top: 90, behavior: 'smooth' }); break;
    case 'k': reader.scrollBy({ top: -90, behavior: 'smooth' }); break;
  }
});

// =========================================================================
// Arrastrar y soltar
// =========================================================================
const mdRe = /\.(md|markdown|mdown|mkd|mkdn|mdwn|mdtxt|mdtext|text|rmd|qmd|mdx)$/i;
window.addEventListener('dragover', (e) => { e.preventDefault(); body.classList.add('dragover'); });
window.addEventListener('dragleave', (e) => { if (!e.relatedTarget) body.classList.remove('dragover'); });
window.addEventListener('drop', async (e) => {
  e.preventDefault(); body.classList.remove('dragover');
  const f = e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files[0];
  if (!f) return;
  if (!mdRe.test(f.name) && !/text\/(markdown|plain)/.test(f.type)) { toast('No es un documento Markdown'); return; }
  try { renderRawText(await f.text(), f.name); } catch (_) { toast('No se pudo leer'); }
});

// =========================================================================
// Varios
// =========================================================================
let toastTimer;
function toast(msg) {
  const t = $('toast'); t.textContent = msg; t.classList.add('show');
  clearTimeout(toastTimer); toastTimer = setTimeout(() => t.classList.remove('show'), 2400);
}
window.addEventListener('contextmenu', (e) => { if (!typing() && !window.getSelection().toString()) e.preventDefault(); });
window.addEventListener('resize', () => {
  body.classList.toggle('maximized', !isFs && window.innerWidth >= screen.availWidth - 6);
  updateProgress();
  applyTocWidth(); // re-clampear el ancho del índice si la ventana se achicó
});
// Zoom con Ctrl+rueda -> ajusta el tamaño de lectura (persistido); preventDefault corta el zoom
// nativo de WebView2 (que no se recuerda al cerrar).
window.addEventListener('wheel', (e) => {
  if (!e.ctrlKey) return;
  e.preventDefault();
  rscale = Math.min(1.9, Math.max(0.7, rscale + (e.deltaY < 0 ? 0.07 : -0.07)));
  applyScale(); saveSettings();
}, { passive: false });

// =========================================================================
// Arranque — la ventana se muestra enseguida (con el SPLASH); el contenido se renderiza por debajo
// y, cuando está listo, el splash se funde dejándolo ver. Avisamos al host por load/timeout, NUNCA
// por rAF (con la ventana aún oculta el navegador PAUSA rAF y se colgaría el aviso).
// =========================================================================
let readySent = false;
function sendReady(why) {
  if (readySent) return; readySent = true;
  window.__log('ready via ' + why);
  bridge('folioReady');
}
let revealed = false;
function reveal() {                    // funde el splash y deja ver el documento ya pintado
  if (revealed) return; revealed = true;
  body.classList.add('ready');
}
function boot() {
  if (document.readyState === 'complete') sendReady('load');
  else window.addEventListener('load', () => sendReady('load'));
  setTimeout(() => sendReady('timeout'), 400);
  setTimeout(reveal, 4000); // rescate: si el render se cuelga, revelar igual
  // canal del daemon caliente: al reabrir, el host nos avisa por acá qué .md mostrar
  try {
    const oe = new EventSource('/openevents');
    oe.onmessage = (ev) => { if (ev.data && ev.data !== current.path) { reveal(); openPath(ev.data); } };
  } catch (e) {}
  fetch('/api/initial').then((r) => r.json()).then(async (j) => {
    if (j && j.path) await openPath(j.path); // texto + KaTeX antes de fundir el splash
    reveal();
  }).catch(() => reveal());
}
boot();
