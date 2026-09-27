/* BePaylot prototype — shared client: config, API calls, app shell, helpers. */
(function () {
  'use strict';

  // ---------------------------------------------------------------- config
  const store = {
    get(k, d) { try { const v = localStorage.getItem('bp.' + k); return v == null ? d : v; } catch { return d; } },
    set(k, v) { try { v == null ? localStorage.removeItem('bp.' + k) : localStorage.setItem('bp.' + k, v); } catch { /* private mode */ } },
  };
  const Cfg = {
    get base() { return (store.get('base', 'http://localhost:8080')).replace(/\/+$/, ''); },
    set base(v) { store.set('base', v); },
    get key() { return store.get('key', ''); },
    set key(v) { store.set('key', v); },
    get kb() { return store.get('kb', ''); },
    set kb(v) { store.set('kb', v); },
  };

  // ---------------------------------------------------------------- API
  class ApiError extends Error {
    constructor(status, message) { super(message); this.status = status; }
  }
  function headers(extra) {
    const h = Object.assign({}, extra);
    if (Cfg.key) h['x-api-key'] = Cfg.key;
    return h;
  }
  async function errorOf(res) {
    let msg = res.status + ' ' + res.statusText;
    try {
      const j = await res.json();
      msg = (j.error && (j.error.message || j.error)) || j.message || msg;
    } catch { /* not JSON */ }
    return new ApiError(res.status, typeof msg === 'string' ? msg : JSON.stringify(msg));
  }
  async function api(path, opt = {}) {
    const init = { method: opt.method || 'GET', headers: headers(opt.headers) };
    if (opt.body !== undefined) {
      init.body = JSON.stringify(opt.body);
      init.headers['Content-Type'] = 'application/json';
      if (!opt.method) init.method = 'POST';
    }
    if (opt.signal) init.signal = opt.signal;
    let res;
    try {
      res = await fetch(Cfg.base + '/v1' + path, init);
    } catch (e) {
      if (e.name === 'AbortError') throw e;
      throw new ApiError(0, 'Không kết nối được ' + Cfg.base + ' (' + e.message + ')');
    }
    if (!res.ok) throw await errorOf(res);
    if (opt.raw) return res;
    const ct = res.headers.get('content-type') || '';
    return ct.includes('json') ? res.json() : res.text();
  }

  // Page images need the API key header, so they are fetched as blobs. The
  // server may redirect to a presigned object-store URL; fetch follows it.
  const blobCache = new Map();
  function blobURL(path) {
    if (!blobCache.has(path)) {
      blobCache.set(path, (async () => {
        const res = await fetch(Cfg.base + '/v1' + path, { headers: headers() });
        if (!res.ok) throw await errorOf(res);
        return URL.createObjectURL(await res.blob());
      })().catch((e) => { blobCache.delete(path); throw e; }));
    }
    return blobCache.get(path);
  }

  // Multipart upload with progress (fetch has no upload progress).
  function upload(kbId, files, { metadata, callbackUrl, onProgress } = {}) {
    return new Promise((resolve, reject) => {
      const fd = new FormData();
      for (const f of files) fd.append('file', f, f.name);
      if (metadata && Object.keys(metadata).length) fd.append('metadata', JSON.stringify(metadata));
      if (callbackUrl) fd.append('callback_url', callbackUrl);
      const xhr = new XMLHttpRequest();
      xhr.open('POST', Cfg.base + '/v1/kbs/' + kbId + '/documents');
      if (Cfg.key) xhr.setRequestHeader('x-api-key', Cfg.key);
      xhr.upload.onprogress = (e) => e.lengthComputable && onProgress && onProgress(e.loaded / e.total);
      xhr.onload = () => {
        let j = null;
        try { j = JSON.parse(xhr.responseText); } catch { /* ignore */ }
        if (xhr.status >= 200 && xhr.status < 300) resolve(j);
        else reject(new ApiError(xhr.status, (j && j.error && (j.error.message || j.error)) || xhr.statusText || 'Upload lỗi'));
      };
      xhr.onerror = () => reject(new ApiError(0, 'Không kết nối được máy chủ'));
      xhr.send(fd);
    });
  }

  // Server-Sent Events over a fetch response (POST streams, custom headers).
  async function readSSE(res, onEvent) {
    const reader = res.body.getReader();
    const dec = new TextDecoder();
    let buf = '';
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      buf += dec.decode(value, { stream: true });
      let i;
      while ((i = buf.search(/\r?\n\r?\n/)) >= 0) {
        const chunk = buf.slice(0, i);
        buf = buf.slice(i).replace(/^\r?\n\r?\n/, '');
        let event = 'message', data = '';
        for (const line of chunk.split(/\r?\n/)) {
          if (line.startsWith('event:')) event = line.slice(6).trim();
          else if (line.startsWith('data:')) data += (data ? '\n' : '') + line.slice(5).replace(/^ /, '');
        }
        if (!data) continue;
        let payload = data;
        try { payload = JSON.parse(data); } catch { /* raw */ }
        onEvent(event, payload);
      }
    }
  }

  // ---------------------------------------------------------------- helpers
  const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const $ = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));
  function h(html) { const t = document.createElement('template'); t.innerHTML = html.trim(); return t.content.firstElementChild; }
  function debounce(fn, ms) { let t; return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); }; }
  function fmtDate(s) {
    if (!s) return '';
    const d = new Date(s);
    return d.toLocaleString('vi-VN', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' });
  }
  function fmtSize(n) {
    if (!n) return '';
    const u = ['B', 'KB', 'MB', 'GB']; let i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return n.toFixed(i ? 1 : 0) + ' ' + u[i];
  }

  const STATUS = {
    queued: ['Đang chờ', 'info'], splitting: ['Tách trang', 'info'], parsing: ['Đang OCR', 'info'],
    assembling: ['Đang ghép', 'info'], indexing: ['Lập chỉ mục', 'info'], enriching: ['Làm giàu', 'ok'],
    completed: ['Hoàn tất', 'ok'], partial: ['Một phần', 'warn'], failed: ['Lỗi', 'err'],
    cancelled: ['Đã huỷ', ''], deleting: ['Đang xoá', 'warn'],
  };
  const TERMINAL = new Set(['completed', 'partial', 'failed', 'cancelled', 'deleting']);
  function statusBadge(s) {
    const [label, cls] = STATUS[s] || [s, ''];
    const spin = TERMINAL.has(s) || s === 'enriching' ? '' : '<span class="spinner" style="width:10px;height:10px;border-width:1.5px"></span>';
    return `<span class="badge ${cls}">${spin}${esc(label)}</span>`;
  }
  function sourceBadge(src) {
    const m = { vlm: ['VLM', 'vlm'], ocr: ['OCR', 'info'], merged: ['OCR+layer', 'ok'], layer: ['Text layer', 'ok'], layer_only: ['Text layer', 'ok'] };
    const [label, cls] = m[src] || [src || '—', ''];
    return `<span class="badge ${cls}">${esc(label)}</span>`;
  }
  function metaChips(meta) {
    if (!meta) return '';
    return Object.entries(meta).map(([k, v]) =>
      `<span class="chip"><span class="muted">${esc(k)}</span> <b>${esc(typeof v === 'object' ? JSON.stringify(v) : v)}</b></span>`).join(' ');
  }

  // ---------------------------------------------------------------- toast
  function toast(msg, kind) {
    let box = document.getElementById('toasts');
    if (!box) { box = h('<div id="toasts"></div>'); document.body.appendChild(box); }
    const t = h(`<div class="toast ${kind === 'err' ? 'err' : ''}">${esc(msg)}</div>`);
    box.appendChild(t);
    setTimeout(() => t.remove(), kind === 'err' ? 6000 : 3000);
  }
  const fail = (e) => { if (e && e.name !== 'AbortError') { console.error(e); toast(e.message || String(e), 'err'); } };

  // ---------------------------------------------------------------- markdown
  const CITE_RE = /\[?\s*(doc:([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})(?::p(\d+))?(?::l(\d+)(?:-(\d+))?)?)\s*\]?/gi;

  function renderMarkdown(md, opt = {}) {
    let src = String(md || '');
    if (opt.wikiLinks) {
      src = src.replace(/\[\[([^\]|]+)(?:\|([^\]]+))?\]\]/g, (_, slug, label) => {
        const dead = opt.knownSlugs && !opt.knownSlugs.has(slug.trim());
        return `<a class="wikilink${dead ? ' dead' : ''}" href="#${encodeURIComponent(slug.trim())}" data-slug="${esc(slug.trim())}">${esc(label || slug)}</a>`;
      });
    }
    let html;
    if (window.marked) {
      html = window.marked.parse(src, { gfm: true, breaks: !!opt.breaks });
      if (window.DOMPurify) html = window.DOMPurify.sanitize(html, { ADD_ATTR: ['data-slug', 'target'] });
    } else {
      html = '<p style="white-space:pre-wrap">' + esc(src) + '</p>';
    }
    const el = document.createElement('div');
    el.className = 'md';
    el.innerHTML = html;
    linkCitations(el);
    return el;
  }

  // Replaces "doc:<uuid>:p3:l1-2" citations in text nodes with buttons.
  function linkCitations(root) {
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
      acceptNode: (n) => n.parentElement && n.parentElement.closest('code,pre,.cite') ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT,
    });
    const nodes = [];
    while (walker.nextNode()) if (/doc:[0-9a-f]{8}-/i.test(walker.currentNode.nodeValue)) nodes.push(walker.currentNode);
    for (const node of nodes) {
      const frag = document.createDocumentFragment();
      let last = 0;
      const text = node.nodeValue;
      text.replace(CITE_RE, (m, id, doc, page, l1, l2, off) => {
        frag.appendChild(document.createTextNode(text.slice(last, off)));
        const b = document.createElement('button');
        b.className = 'cite';
        b.dataset.cite = id;
        b.title = id;
        b.textContent = page ? 'tr.' + page + (l1 ? ' · d.' + l1 + (l2 && l2 !== l1 ? '–' + l2 : '') : '') : 'tài liệu';
        frag.appendChild(b);
        last = off + m.length;
        return m;
      });
      frag.appendChild(document.createTextNode(text.slice(last)));
      node.parentNode.replaceChild(frag, node);
    }
  }

  // ---------------------------------------------------------------- page viewer
  // Renders a page image with line boxes. Boxes are in page pixel units
  // (the same space as page.width/height).
  async function pageView(docId, pageNo, opt = {}) {
    const el = h(`<div class="pageview"><div class="placeholder"><span class="spinner"></span></div></div>`);
    if (opt.hideBoxes) el.classList.add('nobox');
    (async () => {
      try {
        const url = await blobURL(`/documents/${docId}/pages/${pageNo}/image`);
        const img = new Image();
        img.src = url;
        await img.decode().catch(() => {});
        const W = opt.width || img.naturalWidth, H = opt.height || img.naturalHeight;
        el.innerHTML = '';
        el.appendChild(img);
        const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
        svg.setAttribute('viewBox', `0 0 ${W} ${H}`);
        svg.setAttribute('preserveAspectRatio', 'none');
        for (const b of opt.boxes || []) {
          if (!b.bbox) continue;
          const r = document.createElementNS('http://www.w3.org/2000/svg', 'rect');
          r.setAttribute('x', b.bbox.x0); r.setAttribute('y', b.bbox.y0);
          r.setAttribute('width', Math.max(1, b.bbox.x1 - b.bbox.x0));
          r.setAttribute('height', Math.max(1, b.bbox.y1 - b.bbox.y0));
          r.setAttribute('class', 'box' + (b.cls ? ' ' + b.cls : ''));
          if (b.key != null) r.dataset.key = b.key;
          if (b.title) { const t = document.createElementNS('http://www.w3.org/2000/svg', 'title'); t.textContent = b.title; r.appendChild(t); }
          svg.appendChild(r);
        }
        el.appendChild(svg);
        opt.onReady && opt.onReady(el, svg);
      } catch (e) {
        el.innerHTML = `<div class="placeholder small">Không tải được ảnh trang<br>${esc(e.message)}</div>`;
      }
    })();
    return el;
  }

  // ---------------------------------------------------------------- citation popover
  let popover = null;
  function closePopover() { if (popover) { popover.remove(); popover = null; } }
  document.addEventListener('keydown', (e) => e.key === 'Escape' && closePopover());
  document.addEventListener('click', (e) => {
    const c = e.target.closest('.cite');
    if (c) { e.preventDefault(); showCitation(c.dataset.cite, c); return; }
    if (popover && !popover.contains(e.target)) closePopover();
  });

  async function showCitation(id, anchor) {
    closePopover();
    popover = h(`<div class="popover"><div class="row"><span class="spinner"></span><span class="muted small">Đang định vị ${esc(id)}…</span></div></div>`);
    document.body.appendChild(popover);
    const r = anchor.getBoundingClientRect();
    const left = Math.min(window.innerWidth - popover.offsetWidth - 12, Math.max(12, r.left));
    popover.style.left = left + 'px';
    const below = r.bottom + 8;
    popover.style.top = (below + 380 > window.innerHeight ? Math.max(12, r.top - 390) : below) + 'px';
    const pop = popover;
    try {
      const res = await api('/citations?id=' + encodeURIComponent(id));
      const hit = (res.hits || [])[0];
      if (!hit) throw new Error('Không tìm thấy vị trí trích dẫn');
      const page = await api(`/documents/${hit.document_id}/pages/${hit.page_no}`);
      if (pop !== popover) return;
      pop.innerHTML = '';
      pop.appendChild(h(`<div class="row"><b class="ellipsis" style="flex:1">${esc(hit.file_name)}</b><span class="badge info">Trang ${hit.page_no}</span></div>`));
      if (hit.quote) pop.appendChild(h(`<blockquote class="md small" style="margin:0;border-left:3px solid var(--accent);padding-left:10px">${esc(hit.quote)}</blockquote>`));
      const wrap = h('<div class="pv-img"></div>');
      const boxes = (hit.bboxes || []).map((b) => ({ bbox: b, cls: 'hl' }));
      wrap.appendChild(await pageView(hit.document_id, hit.page_no, {
        width: page.width, height: page.height, boxes,
        onReady: (el, svg) => { const first = svg.querySelector('rect'); if (first) setTimeout(() => first.scrollIntoView({ block: 'center' }), 50); },
      }));
      pop.appendChild(wrap);
      const lines = (hit.lines || []).join(',');
      pop.appendChild(h(`<div class="row"><span class="grow muted small mono ellipsis" style="flex:1">${esc(id)}</span>
        <a class="btn sm" href="index.html#doc=${hit.document_id}&page=${hit.page_no}${lines ? '&hl=' + lines : ''}">Mở tài liệu →</a></div>`));
    } catch (e) {
      if (pop === popover) pop.innerHTML = `<div class="small" style="color:var(--err)">${esc(e.message)}</div>`;
    }
  }

  // ---------------------------------------------------------------- dialogs
  function dialog(html) {
    const d = h(`<dialog>${html}</dialog>`);
    document.body.appendChild(d);
    d.addEventListener('close', () => d.remove());
    d.addEventListener('click', (e) => { if (e.target === d) d.close(); });
    $$('[data-close]', d).forEach((b) => b.addEventListener('click', () => d.close()));
    d.showModal();
    return d;
  }

  function openSettings() {
    const d = dialog(`
      <div class="dh"><h2>Kết nối backend</h2><button class="btn icon" data-close>✕</button></div>
      <div class="db">
        <label class="field"><span>API base URL</span><input type="url" name="base" value="${esc(Cfg.base)}" placeholder="http://localhost:8080"></label>
        <label class="field"><span>API key (header x-api-key)</span><input type="password" name="key" value="${esc(Cfg.key)}" placeholder="bp_..." autocomplete="off"></label>
        <div class="small muted">Key được lưu trong localStorage của trình duyệt này. Tạo key bằng <code>make seed</code>.</div>
        <div class="small" data-result></div>
      </div>
      <div class="df"><button class="btn" data-test>Kiểm tra</button><button class="btn primary" data-save>Lưu</button></div>`);
    const read = () => { Cfg.base = $('[name=base]', d).value.trim() || 'http://localhost:8080'; Cfg.key = $('[name=key]', d).value.trim(); };
    $('[data-test]', d).onclick = async () => {
      read();
      const out = $('[data-result]', d);
      out.innerHTML = '<span class="spinner"></span>';
      try {
        const r = await api('/kbs');
        out.innerHTML = `<span class="badge ok">Kết nối OK · ${r.data.length} knowledge base</span>`;
      } catch (e) { out.innerHTML = `<span class="badge err">${esc(e.message)}</span>`; }
    };
    $('[data-save]', d).onclick = () => { read(); d.close(); location.reload(); };
  }

  async function openCreateKB(onDone) {
    let engines = [];
    try { engines = (await api('/parser/engines')).data || []; } catch { /* optional */ }
    const d = dialog(`
      <div class="dh"><h2>Tạo knowledge base</h2><button class="btn icon" data-close>✕</button></div>
      <div class="db">
        <label class="field"><span>Tên</span><input type="text" name="name" placeholder="VD: Hồ sơ đất đai 2026" required></label>
        <label class="field"><span>Mô tả</span><input type="text" name="desc"></label>
        <label class="field"><span>OCR engine</span><select name="engine"><option value="">Mặc định hệ thống</option>
          ${engines.map((e) => `<option value="${esc(e.name)}">${esc(e.name)}${e.default ? ' (mặc định)' : ''}${e.available ? '' : ' — không khả dụng'}</option>`).join('')}</select></label>
        <label class="row"><input type="checkbox" name="graph"> Bật knowledge graph (trích xuất thực thể, quan hệ, wiki)</label>
      </div>
      <div class="df"><button class="btn" data-close>Huỷ</button><button class="btn primary" data-ok>Tạo</button></div>`);
    $('[data-ok]', d).onclick = async () => {
      const name = $('[name=name]', d).value.trim();
      if (!name) return toast('Nhập tên knowledge base', 'err');
      try {
        const kb = await api('/kbs', { body: { name, description: $('[name=desc]', d).value.trim(), config: { parser_engine: $('[name=engine]', d).value, graph_enabled: $('[name=graph]', d).checked } } });
        d.close();
        toast('Đã tạo ' + kb.name);
        onDone && onDone(kb);
      } catch (e) { fail(e); }
    };
  }

  // ---------------------------------------------------------------- shell
  const ICONS = {
    docs: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6M8 13h8M8 17h5"/></svg>',
    chat: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>',
    wiki: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20V2H6.5A2.5 2.5 0 0 0 4 4.5v15z"/><path d="M6.5 17A2.5 2.5 0 0 0 4 19.5 2.5 2.5 0 0 0 6.5 22H20v-5"/></svg>',
    graph: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="5" cy="6" r="2.5"/><circle cx="19" cy="6" r="2.5"/><circle cx="12" cy="18" r="2.5"/><path d="M7.2 7.2 10.8 16M16.8 7.2 13.2 16M7.5 6h9"/></svg>',
    gear: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/></svg>',
    plus: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M12 5v14M5 12h14"/></svg>',
  };
  const PAGES = [
    ['docs', 'index.html', 'Tài liệu'],
    ['chat', 'chat.html', 'Hỏi đáp'],
    ['wiki', 'wiki.html', 'Wiki'],
    ['graph', 'graph.html', 'Graph'],
  ];

  const Shell = {
    kbs: [],
    kb: null,
    // init wraps #page in the app frame; onKB(kb) runs on load and on change.
    async init({ page, title, kbSelector = true, actions = '', onKB }) {
      const content = document.getElementById('page');
      const app = h(`<div class="app">
        <aside class="sidebar">
          <div class="brand"><div class="logo">B</div>BePaylot</div>
          <nav class="nav">${PAGES.map(([k, href, label]) => `<a href="${href}" class="${k === page ? 'active' : ''}">${ICONS[k]}<span>${label}</span></a>`).join('')}</nav>
          <div class="spacer"></div>
          <div class="conn"><span class="dot" data-conn></span><span class="ellipsis" data-conn-text>${esc(Cfg.base)}</span></div>
          <div class="nav"><a href="#" data-settings>${ICONS.gear}<span>Kết nối</span></a></div>
        </aside>
        <div class="main">
          <header class="topbar">
            <div class="title">${esc(title)}</div>
            ${kbSelector ? `<select data-kb style="max-width:280px"><option>Đang tải…</option></select>
              <button class="btn icon" data-newkb title="Tạo knowledge base">${ICONS.plus}</button>` : ''}
            <div class="grow"></div>
            <div class="row" data-actions>${actions}</div>
          </header>
        </div>
      </div>`);
      content.classList.add('content');
      $('.main', app).appendChild(content);
      document.body.prepend(app);
      $('[data-settings]', app).onclick = (e) => { e.preventDefault(); openSettings(); };

      if (!Cfg.key) setTimeout(openSettings, 50);
      if (!kbSelector) { this.ping(); return; }
      const sel = $('[data-kb]', app);
      $('[data-newkb]', app).onclick = () => openCreateKB(async (kb) => { Cfg.kb = kb.id; await this.loadKBs(sel, onKB); });
      sel.onchange = () => { Cfg.kb = sel.value; this.kb = this.kbs.find((k) => k.id === sel.value) || null; onKB && onKB(this.kb); };
      await this.loadKBs(sel, onKB);
    },
    async loadKBs(sel, onKB) {
      try {
        this.kbs = (await api('/kbs')).data || [];
        this.setConn(true);
      } catch (e) {
        this.setConn(false, e.message);
        sel.innerHTML = '<option>— không tải được —</option>';
        fail(e);
        onKB && onKB(null);
        return;
      }
      if (!this.kbs.length) {
        sel.innerHTML = '<option value="">Chưa có knowledge base</option>';
        onKB && onKB(null);
        return;
      }
      this.kb = this.kbs.find((k) => k.id === Cfg.kb) || this.kbs[0];
      Cfg.kb = this.kb.id;
      sel.innerHTML = this.kbs.map((k) => `<option value="${k.id}" ${k.id === this.kb.id ? 'selected' : ''}>${esc(k.name)}</option>`).join('');
      onKB && onKB(this.kb);
    },
    async ping() {
      try { await api('/kbs'); this.setConn(true); } catch (e) { this.setConn(false, e.message); }
    },
    setConn(ok, msg) {
      const d = $('[data-conn]'); if (!d) return;
      d.className = 'dot ' + (ok ? 'ok' : 'err');
      $('[data-conn-text]').textContent = ok ? Cfg.base : (msg || 'Mất kết nối');
      $('[data-conn-text]').title = msg || Cfg.base;
    },
  };

  window.BP = {
    Cfg, api, blobURL, upload, readSSE, ApiError,
    esc, $, $$, h, debounce, fmtDate, fmtSize, statusBadge, sourceBadge, metaChips, TERMINAL,
    toast, fail, renderMarkdown, linkCitations, pageView, showCitation, closePopover, dialog, ICONS, Shell,
  };
})();
