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

window.__log = (m) => { if (!window.__FOLIO_DEBUG__) return; try { fetch('/log?m=' + encodeURIComponent(m)); } catch (e) { /* sin host */ } };
window.addEventListener('error', (e) => window.__log('ERR ' + e.message + ' @' + (e.filename || '') + ':' + e.lineno));
window.addEventListener('unhandledrejection', (e) => window.__log('REJECT ' + (e.reason && (e.reason.message || e.reason))));

function bridge(name, ...args) {
  try { if (typeof window[name] === 'function') return window[name](...args); }
  catch (e) { window.__log('bridge ' + name + ' ' + e); }
}

// ---- estado -------------------------------------------------------------
const current = { path: null, name: '', dir: '', html: '' };
let es = null;               // EventSource de recarga en vivo
let mermaidReady = false;
let tocOpen = window.__FOLIO_TOC__ !== false;   // inyectado por el host desde el config
let openSeq = 0;             // cada apertura se numera: una respuesta vieja no pisa a una nueva

// DOMPurify antepone este prefijo a TODOS los id/name (SANITIZE_NAMED_PROPS). Con la
// proteccion anti-clobbering por defecto, en cambio, BORRABA los id que coinciden con una
// propiedad de document: un "## Title", "## Name", "## Location" o "## Links" se quedaba sin
// id y el indice no podia saltar ahi. Con el prefijo no hay nada que clobbear.
const ID_PREFIX = 'user-content-';
const PURIFY = { ADD_ATTR: ['target'], ALLOW_DATA_ATTR: true, SANITIZE_DOM: false, SANITIZE_NAMED_PROPS: true };
const plainId = (id) => (id && id.startsWith(ID_PREFIX) ? id.slice(ID_PREFIX.length) : id);

// =========================================================================
// Apertura / render
// =========================================================================
async function openPath(path, opts = {}) {
  const seq = ++openSeq;
  try {
    const r = await fetch('/render?path=' + encodeURIComponent(path));
    const j = await r.json();
    if (seq !== openSeq) return;           // mientras tanto se pidio otro documento
    if (!j.ok) { toast(j.error || 'No se pudo abrir'); return; }
    current.path = j.path; current.name = j.name; current.dir = j.dir;
    paintText(j, opts);
    watch(j.path);
    body.classList.add('live-on');
    await renderMath();   // KaTeX (lazy): lo esperamos para no revelar TeX crudo un instante
    if (seq !== openSeq) return;
    if (opts.frag) scrollToAnchor(opts.frag, 'auto'); // saltar a #sección si vino de un doc#sec
    renderMermaid();      // mermaid (lazy + pesado): que aparezca después, sin bloquear
  } catch (e) { window.__log('open ' + e); toast('Error al abrir'); }
}
window.__folioOpen = (p) => openPath(p);   // el host lo llama por Eval

// Arrastrar-y-soltar: render del archivo crudo (sin ruta -> sin assets relativos ni recarga
// viva). Se mandan los bytes tal cual, asi entran tambien los binarios (.docx, .odt, .epub).
async function renderRawText(bytes, name) {
  const seq = ++openSeq;
  try {
    const r = await fetch('/render-text?name=' + encodeURIComponent(name || 'documento.md'), {
      method: 'POST', headers: { 'Content-Type': 'application/octet-stream' }, body: bytes,
    });
    const j = await r.json();
    if (seq !== openSeq) return;
    if (!j.ok) { toast('No se pudo abrir'); return; }
    current.path = null; current.name = j.name; current.dir = '';
    if (es) { es.close(); es = null; }
    body.classList.remove('live-on');
    paintText(j, {});
    await renderMath();
    renderMermaid();
  } catch (e) { window.__log('drop ' + e); toast('Error al abrir'); }
}

// paintText inyecta el HTML y los realces SÍNCRONOS (instantáneos). KaTeX/mermaid van aparte
// (asíncronos, bajo demanda) para no demorar el primer pintado.
function paintText(j, opts) {
  current.html = j.html;
  content.innerHTML = DOMPurify.sanitize(j.html, PURIFY);
  textIndex = null;                       // la busqueda se reindexa en la proxima consulta
  body.classList.add('has-doc');
  body.classList.remove('no-doc', 'empty');
  const cap = $('capName');
  cap.textContent = j.title || j.name || '';
  // al pasar el mouse por el título: archivo, formato de origen y palabras
  cap.title = [j.name, j.format, j.words ? j.words.toLocaleString('es-AR') + ' palabras' : '']
    .filter(Boolean).join('   ·   ');
  addCopyButtons();
  addAnchors();
  collectHeadings();
  buildToc(j.toc || []);
  if (!opts.silent) { reader.scrollTop = 0; updateProgress(); }
  if (body.classList.contains('find-open') && $('findInput').value) runFind($('findInput').value);
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

// Memo acotado (LRU por orden de insercion de Map): la recarga en vivo re-pinta el documento
// entero en cada guardado, y re-tipografiar cien formulas o re-dibujar cinco diagramas por
// cada tecla guardada era la parte cara. Lo que no cambio sale del memo.
function lruMemo(limit) {
  const m = new Map();
  return {
    get(k) { const v = m.get(k); if (v !== undefined) { m.delete(k); m.set(k, v); } return v; },
    set(k, v) { m.set(k, v); if (m.size > limit) m.delete(m.keys().next().value); },
  };
}
const mathMemo = lruMemo(4000);
const mermaidMemo = lruMemo(200);

// ---- KaTeX (bajo demanda) -----------------------------------------------
async function renderMath() {
  const els = content.querySelectorAll('.math:not([data-done])');
  if (!els.length) return;
  const pending = [];
  for (const el of els) {                  // primero lo que ya esta en el memo: instantaneo
    const key = (el.classList.contains('math-display') ? 'D' : 'I') + el.textContent;
    const hit = mathMemo.get(key);
    if (hit !== undefined) { el.innerHTML = hit; el.dataset.done = '1'; }
    else pending.push([el, key]);
  }
  if (!pending.length) return;
  try { katexP = katexP || loadScript('/vendor/katex/katex.min.js'); await katexP; }
  catch (e) { window.__log('katex ' + e); return; }
  for (const [el, key] of pending) {
    try {
      katex.render(el.textContent, el, {
        displayMode: key[0] === 'D', throwOnError: false, errorColor: '#f7768e', strict: 'ignore',
      });
      el.dataset.done = '1';
      mathMemo.set(key, el.innerHTML);
    } catch (e) { el.classList.add('katex-error'); }
  }
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
  const nodes = [];
  for (const n of content.querySelectorAll('pre.mermaid:not(.done)')) {
    const src = n.textContent;
    const hit = mermaidMemo.get(src);
    if (hit !== undefined) { n.innerHTML = hit; n.classList.add('done'); continue; }
    n.dataset.src = src;
    nodes.push(n);
  }
  if (!nodes.length) return; // sin diagramas nuevos no se cargan los 3.2 MB de mermaid
  try { mermaidP = mermaidP || loadScript('/vendor/mermaid/mermaid.min.js'); await mermaidP; }
  catch (e) { window.__log('mermaid load ' + e); return; }
  initMermaid();
  try { await mermaid.run({ nodes, suppressErrors: true }); }
  catch (e) { window.__log('mermaid ' + e); }
  for (const n of nodes) {
    n.classList.add('done');
    if (n.querySelector('svg')) mermaidMemo.set(n.dataset.src, n.innerHTML);
  }
  spy.dirty = true;                        // los diagramas cambian la altura del documento
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
      await copyText(pre.innerText, pre);
      btn.classList.add('done'); btn.querySelector('span').textContent = 'Copiado';
      setTimeout(() => { btn.classList.remove('done'); btn.querySelector('span').textContent = 'Copiar'; }, 1400);
    });
    block.appendChild(btn);
  });
}
async function copyText(text, fallbackNode) {
  try { await navigator.clipboard.writeText(text); return; }
  catch (e) { window.__log('clipboard ' + e); }
  const r = document.createRange(); r.selectNodeContents(fallbackNode);
  const s = getSelection(); s.removeAllRanges(); s.addRange(r);
  try { document.execCommand('copy'); } catch (e) { window.__log('execCommand ' + e); }
  s.removeAllRanges();
}

// ---- anclas en encabezados ---------------------------------------------
const HEADINGS = 'h1[id],h2[id],h3[id],h4[id],h5[id],h6[id]';
function addAnchors() {
  content.querySelectorAll(HEADINGS).forEach((h) => {
    if (h.querySelector('.anchor')) return;
    const a = document.createElement('a');
    a.className = 'anchor'; a.href = '#' + plainId(h.id); a.textContent = '#';
    a.tabIndex = -1; a.setAttribute('aria-hidden', 'true');
    h.insertBefore(a, h.firstChild);
  });
}

// =========================================================================
// Anclas: #fragmento -> elemento. Por id (con y sin el prefijo de DOMPurify), por slug estilo
// GitHub (lo que genera el server) y, al final, por el TEXTO del encabezado sin tildes ni
// mayusculas: un [[Nota#Mi Sección]] de Obsidian apunta al titulo, no a su id.
// =========================================================================
const slug = (s) => s.trim().toLowerCase().replace(/[^\p{L}\p{N}\p{M}_\- ]/gu, '').replace(/ /g, '-');
const foldPlain = (s) => s.normalize('NFD').replace(/\p{M}/gu, '').toLowerCase().replace(/\s+/g, ' ').trim();

function anchorTarget(frag) {
  if (!frag) return null;
  let raw = frag;
  try { raw = decodeURIComponent(frag); } catch (e) { /* ya venia decodificado */ }
  const byId = (id) => id && (document.getElementById(ID_PREFIX + id) || document.getElementById(id));
  const hit = byId(raw) || byId(slug(raw));
  if (hit) return hit;
  const want = foldPlain(raw);
  return spy.headings.find((h) => foldPlain(h.textContent.replace(/^#/, '')) === want) || null;
}

function scrollToAnchor(frag, behavior = 'smooth') {
  const el = anchorTarget(frag);
  if (el) el.scrollIntoView({ block: 'start', behavior });
  return !!el;
}

// =========================================================================
// Indice (TOC) + scroll-spy
//
// Las posiciones de los encabezados se miden UNA vez y se cachean; un ResizeObserver las
// invalida cuando algo cambia la altura (imagenes que terminan de cargar, formulas, diagramas,
// zoom, ancho de ventana). Cada frame de scroll hace una busqueda binaria (O(log n)) y toca el
// DOM solo si cambio la seccion activa (O(1)); antes eran n lecturas de offsetTop + n toggles
// por frame, que en un documento con cientos de titulos se notaba al scrollear.
// =========================================================================
const spy = { headings: [], tops: null, dirty: true, links: new Map(), active: null };

function buildToc(items) {
  tocInner.textContent = '';
  spy.links = new Map(); spy.active = null;
  const frag = document.createDocumentFragment();
  for (const it of items) {
    if (!it.id) continue;
    const a = document.createElement('a');
    a.className = 'toc-link lvl-' + it.level;
    a.textContent = it.text || '—';
    a.href = '#' + it.id;
    a.tabIndex = -1;
    a.addEventListener('click', (e) => { e.preventDefault(); scrollToAnchor(it.id); });
    frag.appendChild(a);
    spy.links.set(it.id, a);
  }
  tocInner.appendChild(frag);
  const hasToc = spy.links.size > 0;
  $('btnOutline').classList.toggle('on', hasToc && tocOpen);
  $('btnOutline').style.opacity = hasToc ? '' : '.35';
  body.classList.toggle('no-toc', !tocOpen || !hasToc);
  scrollSpy();
}

function collectHeadings() {
  spy.headings = [...content.querySelectorAll(HEADINGS)];
  spy.dirty = true;
}
new ResizeObserver(() => { spy.dirty = true; scrollSpy(); }).observe(content);

function measureHeadings() {
  const n = spy.headings.length;
  spy.tops = new Float64Array(n);
  for (let i = 0; i < n; i++) spy.tops[i] = spy.headings[i].offsetTop;
  spy.dirty = false;
}

// ultimo i con tops[i] <= y (busqueda binaria); -1 si ninguno
function lastAtOrAbove(tops, y) {
  let lo = 0, hi = tops.length - 1, ans = -1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    if (tops[mid] <= y) { ans = mid; lo = mid + 1; } else hi = mid - 1;
  }
  return ans;
}

const SPY_OFFSET = 90;   // px bajo el borde de arriba a partir de los cuales una seccion cuenta
let spyScheduled = false;
function scrollSpy() {
  if (spyScheduled) return;
  spyScheduled = true;
  requestAnimationFrame(() => {
    spyScheduled = false;
    if (!spy.headings.length) return;
    if (spy.dirty) measureHeadings();
    const i = Math.max(0, lastAtOrAbove(spy.tops, reader.scrollTop + SPY_OFFSET));
    const id = plainId(spy.headings[i].id);
    if (id === spy.active) return;
    const prev = spy.links.get(spy.active);
    if (prev) prev.classList.remove('active');
    spy.active = id;
    const link = spy.links.get(id);
    if (link) { link.classList.add('active'); keepTocVisible(link); }
  });
}
function keepTocVisible(el) {
  const r = el.getBoundingClientRect(), t = toc.getBoundingClientRect();
  if (r.top < t.top + 40) toc.scrollTop -= (t.top + 40 - r.top);
  else if (r.bottom > t.bottom - 12) toc.scrollTop += (r.bottom - (t.bottom - 12));
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
  } catch (e) { window.__log('watch ' + e); }
}

// ancla de lectura: el primer bloque visible arriba, su firma (texto) y cuanto de el ya se
// scrolleo. Despues del re-pintado se vuelve AL MISMO bloque. Con la fraccion de scroll de antes
// (scrollTop / altura), escribir al final del documento desplazaba la vista del que leia arriba.
function readingAnchor() {
  const kids = content.children;
  const y = reader.scrollTop;
  let lo = 0, hi = kids.length - 1, idx = -1;
  while (lo <= hi) {                        // ultimo hijo cuyo borde de arriba ya paso
    const mid = (lo + hi) >> 1;
    if (kids[mid].offsetTop <= y) { idx = mid; lo = mid + 1; } else hi = mid - 1;
  }
  if (idx < 0) return { idx: -1, delta: y, sig: '', frac: 0 };
  const el = kids[idx];
  const denom = Math.max(1, reader.scrollHeight - reader.clientHeight);
  return { idx, delta: y - el.offsetTop, sig: signature(el), frac: y / denom };
}
const signature = (el) => el.tagName + ':' + (el.textContent || '').slice(0, 120);

const MAX_ANCHOR_DRIFT = 60;   // bloques de distancia en los que se busca el ancla movida
function restoreAnchor(a) {
  const kids = content.children;
  let top;
  if (a.idx < 0) top = a.delta;
  else {
    let target = kids[a.idx] && signature(kids[a.idx]) === a.sig ? kids[a.idx] : null;
    for (let d = 1; !target && d <= MAX_ANCHOR_DRIFT; d++) {   // el bloque se movio: el mas cercano con su firma
      for (const k of [a.idx - d, a.idx + d]) {
        if (kids[k] && signature(kids[k]) === a.sig) { target = kids[k]; break; }
      }
    }
    top = target ? target.offsetTop + a.delta : a.frac * Math.max(1, reader.scrollHeight - reader.clientHeight);
  }
  reader.scrollTo({ top, behavior: 'instant' });   // sin animar: la vista no tiene que "viajar"
}

async function liveReload() {
  if (!current.path) return;
  const seq = openSeq, path = current.path;
  try {
    const r = await fetch('/render?path=' + encodeURIComponent(path));
    const j = await r.json();
    if (!j.ok || seq !== openSeq || path !== current.path) return;
    if (j.html === current.html) return;    // se toco la fecha pero no el contenido: nada que hacer
    const anchor = readingAnchor();
    paintText(j, { silent: true });
    await renderMath();                      // las formulas cambian alturas: antes de restaurar
    restoreAnchor(anchor);
    const settled = reader.scrollTop;
    // los diagramas llegan despues y corren todo: se re-ancla, salvo que el usuario ya se movio
    renderMermaid().then(() => { if (seq === openSeq && reader.scrollTop === settled) restoreAnchor(anchor); });
    updateProgress();
    pulseLive();
  } catch (e) { window.__log('reload ' + e); }
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
// Busqueda (CSS Custom Highlight API) sobre un INDICE DE TEXTO del documento
//
// El indice es una sola cadena "plegada" (minusculas, sin tildes, blancos colapsados) con el
// texto de todos los nodos, mas el mapa de vuelta a (nodo, offset). Se arma UNA vez por render,
// en O(n); cada consulta es un indexOf sobre esa cadena, sin recorrer el DOM. Tres cosas que
// antes no andaban:
//   - frases que cruzan nodos: "**Folio** es" o un enlace en el medio ya no cortan la busqueda;
//   - tildes y mayusculas: "accion" encuentra "Acción" (como el Ctrl+F de Chrome);
//   - los bloques no se pegan: entre parrafos va un separador que ninguna consulta cruza.
// =========================================================================
const find = { matches: [], idx: -1 };
const supportsHL = !!(window.CSS && CSS.highlights && window.Highlight);
const MAX_HITS = 5000;                      // el Highlight API sufre con decenas de miles
const SKIP_TAGS = new Set(['STYLE', 'SCRIPT', 'svg', 'BUTTON', 'NOSCRIPT']);
const SKIP_CLASSES = ['katex', 'copy-btn', 'anchor', 'mermaid'];
const BLOCKS = 'p,li,h1,h2,h3,h4,h5,h6,td,th,pre,blockquote,dd,dt,summary,figcaption,.alert-title,.callout-title,div';
let textIndex = null;

// foldChar: una "letra" plegada (cache por caracter: el texto real repite poquisimos distintos)
const foldCache = new Map();
function foldChar(ch) {
  let f = foldCache.get(ch);
  if (f === undefined) {
    f = ch.normalize('NFD').replace(/\p{M}/gu, '').toLowerCase();
    if (/\s/.test(f)) f = ' ';
    foldCache.set(ch, f);
  }
  return f;
}

function buildTextIndex() {
  const nodes = [], nodeStart = [];         // texto de cada nodo y su offset en el texto ORIGINAL
  const out = [];                           // pedazos de la cadena plegada
  const oStart = [], oEnd = [];             // por caracter plegado: offsets originales globales
  let orig = 0, lastBlock = null, lastSpace = true;
  const walker = document.createTreeWalker(content, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT, {
    acceptNode(n) {
      if (n.nodeType === 1) {                // un elemento: se salta su subarbol ENTERO si no aplica
        if (SKIP_TAGS.has(n.tagName) || SKIP_CLASSES.some((c) => n.classList.contains(c))) return NodeFilter.FILTER_REJECT;
        return NodeFilter.FILTER_SKIP;
      }
      return n.nodeValue ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_REJECT;
    },
  });
  let n;
  while ((n = walker.nextNode())) {
    const block = n.parentElement.closest(BLOCKS);
    if (block !== lastBlock) {               // separador de bloque: ninguna consulta lo cruza
      out.push('\n'); oStart.push(orig); oEnd.push(orig);
      lastBlock = block; lastSpace = true;
    }
    const v = n.nodeValue;
    nodes.push(n); nodeStart.push(orig);
    for (let i = 0; i < v.length;) {
      const from = i;
      const code = v.charCodeAt(i);
      let f;
      if (code < 128) {                      // camino rapido: ASCII
        f = code >= 65 && code <= 90 ? String.fromCharCode(code + 32)
          : (code === 32 || (code >= 9 && code <= 13)) ? ' ' : v[i];
        i++;
      } else {
        const units = v.codePointAt(i) > 0xffff ? 2 : 1;   // un emoji son DOS unidades UTF-16
        f = foldChar(v.slice(i, i + units));
        i += units;
      }
      if (f === ' ') {
        if (lastSpace) continue;            // blancos colapsados, como los muestra el navegador
        lastSpace = true;
      } else lastSpace = false;
      // una entrada POR UNIDAD UTF-16 del plegado: indexOf cuenta unidades, no caracteres
      for (let k = 0; k < f.length; k++) { out.push(f[k]); oStart.push(orig + from); oEnd.push(orig + i); }
    }
    orig += v.length;
  }
  textIndex = {
    text: out.join(''), nodes, nodeStart: Int32Array.from(nodeStart),
    oStart: Int32Array.from(oStart), oEnd: Int32Array.from(oEnd),
  };
}

// offset original global -> (nodo, offset local): busqueda binaria sobre nodeStart
function locate(pos, preferEnd) {
  const s = textIndex.nodeStart;
  let lo = 0, hi = s.length - 1, ans = 0;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    if (s[mid] < pos || (!preferEnd && s[mid] === pos)) { ans = mid; lo = mid + 1; } else hi = mid - 1;
  }
  return [textIndex.nodes[ans], pos - s[ans]];
}

function foldQuery(q) {
  let out = '', space = true;
  for (const ch of q) {
    const f = ch.charCodeAt(0) < 128 ? (/\s/.test(ch) ? ' ' : ch.toLowerCase()) : foldChar(ch);
    if (f === ' ') { if (space) continue; space = true; } else space = false;
    out += f;
  }
  return out.trim();
}

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
  const q = foldQuery(term);
  if (!q) { updateFindCount(); return; }
  if (!textIndex) buildTextIndex();
  const { text, oStart, oEnd } = textIndex;
  const ranges = [];
  for (let i = text.indexOf(q); i >= 0 && ranges.length < MAX_HITS; i = text.indexOf(q, i + q.length)) {
    const [sn, so] = locate(oStart[i], false);
    const [en, eo] = locate(oEnd[i + q.length - 1], true);
    const r = document.createRange();
    try { r.setStart(sn, so); r.setEnd(en, eo); ranges.push(r); }
    catch (e) { window.__log('range ' + e); }
  }
  find.matches = ranges;
  if (ranges.length) {
    const hl = new Highlight(...ranges); hl.priority = 1;
    CSS.highlights.set('folio-find', hl);
    // arrancar por la primera coincidencia DESDE donde se esta leyendo, no desde el principio
    const top = reader.getBoundingClientRect().top;
    const first = ranges.findIndex((r) => r.getBoundingClientRect().bottom >= top);
    find.idx = first >= 0 ? first : 0;
    markCurrent();
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
  // mover la vista SOLO si la coincidencia no esta ya comoda a la vista
  const rr = r.getBoundingClientRect(), vr = reader.getBoundingClientRect();
  const margin = vr.height * 0.12;
  if (rr.top < vr.top + margin || rr.bottom > vr.bottom - margin) {
    reader.scrollTo({ top: reader.scrollTop + (rr.top - vr.top) - vr.height / 2 + rr.height / 2, behavior: 'smooth' });
  }
  updateFindCount();
}
function findStep(dir) {
  if (!find.matches.length) return;
  find.idx = (find.idx + dir + find.matches.length) % find.matches.length;
  markCurrent();
}
function updateFindCount() {
  const c = $('findCount');
  const n = find.matches.length;
  c.textContent = n ? (find.idx + 1) + '/' + (n >= MAX_HITS ? MAX_HITS + '+' : n) : ($('findInput').value ? '0/0' : '');
}
let findFrame = 0;
$('findInput').addEventListener('input', (e) => {    // una rafaga de teclas = una busqueda por frame
  cancelAnimationFrame(findFrame);
  findFrame = requestAnimationFrame(() => runFind(e.target.value));
});
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
const RSCALE_MIN = 0.7, RSCALE_MAX = 1.9, RSCALE_STEP_KEY = 0.08, RSCALE_STEP_WHEEL = 0.07;

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
  fetch('/api/settings', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ rscale, tocOpen, tocWidth }),
  }).catch((e) => window.__log('settings ' + e));
}
function saveSettings() {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(() => { saveTimer = null; postSettings(); }, 180);
}
window.addEventListener('pagehide', () => {
  if (!saveTimer) return;
  clearTimeout(saveTimer); saveTimer = null;
  if (!navigator.sendBeacon('/api/settings', JSON.stringify({ rscale, tocOpen, tocWidth }))) postSettings();
});
// ---- escala tipografica --------------------------------------------------------------------
// Dos reglas, las dos sobre el TAMAÑO FINAL de render (el que queda despues de aplicar el zoom
// de lectura), nunca sobre el rol del texto:
//
//  1. TAMAÑO -> cada cuerpo aterriza en un numero ENTERO de pixeles FISICOS. En un monitor al
//     150 %, 13 px CSS son 19,5 px reales: ese medio pixel el rasterizador lo tiene que repartir
//     y el trazo sale blando. Redondeando, el texto apoya en la grilla del panel.
//
//  2. PESO -> cuanto mas chica queda la letra, mas cuerpo necesita. Por debajo de ~6 pt el trazo
//     de una Light mide menos de un pixel; como no se puede pintar medio pixel de tinta, el
//     motor lo pinta gris sucio: no se ve delgado, se ve BORROSO. Un escalon mas de peso lo
//     arregla sin engordar nada de lo que ya se lee bien.
//
// El peso solo puede SUBIR respecto del que pide el diseño: adelgazar los titulos grandes seria
// un cambio de diseño, no un arreglo de nitidez.

const PT = 0.75;            // 1 px CSS = 0,75 pt
const PISO_PX = 5 / PT;     // piso de 5 pt: mas chico que eso no hay cara que lo salve
const BASE_PX = 13;         // cuerpo de referencia del diseño (zoom 1)

// Los unicos pesos que entregan una cara REAL de Cascadia en este motor; verificado contra el
// render, no contra lo que declara la fuente. OJO: el 500 NO existe -> se resuelve a SemiBold.
const PESOS = [200, 300, 350, 400, 600, 700];

const escalon = (px) => { const pt = px * PT; return pt >= 10 ? 0 : pt >= 7.5 ? 1 : pt >= 6 ? 2 : 3; };
const ESC_REF = escalon(BASE_PX);   // el escalon para el que el diseño eligio sus pesos

function pesoFinal(px, diseno) {
  const i = PESOS.indexOf(diseno);
  return PESOS[Math.min(PESOS.length - 1, i + Math.max(0, escalon(px) - ESC_REF))];
}

// [variable, multiplo del cuerpo, peso de diseño]
const ESCALA = [
  ['body',    1.00, 300],
  ['strong',  1.00, 600],
  ['mid',     1.00, 400],
  ['h1',      1.95, 600],
  ['h2',      1.50, 600],
  ['h3',      1.24, 400],
  ['h4',      1.06, 400],
  ['h5',      0.95, 400],
  ['h6',      0.84, 400],
  ['code',    0.86, 300],
  ['codein',  0.88, 300],
  ['table',   0.92, 400],
  ['note',    0.88, 300],
  ['alert',   0.94, 400],
  ['kbd',     0.78, 300],
  ['tiny',    0.74, 300],
  ['callout', 0.74, 600],
];

// El indice no acompaña el zoom de lectura (es cromo), pero igual tiene que caer en pixel entero.
const ESCALA_TOC = [['toc', 12], ['toc-s', 11.5], ['toc-xs', 11]];

// Resto de medidas FIJAS del cromo (barra de titulo, buscador, estado vacio, chips). Se exponen
// como --px-11_5 y compañia, ya redondeadas, para que toda la app apoye en la misma grilla.
const CROMO = [10, 10.5, 11, 11.5, 12, 12.5, 13, 14];

let scaledFor = null;       // "rscale@dpr" ya aplicado: re-aplicar lo mismo es trabajo tirado
function applyScale() {
  rscale = Math.min(RSCALE_MAX, Math.max(RSCALE_MIN, rscale));
  const dpr = window.devicePixelRatio || 1;
  const key = rscale.toFixed(3) + '@' + dpr;
  if (key === scaledFor) return;
  scaledFor = key;
  const alPixel = (px) => Math.max(1, Math.round(Math.max(px, PISO_PX) * dpr)) / dpr;
  const raiz = document.documentElement.style;

  raiz.setProperty('--rscale', rscale.toFixed(3));
  for (const [nombre, mult, diseno] of ESCALA) {
    const px = alPixel(BASE_PX * rscale * mult);
    raiz.setProperty('--fs-' + nombre, px.toFixed(4) + 'px');
    raiz.setProperty('--w-' + nombre, String(pesoFinal(px, diseno)));
  }
  for (const [nombre, px] of ESCALA_TOC) raiz.setProperty('--fs-' + nombre, alPixel(px).toFixed(4) + 'px');
  for (const px of CROMO) raiz.setProperty('--px-' + String(px).replace('.', '_'), alPixel(px).toFixed(4) + 'px');
  spy.dirty = true;
}
applyScale();

// El DPI puede cambiar sin que haya resize (arrastrar la ventana a un monitor con otra escala):
// hay que volver a redondear ahi tambien, o el texto queda apoyado en la grilla del monitor viejo.
let mqDpr = null;
function watchDpr() {
  if (mqDpr) mqDpr.removeEventListener('change', onDprChange);
  mqDpr = window.matchMedia(`(resolution: ${window.devicePixelRatio}dppx)`);
  mqDpr.addEventListener('change', onDprChange);
}
function onDprChange() { applyScale(); watchDpr(); }
watchDpr();

function zoomBy(delta) {
  rscale = Math.min(RSCALE_MAX, Math.max(RSCALE_MIN, rscale + delta));
  applyScale(); saveSettings();
}

// =========================================================================
// Indice / pantalla completa
// =========================================================================
function toggleToc() {
  tocOpen = !tocOpen;
  saveSettings();
  const hasToc = spy.links.size > 0;
  body.classList.toggle('no-toc', !tocOpen || !hasToc);
  $('btnOutline').classList.toggle('on', tocOpen && hasToc);
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

const DBLCLICK_MS = 300;
let lastTbDown = 0;
$('titlebar').addEventListener('pointerdown', (e) => {
  if (e.button !== 0 || e.target.closest('.winbtn') || e.target.closest('.tbtn')) return;
  const now = Date.now();
  if (now - lastTbDown < DBLCLICK_MS) { lastTbDown = 0; bridge('folioMaxToggle'); body.classList.toggle('maximized'); return; }
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
    rz.setPointerCapture(e.pointerId);
  });
  rz.addEventListener('pointermove', (e) => {
    if (!on) return;
    tocWidth = clampTocW(sw + (e.clientX - sx));
    applyTocWidth();
  });
  const end = (e) => {
    if (!on) return;
    on = false; body.classList.remove('toc-resizing');
    if (rz.hasPointerCapture(e.pointerId)) rz.releasePointerCapture(e.pointerId);
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
const SCROLL_PAGE = 0.86, SCROLL_LINE = 90;
window.addEventListener('keydown', (e) => {
  if (e.ctrlKey || e.metaKey) {
    const k = e.key.toLowerCase();
    if (k === 'o') { e.preventDefault(); bridge('folioPick'); return; }
    if (k === 'f') { e.preventDefault(); openFind(); return; }
    if (k === '=' || k === '+') { e.preventDefault(); zoomBy(RSCALE_STEP_KEY); return; }
    if (k === '-' || k === '_') { e.preventDefault(); zoomBy(-RSCALE_STEP_KEY); return; }
    if (k === '0') { e.preventDefault(); rscale = 1; applyScale(); saveSettings(); return; }
    return;
  }
  if (typing()) return;

  switch (e.key) {
    case 't': case 'T': e.preventDefault(); toggleToc(); break;
    case 'f': case 'F': case 'F11': e.preventDefault(); setFullscreen(!isFs); break;
    case '/': e.preventDefault(); openFind(); break;
    case 'Escape': if (isFs) { e.preventDefault(); setFullscreen(false); } break;
    case 'g': case 'Home': e.preventDefault(); reader.scrollTo({ top: 0, behavior: 'smooth' }); break;
    case 'G': case 'End': e.preventDefault(); reader.scrollTo({ top: reader.scrollHeight, behavior: 'smooth' }); break;
    case ' ': case 'PageDown': e.preventDefault(); reader.scrollBy({ top: reader.clientHeight * SCROLL_PAGE * (e.shiftKey ? -1 : 1), behavior: 'smooth' }); break;
    case 'PageUp': e.preventDefault(); reader.scrollBy({ top: -reader.clientHeight * SCROLL_PAGE, behavior: 'smooth' }); break;
    case 'j': reader.scrollBy({ top: SCROLL_LINE, behavior: 'smooth' }); break;
    case 'k': reader.scrollBy({ top: -SCROLL_LINE, behavior: 'smooth' }); break;
    case 'n': if (find.matches.length) { e.preventDefault(); findStep(1); } break;
    case 'N': if (find.matches.length) { e.preventDefault(); findStep(-1); } break;
  }
});

// =========================================================================
// Arrastrar y soltar
// =========================================================================
// Lo que Folio sabe abrir: Markdown y compañía, datos, marcado, documentos y código.
// La lista larga vive en formats.go; acá alcanza con no dejar pasar imágenes ni binarios
// que no vamos a poder mostrar (si igual cae algo raro, el servidor lo muestra como texto).
const dropNo = /\.(png|jpe?g|gif|bmp|webp|avif|heic|ico|mp[34]|mkv|mov|avi|wav|flac|zip|rar|7z|exe|dll|msi|iso|pdf)$/i;
window.addEventListener('dragover', (e) => { e.preventDefault(); body.classList.add('dragover'); });
window.addEventListener('dragleave', (e) => { if (!e.relatedTarget) body.classList.remove('dragover'); });
window.addEventListener('drop', async (e) => {
  e.preventDefault(); body.classList.remove('dragover');
  const f = e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files[0];
  if (!f) return;
  if (dropNo.test(f.name)) { toast('Folio no abre ese formato'); return; }
  try { renderRawText(await f.arrayBuffer(), f.name); } catch (err) { window.__log('drop ' + err); toast('No se pudo leer'); }
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

// resize coalescido a un frame: arrastrando el borde llegan decenas de eventos por segundo
let resizeFrame = 0;
window.addEventListener('resize', () => {
  cancelAnimationFrame(resizeFrame);
  resizeFrame = requestAnimationFrame(() => {
    body.classList.toggle('maximized', !isFs && window.innerWidth >= screen.availWidth - 6);
    updateProgress();
    applyTocWidth(); // re-clampear el ancho del índice si la ventana se achicó
    applyScale();    // no hace nada salvo que haya cambiado el DPI (ver scaledFor)
  });
});
// Zoom con Ctrl+rueda -> ajusta el tamaño de lectura (persistido); preventDefault corta el zoom
// nativo de WebView2 (que no se recuerda al cerrar).
window.addEventListener('wheel', (e) => {
  if (!e.ctrlKey) return;
  e.preventDefault();
  zoomBy(e.deltaY < 0 ? RSCALE_STEP_WHEEL : -RSCALE_STEP_WHEEL);
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
  } catch (e) { window.__log('openevents ' + e); }
  fetch('/api/initial').then((r) => r.json()).then(async (j) => {
    if (j && j.path) await openPath(j.path); // texto + KaTeX antes de fundir el splash
    reveal();
  }).catch(() => reveal());
}
boot();
