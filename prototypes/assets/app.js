// Prototype tĩnh của frontend BePaylot: cùng màn hình, cùng class Tailwind
// với frontend/src, dữ liệu lấy từ data.js, không gọi backend.
(function () {
  const M = window.MOCK;
  const root = document.getElementById("root");

  // ------------------------------------------------------------ state
  const S = {
    user: { name: "Nguyễn Thanh", email: "thanh@bepaylot.vn", role: "admin" },
    signedIn: true,
    kb: "kb1",
    docView: "list",
    docFilter: { case: "", status: "", type: "" },
    sort: { by: "date", desc: true },
    showBoxes: true,
    panel: true,
    zoom: 100,
    showHistory: true,
    chats: { x1: M.TRANSCRIPT.slice(), x2: [], x3: [] },
    caseCol: "view",
    caseSet: {}, // case -> bộ quy tắc chọn ở Studio
    caseEdits: {}, // "doc:loại:trang:key" -> giá trị người dùng sửa ở cột trường của hồ sơ
    expanded: { d1: true },
    split: null, // bản nháp của màn tách & gom
    splitSel: null,
    sheetTab: {},
    sheetSel: null,
    sheetPanel: "source",
    drafts: {}, // "sheet:table:row:key" -> giá trị người dùng sửa, chưa lưu
    saved: { "s1:hoa_don:1:tong_tien": "2.695.000.000" },
    checkDoc: null, // giấy tờ đang lọc ở trang kết quả đối chiếu
    checkLinks: {},
    excluded: {},
    dtEdits: {}, // mẫu -> {trường: giá trị đúng người dùng sửa, chưa lưu}
  };

  // ------------------------------------------------------------ helpers
  const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
  const icon = (name, o = {}) =>
    `<span class="material-symbols-rounded ${o.fill ? "icon-fill " : ""}${o.cls ?? ""}" style="font-size:${o.size ?? 24}px" aria-hidden="true">${name}</span>`;
  const spinner = (cls = "") => `<span class="spinner ${cls}"></span>`;
  const fileIcon = (name = "", size = 22) => {
    const n = name.toLowerCase();
    if (n.endsWith(".pdf")) return icon("picture_as_pdf", { fill: true, size, cls: "text-[#ea4335]" });
    if (/\.(png|jpe?g|tiff?|webp)$/.test(n)) return icon("image", { fill: true, size, cls: "text-[#d93025]" });
    if (n.endsWith(".xlsx")) return icon("table_view", { fill: true, size, cls: "text-[#107c41]" });
    return icon("description", { fill: true, size, cls: "text-[#4285f4]" });
  };
  const fmtDate = (s) => (s ? new Date(s).toLocaleString("vi-VN", { day: "2-digit", month: "2-digit", year: "numeric", hour: "2-digit", minute: "2-digit" }) : "");
  const fmtSize = (n) => {
    if (!n) return "";
    const u = ["B", "KB", "MB", "GB"];
    let i = 0;
    while (n >= 1024 && i < 3) (n /= 1024), i++;
    return `${n.toFixed(i ? 1 : 0)} ${u[i]}`;
  };
  const fmtMoney = (n) => new Intl.NumberFormat("vi-VN").format(n) + " đ";
  const pages = (a, b) => (a === b ? `tr. ${a}` : `tr. ${a}–${b}`);
  const STATUS = {
    queued: ["Đang chờ", "info"], splitting: ["Tách trang", "info"], parsing: ["Đang đọc", "info"], assembling: ["Đang ghép", "info"],
    indexing: ["Đang lập mục lục", "info"], completed: ["Hoàn tất", "ok"], partial: ["Một phần", "warn"], failed: ["Lỗi", "err"], cancelled: ["Đã huỷ", ""],
    not_started: ["Chưa xử lý", ""],
  };
  const TERMINAL = new Set(["completed", "partial", "failed", "cancelled", "not_started"]);
  const statusBadge = (s) => {
    const [l, t] = STATUS[s] ?? [s, ""];
    return `<span class="badge ${t ? "badge-" + t : ""}">${TERMINAL.has(s) ? "" : '<span class="spinner size-2.5 border-[1.5px]"></span>'}${l}</span>`;
  };
  // Trạng thái từng trang (pstate trong data.js): ba bước đọc dòng chữ, đọc
  // nội dung và phân loại; "-" là trang người dùng chưa chọn xử lý.
  const pst = (d, p) => d.pstate?.[p - 1] ?? "c";
  const pagesWith = (d, ch) => Array.from({ length: d.content.length }, (_, i) => i + 1).filter((p) => ch.includes(pst(d, p)));
  const spans = (ps) => {
    const out = [];
    for (const p of ps) out.at(-1)?.[1] === p - 1 ? (out.at(-1)[1] = p) : out.push([p, p]);
    return out.map(([a, b]) => (a === b ? a : `${a}–${b}`)).join(", ");
  };
  const STEPS = [["o", "Dòng chữ"], ["m", "Nội dung"], ["c", "Phân loại"]];
  const pageSteps = (d, p, compact) => {
    const s = pst(d, p);
    if (compact && s === "c") return `<span class="badge badge-ok" title="Dòng chữ, nội dung và phân loại đều xong">${icon("check", { size: 14 })}Đã xử lý</span>`;
    if (s === "-") return '<span class="badge">Chưa xử lý</span>';
    if (s === "x") return `<span class="badge badge-err">${icon("error", { size: 14 })}Lỗi đọc trang</span>`;
    return STEPS.map(([k, l]) => {
      const ok = s === "c" || (k !== "c" && (s === k || s === "b"));
      return `<span class="badge ${ok ? "badge-ok" : ""}" title="${l}: ${ok ? "xong" : "đang xử lý"}">${ok ? icon("check", { size: 14 }) : '<span class="spinner size-2.5 border-[1.5px]"></span>'}${l}</span>`;
    }).join("");
  };
  // Một dòng tóm tắt: bao nhiêu trang đã xong, lỗi, chưa xử lý.
  const pageSummary = (d) => {
    const n = d.content.length, c = pagesWith(d, "c"), x = pagesWith(d, "x"), todo = pagesWith(d, "-"), busy = n - c.length - x.length - todo.length;
    return [`${c.length}/${n} trang đã phân loại`, busy && `${busy} trang đang xử lý`, x.length && `<span class="text-err">lỗi tr. ${spans(x)}</span>`, todo.length && `chưa xử lý tr. ${spans(todo)}`].filter(Boolean).join(" · ");
  };
  const metaChips = (meta) =>
    Object.entries(meta ?? {}).map(([k, v]) => `<span class="tag"><span class="text-subtle">${esc(k)}</span><span class="font-medium text-fg">${esc(v)}</span></span>`).join("");
  const kb = () => M.KBS.find((k) => k.id === S.kb);
  const cases = () => M.CASES.filter((c) => c.kb_id === S.kb);
  const caseById = (id) => M.CASES.find((c) => c.id === id);
  const caseLabel = (c) => (c.code === "_UNASSIGNED" ? "Chưa gán hồ sơ" : c.code);
  const docById = (id) => M.DOCS.find((d) => d.id === id);
  const docsOf = (caseId) => M.DOCS.filter((d) => d.case_id === caseId);
  const typeTitle = (n) => M.DOC_TYPES.find((t) => t.name === n)?.title ?? (n === "other" ? "Khác" : n === "unknown" ? "Chưa phân loại" : n);
  const go = (h) => (location.hash = h);
  const TYPE_COLORS = ["#1a73e8", "#e37400", "#188038", "#9334e6", "#d93025", "#007b83", "#b06000", "#c5221f"];
  const typeColor = (n) => { const i = M.DOC_TYPES.findIndex((t) => t.name === n); return i < 0 ? "var(--outline)" : TYPE_COLORS[i % TYPE_COLORS.length]; };

  // Đăng ký handler cho chuỗi HTML: on(fn) trả về "A.h(3)" để gắn vào onclick.
  let handlers = [];
  const on = (fn) => `A.h(${handlers.push(fn) - 1},event)`;

  // ------------------------------------------------------------ page image
  // Trang giả lập 1240×1754 (A4, 150 dpi): tiêu đề là dòng 1, thân từ dòng 2.
  const W = 1240, H = 1754;
  function linesOf(doc, n) {
    const p = doc.content[n - 1];
    if (!p || p.blank) return [];
    const out = [];
    if (p.title) out.push({ line_no: 1, text: p.title, bbox: [W / 2 - p.title.length * 11, 150, W / 2 + p.title.length * 11, 205], title: true });
    p.body.forEach((t, i) =>
      out.push({ line_no: i + 2, text: t, bbox: [130, 290 + i * 92, 130 + Math.min(980, t.length * 15.5), 336 + i * 92], vlm: p.vlm?.includes(i + 1), low: p.low?.includes(i + 1) }),
    );
    return out.map((l) => ({ ...l, conf: l.low ? 0.58 : 0.93 + ((l.line_no * 7) % 6) / 100 }));
  }
  function pageSVG(doc, n, o = {}) {
    const p = doc.content[n - 1];
    const lines = linesOf(doc, n);
    const hl = new Set(o.hl ?? []);
    const text = lines
      .map((l) => `<text x="${l.title ? W / 2 : l.bbox[0]}" y="${l.bbox[3] - 10}" font-size="${l.title ? 40 : 30}" ${l.title ? 'text-anchor="middle" font-weight="700"' : ""} fill="#222" font-family="Times New Roman, serif">${esc(l.text)}</text>`)
      .join("");
    const deco = p?.blank ? "" : `<rect x="130" y="${330 + (p?.body.length ?? 0) * 92}" width="980" height="2" fill="#ccc"/>` + Array.from({ length: 6 }, (_, i) => `<rect x="130" y="${400 + (p?.body.length ?? 0) * 92 + i * 60}" width="${900 - ((i * 137) % 300)}" height="14" rx="7" fill="#e6e6e6"/>`).join("");
    const boxes = o.boxes
      ? lines.map((l) => `<rect class="pagebox ${l.vlm ? "vlm" : ""} ${l.low ? "low" : ""} ${hl.has(l.line_no) ? "hl" : ""}" data-line="${l.line_no}" x="${l.bbox[0] - 8}" y="${l.bbox[1]}" width="${l.bbox[2] - l.bbox[0] + 16}" height="${l.bbox[3] - l.bbox[1]}" rx="4"><title>L${l.line_no}: ${esc(l.text)}</title></rect>`).join("")
      : "";
    return `<svg viewBox="0 0 ${W} ${H}" class="block h-auto w-full bg-white ${o.cls ?? ""} ${o.hideBoxes ? "nobox" : ""}" ${o.onBox ? `onclick="${o.onBox}"` : ""}>${text}${deco}${boxes}${p?.failed ? `<text x="${W / 2}" y="${H / 2}" text-anchor="middle" font-size="40" fill="#b3261e">Trang lỗi OCR</text>` : ""}</svg>`;
  }

  // ------------------------------------------------------------ markdown + citations
  const CITE = /\[?doc:(d\d+)(?::p(\d+))?(?::l(\d+)(?:-l?(\d+))?)?\]?/g;
  function md(text) {
    const html = DOMPurify.sanitize(marked.parse(text), { ADD_ATTR: ["onclick"] });
    return html.replace(CITE, (_, d, p, a, b) => `<button class="cite" onclick="A.cite('${d}',${p || 1},${a || 0},${b || a || 0})">tr.${p}${a ? " · d." + a + (b && b !== a ? "–" + b : "") : ""}</button>`);
  }

  // ------------------------------------------------------------ menus, dialogs, toasts
  function openMenu(ev, items, align = "left") {
    ev.stopPropagation();
    closeMenu();
    const r = ev.currentTarget.getBoundingClientRect();
    const el = document.createElement("div");
    el.id = "menu";
    el.className = "menu fixed";
    el.innerHTML = items
      .map((it, i) =>
        it.divider
          ? '<div class="my-2 border-t border-line"></div>'
          : `<button class="menu-item ${it.danger ? "text-err" : ""}" data-i="${i}" ${it.disabled ? "disabled" : ""}>${
              it.checked !== undefined ? icon(it.checked ? "check" : "", { cls: "w-5 text-accent" }) : it.icon ? icon(it.icon, { cls: it.danger ? "text-err" : "text-muted" }) : ""
            }<span class="flex-1">${it.label}</span></button>`,
      )
      .join("");
    document.body.appendChild(el);
    const m = el.getBoundingClientRect();
    let left = align === "right" ? r.right - m.width : r.left;
    left = Math.max(8, Math.min(innerWidth - m.width - 8, left));
    let top = r.bottom + 4;
    if (top + m.height > innerHeight - 8) top = Math.max(8, r.top - m.height - 4);
    el.style.left = left + "px";
    el.style.top = top + "px";
    el.onclick = (e) => {
      const b = e.target.closest("[data-i]");
      if (!b) return;
      closeMenu();
      items[+b.dataset.i].onClick?.();
    };
  }
  const closeMenu = () => document.getElementById("menu")?.remove();
  document.addEventListener("mousedown", (e) => !e.target.closest("#menu") && closeMenu());
  document.addEventListener("keydown", (e) => e.key === "Escape" && closeMenu());

  let modalRender = null;
  function modal({ title, icon: ic, width = 560, body, footer }) {
    closeModal();
    modalRender = arguments[0];
    const d = document.createElement("dialog");
    d.id = "modal";
    d.className = "m-auto rounded-[28px] border-0 bg-surface p-0 text-fg shadow-3 outline-none";
    d.style.width = `min(${width}px, calc(100vw - 32px))`;
    d.innerHTML = `<div class="flex items-start gap-3 px-6 pt-6 pb-4">${ic ? icon(ic, { cls: "mt-0.5 text-accent" }) : ""}<h2 class="flex-1 text-2xl font-normal">${title}</h2><button class="btn-icon btn-sm -mt-1 -mr-2" onclick="A.closeModal()" aria-label="Đóng">${icon("close")}</button></div>
      <div class="flex max-h-[68vh] flex-col gap-4 overflow-auto px-6 pb-2 text-muted">${typeof body === "function" ? body() : body}</div>
      ${footer ? `<div class="flex justify-end gap-2 px-6 pt-4 pb-6">${footer}</div>` : '<div class="h-4"></div>'}`;
    d.addEventListener("cancel", (e) => (e.preventDefault(), closeModal()));
    d.addEventListener("click", (e) => e.target === d && closeModal());
    document.body.appendChild(d);
    d.showModal();
  }
  function closeModal() {
    const d = document.getElementById("modal");
    if (d) d.close(), d.remove();
    modalRender = null;
  }
  function toast(msg) {
    const t = document.createElement("div");
    t.className = "pointer-events-auto rounded-lg bg-[#303030] px-4 py-3 text-sm text-[#f2f2f2] shadow-3";
    t.textContent = msg;
    document.getElementById("toasts").appendChild(t);
    setTimeout(() => t.remove(), 3200);
  }

  // Popover trích dẫn: ảnh trang có khung vàng ở đúng dòng.
  function cite(docId, page, from, to) {
    const d = docById(docId);
    const hl = [];
    for (let i = from; i && i <= to; i++) hl.push(i);
    const line = linesOf(d, page).filter((l) => hl.includes(l.line_no)).map((l) => l.text).join(" ");
    modal({
      title: esc(d.file_name),
      icon: "format_quote",
      width: 720,
      body: `<div class="flex flex-wrap items-center gap-2 text-sm">${icon("article", { size: 18 })} Trang ${page}${from ? ` · dòng ${from}${to !== from ? "–" + to : ""}` : ""}<span class="flex-1"></span><a class="btn btn-text btn-sm" href="#/documents/${d.id}?page=${page}&hl=${hl.join(",")}" onclick="A.closeModal()">${icon("open_in_new", { size: 18 })} Mở trong trình xem</a></div>
        ${line ? `<div class="rounded-xl bg-warn-soft px-4 py-3 font-body text-sm text-fg">${esc(line)}</div>` : ""}
        <div class="overflow-hidden rounded-xl border border-line">${pageSVG(d, page, { boxes: true, hl, hideBoxes: true })}</div>`,
    });
  }

  // ------------------------------------------------------------ layout
  const logo = (cls = "size-9") => `<svg viewBox="0 0 32 32" class="${cls}" aria-hidden="true"><rect width="32" height="32" rx="9" class="fill-accent"/><path d="M11 7.7v16.6M11 7.7h5.6c2.4 0 4 1.6 4 4.2s-1.6 4.2-4 4.2H11M11 16.1h6.3c2.8 0 4.6 1.6 4.6 4.1s-1.8 4.1-4.6 4.1H11" fill="none" class="stroke-on-accent" stroke-width="2.6" stroke-linejoin="round" stroke-linecap="round"/></svg>`;
  const NAV = [
    ["#/documents", "folder_open", "Tài liệu"],
    ["#/cases", "account_tree", "Hồ sơ"],
    ["#/chat", "forum", "Hỏi đáp"],
  ];
  function layout(path, content) {
    const navCls = (on) => "flex h-8 items-center gap-4 rounded-full pr-4 pl-4 text-sm " + (on ? "bg-accent-soft font-semibold text-on-accent-soft" : "text-fg hover:bg-fg/8");
    const sidebar = `
      <div class="flex h-full w-64 flex-col gap-1 overflow-y-auto pr-3 pb-3 pl-3">
        <button onclick="${on((e) => openMenu(e, [
          { icon: "upload_file", label: "Tải file lên", onClick: openUpload },
          { icon: "create_new_folder", label: "Thư viện mới", onClick: () => openKB() },
          { divider: true },
          { icon: "chat_add_on", label: "Cuộc trò chuyện mới", onClick: () => go("#/chat") },
        ]))}" class="mt-1 mb-4 flex h-14 w-fit items-center gap-3 rounded-2xl bg-surface pr-5 pl-4 text-sm font-medium text-fg shadow-1 transition-shadow hover:bg-accent-container/40 hover:shadow-2">${icon("add")} Mới</button>
        <nav class="flex flex-col gap-0.5">${NAV.map(([to, ic, l]) => {
          const a = path.startsWith(to.slice(1));
          return `<a href="${to}" class="${navCls(a)}">${icon(ic, { fill: a, size: 20 })}${l}</a>`;
        }).join("")}</nav>
        <div class="mt-4 flex items-center pr-1 pl-4"><span class="flex-1 text-xs font-medium text-muted">Thư viện</span><button class="btn-icon btn-sm" title="Thư viện mới" onclick="${on(() => openKB())}">${icon("add", { size: 20 })}</button></div>
        <div class="flex flex-col gap-0.5">${M.KBS.map((k) => {
          const a = k.id === S.kb;
          return `<div class="group flex h-8 items-center rounded-full pr-1 text-sm ${a ? "bg-surface-3 font-medium text-fg" : "text-fg hover:bg-fg/8"}">
            <button onclick="${on(() => ((S.kb = k.id), render()))}" title="${esc(k.description)}" class="flex h-full min-w-0 flex-1 items-center gap-4 pl-4 text-left">${icon("folder_shared", { fill: a, size: 20, cls: a ? "text-accent" : "text-muted" })}<span class="min-w-0 flex-1 truncate">${esc(k.name)}</span></button>
            <button class="btn-icon btn-sm ${a ? "" : "opacity-0 group-hover:opacity-100"}" title="Sửa thư viện, loại giấy tờ" onclick="${on(() => openKB(k))}">${icon("edit", { size: 18 })}</button></div>`;
        }).join("")}</div>
        <div class="flex-1"></div>
        <a href="#/settings" class="mt-3 flex-none ${navCls(path.startsWith("/settings"))}" title="Cài đặt: loại giấy tờ và trường, người dùng, máy chủ">${icon("settings", { fill: path.startsWith("/settings"), size: 20 })}Cài đặt</a>
        <div class="flex items-start gap-3 px-4 py-2">${icon("cloud_done", { size: 20, cls: "text-muted" })}<span class="min-w-0 text-xs text-muted"><span class="block font-medium text-fg">Đã kết nối</span><span class="block truncate">prototype · dữ liệu mẫu</span></span></div>
      </div>`;
    return `
      <div class="flex h-full flex-col bg-bg">
        <header class="flex h-16 flex-none items-center gap-2 pr-3 pl-2 md:pl-4">
          <button class="btn-icon md:hidden" onclick="${on(() => document.getElementById("drawer").classList.toggle("hidden"))}" aria-label="Menu">${icon("menu")}</button>
          <a href="#/documents" class="flex w-59 flex-none items-center gap-2.5 pl-1 text-[22px] text-muted max-md:w-auto">${logo()}<span class="max-sm:hidden">BePaylot</span></a>
          <form class="group flex h-12 max-w-180 min-w-0 flex-1 items-center gap-2 rounded-full bg-surface-3 px-2 transition-shadow focus-within:bg-surface focus-within:shadow-1" onsubmit="${on((e) => (e.preventDefault(), (S.q = e.target.q.value.trim()), go("#/documents"), render()))}">
            <button type="submit" class="btn-icon" aria-label="Tìm">${icon("search")}</button>
            <input name="q" value="${esc(S.q ?? "")}" placeholder="Tìm trong ${esc(kb().name)}" class="min-w-0 flex-1 bg-transparent text-base text-fg outline-none placeholder:text-muted">
          </form>
          <div class="flex-1 max-md:hidden"></div>
          <button onclick="${on((e) => openMenu(e, [
            { icon: "account_circle", label: S.user.email, disabled: true },
            { divider: true },
            { icon: "manage_accounts", label: "Thông tin", onClick: () => openInfo("account") },
            { icon: "account_balance_wallet", label: "Chi phí AI", onClick: () => openInfo("keys") },
            { icon: "logout", label: "Đăng xuất", onClick: () => go("#/login") },
          ], "right"))}" class="grid size-8 flex-none cursor-pointer place-items-center rounded-full bg-accent text-sm font-medium text-on-accent ring-offset-2 ring-offset-bg hover:ring-4 hover:ring-fg/8" title="${esc(S.user.name)}">${S.user.name[0]}</button>
        </header>
        <div class="flex min-h-0 flex-1">
          <aside class="hidden md:block">${sidebar}</aside>
          <div id="drawer" class="fixed inset-0 z-700 hidden md:hidden" onclick="this.classList.add('hidden')"><div class="absolute inset-0 bg-(--scrim)"></div><div class="absolute inset-y-0 left-0 rounded-r-2xl bg-bg pt-4 shadow-3" onclick="event.stopPropagation()">${sidebar}</div></div>
          <main class="mr-0 mb-0 flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-none bg-surface md:mr-4 md:mb-4 md:rounded-2xl">${content}</main>
        </div>
      </div>`;
  }
  const topBar = (title, subtitle, right = "") =>
    `<header class="flex min-h-16 flex-none flex-wrap items-center gap-2 px-6 pt-3 pb-2"><div class="min-w-0 flex-1"><h1 class="truncate text-[22px] leading-7 font-normal text-fg">${title}</h1>${subtitle ? `<div class="truncate text-xs text-muted">${subtitle}</div>` : ""}</div><div class="flex flex-wrap items-center gap-1">${right}</div></header>`;
  // Lối quay lại: mỗi màn (đường dẫn không tính query) là một mục của trail.
  // Đổi query trên cùng màn chỉ cập nhật mục đó; về lại một màn đã có trong
  // trail thì cắt bỏ phần sau nó; vào màn gốc (mục ở thanh bên) thì bắt đầu
  // trail mới. Nút quay lại về đúng trạng thái của màn trước (cả file, mục,
  // trang đang chọn); mở thẳng bằng link thì về màn cha mặc định.
  const trail = [];
  const isRoot = (key) => /^#\/(documents|chat(\/[^/]+)?|cases(\/[^/]+)?|settings(\/[^/]+)?|login)$/.test(key);
  function track() {
    const hash = location.hash || "#/documents", key = hash.split("?")[0];
    if (isRoot(key)) return void trail.splice(0, trail.length, { key, hash });
    const i = trail.findIndex((x) => x.key === key);
    if (i >= 0) (trail.length = i + 1), (trail[i].hash = hash);
    else trail.push({ key, hash });
  }
  // Tên của màn để ghi trên nút quay lại.
  function screenLabel(hash) {
    const p = hash.slice(2).split("?")[0].split("/");
    if (p[0] === "documents") return p[1] ? docById(p[1])?.file_name ?? "Tài liệu" : "Tài liệu";
    if (p[0] === "cases" && p[2] === "checks") return "Kết quả đối chiếu";
    if (p[0] === "cases" && p[2] === "sheets") return M.SHEETS.find((x) => x.id === p[3])?.name ?? "Bảng";
    if (p[0] === "cases" && p[2] === "split") return "Tách & gom trang";
    if (p[0] === "cases") return "Hồ sơ " + (caseById(p[1])?.code ?? "");
    if (p[0] === "settings" && p[1] === "doctypes" && p[2]) return M.DOC_TYPES.find((t) => t.name === p[2])?.title ?? "Loại giấy tờ";
    if (p[0] === "settings" && p[1] === "checks" && p[2]) return setById(p[2])?.name ?? "Bộ quy tắc";
    if (p[0] === "settings") return "Cài đặt";
    if (p[0] === "chat") return "Hỏi đáp";
    return "Quay lại";
  }
  // Đích của nút quay lại: màn trước trong trail, không có thì màn cha mặc định.
  const backOf = (fallback) => { const prev = trail.at(-2); return prev ? [prev.hash, screenLabel(prev.hash)] : [fallback, screenLabel(fallback)]; };
  const subHeader = (back, ic, title, sub, right) => {
    const [href, label] = backOf(back);
    return `<header class="flex min-h-16 flex-none flex-wrap items-center gap-2 px-4 pt-3 pb-2">
      <a class="btn btn-text max-w-64" href="${href}" title="Quay lại ${esc(label)}">${icon("arrow_back", { size: 20 })}<span class="truncate">${esc(label)}</span></a>
      <span class="h-6 border-l border-line max-sm:hidden"></span>
      <div class="min-w-0 flex-1 px-2"><h1 class="flex items-center gap-2 truncate text-[22px] leading-7 font-normal text-fg">${ic} ${title}</h1><div class="truncate text-xs text-muted">${sub}</div></div>${right}</header>`;
  };
  const empty = (ic, title, body = "") =>
    `<div class="empty">${ic ? `<div class="mb-4 grid size-24 place-items-center rounded-full bg-surface-2">${icon(ic, { size: 44, cls: "text-subtle" })}</div>` : ""}${title ? `<div class="mb-1 text-[18px] text-fg">${title}</div>` : ""}${body ? `<div class="max-w-115 text-sm">${body}</div>` : ""}</div>`;

  // Chip chọn hồ sơ (CasePicker).
  function casePicker(cur, onPick, disabled) {
    return `<button ${disabled ? "disabled" : ""} class="chip max-w-72 ${cur ? "chip-on" : ""}" title="${disabled ? "Cuộc trò chuyện đã gắn với hồ sơ này" : "Chọn hồ sơ"}" onclick="${on((e) =>
      openMenu(e, cases().map((c) => ({ checked: c.id === cur?.id, label: `<span class="block truncate text-sm text-fg">${caseLabel(c)}</span><span class="block truncate text-xs text-muted">${esc(c.title ? c.title + " · " : "")}${docsOf(c.id).length} file${c.case_type ? " · " + c.case_type : ""}</span>`, onClick: () => onPick(c) }))),
    )}">${icon("folder_open", { size: 18 })}<span class="truncate">${cur ? caseLabel(cur) : "Chọn hồ sơ"}</span>${disabled ? "" : icon("arrow_drop_down", { size: 18, cls: "-mr-1" })}</button>`;
  }

  // ------------------------------------------------------------ login
  function viewLogin() {
    const nodes = [[12, 18], [30, 62], [58, 30], [78, 70], [86, 22], [46, 84]];
    return `<div class="flex h-full bg-bg">
      <div class="relative hidden flex-1 overflow-hidden bg-surface-2 lg:block">
        <svg class="absolute inset-0 size-full" aria-hidden="true">${nodes.map(([x, y], i) => nodes.slice(i + 1, i + 3).map(([x2, y2]) => `<line class="login-line" x1="${x}%" y1="${y}%" x2="${x2}%" y2="${y2}%" stroke="var(--outline)" stroke-opacity=".35"/>`).join("")).join("")}${nodes.map(([x, y]) => `<circle cx="${x}%" cy="${y}%" r="6" fill="var(--accent)" fill-opacity=".5"/>`).join("")}</svg>
        <div class="relative flex h-full flex-col justify-center gap-6 px-16">
          <div class="flex items-center gap-3 text-[26px] text-fg">${logo("size-11")} BePaylot</div>
          <p class="max-w-md text-[32px] leading-10 text-fg">Đọc hồ sơ tín dụng như một chuyên viên: đọc tài liệu, lập mục lục, hỏi đáp có trích dẫn.</p>
          <div class="flex flex-col gap-2 text-sm text-muted">${["Mục lục tự dựng cho từng hồ sơ", "Trả lời kèm vùng trên trang gốc", "Xuất Excel và đối chiếu chứng từ"].map((t) => `<span class="flex items-center gap-2">${icon("check_circle", { size: 18, fill: true, cls: "text-ok" })}${t}</span>`).join("")}</div>
        </div>
      </div>
      <div class="flex w-full flex-col items-center justify-center px-6 lg:w-[520px]">
        <form class="flex w-full max-w-sm flex-col gap-4" onsubmit="event.preventDefault();location.hash='#/documents'">
          <div class="lg:hidden">${logo("size-11")}</div>
          <h1 class="text-[28px] leading-9 font-normal text-fg">Đăng nhập</h1>
          <p class="-mt-2 text-sm text-muted">Dùng email công ty hoặc đăng nhập một lần (SSO).</p>
          <label class="field">Email<input class="input h-12" value="thanh@bepaylot.vn" placeholder="ban@congty.vn"></label>
          <label class="field">Mật khẩu<input class="input h-12" type="password" value="password" placeholder="Nhập mật khẩu"></label>
          <button class="btn btn-primary h-12">Đăng nhập</button>
          <div class="flex items-center gap-3 text-xs text-subtle"><span class="h-px flex-1 bg-line"></span>hoặc<span class="h-px flex-1 bg-line"></span></div>
          <button type="button" class="btn h-12" onclick="location.hash='#/documents'">${icon("key", { size: 18 })} Đăng nhập bằng SSO</button>
          <p class="text-center text-sm text-muted">Chưa có tài khoản? <a href="#/login">Tạo tài khoản</a></p>
        </form>
      </div></div>`;
  }

  // ------------------------------------------------------------ documents
  const STATUS_FILTERS = [["busy", "Đang xử lý"], ["completed", "Hoàn tất"], ["partial", "Một phần"], ["not_started", "Chưa xử lý"], ["failed", "Lỗi"]];
  function docActions(d) {
    return [
      { icon: "open_in_new", label: "Mở", onClick: () => go("#/documents/" + d.id) },
      { icon: "download", label: "Tải xuống", onClick: () => toast("Prototype: không có file gốc để tải") },
      { icon: "forum", label: "Hỏi đáp về thư viện", onClick: () => go("#/chat") },
      { divider: true },
      { icon: "refresh", label: "Đọc lại", onClick: () => openReparse(d) },
      { icon: "delete", label: "Xoá", danger: true, onClick: () => confirm(`Xoá “${d.file_name}”? Không thể hoàn tác.`) && toast("Đã xoá 1 file") },
    ];
  }
  const caseChip = (c) =>
    !c ? '<span class="text-subtle">—</span>'
      : `<button class="inline-flex h-6 max-w-44 items-center gap-1 rounded-md px-2 text-xs ${c.code === "_UNASSIGNED" ? "bg-surface-3 text-muted" : "bg-accent-soft text-on-accent-soft"}" title="${esc(c.title)} · Lọc theo hồ sơ này" onclick="${on((e) => (e.stopPropagation(), (S.docFilter.case = c.id), render()))}">${icon("folder_open", { size: 14 })}<span class="truncate">${caseLabel(c)}</span></button>`;
  function viewDocuments() {
    const f = S.docFilter;
    let list = M.DOCS.filter((d) => d.kb_id === S.kb);
    if (f.case) list = list.filter((d) => d.case_id === f.case);
    if (f.status) list = list.filter((d) => (f.status === "busy" ? !TERMINAL.has(d.status) : d.status === f.status));
    if (f.type === "pdf") list = list.filter((d) => d.mime_type === "application/pdf");
    if (f.type === "image") list = list.filter((d) => d.mime_type.startsWith("image/"));
    if (S.q) list = list.filter((d) => (d.file_name + " " + (d.title ?? "")).toLowerCase().includes(S.q.toLowerCase()));
    const key = { name: (d) => d.file_name.toLowerCase(), date: (d) => d.created_at, size: (d) => d.size_bytes, pages: (d) => d.page_count }[S.sort.by];
    const dir = S.sort.desc ? -1 : 1;
    list.sort((a, b) => (key(a) < key(b) ? -dir : key(a) > key(b) ? dir : 0));
    const k = kb();
    const sortTh = (by, label, cls = "") => {
      const a = S.sort.by === by;
      return `<th class="h-12 border-b border-line px-3 font-medium whitespace-nowrap ${cls}"><button class="flex items-center gap-1 rounded-full py-1 hover:text-fg ${a ? "text-fg" : ""}" onclick="${on(() => ((S.sort = { by, desc: a ? !S.sort.desc : by !== "name" }), render()))}">${label}${a ? icon(S.sort.desc ? "arrow_downward" : "arrow_upward", { size: 16 }) : ""}</button></th>`;
    };
    const chipMenu = (label, active, items) =>
      `<button class="chip ${active ? "chip-on" : ""}" onclick="${on((e) => openMenu(e, items))}">${active ? icon("check", { size: 18 }) : ""}${label}${icon("arrow_drop_down", { size: 20, cls: "-mr-1.5" })}</button>`;
    const filtered = f.case || f.status || f.type || S.q;
    const progress = (d) => (TERMINAL.has(d.status) || !d.page_count ? "" : `<div class="mt-1 h-1 w-24 overflow-hidden rounded-full bg-accent/20"><div class="h-full rounded-full bg-accent" style="width:${Math.round((100 * d.pages_done) / d.page_count)}%"></div></div>`);
    const rows = list.map((d) => {
      const td = "border-b border-line px-3 py-2 align-middle";
      return `<tr class="group cursor-default hover:bg-surface-2" onclick="location.hash='#/documents/${d.id}'">
        <td class="${td} rounded-l-lg pl-4"><div class="flex items-center gap-4">${fileIcon(d.file_name)}<div class="min-w-0"><div class="max-w-105 truncate font-medium text-fg">${esc(d.file_name)}</div>${d.title ? `<div class="max-w-105 truncate text-xs text-muted">${esc(d.title)}</div>` : ""}</div></div></td>
        <td class="${td} whitespace-nowrap">${caseChip(caseById(d.case_id))}</td>
        <td class="${td}">${statusBadge(d.status)}${progress(d)}${d.error ? `<div class="mt-0.5 max-w-50 truncate text-xs text-err">${esc(d.error)}</div>` : ""}</td>
        <td class="${td} text-muted max-lg:hidden">${d.page_count || "—"}${d.pages_failed ? `<span class="badge badge-err ml-1.5">${d.pages_failed} lỗi</span>` : ""}${d.status !== "not_started" && pagesWith(d, "-").length ? `<span class="badge ml-1.5" title="Trang chưa xử lý: ${spans(pagesWith(d, "-"))}">${pagesWith(d, "-").length} chưa xử lý</span>` : ""}</td>
        <td class="${td} max-xl:hidden"><div class="flex max-w-80 flex-wrap gap-1">${metaChips(d.metadata)}</div></td>
        <td class="${td} whitespace-nowrap text-muted max-md:hidden">${fmtDate(d.created_at)}</td>
        <td class="${td} whitespace-nowrap text-muted max-md:hidden">${fmtSize(d.size_bytes)}</td>
        <td class="${td} rounded-r-lg pr-2" onclick="event.stopPropagation()"><button class="btn-icon btn-sm opacity-0 group-hover:opacity-100" title="Thao tác khác" onclick="${on((e) => openMenu(e, docActions(d), "right"))}">${icon("more_vert", { size: 20 })}</button></td></tr>`;
    });
    const grid = list.map((d) => {
      const [label, tone] = STATUS[d.status];
      const img = d.page_count && d.status !== "queued";
      return `<div class="group flex cursor-pointer flex-col rounded-xl bg-surface-2 p-1.5 pt-0 transition-colors hover:bg-surface-3" onclick="location.hash='#/documents/${d.id}'">
        <div class="flex h-12 items-center gap-3 pr-0 pl-2.5">${fileIcon(d.file_name, 20)}<span class="min-w-0 flex-1 truncate text-sm font-medium">${esc(d.file_name)}</span><span onclick="event.stopPropagation()"><button class="btn-icon btn-sm" onclick="${on((e) => openMenu(e, docActions(d), "right"))}">${icon("more_vert", { size: 20 })}</button></span></div>
        <div class="relative aspect-4/3 overflow-hidden rounded-lg bg-surface">${img ? pageSVG(d, 1) : `<div class="grid size-full place-items-center">${fileIcon(d.file_name, 64)}</div>`}${!TERMINAL.has(d.status) || tone === "err" || tone === "warn" ? `<div class="absolute right-2 bottom-2">${statusBadge(d.status)}</div>` : ""}</div>
        <div class="flex items-center gap-2 px-2.5 pt-2 pb-1.5 text-xs text-muted">${caseChip(caseById(d.case_id))}<span class="min-w-0 flex-1 truncate">${esc(d.title || label)}</span><span class="whitespace-nowrap">${d.page_count ? d.page_count + " trang" : ""}</span></div></div>`;
    });
    return `${topBar(esc(k.name), esc(k.description), `
        <div class="mr-2 flex rounded-full border border-outline" role="group">${["list", "grid"].map((v) => `<button onclick="${on(() => ((S.docView = v), render()))}" title="${v === "list" ? "Dạng danh sách" : "Dạng lưới"}" class="flex h-8 w-14 items-center justify-center first:rounded-l-full last:rounded-r-full ${S.docView === v ? "bg-accent-soft text-on-accent-soft" : "text-muted hover:bg-fg/8"}">${S.docView === v ? icon("check", { size: 18 }) : ""}${icon(v === "list" ? "view_list" : "grid_view", { size: 18 })}</button>`).join("")}</div>
        <button class="btn-icon" title="Làm mới" onclick="${on(render)}">${icon("refresh")}</button>`)}
      <div class="flex flex-none flex-wrap items-center gap-2 px-6 pb-3">
        <button class="chip ${f.case ? "chip-on" : ""}" onclick="${on((e) => openMenu(e, [{ label: "Tất cả hồ sơ", checked: !f.case, onClick: () => ((f.case = ""), render()) }, { divider: true }, ...cases().map((c) => ({ label: `${caseLabel(c)} · ${docsOf(c.id).length} file`, checked: f.case === c.id, onClick: () => ((f.case = c.id), render()) }))]))}">${icon("folder_open", { size: 18 })}${f.case ? caseLabel(caseById(f.case)) : "Hồ sơ"}${icon("arrow_drop_down", { size: 20, cls: "-mr-1.5" })}</button>
        ${chipMenu(f.type ? (f.type === "pdf" ? "PDF" : "Hình ảnh") : "Loại", f.type, [{ icon: "picture_as_pdf", label: "PDF", onClick: () => ((f.type = "pdf"), render()) }, { icon: "image", label: "Hình ảnh", onClick: () => ((f.type = "image"), render()) }, ...(f.type ? [{ divider: true }, { icon: "close", label: "Bỏ lọc", onClick: () => ((f.type = ""), render()) }] : [])])}
        ${chipMenu(STATUS_FILTERS.find(([v]) => v === f.status)?.[1] ?? "Trạng thái", f.status, [...STATUS_FILTERS.map(([v, l]) => ({ label: l, checked: f.status === v, onClick: () => ((f.status = v), render()) })), ...(f.status ? [{ divider: true }, { icon: "close", label: "Bỏ lọc", onClick: () => ((f.status = ""), render()) }] : [])])}
        <button class="chip" onclick="${on(() => toast("Lọc theo thông tin thêm, ví dụ mã nhóm G1"))}">Thông tin thêm${icon("arrow_drop_down", { size: 20, cls: "-mr-1.5" })}</button>
        ${S.q ? `<button class="chip chip-on" onclick="${on(() => ((S.q = ""), render()))}">${icon("search", { size: 18 })} “${esc(S.q)}” ${icon("close", { size: 18, cls: "-mr-1" })}</button>` : ""}
        ${f.case || f.status || f.type ? `<button class="btn btn-text btn-sm" onclick="${on(() => ((S.docFilter = { case: "", status: "", type: "" }), render()))}">Xoá bộ lọc</button>` : ""}
      </div>
      <div class="min-h-0 flex-1 overflow-auto px-3 pb-4">
        ${!list.length ? empty("cloud_upload", filtered ? "Không có file nào khớp" : "Thả file vào đây", filtered ? "Thử bỏ bớt bộ lọc." : "hoặc dùng nút “Mới” để tải PDF, ảnh scan lên thư viện này.")
        : S.docView === "list" ? `<table class="w-full border-collapse text-sm"><thead><tr class="text-left text-[13px] font-medium text-muted">${sortTh("name", "Tên", "pl-4")}<th class="h-12 border-b border-line px-3 font-medium">Hồ sơ</th><th class="h-12 border-b border-line px-3 font-medium">Trạng thái</th>${sortTh("pages", "Trang", "max-lg:hidden")}<th class="h-12 border-b border-line px-3 font-medium max-xl:hidden">Thông tin thêm</th>${sortTh("date", "Ngày tải lên", "max-md:hidden")}${sortTh("size", "Kích thước", "max-md:hidden")}<th class="h-12 w-12 border-b border-line"></th></tr></thead><tbody>${rows.join("")}</tbody></table>`
        : `<div class="grid grid-cols-[repeat(auto-fill,minmax(220px,1fr))] gap-4 px-3 pt-1">${grid.join("")}</div>`}
      </div>`;
  }

  // Hai bước: 1. tải file lên; 2. chọn file và khoảng trang cần đọc & phân loại.
  const stepper = (n) => `<div class="flex items-center gap-2 text-xs font-medium">${["Tải file lên", "Chọn trang cần xử lý"].map((l, i) => `<span class="flex items-center gap-1.5 ${i + 1 === n ? "text-fg" : "text-muted"}"><span class="grid size-5 place-items-center rounded-full text-[11px] ${i + 1 < n ? "bg-accent text-on-accent" : i + 1 === n ? "bg-accent-soft text-on-accent-soft" : "bg-surface-3"}">${i + 1 < n ? icon("check", { size: 14 }) : i + 1}</span>${l}</span>`).join('<span class="h-px w-6 bg-line"></span>')}</div>`;
  const UPLOAD = Array.from({ length: 100 }, (_, i) => {
    const kind = ["HoaDon", "UNC", "BienBan", "HopDong", "SaoKe"][i % 5];
    return [`${kind}_${String(i + 1).padStart(3, "0")}.${i % 7 === 3 ? "jpg" : "pdf"}`, 180_000 + ((i * 7919) % 40) * 61_000, i % 7 === 3 ? 1 : 1 + ((i * 37) % 14)];
  });
  function openUpload() {
    let code = "RT112233";
    const files = UPLOAD;
    modal({
      title: "Tải file lên",
      icon: "upload_file",
      width: 620,
      body: `${stepper(1)}<p class="text-sm">Vào thư viện <b class="font-medium text-fg">${esc(kb().name)}</b></p>
        <div class="grid grid-cols-[1.4fr_1fr] gap-2">
          <label class="field">Mã hồ sơ<input class="input" list="case-codes" value="${code}" placeholder="VD: RT112233"><datalist id="case-codes">${cases().filter((c) => c.code !== "_UNASSIGNED").map((c) => `<option value="${c.code}">${esc(c.title)}</option>`).join("")}</datalist></label>
          <label class="field">Loại hồ sơ<select class="input" disabled><option>vay_von_luu_dong</option></select></label>
        </div>
        <p class="-mt-2 text-xs text-subtle">Thêm file vào hồ sơ có sẵn.</p>
        <div class="flex cursor-pointer flex-col items-center gap-2 rounded-2xl border-2 border-dashed border-line px-6 py-7 text-center text-muted hover:bg-fg/4">${icon("cloud_upload", { size: 36, cls: "text-accent" })}<div class="text-sm">Kéo thả file vào đây hoặc <span class="font-medium text-accent">chọn từ máy</span></div><div class="text-xs text-subtle">PDF, ảnh scan (JPG, PNG, TIFF) · chọn được nhiều file</div></div>
        <div class="overflow-hidden rounded-xl border border-line"><div class="flex h-10 items-center bg-surface-2 px-4 text-xs font-medium"><span class="flex-1">${files.length} file</span><span>${fmtSize(files.reduce((a, f) => a + f[1], 0))}</span></div>
          <div class="max-h-60 overflow-auto">${files.map(([n, s]) => `<div class="flex h-11 items-center gap-3 border-t border-line pr-1 pl-4 text-sm text-fg">${fileIcon(n, 20)}<span class="flex-1 truncate">${n}</span><span class="text-xs text-muted">${fmtSize(s)}</span><button class="btn-icon btn-sm">${icon("close", { size: 18 })}</button></div>`).join("")}</div></div>
        <div class="flex flex-col gap-2"><div class="flex items-center"><span class="flex-1 text-sm font-medium text-fg">Thông tin thêm</span><button class="btn btn-text btn-sm">${icon("add", { size: 18 })} Thêm trường</button></div>
          <div class="grid grid-cols-[1fr_1.4fr_40px] items-center gap-2"><input class="input" placeholder="Khoá" value="group_code"><input class="input" placeholder="Giá trị" value="G1"><button class="btn-icon">${icon("remove_circle_outline", { size: 20 })}</button></div>
          <p class="text-xs text-subtle">Áp dụng cho mọi file trong lần tải này (tuỳ chọn).</p></div>
        <label class="field">Callback URL (tuỳ chọn)<input class="input" type="url" placeholder="https://… nhận POST khi xử lý xong"></label>`,
      footer: `<button class="btn btn-text" onclick="A.closeModal()">Huỷ</button><button class="btn btn-primary" onclick="${on(() =>
        pickPages({
          title: "Tải file lên",
          head: `${stepper(2)}<p class="text-sm">Đã tải lên <b class="font-medium text-fg">${files.length} file</b>. Bỏ tick file không cần xử lý, sửa khoảng trang của từng file nếu chỉ cần một phần.</p>`,
          files: files.map(([name, , pages]) => ({ name, pages })),
          later: true,
          onOk: (sel, nPages) => toast(`Đang đọc & phân loại ${sel.length} file (${nPages} trang)${sel.length < files.length ? ` · ${files.length - sel.length} file để Chưa xử lý` : ""}`),
        }))}">Tải lên ${files.length} file</button>`,
    });
  }

  // Chọn file (mặc định chọn hết) và khoảng trang riêng của từng file: từ
  // trang … đến trang …, mặc định toàn bộ. Thanh áp dụng hàng loạt điền một
  // khoảng cho mọi file đang tick; sau đó vẫn sửa riêng từng file được.
  // Dùng cho bước 2 của tải lên và cho Đọc lại.
  function pickPages({ title, icon: ic = "upload_file", head = "", files, range, later, ok = "Đọc & phân loại", onOk }) {
    const st = { sel: new Set(files.map((_, i) => i)), r: files.map((f) => (files.length === 1 && range ? [...range] : [1, f.pages])), q: "", last: null };
    const one = files.length === 1;
    // Sai: khoảng không hợp lệ. Không khớp số trang của file: thiếu (không có
    // trang bắt đầu) hoặc vượt (đến trang lớn hơn trang cuối). Cả hai chặn nút
    // chính đến khi người dùng sửa tay hoặc dùng nút sửa nhanh trên băng cảnh báo.
    const bad = (i) => { const [a, z] = st.r[i]; return a < 1 || a > z; };
    const missing = (i) => !bad(i) && st.r[i][0] > files[i].pages;
    const over = (i) => !bad(i) && !missing(i) && st.r[i][1] > files[i].pages;
    const count = (i) => (bad(i) || missing(i) ? 0 : Math.min(st.r[i][1], files[i].pages) - st.r[i][0] + 1);
    const total = () => [...st.sel].reduce((a, i) => a + count(i), 0);
    const visible = () => files.map((_, i) => i).filter((i) => !st.q || files[i].name.toLowerCase().includes(st.q.toLowerCase()));
    const num = (i, k) => `<input type="number" min="1" max="${files[i].pages}" data-r="${i}:${k}" value="${st.r[i][k]}" aria-label="${k ? "Đến trang" : "Từ trang"}" class="input h-8 w-14 px-1 text-center disabled:opacity-40">`;
    const row = (f, i) => `<div data-row="${i}" class="flex min-h-11 items-center gap-3 px-3 py-1 text-sm ${one ? "" : "border-t border-line"}">
        ${one ? "" : `<input type="checkbox" data-i="${i}" class="size-4 accent-accent" aria-label="Chọn ${esc(f.name)}">`}${fileIcon(f.name, 18)}
        <span class="min-w-0 flex-1"><span class="block truncate">${esc(f.name)}</span>${f.note ? `<span class="block text-xs text-muted">${f.note}</span>` : ""}<span data-note class="block text-xs text-warn"></span></span>
        <span class="flex items-center gap-1.5 text-xs whitespace-nowrap text-muted">Từ trang ${num(i, 0)} đến ${num(i, 1)} <span class="w-9">/ ${f.pages}</span></span></div>`;
    const rows = () => visible().map((i) => row(files[i], i)).join("") || '<div class="border-t border-line px-3 py-4 text-center text-xs text-subtle">Không có file nào khớp</div>';
    modal({
      title,
      icon: ic,
      width: 680,
      body: `${head}
        ${one ? `<div class="rounded-xl border border-line">${row(files[0], 0)}</div><p class="-mt-2 text-xs text-subtle">Trang ngoài khoảng giữ nguyên kết quả cũ.</p>`
          : `<input id="pp-q" class="input" placeholder="Tìm file theo tên, rồi tick đầu bảng để chọn các file đang hiện">
        <div class="overflow-hidden rounded-xl border border-line">
          <div class="flex h-10 items-center gap-3 bg-surface-2 px-3 text-xs font-medium"><input type="checkbox" data-all class="size-4 accent-accent" aria-label="Chọn các file đang hiện"><span id="pp-head" class="flex-1"></span><span>Trang cần xử lý</span></div>
          <div id="pp-bulk" class="flex flex-wrap items-center gap-2 border-t border-line bg-accent-soft/40 px-3 py-2 text-xs text-fg">
            <span>Từ trang</span><input id="pp-bf" type="number" min="1" value="1" class="input h-8 w-14 px-1 text-center"><span>đến</span><input id="pp-bt" type="number" min="1" value="3" class="input h-8 w-14 px-1 text-center">
            <button id="pp-apply" class="btn btn-tonal btn-sm"></button><span class="flex-1"></span><button id="pp-reset" class="btn btn-text btn-sm">Đặt lại toàn bộ trang</button></div>
          <div id="pp-rows" class="max-h-80 overflow-auto">${rows()}</div></div>
        <p class="-mt-2 text-xs text-subtle">Mặc định xử lý toàn bộ trang của mỗi file. Giữ Shift khi tick để chọn liền một dải file. File không chọn vẫn được lưu ở trạng thái “Chưa xử lý”.</p>`}
        <div id="pp-warn" class="hidden rounded-xl bg-warn-soft px-4 py-3 text-sm text-fg"></div>
        <p class="text-xs text-subtle">Bảng Excel chỉ lấy thông tin từ các trang đã phân loại.</p>`,
      footer: `${later ? `<button class="btn btn-text" onclick="${on(() => (closeModal(), toast(`Đã lưu ${files.length} file ở trạng thái Chưa xử lý`)))}">Để sau</button>` : '<button class="btn btn-text" onclick="A.closeModal()">Huỷ</button>'}<button id="pp-ok" class="btn btn-primary" onclick="${on(() => {
        const sel = [...st.sel].map((i) => ({ ...files[i], range: [st.r[i][0], Math.min(st.r[i][1], files[i].pages)] }));
        const n = total();
        closeModal();
        onOk(sel, n);
      })}"></button>`,
    });
    const m = document.getElementById("modal");
    // Chỉ sửa đúng dòng thay đổi, để danh sách dài không bị cuộn về đầu và ô đang gõ không mất con trỏ.
    const paint = (i) => {
      const el = m.querySelector(`[data-row="${i}"]`);
      if (!el) return;
      const on_ = st.sel.has(i);
      el.classList.toggle("text-fg", on_);
      el.classList.toggle("text-subtle", !on_);
      el.classList.toggle("bg-warn-soft/50", on_ && (missing(i) || over(i)));
      const cb = el.querySelector("[data-i]");
      if (cb) cb.checked = on_;
      el.querySelectorAll("[data-r]").forEach((x) => {
        const k = +x.dataset.r.split(":")[1];
        if (document.activeElement !== x) x.value = st.r[i][k];
        x.disabled = !on_;
        x.classList.toggle("border-err", on_ && bad(i));
        x.classList.toggle("border-warn", on_ && (k ? over(i) : missing(i)));
      });
      el.querySelector("[data-note]").textContent = !on_ ? "" : missing(i) ? `Không có trang ${st.r[i][0]} — file chỉ có ${files[i].pages} trang` : over(i) ? `Không có trang ${files[i].pages + 1}–${st.r[i][1]} — file chỉ có ${files[i].pages} trang` : "";
    };
    const refresh = () => {
      const vis = visible(), nVis = vis.filter((i) => st.sel.has(i)).length;
      const all = m.querySelector("[data-all]");
      if (all) (all.checked = vis.length > 0 && nVis === vis.length), (all.indeterminate = nVis > 0 && nVis < vis.length);
      const head = m.querySelector("#pp-head");
      if (head) head.textContent = `Đã chọn ${st.sel.size}/${files.length} file · ${total()} trang${st.q ? ` · đang hiện ${vis.length} file` : ""}`;
      const bulk = m.querySelector("#pp-bulk");
      if (bulk) {
        bulk.classList.toggle("hidden", !st.sel.size);
        const bf = +m.querySelector("#pp-bf").value, bt = +m.querySelector("#pp-bt").value, ap = m.querySelector("#pp-apply");
        ap.disabled = !(bf >= 1 && bt >= bf);
        ap.textContent = `Áp dụng cho ${st.sel.size} file đã chọn`;
      }
      const wrong = [...st.sel].filter(bad), miss = [...st.sel].filter(missing), ov = [...st.sel].filter(over), n = total(), b = m.querySelector("#pp-ok");
      const warn = m.querySelector("#pp-warn");
      warn.classList.toggle("hidden", !miss.length && !ov.length);
      warn.innerHTML = `<div class="flex gap-2">${icon("warning", { size: 20, cls: "text-warn" })}<div class="min-w-0 flex-1"><div class="font-medium">${one ? "Khoảng trang không khớp số trang của file" : `${miss.length + ov.length} file không khớp số trang`}</div>
        <div class="mt-0.5 text-xs text-muted">${[ov.length && `${one ? "Đến trang" : `${ov.length} file có “đến trang”`} lớn hơn trang cuối`, miss.length && `${one ? "Không có" : `${miss.length} file không có`} trang bắt đầu`].filter(Boolean).join(" · ")}. Sửa ở dòng tô vàng hoặc:</div>
        <div class="mt-2 flex flex-wrap gap-2">${ov.length ? `<button data-fix="over" class="btn btn-sm bg-surface">Lấy tới trang cuối${one ? "" : ` cho ${ov.length} file`}</button>` : ""}${miss.length && !one ? `<button data-fix="missing" class="btn btn-sm bg-surface">Bỏ chọn ${miss.length} file không có trang bắt đầu</button>` : ""}</div></div></div>`;
      b.disabled = !!(wrong.length || miss.length || ov.length) || !n;
      b.textContent = wrong.length ? `Sửa khoảng trang của ${wrong.length} file`
        : miss.length || ov.length ? (one ? "Khoảng trang không khớp" : `${miss.length + ov.length} file không khớp số trang`)
        : one ? `${ok} ${n} trang` : `${ok} ${st.sel.size} file · ${n} trang`;
    };
    m.addEventListener("click", (e) => {
      const t = e.target;
      // Shift + tick: đặt cả dải file đang hiện từ lần tick trước tới file này theo cùng trạng thái.
      if (t.dataset.i !== undefined) {
        const i = +t.dataset.i, vis = visible();
        const span = e.shiftKey && st.last !== null && vis.includes(st.last) ? vis.slice(Math.min(vis.indexOf(st.last), vis.indexOf(i)), Math.max(vis.indexOf(st.last), vis.indexOf(i)) + 1) : [i];
        for (const j of span) (t.checked ? st.sel.add(j) : st.sel.delete(j)), paint(j);
        st.last = i;
      } else if (t.dataset.all !== undefined) {
        for (const i of visible()) (t.checked ? st.sel.add(i) : st.sel.delete(i)), paint(i);
      } else if (t.closest("#pp-apply")) {
        const bf = +m.querySelector("#pp-bf").value, bt = +m.querySelector("#pp-bt").value;
        for (const i of st.sel) (st.r[i] = [bf, bt]), paint(i);
        const off = [...st.sel].filter((i) => missing(i) || over(i)).length;
        toast(`Đã áp dụng ${pages(bf, bt)} cho ${st.sel.size} file${off ? ` · ${off} file không khớp số trang` : ""}`);
      } else if (t.closest('[data-fix="over"]')) {
        for (const i of [...st.sel].filter(over)) (st.r[i][1] = files[i].pages), paint(i);
      } else if (t.closest('[data-fix="missing"]')) {
        for (const i of [...st.sel].filter(missing)) st.sel.delete(i), paint(i);
      } else if (t.closest("#pp-reset")) {
        for (const i of st.sel) (st.r[i] = [1, files[i].pages]), paint(i);
      } else return;
      refresh();
    });
    m.addEventListener("input", (e) => {
      const t = e.target;
      if (t.id === "pp-q") {
        st.q = t.value;
        m.querySelector("#pp-rows").innerHTML = rows();
        visible().forEach(paint);
      } else if (t.dataset.r) {
        const [i, k] = t.dataset.r.split(":").map(Number);
        st.r[i][k] = +t.value || 0;
        paint(i);
      } else if (t.id !== "pp-bf" && t.id !== "pp-bt") return;
      refresh();
    });
    files.forEach((_, i) => paint(i));
    refresh();
  }

  // Thư viện mới / sửa thư viện và loại giấy tờ.
  function openKB(k) {
    const used = new Set(k ? k.config.doc_types : []);
    modal({
      title: k ? "Sửa thư viện" : "Thư viện mới",
      icon: k ? "edit" : "create_new_folder",
      width: 600,
      body: `<label class="field">Tên thư viện<input class="input" value="${esc(k?.name ?? "")}" placeholder="VD: Tín dụng doanh nghiệp"></label>
        <label class="field">Mô tả<input class="input" value="${esc(k?.description ?? "")}"></label>
        <fieldset class="flex flex-col gap-1"><legend class="text-sm font-medium text-fg">Loại giấy tờ dùng trong thư viện</legend>
          <p class="mb-1 text-xs">Chọn từ danh mục chung. Trang được phân loại theo các loại đã chọn. Tạo loại mới hoặc sửa trường, hướng dẫn AI ở <a href="#/settings/doctypes" onclick="A.closeModal()">Cài đặt → Loại giấy tờ</a>.</p>
          <div class="grid gap-1 sm:grid-cols-2">${M.DOC_TYPES.map((t) => `<label class="flex h-10 cursor-pointer items-center gap-3 rounded-lg px-2 text-sm text-fg hover:bg-fg/5"><input type="checkbox" ${used.has(t) ? "checked" : ""} data-dt="${t.name}" class="size-4 accent-accent"><i class="size-2.5 flex-none rounded-full" style="background:${typeColor(t.name)}" aria-hidden="true"></i><span class="min-w-0 flex-1 truncate">${esc(t.title)}</span><span class="text-xs text-subtle">${t.fields.length} trường</span></label>`).join("")}</div></fieldset>`,
      footer: `<button class="btn btn-text" onclick="A.closeModal()">Huỷ</button><button class="btn btn-primary" onclick="${on(() => {
        if (k) k.config.doc_types = M.DOC_TYPES.filter((t) => document.querySelector(`#modal [data-dt="${t.name}"]`)?.checked);
        closeModal();
        toast(k ? "Đã lưu thư viện" : "Đã tạo thư viện");
        render();
      })}">${k ? "Lưu" : "Tạo"}</button>`,
    });
  }

  // ------------------------------------------------------------ document detail
  const DTABS = [["info", "Chi tiết"], ["lines", "Dòng"], ["md", "Nội dung"], ["blocks", "Bố cục"], ["find", "Tìm"], ["tree", "Mục lục"]];
  const darkBtn = "btn-icon text-muted disabled:opacity-30";
  function viewDocument(id, q) {
    const d = docById(id);
    if (!d) return empty("error", "Không mở được tài liệu");
    const n = Math.min(Math.max(1, +(q.get("page") || 1)), d.content.length);
    const hl = (q.get("hl") || "").split(",").filter(Boolean).map(Number);
    const tab = q.get("tab") || (hl.length ? "lines" : "info");
    const url = (o = {}) => `#/documents/${id}?page=${o.page ?? n}&tab=${o.tab ?? tab}${o.hl ? "&hl=" + o.hl : ""}`;
    const tree = M.TREES[id] ?? [];
    const groups = tree.filter((x) => x.doc_type);
    const groupOf = (p) => groups.find((g) => p >= g.page_start && p <= g.page_end);
    const ready = (p) => "obmc".includes(pst(d, p));
    // Dải ảnh nhỏ theo giấy tờ: mỗi giấy tờ là một khối có vạch màu theo loại,
    // tên và khoảng trang, ảnh trang xếp hai cột. Số trang và các loại giấy tờ
    // của file hiện ở dòng dưới tên file trên đầu trang.
    const thumb = (p) => {
      const pg = d.content[p - 1];
      return `<a href="${url({ page: p })}" class="group flex flex-col items-center gap-1" title="Trang ${p}"><div class="relative w-full overflow-hidden rounded border-2 ${p === n ? "border-accent" : "border-line group-hover:border-outline"}">${
        pg.failed ? `<div class="grid aspect-3/4 w-full place-items-center bg-surface-3">${icon("error", { cls: "text-err" })}</div>` : ready(p) ? pageSVG(d, p) : pst(d, p) === "-" ? pageSVG(d, p, { cls: "opacity-40" }) : `<div class="grid aspect-3/4 w-full place-items-center bg-surface-3">${spinner("size-4 border-2 ")}</div>`
      }${pst(d, p) === "-" ? '<span class="absolute inset-x-0 bottom-0 bg-black/60 text-center text-[10px] text-white leading-4">chưa xử lý</span>' : pg.blank ? '<span class="absolute inset-x-0 bottom-0 bg-black/60 text-center text-[10px] text-white leading-4">trắng</span>' : ""}</div><span class="text-[11px] tabular-nums ${p === n ? "font-medium text-accent" : "text-muted"}">${p}</span></a>`;
    };
    const blocks = [];
    for (let p = 1; p <= d.content.length; p++) {
      const g = groupOf(p) ?? (pst(d, p) === "-" ? "todo" : null), last = blocks.at(-1);
      if (last && last.g === g) last.pages.push(p);
      else blocks.push({ g, pages: [p] });
    }
    const typeCount = {};
    for (const g of groups) typeCount[g.doc_type] = (typeCount[g.doc_type] ?? 0) + 1;
    const rail = `
      ${blocks.map(({ g, pages: ps }) => {
        const cur = ps.includes(n);
        const color = g?.doc_type ? typeColor(g.doc_type) : "var(--outline)";
        return `<section ${cur ? "data-cur-group" : ""} class="scroll-mt-40 rounded-md border-l-[3px] py-1.5 pr-0.5 pl-1.5 ${cur ? "bg-accent-soft/40" : ""}" style="border-color:${color}">
          <div class="mb-1.5"><div class="truncate text-[11px] leading-4 font-medium ${g === "todo" ? "text-muted" : "text-fg"}" title="${esc(g?.title ?? "")}">${g === "todo" ? "Chưa xử lý" : g ? esc(g.title) : "Chưa phân loại"}</div><div class="truncate text-[10.5px] text-muted">${pages(ps[0], ps.at(-1))}</div></div>
          <div class="grid grid-cols-2 gap-x-1 gap-y-1">${ps.map(thumb).join("")}</div></section>`;
      }).join("")}`;
    const lines = linesOf(d, n);
    const pg = d.content[n - 1];
    const ok = ready(n) && !pg.failed;
    const row = (l, v) => `<div class="py-2"><div class="text-xs text-muted">${l}</div><div class="mt-0.5 text-sm text-fg">${v}</div></div>`;
    let panel = "";
    if (tab === "info")
      panel = `<div class="px-5 py-3">${d.title || d.summary ? `<div class="mb-3 rounded-xl bg-surface-2 p-4">${d.title ? `<div class="font-medium">${esc(d.title)}</div>` : ""}${d.summary ? `<p class="mt-2 font-body text-[13px] leading-5 text-muted">${esc(d.summary)}</p>` : ""}</div>` : ""}
        <h3 class="pt-2 pb-1 text-sm font-medium">Chi tiết tệp</h3>${row("Trạng thái", statusBadge(d.status))}${row("Số trang", d.page_count + (d.pages_failed ? ` <span class="ml-2 text-err">${d.pages_failed} trang lỗi</span>` : ""))}${row("Kích thước", fmtSize(d.size_bytes))}
        ${Object.keys(d.metadata).length ? row("Thông tin thêm", `<span class="flex flex-wrap gap-1.5">${metaChips(d.metadata)}</span>`) : ""}
        ${row("Tải lên", fmtDate(d.created_at))}${d.error ? row("Lỗi", `<span class="text-err">${esc(d.error)}</span>`) : ""}${d.failed_pages ? row("Trang lỗi", d.failed_pages.map((f) => `<a class="chip h-7" title="${esc(f.error)}" href="${url({ page: f.page_no })}">${f.page_no}</a>`).join(" ")) : ""}
        <h3 class="pt-4 pb-1 text-sm font-medium">Trang ${n}</h3>${row("Đã xử lý", `<span class="flex flex-wrap gap-1">${pageSteps(d, n)}</span>`)}${pst(d, n) === "c" && pg.doc_type && pg.doc_type !== "other" ? row("Loại giấy tờ", `${typeTitle(pg.doc_type)} · ${92 + (n % 7)}%`) : ""}</div>`;
    if (tab === "lines")
      panel = !ok ? empty("hourglass_empty", "", pst(d, n) === "-" ? "Trang chưa xử lý" : "Trang chưa sẵn sàng") : lines.map((l) => `<a href="${url({ tab: "lines", hl: l.line_no })}" class="grid grid-cols-[34px_1fr] gap-2 border-b border-line px-5 py-2 text-fg ${hl.includes(l.line_no) ? "bg-warn-soft" : "hover:bg-fg/4"}"><span class="pt-0.5 font-mono text-[11px] text-subtle">L${l.line_no}</span><span class="min-w-0"><span class="block font-body text-[13.5px] ${l.low ? "underline decoration-[#e37400] decoration-wavy underline-offset-3" : ""}">${esc(l.text)}</span><span class="mt-1 flex flex-wrap gap-1">${l.vlm ? '<span class="badge badge-vlm">AI đọc lại</span>' : ""}<span class="badge ${l.low ? "badge-warn" : ""}" title="Độ tin cậy">${Math.round(l.conf * 100)}%</span></span>${l.vlm ? `<span class="mt-1 block text-xs text-muted">Bản đọc đầu: ${esc(l.text.replace(/[ăâđêôơư]/g, "?"))}</span>` : ""}</span></a>`).join("");
    if (tab === "md")
      panel = `<div class="px-5 py-4"><div class="md text-[14px]">${ok ? md((pg.title ? "## " + pg.title + "\n\n" : "") + pg.body.join("\n\n")) : `<em>${pst(d, n) === "-" ? "Trang chưa xử lý" : "Trang trống"}</em>`}</div></div>`;
    if (tab === "blocks")
      panel = !ok ? "" : [["title", pg.title], ...pg.body.map((t) => ["text", t])].filter(([, t]) => t).map(([k, t], i) => `<div class="border-b border-line px-5 py-2.5"><div class="mb-1 flex gap-1.5"><span class="badge ${k === "title" ? "badge-info" : ""}">${k === "title" ? "Tiêu đề" : "Đoạn"} ${i + 1}</span></div><div class="font-body text-[13px] text-fg">${esc(t)}</div></div>`).join("");
    if (tab === "find") {
      const term = S.findQ ?? "tài khoản";
      const hits = d.content.flatMap((_, i) => linesOf(d, i + 1).filter((l) => l.text.toLowerCase().includes(term.toLowerCase())).map((l) => [i + 1, l]));
      panel = `<form class="flex gap-2 px-5 pt-4 pb-2" onsubmit="${on((e) => (e.preventDefault(), (S.findQ = e.target.q.value), render()))}"><input name="q" class="input flex-1" value="${esc(term)}" placeholder="Tìm trong file"><input class="input w-16" placeholder="từ"><input class="input w-16" placeholder="đến"></form>
        <div class="px-5 pb-1 text-xs text-muted">${hits.length} kết quả</div>${hits.map(([p, l]) => `<a href="${url({ page: p, tab: "find", hl: l.line_no })}" class="block border-b border-line px-5 py-2 text-fg hover:bg-fg/4"><span class="text-xs text-muted">Trang ${p} · L${l.line_no}</span><span class="block font-body text-[13px]">${esc(l.text).replace(new RegExp(term.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "ig"), (m) => `<mark class="rounded bg-warn-soft px-0.5 text-fg">${m}</mark>`)}</span></a>`).join("")}`;
    }
    if (tab === "tree") {
      const kids = (pid) => tree.filter((x) => x.parent_id === pid);
      const node = (x, dep) => `<a href="${url({ page: x.page_start, tab: "tree" })}" class="flex gap-2 py-1.5 pr-5 text-fg hover:bg-fg/5 ${n >= x.page_start && n <= x.page_end && dep ? "bg-accent-soft/60" : ""}" style="padding-left:${20 + dep * 16}px" title="${esc(x.summary)}">${x.doc_type ? icon("description", { size: 16, cls: "text-muted" }) : ""}<span class="min-w-0 flex-1 truncate ${x.doc_type ? "font-medium" : ""}">${esc(x.title)}</span><span class="text-xs whitespace-nowrap text-muted">${pages(x.page_start, x.page_end)}</span></a>${kids(x.id).map((c) => node(c, dep + 1)).join("")}`;
      panel = tree.length ? `<div class="py-2">${node(tree[0], 0)}</div>` : empty("account_tree", "", "Mục lục có sau khi xử lý xong file.");
    }
    return `<div class="flex min-h-0 flex-1 flex-col bg-surface-2/60 text-fg">
      <header class="flex h-16 flex-none items-center gap-1 px-2">
        ${(([h, l]) => `<a class="${darkBtn}" href="${h}" title="Quay lại ${esc(l)}">${icon("arrow_back")}</a>`)(backOf("#/documents"))}${fileIcon(d.file_name)}
        <div class="ml-2 min-w-0 flex-1"><div class="truncate text-base">${esc(d.file_name)}</div>${TERMINAL.has(d.status) ? `<div class="mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-muted"><span>${d.content.length} trang${groups.length ? ` · ${groups.length} giấy tờ` : ""}</span>${pagesWith(d, "c").length < d.content.length ? `<span>${pageSummary(d)}</span>` : ""}${Object.entries(typeCount).map(([t, c]) => `<a href="${url({ page: groups.find((g) => g.doc_type === t).page_start })}" class="inline-flex items-center gap-1.5 text-fg hover:underline" title="Tới ${esc(typeTitle(t))} đầu tiên"><i class="size-2 rounded-full" style="background:${typeColor(t)}"></i>${esc(typeTitle(t))}${c > 1 ? ` <span class="text-muted">×${c}</span>` : ""}</a>`).join("")}</div>` : ""}${!TERMINAL.has(d.status) ? `<div class="flex items-center gap-2 text-xs text-muted">${spinner("size-3 border-2")}${Math.round((d.progress || 0) * 100)}% · ${d.pages_done}/${d.page_count} trang</div>` : ""}</div>
        <a class="${darkBtn}" title="Tìm trong file" href="${url({ tab: "find" })}" onclick="${on(() => (S.panel = true))}">${icon("search")}</a>
        <button class="${darkBtn}" title="Tải xuống" onclick="${on(() => toast("Prototype: không có file gốc"))}">${icon("download")}</button>
        <button class="${darkBtn}" title="Toàn văn" onclick="${on(() => modal({ title: "Toàn văn", icon: "article", width: 900, body: `<div class="md text-fg">${md(d.content.map((p, i) => `**— Trang ${i + 1} —**\n\n${p.title ? "## " + p.title + "\n\n" : ""}${p.body.join("\n\n")}`).join("\n\n---\n\n"))}</div>` }))}">${icon("article")}</button>
        <button class="${darkBtn}" title="Đọc lại" onclick="${on(() => openReparse(d))}">${icon("refresh")}</button>
        <button class="${darkBtn}" title="Xoá" onclick="${on(() => confirm(`Xoá “${d.file_name}”?`) && go("#/documents"))}">${icon("delete")}</button>
        <button class="${darkBtn} ${S.panel ? "bg-accent-soft text-on-accent-soft" : ""}" title="Chi tiết" onclick="${on(() => ((S.panel = !S.panel), render()))}">${icon("info", { fill: S.panel })}</button>
      </header>
      <div class="flex min-h-0 flex-1">
        <div class="relative hidden w-40 flex-none flex-col gap-1.5 overflow-y-auto px-2 pt-1 pb-6 lg:flex" aria-label="Các trang theo giấy tờ">${rail}</div>
        <div class="relative min-w-0 flex-1">
          <div class="absolute inset-0 overflow-auto px-6 pt-2 pb-24"><div class="mx-auto" style="width:${S.zoom}%;max-width:${S.zoom <= 100 ? "980px" : "none"}">
            ${pg.failed ? `<div class="grid aspect-3/4 place-items-center rounded bg-surface-3 p-6 text-center text-sm text-muted"><span>${icon("error", { size: 40, cls: "mb-2 text-err" })}<br>Trang ${n} lỗi: ${esc(d.failed_pages?.[0]?.error ?? "")}</span></div>`
            : pst(d, n) === "-" ? `<div class="relative overflow-hidden rounded-md border border-line">${pageSVG(d, n, { cls: "opacity-40" })}<div class="absolute inset-x-0 top-1/3 mx-auto flex w-fit flex-col items-center gap-3 rounded-2xl bg-surface px-6 py-5 text-center text-sm text-muted shadow-2">${icon("pending", { size: 32, cls: "text-subtle" })}<span>Trang ${n} chưa được đọc và phân loại</span><button class="btn btn-primary btn-sm" onclick="${on(() => openReparse(d, n))}">${icon("play_arrow", { size: 18 })} Xử lý từ trang này…</button></div></div>`
            : !ready(n) ? `<div class="grid aspect-3/4 place-items-center rounded bg-surface-3 p-6 text-center text-sm text-muted"><span>${icon("hourglass_empty", { size: 40, cls: "mb-2 text-subtle" })}<br>Trang ${n} chưa sẵn sàng</span></div>`
            : `<div class="overflow-hidden rounded-md border border-line shadow-1">${pageSVG(d, n, { boxes: true, hl, hideBoxes: !S.showBoxes, onBox: on((e) => { const l = e.target.dataset?.line; if (l) go(url({ tab: "lines", hl: l })); }) })}</div>`}
          </div></div>
          <div class="absolute bottom-5 left-1/2 flex h-12 -translate-x-1/2 items-center gap-0.5 rounded-full border border-line bg-surface/95 px-2 text-sm text-fg shadow-2 backdrop-blur">
            <a class="${darkBtn} ${n <= 1 ? "pointer-events-none opacity-30" : ""}" href="${url({ page: n - 1 })}" title="Trang trước (←)">${icon("chevron_left")}</a>
            <span class="flex items-center gap-1.5 px-1 whitespace-nowrap">Trang <input value="${n}" onkeydown="if(event.key==='Enter')location.hash='#/documents/${id}?tab=${tab}&page='+this.value" class="h-7 w-10 rounded bg-surface-2 text-center outline-none focus:ring-2 focus:ring-accent"> / ${d.content.length}</span>
            <a class="${darkBtn} ${n >= d.content.length ? "pointer-events-none opacity-30" : ""}" href="${url({ page: n + 1 })}" title="Trang sau (→)">${icon("chevron_right")}</a>
            <span class="mx-1 h-6 w-px bg-line"></span>
            <button class="${darkBtn}" onclick="${on(() => ((S.zoom = Math.max(50, S.zoom - 25)), render()))}" title="Thu nhỏ">${icon("zoom_out")}</button>
            <button class="h-8 w-14 rounded-full hover:bg-fg/8" onclick="${on(() => ((S.zoom = 100), render()))}" title="Vừa khung">${S.zoom}%</button>
            <button class="${darkBtn}" onclick="${on(() => ((S.zoom = Math.min(200, S.zoom + 25)), render()))}" title="Phóng to">${icon("zoom_in")}</button>
            <span class="mx-1 h-6 w-px bg-line"></span>
            <button class="${darkBtn} ${S.showBoxes ? "bg-accent-soft text-on-accent-soft" : ""}" onclick="${on(() => ((S.showBoxes = !S.showBoxes), render()))}" title="Khung từng dòng chữ">${icon("select")}</button>
          </div>
        </div>
        ${S.panel ? `<aside class="flex w-105 max-w-[90vw] flex-none flex-col overflow-hidden rounded-tl-2xl bg-surface text-fg max-md:absolute max-md:inset-y-16 max-md:right-0 max-md:z-10 max-md:shadow-3">
          <div class="flex items-center gap-2 px-5 pt-4 pb-1">${fileIcon(d.file_name, 20)}<span class="min-w-0 flex-1 truncate text-base">${esc(d.file_name)}</span><button class="btn-icon btn-sm" onclick="${on(() => ((S.panel = false), render()))}">${icon("close", { size: 20 })}</button></div>
          <div class="flex flex-none overflow-x-auto border-b border-line px-2">${DTABS.map(([k, l]) => `<a href="${url({ tab: k })}" class="tab flex items-center ${tab === k ? "tab-active" : ""}">${l}</a>`).join("")}</div>
          <div class="min-h-0 flex-1 overflow-auto">${panel}</div></aside>` : ""}
      </div></div>`;
  }
  // Đọc lại cùng hộp chọn trang như lúc tải lên; còn trang chưa xử lý thì
  // điền sẵn khoảng đó, mở từ một trang thì điền sẵn trang ấy.
  function openReparse(d, n) {
    const todo = pagesWith(d, "-");
    const fresh = todo.length === d.content.length;
    pickPages({
      title: fresh ? "Xử lý tài liệu" : "Đọc lại tài liệu",
      icon: fresh ? "play_arrow" : "refresh",
      files: [{ name: d.file_name, pages: d.content.length, note: pageSummary(d) }],
      range: n ? [n, todo.includes(n) ? todo.find((p, i) => todo[i + 1] !== p + 1 && p >= n) : n] : todo.length && !fresh ? [todo[0], todo.at(-1)] : null,
      ok: fresh ? "Đọc & phân loại" : "Đọc lại",
      onOk: ([f]) => runPages(d, f.range),
    });
  }
  // Mô phỏng: trang trong khoảng chuyển đang chờ → đã đọc → đã phân loại; trang
  // mới phân loại được gom vào giấy tờ liền trước cùng loại, hoặc thành giấy tờ mới.
  function runPages(d, [from, to]) {
    const set = (ch) => {
      const a = Array.from({ length: d.content.length }, (_, i) => pst(d, i + 1));
      for (let p = from; p <= Math.min(to, a.length); p++) a[p - 1] = ch;
      d.pstate = a.join("");
      render();
    };
    toast(`Đang đọc & phân loại ${pages(from, Math.min(to, d.content.length))} của “${d.file_name}”`);
    set("0");
    setTimeout(() => set("b"), 1200);
    setTimeout(() => {
      for (let p = from; p <= to; p++) if (d.content[p - 1]) d.content[p - 1].failed = false;
      d.failed_pages = d.failed_pages?.filter((f) => f.page_no < from || f.page_no > to);
      if (!d.failed_pages?.length) delete d.error;
      const t = (M.TREES[d.id] ??= [{ id: d.id + "n1", short_id: "n1", parent_id: null, title: d.file_name, page_start: 1, page_end: d.content.length, doc_type: "", summary: "", ord: 0 }]);
      const segs = () => t.filter((x) => x.parent_id === t[0].id);
      for (let p = from; p <= Math.min(to, d.content.length); p++) {
        if (segs().some((g) => p >= g.page_start && p <= g.page_end)) continue;
        const type = d.content[p - 1].doc_type ?? "other", prev = segs().find((g) => g.page_end === p - 1);
        if (prev?.doc_type === type) prev.page_end = p;
        else t.push({ id: `${d.id}g${p}`, short_id: "g" + p, parent_id: t[0].id, title: typeTitle(type), page_start: p, page_end: p, doc_type: type, summary: "", ord: t.length });
      }
      set("c");
      d.status = pagesWith(d, "-x").length ? "partial" : "completed";
      d.pages_failed = pagesWith(d, "x").length;
      render();
    }, 2600);
  }

  // ------------------------------------------------------------ chat
  const spark = (busy) => `<div class="grid size-8 flex-none place-items-center rounded-full ${busy ? "animate-pulse" : ""}"><svg viewBox="0 0 24 24" class="size-7"><defs><linearGradient id="spark" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#4285f4"/><stop offset=".55" stop-color="#9b72cb"/><stop offset="1" stop-color="#d96570"/></linearGradient></defs><path d="M12 2c.6 4.8 3.2 8.4 10 10-6.8 1.6-9.4 5.2-10 10-.6-4.8-3.2-8.4-10-10 6.8-1.6 9.4-5.2 10-10z" fill="url(#spark)"/></svg></div>`;
  // Tên bước của trợ lý bằng lời thường thay cho tên công cụ.
  const stepLabel = (name, input) => {
    const f = input.document_id ? docById(input.document_id)?.file_name : "";
    if (name === "kb_case_toc") return "Xem danh sách tài liệu của hồ sơ";
    if (name === "kb_document_tree") return `Xem mục lục · ${f}`;
    if (name === "kb_read_pages") return `Đọc ${f} · trang ${input.pages.join(", ")}`;
    return "Tra cứu tài liệu";
  };
  function turns(items) {
    return items.map((it) => {
      if (it.role === "user") return `<div class="max-w-[80%] self-end rounded-3xl rounded-tr-md bg-surface-2 px-5 py-3 text-[15px] leading-6 whitespace-pre-wrap">${esc(it.text)}</div>`;
      if (it.pending) return `<div class="flex gap-4">${spark(true)}<div class="flex items-center gap-2 pt-1 text-sm text-muted">${spinner("size-4 border-2")} Đang đọc tài liệu…</div></div>`;
      const tools = it.tools ?? [];
      const work = tools.length
        ? `<details class="group/w rounded-2xl border border-line"><summary class="flex h-11 cursor-pointer list-none items-center gap-2 rounded-2xl px-4 text-sm text-muted hover:bg-fg/4">${icon("manage_search", { size: 18 })}<span class="min-w-0 flex-1 truncate">Trợ lý đã làm ${tools.length} bước để tìm câu trả lời</span>${icon("expand_more", { size: 20, cls: "transition-transform group-open/w:rotate-180" })}</summary>
          <div class="flex flex-col gap-2 border-t border-line p-3">${tools.map(([name, input, out]) => `<details class="rounded-xl bg-surface-2 text-[13px]"><summary class="flex min-h-10 cursor-pointer list-none items-center gap-2 px-3 py-2">${icon("check_circle", { size: 18, cls: "text-ok" })}<span class="min-w-0 flex-1 text-fg">${esc(stepLabel(name, input))}</span></summary><div class="max-h-75 overflow-auto border-t border-line px-3 py-2.5 text-xs whitespace-pre-wrap text-muted">${md(esc(out)).replace(/^<p>|<\/p>\s*$/g, "")}</div></details>`).join("")}</div></details>`
        : "";
      return `<div class="flex gap-4">${spark(false)}<div class="flex min-w-0 flex-1 flex-col gap-3 pt-1">${work}<div class="md text-[15px]">${md(it.text)}</div></div></div>`;
    }).join("");
  }
  function composer(placeholder, onSend, note) {
    return `<div class="mx-auto flex max-w-200 items-end gap-2 rounded-[28px] bg-surface-2 py-2 pr-2 pl-6 focus-within:bg-surface-3">
        <textarea id="composer" rows="1" placeholder="${placeholder}" oninput="this.style.height='auto';this.style.height=Math.min(220,this.scrollHeight)+'px';this.nextElementSibling.classList.toggle('bg-accent',!!this.value.trim());this.nextElementSibling.classList.toggle('text-on-accent',!!this.value.trim())" onkeydown="if(event.key==='Enter'&&!event.shiftKey&&!event.isComposing){event.preventDefault();this.nextElementSibling.click()}" class="max-h-55 min-h-10 flex-1 resize-none bg-transparent py-2.5 text-base text-fg outline-none placeholder:text-muted"></textarea>
        <button class="btn-icon size-12 hover:bg-accent" title="Gửi" onclick="${on(() => { const t = document.getElementById("composer").value.trim(); if (t) onSend(t); })}">${icon("send", { fill: true })}</button></div>
      <p class="mx-auto mt-2 max-w-200 text-center text-xs text-subtle">${note}</p>`;
  }
  // Gửi giả: hiện "Đang đọc tài liệu…" rồi trả lời mẫu.
  function fakeSend(list, text) {
    list.push({ role: "user", text }, { role: "assistant", pending: true });
    render();
    setTimeout(() => {
      list.pop();
      list.push({ role: "assistant", tools: M.TRANSCRIPT[1].tools.slice(0, 3), text: "Đây là prototype nên câu trả lời là mẫu. Trong ứng dụng thật, trợ lý AI duyệt mục lục hồ sơ, đọc đúng trang rồi trả lời kèm trích dẫn như [doc:d1:p2:l6]." });
      render();
    }, 1200);
  }
  const CHAT_SUGGEST = [
    ["list_alt", "Liệt kê các tài liệu trong phạm vi, mỗi tài liệu gồm những loại giấy tờ nào và ở trang nào."],
    ["gavel", "Xem tổng quan từng trang, tìm các trang là hợp đồng rồi cho biết giá trị hợp đồng và các bên."],
    ["badge", "Số định danh cá nhân của người đứng tên là gì? Trích dẫn đúng trang."],
    ["manage_search", "Tìm trong trang 7–12 các điều khoản về thời hạn thanh toán."],
  ];
  function viewChat(sid) {
    const sess = M.SESSIONS.find((s) => s.id === sid);
    S.chatCase = sess ? sess.case_id : (S.chatCase ?? "c1");
    const scope = caseById(S.chatCase);
    const items = sid ? (S.chats[sid] ??= []) : (S.chats._new ??= []);
    return `<div class="flex min-h-0 flex-1">
      ${S.showHistory ? `<aside class="hidden w-72 flex-none flex-col border-r border-line md:flex">
        <div class="flex items-center gap-1 px-3 pt-4 pb-2"><a class="btn btn-tonal flex-1 justify-start" href="#/chat" onclick="${on(() => (S.chats._new = []))}">${icon("edit_square", { size: 20 })} Cuộc trò chuyện mới</a><button class="btn-icon" title="Ẩn lịch sử" onclick="${on(() => ((S.showHistory = false), render()))}">${icon("left_panel_close")}</button></div>
        <div class="px-6 pt-3 pb-1 text-xs font-medium text-muted">Gần đây</div>
        <div class="min-h-0 flex-1 overflow-auto px-3 pb-3">${M.SESSIONS.map((s) => `<a href="#/chat/${s.id}" class="group flex h-10 items-center gap-3 rounded-full pr-1 pl-4 ${s.id === sid ? "bg-accent-soft text-on-accent-soft" : "text-fg hover:bg-fg/8"}" title="${esc(s.title)} · ${fmtDate(s.updated_at)}">${icon("chat_bubble", { size: 18, cls: s.id === sid ? "" : "text-muted" })}<span class="min-w-0 flex-1 truncate text-sm">${esc(s.title)}</span><button class="btn-icon btn-sm ${s.id === sid ? "" : "opacity-0 group-hover:opacity-100"}" onclick="${on((e) => (e.preventDefault(), openMenu(e, [{ icon: "delete", label: "Xoá", danger: true, onClick: () => toast("Đã xoá cuộc trò chuyện") }], "right")))}">${icon("more_vert", { size: 18 })}</button></a>`).join("")}</div></aside>` : ""}
      <section class="flex min-h-0 min-w-0 flex-1 flex-col">
        <div class="flex flex-none flex-wrap items-center gap-2 px-4 pt-3 pb-2">
          ${S.showHistory ? "" : `<button class="btn-icon max-md:hidden" title="Hiện lịch sử" onclick="${on(() => ((S.showHistory = true), render()))}">${icon("left_panel_open")}</button>`}
          ${casePicker(scope, (c) => ((S.chatCase = c.id), render()), !!sess)}
          <span class="text-xs text-muted">${icon("lock", { size: 14, cls: "align-[-2px]" })} Trợ lý chỉ đọc tài liệu của hồ sơ này</span>
        </div>
        <div class="min-h-0 flex-1 overflow-auto" id="scroller"><div class="mx-auto flex max-w-200 flex-col gap-8 px-6 pt-4 pb-8">
          ${!items.length ? `<div class="pt-[8vh]"><h2 class="gradient-text text-[44px] leading-13 font-medium tracking-tight">Xin chào</h2><p class="mt-1 text-[32px] leading-10 text-subtle">Bạn cần tìm gì trong tài liệu?</p>
            <div class="mt-10 grid gap-3 sm:grid-cols-2">${CHAT_SUGGEST.map(([ic, s]) => `<button class="flex min-h-30 flex-col justify-between rounded-2xl bg-surface-2 p-4 text-left text-sm text-fg transition-colors hover:bg-surface-3" onclick="${on(() => { const c = document.getElementById("composer"); c.value = s; c.focus(); c.dispatchEvent(new Event("input")); })}">${s}<span class="grid size-9 place-items-center self-end rounded-full bg-surface">${icon(ic, { size: 20, cls: "text-muted" })}</span></button>`).join("")}</div></div>` : turns(items)}
        </div></div>
        <div class="flex-none px-4 pb-4">${composer("Hỏi về tài liệu…", (t) => fakeSend(items, t), "Trợ lý duyệt mục lục của hồ sơ, chỉ đọc đúng các trang cần thiết rồi trả lời. Bấm vào nhãn trích dẫn để xem vùng trên trang gốc.")}</div>
      </section></div>`;
  }

  // ------------------------------------------------------------ case (Nguồn · nội dung nguồn · Studio)
  const rowCls = (a) => "flex min-h-8 w-full items-center gap-2 rounded-full py-1 pr-3 text-left text-sm " + (a ? "bg-accent-soft font-medium text-on-accent-soft" : "text-fg hover:bg-fg/8");
  function viewCase(caseId, q) {
    const c = caseById(caseId);
    if (!c) return go("#/cases/c1"), "";
    const docs = docsOf(caseId);
    const done = docs.filter((d) => TERMINAL.has(d.status) && d.status !== "failed");
    // Cột giữa luôn là trình xem trang; chưa chọn nguồn thì mở file đầu tiên.
    const docId = q.get("doc") || done[0]?.id, nodeId = q.get("node");
    const pending = docs.filter((d) => !TERMINAL.has(d.status) || d.status === "failed");
    const split = caseId === "c1" ? M.SPLIT : null;
    const show = (k) => (S.caseCol === k ? "flex" : "hidden lg:flex");
    const sel = (doc, node) => `#/cases/${caseId}?doc=${doc}${node ? "&node=" + node : ""}`;

    // Cây nguồn: file là tiêu đề nhóm; dưới là giấy tờ (chấm màu theo loại),
    // mục con thu gọn đến khi bấm ›; đường dọc thay cho thụt lề sâu.
    const fileEntry = (d) => {
      const open = d.id === docId;
      const ex = S.expanded[d.id] ?? open;
      const tree = M.TREES[d.id] ?? [];
      const kids = (pid) => tree.filter((x) => x.parent_id === pid);
      const sf = split?.files.find((f) => f.document_id === d.id);
      const selected = open && nodeId ? tree.find((x) => x.short_id === nodeId) : null;
      const onPath = (x) => { for (let y = selected; y; y = tree.find((z) => z.id === y.parent_id)) if (y.id === x.id) return true; return false; };
      const row = (x, dep) => {
        const sub = kids(x.id);
        const nx = (S.nodeOpen ??= {})[x.id] ?? onPath(x);
        const cur = selected?.id === x.id;
        return `<div class="group flex h-8 items-center rounded-lg pr-1 ${cur ? "bg-accent-soft text-on-accent-soft" : "text-fg hover:bg-fg/6"}">
            <a href="${sel(d.id, x.short_id)}" title="${esc(x.summary || x.title)}" class="flex h-full min-w-0 flex-1 items-center gap-2 pl-2 text-inherit">
              ${dep ? "" : `<i class="size-2 flex-none rounded-full" style="background:${typeColor(x.doc_type)}" title="${esc(typeTitle(x.doc_type))}"></i>`}
              <span class="min-w-0 flex-1 truncate ${dep ? "text-[13px] " + (cur ? "" : "text-muted") : "text-sm"}">${esc(x.title)}</span>
              <span class="font-mono text-[11px] whitespace-nowrap ${cur ? "" : "text-subtle"}">${x.page_start === x.page_end ? x.page_start : x.page_start + "–" + x.page_end}</span></a>
            ${sub.length ? `<button class="btn-icon size-6 flex-none" title="${nx ? "Thu gọn" : "Mục con"}" onclick="${on(() => ((S.nodeOpen[x.id] = !nx), render()))}">${icon("chevron_right", { size: 18, cls: "transition-transform " + (nx ? "rotate-90" : "") })}</button>` : '<span class="w-6 flex-none"></span>'}</div>
          ${sub.length && nx ? `<div class="ml-3 border-l border-line pl-2">${sub.map((y) => row(y, dep + 1)).join("")}</div>` : ""}`;
      };
      const st = !sf ? "" : sf.status === "reviewed" ? icon("check_circle", { size: 16, fill: true, cls: "text-ok" }) : icon("auto_awesome", { size: 16, cls: "text-warn" });
      return `<div class="mb-2">
        <div class="flex h-9 items-center gap-1 rounded-lg pr-2 ${open && !nodeId ? "bg-accent-soft text-on-accent-soft" : "text-fg hover:bg-fg/6"}">
          <button class="btn-icon size-7 flex-none" onclick="${on(() => ((S.expanded[d.id] = !ex), render()))}">${icon("arrow_right", { size: 20, cls: "transition-transform " + (ex ? "rotate-90" : "") })}</button>
          <a href="${sel(d.id)}" class="flex min-w-0 flex-1 items-center gap-2 text-inherit" title="${esc(d.file_name)}">${fileIcon(d.file_name, 18)}<span class="min-w-0 flex-1 truncate text-sm font-medium">${esc(d.file_name)}</span></a>
          ${(() => { const c = pagesWith(d, "c").length; return c === d.page_count ? `<span class="text-xs whitespace-nowrap text-muted">${d.page_count} tr</span>` : `<span class="text-xs whitespace-nowrap text-warn" title="${esc(pageSummary(d).replace(/<[^>]+>/g, ""))}">${c}/${d.page_count} tr</span>`; })()}${st ? `<span title="${sf.status === "reviewed" ? "Tách & gom đã duyệt" : "AI đề xuất tách, cần duyệt"}">${st}</span>` : ""}</div>
        ${ex && (tree.length || pagesWith(d, "-").length) ? `<div class="ml-5 border-l border-line pl-2">${tree.length ? kids(tree[0].id).map((x) => row(x, 0)).join("") : ""}${pagesWith(d, "-").length ? `<button class="flex h-8 w-full items-center gap-2 rounded-lg pr-8 pl-2 text-left text-muted hover:bg-fg/6" title="Chọn trang để đọc & phân loại" onclick="${on(() => openReparse(d))}"><i class="size-2 flex-none rounded-full border border-outline"></i><span class="min-w-0 flex-1 truncate text-sm italic">Chưa xử lý</span><span class="font-mono text-[11px] whitespace-nowrap text-subtle">${spans(pagesWith(d, "-"))}</span></button>` : ""}</div>` : ""}</div>`;
    };
    const sidebar = `<aside class="${show("sources")} w-full flex-none flex-col overflow-auto border-r border-line px-2 py-3 lg:w-72">
      <div class="flex items-center pr-1 pb-1 pl-3"><span class="flex-1 text-sm font-medium text-fg">Nguồn</span><button class="btn-icon btn-sm" title="Thêm nguồn" onclick="${on(openUpload)}">${icon("add", { size: 20 })}</button></div>
      ${split ? `<a class="btn btn-tonal btn-sm mx-2 mb-2 flex-none" href="#/cases/${caseId}/split">${icon("content_cut", { size: 18 })} Tách & gom trang <span class="badge badge-warn">${split.files.filter((f) => f.status !== "reviewed").length}</span></a>` : ""}
      ${done.map(fileEntry).join("")}
      ${pending.length ? `<div class="mt-3 px-3 pb-1 text-xs font-medium text-muted">Đang xử lý</div>${pending.map((p) => `<a href="#/documents/${p.id}" class="flex h-8 items-center gap-2 rounded-full px-3 text-sm text-muted hover:bg-fg/8">${fileIcon(p.file_name, 18)}<span class="min-w-0 flex-1 truncate">${esc(p.file_name)}</span>${p.status === "failed" ? statusBadge(p.status) : spinner("size-3.5 border-2")}</a>`).join("")}` : ""}</aside>`;

    let center, fields = "";
    const doc = docId && docById(docId);
    if (!doc) center = empty("folder_off", "Hồ sơ chưa có nguồn nào có mục lục");
    else {
      const node = nodeId && (M.TREES[doc.id] ?? []).find((x) => x.short_id === nodeId);
      const from = node?.page_start ?? 1, to = node?.page_end ?? doc.page_count;
      // Dòng đang tô trên trang gốc (bấm một trường): "trang:dòng".
      const [hlPage, hlLine] = (q.get("hl") ?? "").split(":").map(Number);

      // Cột trường trích xuất: mỗi giấy tờ trong phạm vi đang xem là một nhóm.
      const groups = Object.entries(M.ROWS).flatMap(([type, rows]) => rows.filter((r) => r[0] === doc.id && r[1] <= to && r[2] >= from).map((r) => ({ type, r })))
        .sort((a, b) => a.r[1] - b.r[1]);
      if (S.caseFieldsOpen === false && S.caseCol !== "fields")
        fields = `<aside class="hidden w-12 flex-none flex-col items-center gap-2 border-l border-line py-3 lg:flex"><button class="btn-icon" title="Hiện trường trích xuất" onclick="${on(() => ((S.caseFieldsOpen = true), render()))}">${icon("right_panel_open")}</button><span class="text-xs text-muted [writing-mode:vertical-rl]">Trường trích xuất${groups.length ? ` (${groups.length})` : ""}</span></aside>`;
      else {
        const body = groups.length
          ? groups.map(({ type, r }) => {
              const t = M.DOC_TYPES.find((x) => x.name === type);
              const title = (M.TREES[doc.id] ?? []).find((x) => x.doc_type === type && x.page_start === r[1])?.title ?? typeTitle(type);
              return `<section class="border-b border-line pb-3 last:border-b-0">
                <div class="flex items-start gap-2 px-1 pt-3 pb-2"><i class="mt-1.5 size-2 flex-none rounded-full" style="background:${typeColor(type)}"></i><div class="min-w-0 flex-1"><div class="text-sm font-medium wrap-break-word text-fg">${esc(title)}</div><div class="text-xs text-muted">${esc(typeTitle(type))} · ${pages(r[1], r[2])}</div></div></div>
                <dl class="flex flex-col">${t.fields.map((f) => {
                  const v = r[4][f.key];
                  const ai = v?.[0] ?? "";
                  const ek = `${doc.id}:${type}:${r[1]}:${f.key}`;
                  const val = S.caseEdits[ek] ?? ai;
                  const edited = S.caseEdits[ek] !== undefined;
                  // Sửa tại chỗ: Enter hoặc rời ô là lưu, Esc là huỷ.
                  if (S.caseEditing === ek)
                    return `<div class="rounded-lg bg-accent/5 px-2 py-1.5"><dt class="text-xs font-medium text-accent">${esc(f.label)}</dt>
                      <textarea id="case-edit" rows="1" aria-label="${esc(f.label)}" class="input mt-1 h-auto w-full resize-none py-1.5 text-sm field-sizing-content"
                        onkeydown="${on((e) => { if (e.key === "Enter" && !e.shiftKey) (e.preventDefault(), e.target.blur()); if (e.key === "Escape") (S.caseEditing = null), render(); })}"
                        onblur="${on((e) => { if (S.caseEditing !== ek) return; const nv = e.target.value.trim(); if (nv === ai) delete S.caseEdits[ek]; else S.caseEdits[ek] = nv; S.caseEditing = null; if (nv !== val) toast(`Đã lưu ${f.label}`); render(); })}">${esc(val)}</textarea>
                      <div class="mt-1 text-[11px] text-subtle">Enter để lưu · Esc để huỷ</div></div>`;
                  const cur = ai && hlPage === v[2] && hlLine === v[3] + 1;
                  const startEdit = on((e) => { e.preventDefault(); S.caseEditing = ek; render(); const ta = document.getElementById("case-edit"); ta.focus(); ta.setSelectionRange(ta.value.length, ta.value.length); });
                  const inner = `<dt class="pr-7 text-xs text-muted">${esc(f.label)}</dt><dd class="mt-0.5 text-sm wrap-break-word whitespace-normal ${val ? "text-fg" : "text-subtle italic"}">${val ? esc(val) : "không thấy"}${edited ? ' <span class="badge badge-info align-middle">đã sửa</span>' : v?.[4] === "low" ? ` <span class="badge badge-warn align-middle" title="Độ tin cậy ${Math.round(v[1] * 100)}%">kiểm tra lại</span>` : ""}</dd>`;
                  return `<div class="group relative">${ai
                      ? `<a href="${sel(doc.id, nodeId)}&hl=${v[2]}:${v[3] + 1}" title="Xem trên trang ${v[2]} · nháy đúp để sửa" ondblclick="${startEdit}" class="block rounded-lg px-2 py-1.5 ${cur ? "bg-warn-soft" : "hover:bg-fg/5"}">${inner}</a>`
                      : `<div class="rounded-lg px-2 py-1.5 hover:bg-fg/5" ondblclick="${startEdit}">${inner}</div>`}
                    <button class="btn-icon absolute top-1 right-1 size-7 opacity-0 group-hover:opacity-100 focus-visible:opacity-100 max-lg:opacity-100" title="Sửa ${esc(f.label)}" aria-label="Sửa ${esc(f.label)}" onclick="${startEdit}">${icon("edit", { size: 16 })}</button>
                    ${edited ? `<div class="flex items-center gap-2 px-2 pb-1 text-[11px] text-muted"><span class="min-w-0 wrap-break-word">AI: <s>${esc(ai || "không thấy")}</s></span><button class="flex-none text-accent hover:underline" onclick="${on(() => (delete S.caseEdits[ek], render()))}">Hoàn tác</button></div>` : ""}</div>`;
                }).join("")}</dl></section>`;
            }).join("")
          : `<div class="px-2 py-8 text-center text-sm text-subtle">Chưa có trường nào được trích xuất cho ${node ? "mục" : "file"} này.<div class="mt-1 text-xs">Trường được lấy khi tạo bảng ở Studio → Xuất Excel.</div></div>`;
        fields = `<aside class="${show("fields")} w-full flex-none flex-col border-l border-line lg:w-80">
          <div class="flex h-11 flex-none items-center gap-2 border-b border-line pr-2 pl-4"><span class="flex-1 text-sm font-medium text-fg">Trường trích xuất</span><button class="btn-icon btn-sm max-lg:hidden" title="Thu gọn" onclick="${on(() => ((S.caseFieldsOpen = false), render()))}">${icon("right_panel_close", { size: 20 })}</button></div>
          <div id="case-fields" class="min-h-0 flex-1 overflow-auto px-3">${body}</div></aside>`;
      }
      const all = Array.from({ length: doc.page_count }, (_, i) => i + 1);
      center = `<div class="flex min-h-0 flex-1 flex-col">
        <div class="flex h-11 flex-none items-center gap-2 border-b border-line px-4 text-sm text-fg">${fileIcon(doc.file_name, 18)}<span class="min-w-0 flex-1 truncate">${esc(doc.file_name)}${node ? ` <span class="text-muted">· ${esc(node.title)} · ${pages(from, to)}</span>` : ""}</span>
          <a class="btn-icon btn-sm" href="#/documents/${doc.id}?page=${from}" title="Mở trong trình xem tài liệu">${icon("open_in_new", { size: 18 })}</a></div>
        ${pagesWith(doc, "c").length < doc.page_count ? `<div class="flex flex-none flex-wrap items-center gap-x-3 gap-y-1 border-b border-line bg-surface-2/60 px-4 py-2 text-xs text-muted"><span class="min-w-0 flex-[1_1_12rem]">${pageSummary(doc)}</span>${pagesWith(doc, "-x").length ? `<button class="btn btn-tonal btn-sm" onclick="${on(() => openReparse(doc))}">${icon("play_arrow", { size: 18 })} Xử lý thêm trang…</button>` : ""}</div>` : ""}
        <div id="case-pages" class="relative min-h-0 flex-1 overflow-auto px-6 py-4"><div class="mx-auto flex max-w-200 flex-col gap-4">
          ${all.map((n) => { const inNode = node && n >= from && n <= to; return `<div ${n === from ? 'id="case-first"' : ""} class="relative"><div class="overflow-hidden rounded-md border border-line ${inNode ? "outline-3 outline-offset-2 outline-accent" : ""}">${n === hlPage ? pageSVG(doc, n, { boxes: true, hl: [hlLine], hideBoxes: true }) : pageSVG(doc, n, { cls: pst(doc, n) === "-" ? "opacity-40" : "" })}</div>
            <div class="absolute top-2 left-2 flex flex-wrap gap-1 rounded-lg bg-surface/90 p-1 shadow-1">${pageSteps(doc, n, true)}</div>
            ${pst(doc, n) === "-" ? `<button class="btn btn-sm absolute inset-0 m-auto w-fit bg-surface shadow-2" onclick="${on(() => openReparse(doc, n))}">${icon("play_arrow", { size: 18 })} Xử lý từ trang này…</button>` : ""}
            <span class="absolute right-2 bottom-2 rounded bg-black/60 px-1.5 text-xs text-white">${n} / ${doc.page_count}</span></div>`; }).join("")}
        </div></div></div>`;
    }
    return `${topBar(caseLabel(c), `${done.length} file đã có mục lục${pending.length ? ` · ${pending.length} file đang xử lý` : ""}${c.title ? " · " + esc(c.title) : ""}`, `<a class="btn btn-text" href="#/chat" onclick="${on(() => ((S.chatCase = c.id), (S.chats._new = [])))}" title="Hỏi trợ lý AI về hồ sơ này">${icon("forum", { size: 18 })} Hỏi đáp</a>` + casePicker(c, (x) => go("#/cases/" + x.id)))}
      <div class="flex flex-none border-t border-line lg:hidden">${[["sources", "Nguồn"], ["view", "Xem"], ["fields", "Trường"], ["studio", "Studio"]].map(([k, l]) => `<button class="tab flex-1 ${S.caseCol === k ? "tab-active" : ""}" onclick="${on(() => ((S.caseCol = k), render()))}">${l}</button>`).join("")}</div>
      <div class="flex min-h-0 flex-1 border-t border-line">${sidebar}<section class="${show("view")} min-w-0 flex-1 flex-col">${center}</section>${fields}${studio(c, split)}</div>`;
  }
  const XL = "#107c41";
  function studio(c, split) {
    const types = kb().config.doc_types.filter((t) => t.fields.length);
    const picked = types.filter((t) => !S.excluded[t.name]);
    const nFiles = split ? split.files.length : 0;
    const sheets = M.SHEETS.filter((s) => s.case_id === c.id);
    const checks = c.id === "c1" ? [M.CHECK] : [];
    const sets = setsOfKB(kb());
    const set = setById(S.caseSet[c.id]) ?? sets[0];
    const nRules = set ? set.checks.filter((r) => r.on).length : 0;
    return `<aside class="${S.caseCol === "studio" ? "flex" : "hidden lg:flex"} w-full flex-none flex-col overflow-auto border-l border-line px-3 py-3 lg:w-80">
      <div class="px-1 pb-3 text-sm font-medium text-fg">Studio</div>
      <div class="rounded-2xl p-4" style="background:color-mix(in srgb, ${XL} 14%, var(--surface))">
        <div class="flex items-start gap-3"><span class="grid size-10 flex-none place-items-center rounded-xl bg-surface">${icon("table_view", { size: 24, fill: true, cls: "text-[#107c41]" })}</span><div class="min-w-0 flex-1"><div class="text-sm font-medium text-fg">Xuất Excel</div><div class="truncate text-xs text-muted">Mỗi loại giấy tờ một sheet, mỗi giấy tờ một dòng</div></div>
          <button class="btn-icon btn-sm" title="Chọn loại giấy tờ cần xuất" onclick="${on(() => openPickTypes(types))}">${icon("tune", { size: 18 })}</button></div>
        <button class="btn mt-3 w-full border-transparent text-white hover:shadow-1" style="background:${XL}" onclick="${on(() => toast("Đang tạo bảng; thông tin chưa có sẽ được bóc tách và lưu lại"))}">${icon("auto_awesome", { size: 18 })} Tạo ${picked.length} bảng${nFiles ? ` từ ${nFiles} file` : ""}</button>
        ${(() => { const ds = docsOf(c.id), all = ds.reduce((a, d) => a + d.content.length, 0), cls = ds.reduce((a, d) => a + pagesWith(d, "c").length, 0); return cls < all ? `<div class="mt-2 flex gap-1.5 text-xs text-fg">${icon("info", { size: 16, cls: "text-muted" })}<span>Chỉ lấy từ ${cls}/${all} trang đã phân loại; trang chưa xử lý không vào bảng.</span></div>` : ""; })()}
        <div class="mt-2 text-xs text-muted">Thông tin được bóc tách khi tạo bảng và lưu lại; lần sau chỉ bóc tách trường mới và hỏi lại một lần các trường chưa tìm thấy.</div></div>
      <div class="mt-3 rounded-2xl border border-line p-3"><div class="flex items-center gap-2 px-1 pb-1"><span class="min-w-0 flex-1 text-sm font-medium text-fg">Xuất theo mẫu</span><a class="btn-icon btn-sm" href="#/settings/forms" title="Quản lý mẫu ở Cài đặt">${icon("settings", { size: 18 })}</a></div>
        ${M.FORMS.map((t) => `<button onclick="${on(() => toast(`Đang tạo “${t.name}”; xong thì bấm tải ở danh sách Đã tạo`))}" class="flex w-full min-w-0 items-center gap-3 rounded-xl px-2 py-2 text-left hover:bg-fg/5" title="Tạo bảng theo mẫu">${icon("request_page", { size: 22, fill: true, cls: "text-[#107c41]" })}<span class="min-w-0 flex-1"><span class="block truncate text-sm text-fg">${esc(t.name)}</span><span class="block truncate text-xs text-muted">${esc(t.file_name)}</span></span></button>`).join("")}</div>
      <div class="mt-3 rounded-2xl p-4" style="background:color-mix(in srgb, #5b47c9 12%, var(--surface))">
        <div class="flex items-start gap-3"><span class="grid size-10 flex-none place-items-center rounded-xl bg-surface">${icon("fact_check", { size: 24, fill: true, cls: "text-[#5b47c9]" })}</span><div class="min-w-0 flex-1"><div class="text-sm font-medium text-fg">Đối chiếu chứng từ</div><div class="text-xs text-muted">Gom chứng từ theo giấy tờ gốc và kiểm theo bộ quy tắc đã chọn</div></div><a class="btn-icon btn-sm" href="#/settings/checks${set ? "/" + set.id : ""}" title="Sửa bộ quy tắc">${icon("tune", { size: 18 })}</a></div>
        ${set ? `<button class="mt-3 flex w-full items-center gap-2 rounded-xl bg-surface px-3 py-2 text-left hover:bg-surface-2" aria-label="Chọn bộ quy tắc" onclick="${on((e) => pickRuleSet(e, c))}">${icon("rule", { size: 18, cls: "text-[#5b47c9]" })}<span class="min-w-0 flex-1"><span class="block text-[11px] text-muted">Bộ quy tắc</span><span class="block truncate text-sm text-fg">${esc(set.name)}</span></span><span class="text-xs text-muted">${nRules} quy tắc</span>${icon("arrow_drop_down", { size: 20, cls: "text-muted" })}</button>
        <a class="btn mt-2 w-full border-transparent bg-[#5b47c9] text-white hover:shadow-1" href="#/cases/${c.id}/checks/k1" onclick="${on(() => toast(`Đang đối chiếu theo bộ “${set.name}”`))}">${icon("play_arrow", { size: 18 })} Chạy đối chiếu</a>` : `<a class="btn mt-3 w-full" href="#/settings/checks">${icon("add", { size: 18 })} Thêm bộ quy tắc cho thư viện</a>`}
        ${checks.map((k) => `<a href="#/cases/${c.id}/checks/${k.id}" class="group mt-1 flex items-center gap-2 rounded-xl px-2 py-2 hover:bg-surface/60">${icon("cancel", { size: 18, fill: true, cls: "text-err" })}<span class="min-w-0 flex-1 truncate text-xs text-fg">${k.counts.groups} hợp đồng · ${k.counts.fail} không đạt · ${k.counts.unlinked} chưa xác định</span><span class="text-[11px] whitespace-nowrap text-muted">${fmtDate(k.created_at)}</span></a>`).join("")}</div>
      <div class="mt-4 border-t border-line pt-3"><div class="px-1 pb-1 text-xs font-medium text-muted">Đã tạo</div>
        ${!sheets.length ? '<div class="px-1 py-2 text-xs text-subtle">Chưa có bảng nào.</div>' : sheets.map((s) => {
          const busy = s.status === "running";
          return `<div class="group flex items-center rounded-xl hover:bg-fg/5"><a ${busy ? "" : `href="#/cases/${c.id}/sheets/${s.id}"`} class="flex min-w-0 flex-1 items-center gap-3 px-2 py-2.5 text-left">${busy ? spinner("size-5") : icon("table_view", { size: 22, fill: true, cls: "text-[#107c41]" })}<span class="min-w-0 flex-1"><span class="block truncate text-sm text-fg">${esc(s.name)}</span><span class="block truncate text-xs text-muted">${busy ? `Đang bóc tách ${s.filled}/${s.total} ô…` : `${s.tables.map(typeTitle).join(" · ")} · ${s.tables.reduce((a, t) => a + (M.ROWS[t]?.length ?? 0), 0)} dòng · ${fmtDate(s.created_at)}`}</span></span>${busy ? "" : icon("chevron_right", { size: 20, cls: "text-muted" })}</a>
            ${s.form ? `<button class="btn-icon btn-sm flex-none" title="Tải theo mẫu" onclick="${on(() => toast("Tải " + s.form))}">${icon("download", { size: 18 })}</button>` : ""}<button class="btn-icon btn-sm mr-1 flex-none lg:opacity-0 lg:group-hover:opacity-100" title="Xoá bảng">${icon("delete", { size: 18 })}</button></div>`;
        }).join("")}</div></aside>`;
  }
  function openPickTypes(types) {
    modal({
      title: "Chọn loại giấy tờ cần xuất",
      icon: "checklist",
      width: 480,
      body: `<div class="flex flex-col gap-0.5">${types.map((t) => `<label class="flex h-9 cursor-pointer items-center gap-2 rounded-lg px-2 text-sm text-fg hover:bg-fg/5"><input type="checkbox" ${S.excluded[t.name] ? "" : "checked"} onchange="${on((e) => ((S.excluded[t.name] = !e.target.checked), render()))}"><span class="min-w-0 flex-1 truncate">${t.title}</span><span class="text-xs text-muted">${t.fields.length} cột</span></label>`).join("")}</div>`,
      footer: `<button class="btn btn-primary" onclick="A.closeModal()">Xong</button>`,
    });
  }

  // ------------------------------------------------------------ split
  function viewSplit(caseId) {
    const sp = M.SPLIT;
    if (!S.split) {
      let k = 0;
      S.split = sp.files.flatMap((f) => f.segments.map((g) => ({ ...g, key: "s" + ++k, document_id: f.document_id })));
      S.splitSel = { doc: "d2", page: 4 };
    }
    const draft = S.split;
    const order = Object.fromEntries(sp.files.map((f, i) => [f.document_id, i]));
    const sorted = (l) => [...l].sort((a, b) => order[a.document_id] - order[b.document_id] || a.page_start - b.page_start);
    const bundles = [...new Set(draft.map((d) => d.bundle))].sort();
    const upd = (g, patch) => (Object.assign(g, patch, { source: "user", confidence: 1 }), render());
    const sel = S.splitSel;
    const selFile = sp.files.find((f) => f.document_id === sel?.doc);
    const nDocs = draft.filter((d) => d.label !== "other").length;
    const docRow = (g) => {
      const f = sp.files.find((x) => x.document_id === g.document_id);
      const d = docById(g.document_id);
      const low = g.source === "pipeline" && (g.label === "unknown" || g.confidence < 0.75);
      const prev = draft.find((x) => x.document_id === g.document_id && x.page_end === g.page_start - 1);
      const thumbs = [];
      for (let p = g.page_start; p <= g.page_end; p++)
        thumbs.push(`<span class="flex flex-none items-center gap-1">${p > g.page_start ? `<button class="grid h-19 w-4.5 place-items-center rounded-md hover:bg-fg/8 ${f.marks[p] ? "text-warn" : "text-subtle opacity-40 hover:opacity-100"}" title="Tách tại đây${f.marks[p] ? " · dấu điểm cắt: " + esc(f.marks[p]) : ""}" onclick="${on(() => { draft.splice(draft.indexOf(g) + 1, 0, { ...g, key: "s" + Date.now(), page_start: p, source: "user", confidence: 1 }); upd(g, { page_end: p - 1 }); })}">${icon("content_cut", { size: 16 })}</button>` : ""}<button onclick="${on(() => ((S.splitSel = { doc: g.document_id, page: p }), render()))}" title="Trang ${p}" class="relative h-19 w-14.5 flex-none overflow-hidden rounded-md border border-line bg-surface ${sel?.doc === g.document_id && sel.page === p ? "outline-2 outline-offset-1 outline-accent" : ""}">${pageSVG(d, p)}<span class="absolute right-1 bottom-0.5 rounded bg-surface/80 px-0.5 text-[10px] text-subtle">${p}</span></button></span>`);
      return `<div class="flex gap-3 rounded-xl border p-2.5 max-sm:flex-col ${low ? "border-[#f9ab00]" : "border-line"}">
        <div class="flex w-52 flex-none flex-col gap-1.5">
          <select class="input h-8 px-2 text-[13px]" onchange="${on((e) => upd(g, { label: e.target.value }))}">${g.label === "unknown" ? '<option value="unknown" selected>Chưa phân loại…</option>' : ""}${sp.labels.map((l) => `<option value="${l.name}" ${l.name === g.label ? "selected" : ""}>${l.title}</option>`).join("")}<option value="other" ${g.label === "other" ? "selected" : ""}>Khác</option></select>
          <div class="flex items-center gap-1 text-xs text-muted">${fileIcon(d.file_name, 14)}<span class="min-w-0 truncate">${esc(d.file_name)}</span><span class="whitespace-nowrap">· ${pages(g.page_start, g.page_end)}</span></div>
          <div class="flex flex-wrap items-center gap-1">${g.source === "user" ? '<span class="badge">đã chỉnh</span>' : low ? `<span class="badge badge-warn">AI ${Math.round(g.confidence * 100)}% · cần xem</span>` : `<span class="badge badge-info">AI ${Math.round(g.confidence * 100)}%</span>`}
            <select class="input h-7 px-1.5 text-xs" onchange="${on((e) => upd(g, { bundle: e.target.value === "new" ? "B" + String(bundles.length + 1).padStart(2, "0") : e.target.value }))}">${bundles.map((b) => `<option ${b === g.bundle ? "selected" : ""}>${b}</option>`).join("")}<option value="new">+ Bộ mới</option></select>
            ${prev ? `<button class="btn btn-text btn-sm h-7 px-2 text-xs" title="Gộp với giấy tờ ngay trước trong cùng file" onclick="${on(() => { draft.splice(draft.indexOf(g), 1); upd(prev, { page_end: g.page_end }); })}">${icon("merge", { size: 16 })} Gộp lên</button>` : ""}</div></div>
        <div class="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto pb-1">${thumbs.join("")}</div></div>`;
    };
    return `<div class="flex min-h-0 flex-1 flex-col">
      ${subHeader(`#/cases/${caseId}`, icon("content_cut", { size: 24, cls: "text-accent" }), "Tách & gom trang", `${sp.files.length} file · ${nDocs} giấy tờ · ${bundles.length} bộ · ${sp.opens_with.map(typeTitle).join(", ")} mở bộ mới, giấy tờ sau nối vào bộ đang mở (sửa được)`,
        `<button class="btn" onclick="${on((e) => openMenu(e, [{ icon: "title", label: "Chỉ đọc tiêu đề (mặc định)", onClick: () => toast("AI đang đề xuất lại: 1 lần gọi, chỉ đọc tiêu đề và dấu điểm cắt…") }, { icon: "article", label: "Đọc cả đầu mỗi trang (tốn hơn)", onClick: () => toast("AI đang đọc đầu mỗi trang (tốn hơn)…") }], "right"))}">${icon("auto_awesome", { size: 18 })} AI đề xuất lại${icon("arrow_drop_down", { size: 18, cls: "-mr-2" })}</button>
         <button class="btn btn-primary" onclick="${on(() => { const u = draft.find((d) => d.label === "unknown"); if (u) { S.splitSel = { doc: u.document_id, page: u.page_start }; render(); return toast("Còn giấy tờ chưa phân loại: chọn loại (hoặc Khác) trước khi xác nhận"); } toast(`Đã xác nhận: ${nDocs} giấy tờ trong ${bundles.length} bộ`); go("#/cases/" + caseId); })}">${icon("check", { size: 18 })} Xác nhận tách & gom</button>`)}
      <div class="flex min-h-0 flex-1 border-t border-line">
        <aside class="hidden w-60 flex-none flex-col gap-1 overflow-auto border-r border-line p-2 lg:flex"><div class="px-2 pb-1 text-xs font-medium text-muted">File</div>
          ${sp.files.map((f) => { const d = docById(f.document_id); return `<button class="flex flex-col gap-1 rounded-xl px-3 py-2 text-left ${sel?.doc === f.document_id ? "bg-accent-soft text-on-accent-soft" : "hover:bg-fg/5"}" onclick="${on(() => ((S.splitSel = { doc: f.document_id, page: 1 }), render()))}"><span class="flex items-center gap-2 text-sm">${fileIcon(d.file_name, 16)}<span class="min-w-0 flex-1 truncate">${esc(d.file_name)}</span></span><span class="flex items-center gap-2 text-xs">${f.status === "reviewed" ? '<span class="badge badge-ok">đã duyệt</span>' : '<span class="badge badge-warn">AI đề xuất</span>'}<span class="text-muted">${d.page_count} trang</span></span></button>`; }).join("")}
          <div class="mt-3 rounded-xl bg-surface-2 p-3 text-xs text-muted"><div class="mb-1 font-medium text-fg">Cách AI đề xuất</div>Hệ thống tự đánh dấu chỗ "Trang 1/n", trang trắng, đổi khổ giấy (${icon("content_cut", { size: 12, cls: "align-[-2px] text-warn" })}). AI đọc tiêu đề từng trang để gán loại giấy tờ. Gom bộ theo quy tắc của loại hồ sơ.</div></aside>
        <div class="min-h-0 min-w-0 flex-1 overflow-auto p-4"><div class="mx-auto flex max-w-230 flex-col gap-5">${bundles.map((b) => { const l = sorted(draft.filter((d) => d.bundle === b)); return `<section><div class="mb-2 flex items-center gap-2"><span class="rounded-lg bg-surface-3 px-2.5 py-1 text-sm font-medium text-fg">Bộ ${b}</span><span class="truncate text-xs text-muted">${l.filter((d) => d.label !== "other").map((d) => typeTitle(d.label)).join(" · ")}</span></div><div class="flex flex-col gap-2">${l.map(docRow).join("")}</div></section>`; }).join("")}</div></div>
        <aside class="hidden w-80 flex-none flex-col gap-3 overflow-auto border-l border-line p-3 xl:flex">${sel && selFile ? `<div class="truncate text-sm font-medium text-fg">${esc(docById(sel.doc).file_name)} · trang ${sel.page}</div><div class="overflow-hidden rounded-xl border border-line bg-surface-2">${pageSVG(docById(sel.doc), sel.page)}</div>${selFile.marks[sel.page] ? `<div class="flex gap-2 rounded-lg bg-warn-soft p-2 text-xs text-warn">${icon("content_cut", { size: 16 })} Dấu điểm cắt: ${esc(selFile.marks[sel.page])}</div>` : ""}` : '<div class="py-6 text-center text-sm text-subtle">Chọn một trang để xem lớn.</div>'}</aside>
      </div></div>`;
  }

  // ------------------------------------------------------------ sheet (bảng Excel)
  const colName = (i) => String.fromCharCode(65 + i);
  function viewSheet(caseId, sid) {
    const sh = M.SHEETS.find((s) => s.id === sid);
    const c = caseById(caseId);
    if (!sh) return empty("error", "Không mở được bảng");
    const tab = S.sheetTab[sid] ?? sh.tables[0];
    const type = M.DOC_TYPES.find((t) => t.name === tab);
    const rows = M.ROWS[tab] ?? [];
    const id = (ri, k) => `${sid}:${tab}:${ri}:${k}`;
    const val = (ri, k) => S.drafts[id(ri, k)] ?? S.saved[id(ri, k)] ?? rows[ri][4][k][0];
    const unsaved = Object.keys(S.drafts).filter((k) => k.startsWith(sid + ":"));
    const changes = Object.keys({ ...S.saved, ...S.drafts }).filter((k) => k.startsWith(sid + ":"));
    const selId = S.sheetSel?.startsWith(sid + ":" + tab + ":") ? S.sheetSel : null;
    const cellCls = (ri, k) => {
      const v = rows[ri][4][k];
      if (S.drafts[id(ri, k)] !== undefined || (S.saved[id(ri, k)] !== undefined && S.saved[id(ri, k)] !== v[0])) return "bg-accent-container";
      if (v[4] === "low") return "bg-warn-soft";
      if (v[4] === "calc") return "bg-vlm-soft";
      return "";
    };
    const th = "h-6 border border-line bg-surface-2 px-2 text-center text-[11px] font-normal text-muted";
    const grid = `<table class="border-collapse font-body text-[13px]"><thead><tr><th class="${th} w-10"></th>${["A", "B", ...type.fields.map((_, i) => colName(i + 2))].map((l) => `<th class="${th}">${l}</th>`).join("")}</tr></thead><tbody>
      <tr><td class="${th}">1</td><td class="h-7 border border-line px-2 font-medium">Loại tài liệu</td><td class="h-7 border border-line px-2 font-medium">Bộ</td>${type.fields.map((f) => `<td class="h-7 min-w-36 border border-line px-2 font-medium" title="${esc(f.description)}">${esc(f.label)}</td>`).join("")}</tr>
      ${rows.map((r, ri) => `<tr><td class="${th}">${ri + 2}</td><td class="h-7 border border-line px-2 whitespace-nowrap">${type.title}</td><td class="h-7 border border-line px-2">${r[3]}</td>${type.fields.map((f) => `<td class="h-7 max-w-64 cursor-cell truncate border px-2 whitespace-nowrap ${cellCls(ri, f.key)} ${selId === id(ri, f.key) ? "border-2 border-accent" : "border-line"}" onclick="${on(() => ((S.sheetSel = id(ri, f.key)), (S.sheetPanel = "source"), render()))}" ondblclick="${on((e) => editCell(e.currentTarget, id(ri, f.key)))}">${esc(val(ri, f.key))}</td>`).join("")}</tr>`).join("")}
      ${Array.from({ length: 12 }, (_, i) => `<tr><td class="${th}">${rows.length + 2 + i}</td>${["", "", ...type.fields].map(() => '<td class="h-7 border border-line"></td>').join("")}</tr>`).join("")}</tbody></table>`;
    let side = "";
    if (S.sheetPanel === "edits") {
      side = changes.length ? changes.map((k) => {
        const [, t, ri, key] = k.split(":");
        const r = M.ROWS[t][+ri], f = M.DOC_TYPES.find((x) => x.name === t).fields.find((x) => x.key === key);
        const draft = S.drafts[k] !== undefined;
        return `<div class="cursor-pointer rounded-xl border border-line p-3 hover:bg-fg/4" onclick="${on(() => ((S.sheetTab[sid] = t), (S.sheetSel = k), (S.sheetPanel = "source"), render()))}"><div class="flex items-center gap-2"><span class="min-w-0 flex-1 truncate text-sm font-medium text-fg">${esc(f.label)}</span><span class="font-mono text-[11px] text-subtle">${typeTitle(t)}</span></div><div class="text-xs text-muted">Bộ ${r[3]} · ${esc(docById(r[0]).file_name)} · ${pages(r[1], r[2])}</div><div class="mt-1 text-[13px] text-err line-through">${esc(r[4][key][0] || "(AI không tìm thấy)")}</div><div class="text-[13px] font-medium text-ok">${esc(S.drafts[k] ?? S.saved[k])}</div><div class="mt-2 flex items-center gap-2"><span class="badge ${draft ? "badge-info" : "badge-ok"}">${draft ? "sửa trên trang này" : "đã lưu"}</span><span class="flex-1"></span>${draft ? `<button class="btn btn-text btn-sm" onclick="${on((e) => (e.stopPropagation(), delete S.drafts[k], render()))}">Hoàn tác</button>` : ""}</div></div>`;
      }).join("") + '<p class="px-1 text-xs text-muted">Khi lưu, mỗi chỉnh sửa thành giá trị đã duyệt của trường; ô khác giá trị AI được ghi là <b>AI sai</b>, theo bảng và trường, để cải thiện mô tả trường.</p>'
        : '<div class="px-2 py-6 text-center text-sm text-subtle">Chưa có ô nào khác giá trị AI.<br>Sửa trực tiếp trên lưới hoặc tải lên file .xlsx đã sửa.</div>';
    } else if (selId) {
      const [, , ri, key] = selId.split(":");
      const r = rows[+ri], v = r[4][key], f = type.fields.find((x) => x.key === key);
      const d = docById(r[0]);
      const edited = cellCls(+ri, key) === "bg-accent-container";
      const status = edited ? '<span class="badge badge-info">người dùng sửa</span>' : !v[0] ? '<span class="badge">không tìm thấy</span>' : v[4] === "calc" ? '<span class="badge badge-vlm">AI tính</span>' : v[4] === "low" ? '<span class="badge badge-warn">cần xem</span>' : '<span class="badge">AI đề xuất</span>';
      const line = v[2] ? linesOf(d, v[2]).find((l) => l.line_no === v[3] + 1) : null;
      side = `<div class="flex items-center gap-2"><span class="flex-1 text-sm font-medium text-fg">${esc(f.label)}</span><span class="font-mono text-[11px] text-subtle">${colName(type.fields.indexOf(f) + 2)}${+ri + 2}</span></div>
        <div class="flex flex-wrap gap-1"><span class="badge">Bộ ${r[3]}</span><span class="badge">${type.title}</span>${status}</div>
        <div class="flex items-center gap-2 text-xs text-muted">${fileIcon(d.file_name, 16)}<span class="truncate">${esc(d.file_name)} · ${pages(r[1], r[2])}</span></div>
        ${f.description ? `<div class="rounded-lg bg-surface-2 p-3 text-[13px] text-muted">${esc(f.description)}</div>` : ""}
        ${v[0] ? `<div class="flex flex-col gap-1"><div class="flex text-xs"><span class="flex-1 text-muted">Độ tin cậy</span><span class="text-fg tabular-nums">${Math.round(v[1] * 100)}%</span></div><div class="h-1.5 overflow-hidden rounded-full bg-surface-3"><i class="block h-full rounded-full ${v[4] === "low" ? "bg-warn" : "bg-ok"}" style="width:${Math.round(v[1] * 100)}%"></i></div></div>
          <a class="block overflow-hidden rounded-xl border border-line bg-surface-2" href="#/documents/${d.id}?page=${v[2]}&hl=${v[3] + 1}" title="Mở trang gốc">${pageSVG(d, v[2], { boxes: true, hl: [v[3] + 1], hideBoxes: true })}</a>
          ${line ? `<div class="rounded-lg bg-surface-2 p-3 text-[13px]"><div class="text-xs text-muted">Câu gốc · trang ${v[2]}</div><div class="mt-1 text-fg">${esc(line.text)}</div></div>` : ""}
          ${v[4] === "calc" ? '<div class="rounded-lg bg-warn-soft p-2 text-xs text-warn">Giá trị do AI tính: lấy số hợp đồng chính cho phụ lục cùng bộ.</div>' : ""}` : '<div class="rounded-lg bg-surface-2 p-3 text-[13px] text-muted">AI không tìm thấy giá trị này trong các trang của giấy tờ; đã hỏi lại một lần.</div>'}
        ${edited ? `<div class="text-xs text-muted">Giá trị AI: <s>${esc(v[0] || "(trống)")}</s></div>` : ""}`;
    } else side = '<div class="px-2 py-6 text-center text-sm text-subtle">Chọn một ô trên lưới để xem nguồn, độ tin cậy và trạng thái.</div>';
    const panel = S.sheetPanel;
    return `<div class="flex min-h-0 flex-1 flex-col">
      ${subHeader(`#/cases/${caseId}`, icon("table_view", { size: 24, fill: true, cls: "text-[#107c41]" }), esc(sh.name), `Tạo ${fmtDate(sh.created_at)} · ${sh.tables.length} bảng · ${sh.tables.reduce((a, t) => a + (M.ROWS[t]?.length ?? 0), 0)} giấy tờ · 1 bộ`,
        `<button class="btn" onclick="${on(() => toast("Chọn file .xlsx đã sửa để nhập lại"))}">${icon("upload_file", { size: 18 })} Tải lên bản đã sửa</button>
         <button class="btn" onclick="${on(() => openDownload(sh))}">${icon("download", { size: 18 })} Tải .xlsx</button>
         <button class="btn btn-primary" ${unsaved.length ? "" : "disabled"} onclick="${on(() => { unsaved.forEach((k) => (S.saved[k] = S.drafts[k])); S.drafts = {}; toast(`Đã lưu ${unsaved.length} chỉnh sửa`); render(); })}">${icon("save", { size: 18 })} Lưu${unsaved.length ? ` ${unsaved.length} chỉnh sửa` : ""}</button>
         <button class="btn-icon ${panel ? "bg-accent-soft text-on-accent-soft" : ""}" title="${panel ? "Ẩn" : "Hiện"} nguồn & lịch sử" onclick="${on(() => ((S.sheetPanel = panel ? null : "source"), render()))}">${icon(panel ? "right_panel_close" : "right_panel_open", { size: 22 })}</button>`)}
      <div class="flex min-h-0 flex-1 border-t border-line max-lg:flex-col">
        <div class="flex min-h-0 min-w-0 flex-1 flex-col"><div class="min-h-0 flex-1 overflow-auto bg-surface">${grid}</div>
          <div class="flex flex-none flex-wrap gap-1 border-t border-line bg-surface-2 px-2 pt-1">${sh.tables.map((t) => `<button class="rounded-t-md px-4 py-1.5 text-[13px] ${t === tab ? "border-b-2 border-[#107c41] bg-surface font-medium" : "text-muted"}" onclick="${on(() => ((S.sheetTab[sid] = t), render()))}">${typeTitle(t)} <span class="text-xs text-muted">${M.ROWS[t]?.length ?? 0}</span></button>`).join("")}<span class="flex-1"></span>
            <div class="flex flex-wrap items-center gap-3 px-2 text-[11.5px] text-muted"><span class="flex items-center gap-1"><i class="inline-block size-3 rounded-sm bg-accent-container"></i> người dùng sửa</span><span class="flex items-center gap-1"><i class="inline-block size-3 rounded-sm bg-warn-soft"></i> cần xem</span><span class="flex items-center gap-1"><i class="inline-block size-3 rounded-sm bg-vlm-soft"></i> AI tính</span></div></div></div>
        ${panel ? `<aside class="flex w-full flex-none flex-col border-line max-lg:max-h-[50%] max-lg:border-t lg:w-88 lg:border-l"><div class="flex flex-none items-center border-b border-line px-2"><button class="tab ${panel === "source" ? "tab-active" : ""}" onclick="${on(() => ((S.sheetPanel = "source"), render()))}">Nguồn${selId ? " ô" : ""}</button><button class="tab ${panel === "edits" ? "tab-active" : ""}" onclick="${on(() => ((S.sheetPanel = "edits"), render()))}">Lịch sử chỉnh sửa ${changes.length ? `(${changes.length})` : ""}</button><span class="flex-1"></span><button class="btn-icon btn-sm" onclick="${on(() => ((S.sheetPanel = null), render()))}">${icon("right_panel_close", { size: 20 })}</button></div><div class="flex min-h-0 flex-1 flex-col gap-3 overflow-auto p-3">${side}</div></aside>`
        : `<aside class="flex flex-none items-center gap-1 border-line p-1.5 max-lg:border-t lg:w-12 lg:flex-col lg:border-l"><button class="btn-icon" title="Nguồn của ô" onclick="${on(() => ((S.sheetPanel = "source"), render()))}">${icon("info")}</button><button class="btn-icon" title="Lịch sử chỉnh sửa" onclick="${on(() => ((S.sheetPanel = "edits"), render()))}">${icon("history")}</button></aside>`}
      </div></div>`;
  }
  function editCell(td, key) {
    td.contentEditable = "true";
    td.classList.add("outline-2", "outline-accent");
    td.focus();
    document.getSelection().selectAllChildren(td);
    const done = () => {
      td.contentEditable = "false";
      S.drafts[key] = td.textContent.trim();
      render();
    };
    td.onblur = done;
    td.onkeydown = (e) => e.key === "Enter" && (e.preventDefault(), td.blur());
  }
  function openDownload(sh) {
    modal({
      title: "Tải .xlsx",
      icon: "download",
      body: `<p class="text-sm">Mỗi bảng được chọn là một sheet: dòng 1 là tên trường, cột A là loại tài liệu, các cột sau là trường của mẫu bảng, mỗi file có loại tài liệu đó một dòng. File chỉ có dữ liệu, không có cột nguồn, độ tin cậy hay trạng thái.</p>
        <div class="flex flex-col gap-1 rounded-xl border border-line p-1.5">${sh.tables.map((t) => `<label class="flex h-10 cursor-pointer items-center gap-3 rounded-lg px-2 text-sm text-fg hover:bg-fg/5"><input type="checkbox" checked><span class="flex-1">${typeTitle(t)}</span><span class="text-xs text-muted">${M.ROWS[t]?.length ?? 0} dòng</span></label>`).join("")}</div>
        <p class="text-xs">Kèm một sheet ẩn chứa mã ô để nhập lại bản đã sửa.</p>`,
      footer: `<button class="btn btn-text" onclick="A.closeModal()">Huỷ</button><button class="btn btn-primary" onclick="${on(() => (closeModal(), toast("Prototype: không tạo file thật")))}">${icon("download", { size: 18 })} Tải xuống</button>`,
    });
  }

  // ------------------------------------------------------------ check (đối chiếu)
  // Kết quả đối chiếu: trên cùng là kết luận (bao nhiêu vấn đề); trái là các
  // giấy tờ của bộ kèm trạng thái (giấy tờ nào sai); giữa là các vấn đề cần xử
  // lý, mỗi quy tắc một bảng so giá trị từng giấy tờ, dòng sai tô đỏ; quy tắc
  // đạt thu gọn ở dưới. Bấm một giấy tờ để chỉ xem các quy tắc liên quan tới nó.
  function viewCheck(caseId) {
    const k = M.CHECK, c = caseById(caseId), set = setById(k.ruleset);
    const sel = S.checkDoc ?? null;
    const docLink = (label, doc, page, cls = "") => `<a class="${cls} hover:underline" href="#/documents/${doc}?page=${page}" title="Mở trang gốc">${esc(label)}</a>`;
    const issuesOf = (label) => k.results.filter((r) => r.rows?.some((x) => x[0] === label && !x[5]));
    const unsureOf = (label) => k.results.some((r) => r.rows?.some((x) => x[0] === label && x[6]));
    const fails = k.results.filter((r) => r.status === "fail"), unknowns = k.results.filter((r) => r.status === "unknown"), passes = k.results.filter((r) => r.status === "pass");
    const nIssues = fails.length + unknowns.length + k.unlinked.length;
    const visible = (r) => !sel || r.rows?.some((x) => x[0] === sel) || (sel === k.anchor[0] && !r.rows);
    const rule = (r) => set.checks[r.rule];
    const cols = (r) => rule(r).text ? ["", ""] : [fieldOf(rule(r).a).label, fieldOf(rule(r).b).label];
    const table = (r) => {
      if (!r.rows) return "";
      const [ca, cb] = cols(r);
      const sum = rule(r).op === "sum_le";
      return `<div class="overflow-x-auto"><table class="w-full min-w-120 text-sm"><thead><tr class="text-left text-xs text-muted"><th class="py-2 pl-4 font-medium">Giấy tờ</th><th class="px-3 font-medium">${esc(ca)}</th>${sum ? "" : `<th class="px-3 font-medium">${esc(cb)} <span class="font-normal text-subtle">(${esc(typeTitle(rule(r).b[0]))})</span></th>`}<th class="w-28 pr-4 text-right font-medium">Kết quả</th></tr></thead><tbody>
        ${r.rows.map(([label, doc, page, va, vb, ok, unsure]) => `<tr class="border-t border-line ${ok ? "" : "bg-err-soft/50"} ${sel === label ? "outline-2 -outline-offset-2 outline-accent" : ""}">
          <td class="py-2 pl-4">${docLink(label, doc, page, ok ? "text-fg" : "font-medium text-err")}<span class="ml-1.5 text-xs text-muted">tr. ${page}</span></td>
          <td class="px-3 tabular-nums ${ok ? "text-fg" : "font-medium text-err"}">${esc(va)}${unsure ? ` <span class="badge badge-warn align-middle" title="AI đọc chưa chắc chắn, nên xem trang gốc">kiểm tra lại</span>` : ""}</td>
          ${sum ? "" : `<td class="px-3 tabular-nums text-fg">${esc(vb)}</td>`}
          <td class="pr-4 text-right whitespace-nowrap">${ok ? `<span class="inline-flex items-center gap-1 text-xs text-ok">${icon("check_circle", { size: 16, fill: true })}Khớp</span>` : `<span class="inline-flex items-center gap-1 text-xs font-medium text-err">${icon("cancel", { size: 16, fill: true })}Không khớp</span>`}</td></tr>`).join("")}
        ${r.total ? `<tr class="border-t-2 border-line bg-surface-2/60 font-medium"><td class="py-2 pl-4 text-fg">Tổng</td><td class="px-3 tabular-nums text-fg">${esc(r.total[0])}</td>${sum ? "" : "<td></td>"}<td class="pr-4 text-right text-xs ${r.status === "pass" ? "text-ok" : "text-err"}">${r.status === "pass" ? "≤" : ">"} ${esc(r.total[1])}<span class="block font-normal text-muted">${esc(r.total[2])}</span></td></tr>` : ""}
        </tbody></table></div>`;
    };
    const statusPill = (st) => ({ fail: `<span class="badge badge-err h-7 px-2.5">${icon("cancel", { size: 14 })} Không đạt</span>`, unknown: `<span class="badge badge-warn h-7 px-2.5">${icon("help", { size: 14 })} Thiếu thông tin</span>`, pass: `<span class="badge badge-ok h-7 px-2.5">${icon("check_circle", { size: 14 })} Đạt</span>` })[st];
    const issueCard = (r) => {
      const R = rule(r), wrong = r.rows?.filter((x) => !x[5]) ?? [];
      return `<section class="overflow-hidden rounded-2xl border border-line border-l-4 ${r.status === "fail" ? "border-l-err" : "border-l-warn"} bg-surface">
        <div class="flex flex-wrap items-start gap-3 px-4 pt-3 pb-2"><div class="min-w-0 flex-1"><p class="text-sm leading-7 text-fg">${ruleSentence(R, set)}</p>${R.text ? `<span class="badge badge-vlm">${icon("auto_awesome", { size: 12 })} AI đánh giá</span>` : ""}</div>${statusPill(r.status)}</div>
        ${wrong.length ? `<div class="mx-4 mb-2 flex flex-wrap items-center gap-2 text-sm"><span class="text-muted">Giấy tờ sai:</span>${wrong.map(([l, d, pg]) => `<a class="tag !bg-err-soft !text-err hover:underline" href="#/documents/${d}?page=${pg}">${icon("description", { size: 14 })}${esc(l)} · tr. ${pg}</a>`).join("")}</div>` : ""}
        ${table(r)}
        ${r.missing ? `<ul class="mx-4 mb-3 flex flex-col gap-1 text-sm text-fg">${r.missing.map((m) => `<li class="flex items-start gap-2">${icon("help", { size: 18, cls: "mt-0.5 flex-none text-warn" })}${esc(m)}</li>`).join("")}</ul>` : ""}
        <div class="flex flex-wrap items-center gap-2 border-t border-line bg-surface-2/50 px-4 py-2.5">${r.hint ? `<p class="min-w-0 flex-1 text-xs text-muted">${esc(r.hint)}</p>` : r.missing ? '<p class="min-w-0 flex-1 text-xs text-muted">Bổ sung giấy tờ còn thiếu vào hồ sơ rồi đối chiếu lại; nếu không cần thì tắt quy tắc này trong bộ.</p>' : '<span class="flex-1"></span>'}
          ${wrong[0] ? `<a class="btn btn-sm" href="#/documents/${wrong[0][1]}?page=${wrong[0][2]}">${icon("open_in_new", { size: 16 })} Xem trang gốc</a>` : ""}<a class="btn btn-sm" href="#/cases/${caseId}/sheets/${k.sheet_id}">${icon("table_view", { size: 16 })} Sửa số liệu</a></div></section>`;
    };
    const unlinkedCard = k.unlinked.map(([label, doc, page, type, , reason]) => `<section class="rounded-2xl border border-line border-l-4 border-l-warn bg-surface px-4 py-3">
        <div class="flex flex-wrap items-start gap-3"><div class="min-w-0 flex-1"><p class="text-sm text-fg"><i class="mr-1.5 inline-block size-2 rounded-full align-middle" style="background:${typeColor(type)}"></i>${docLink(label, doc, page, "font-medium text-fg")} <span class="text-muted">chưa ghép được với hợp đồng nào</span></p><p class="mt-0.5 text-xs text-muted">${esc(reason)}</p></div><span class="badge badge-warn h-7 px-2.5">${icon("link_off", { size: 14 })} Chưa ghép</span></div>
        <div class="mt-2 flex flex-wrap items-center gap-2"><label class="text-xs text-muted" for="ul-${label}">Thuộc hợp đồng</label><select id="ul-${label}" class="input h-9 w-64" onchange="${on(() => toast("Đã ghép và đối chiếu lại"))}"><option value="">— chọn —</option><option>${esc(k.anchor[0])}</option><option>Không thuộc hợp đồng nào</option></select><span class="text-xs text-subtle">Lựa chọn được giữ cho các lần sau.</span></div></section>`).join("");
    // Cột trái: các giấy tờ của bộ và trạng thái của từng giấy tờ.
    const docRow = ([label, doc, page, type, src, reason], kind) => {
      const bad = issuesOf(label).length, unsure = unsureOf(label), on_ = sel === label;
      const st = kind === "anchor" ? `<span class="text-[11px] text-muted">gốc</span>` : kind === "unlinked" ? icon("link_off", { size: 18, cls: "text-warn" }) : bad ? `<span class="inline-flex items-center gap-1 text-[11px] font-medium text-err">${icon("cancel", { size: 16, fill: true })}${bad} lỗi</span>` : unsure ? icon("error", { size: 18, cls: "text-warn" }) : icon("check_circle", { size: 18, fill: true, cls: "text-ok" });
      return `<button class="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left ${on_ ? "bg-accent-soft" : bad ? "bg-err-soft/40 hover:bg-err-soft/70" : "hover:bg-fg/5"}" title="${esc(reason ?? "")}" aria-pressed="${on_}" onclick="${on(() => ((S.checkDoc = on_ ? null : label), render()))}"><i class="size-2 flex-none rounded-full" style="background:${typeColor(type)}"></i><span class="min-w-0 flex-1"><span class="block truncate text-sm ${bad ? "font-medium text-err" : "text-fg"}">${esc(label)}</span><span class="block truncate text-[11px] text-muted">${esc(typeTitle(type))} · tr. ${page}</span></span>${st}</button>`;
    };
    const nBadDocs = k.members.filter((m) => issuesOf(m[0]).length).length;
    const shownIssues = [...fails, ...unknowns].filter(visible);
    return `<div class="flex min-h-0 flex-1 flex-col">
      ${subHeader(`#/cases/${caseId}`, icon("fact_check", { size: 24, fill: true, cls: "text-[#5b47c9]" }), "Kết quả đối chiếu", `Bộ quy tắc “${esc(set.name)}” · ${esc(k.anchor[0])} · chạy lúc ${fmtDate(k.ran_at)}`,
        `<a class="btn btn-text" href="#/settings/checks/${set.id}">${icon("rule", { size: 18 })} Bộ quy tắc</a><button class="btn btn-tonal" onclick="${on(() => toast("Đã đối chiếu lại"))}">${icon("refresh", { size: 18 })} Đối chiếu lại</button>`)}
      <div class="px-6 pb-3"><div class="flex flex-wrap items-center gap-4 rounded-2xl ${nIssues ? "bg-err-soft/50" : "bg-ok-soft/60"} px-5 py-3">
        ${icon(nIssues ? "report" : "verified", { size: 32, fill: true, cls: nIssues ? "text-err" : "text-ok" })}
        <div class="min-w-0 flex-1"><div class="text-base font-medium text-fg">${nIssues ? `${nIssues} vấn đề cần xử lý` : "Mọi quy tắc đều đạt"}</div><div class="text-sm text-muted">${nBadDocs ? `${nBadDocs} giấy tờ có lỗi` : "Không giấy tờ nào sai"} · ${fails.length} quy tắc không đạt · ${unknowns.length} thiếu thông tin · ${k.unlinked.length} chứng từ chưa ghép · ${passes.length} quy tắc đạt</div></div></div></div>
      <div class="flex min-h-0 flex-1 border-t border-line max-md:flex-col">
        <nav class="flex-none overflow-auto border-r border-line p-2 md:w-64 max-md:max-h-56" aria-label="Giấy tờ trong bộ">
          <div class="flex items-center px-2 pt-1 pb-1.5"><span class="flex-1 text-xs font-medium text-muted">Giấy tờ trong bộ (${k.members.length + 1})</span>${sel ? `<button class="text-xs text-accent hover:underline" onclick="${on(() => ((S.checkDoc = null), render()))}">Xem tất cả</button>` : ""}</div>
          ${docRow(k.anchor, "anchor")}${k.members.map((m) => docRow(m)).join("")}
          ${k.unlinked.length ? `<div class="px-2 pt-3 pb-1.5 text-xs font-medium text-muted">Chưa ghép được</div>${k.unlinked.map((m) => docRow(m, "unlinked")).join("")}` : ""}
        </nav>
        <main class="min-w-0 flex-1 overflow-auto px-6 py-4"><div class="flex max-w-5xl flex-col gap-3">
          ${sel ? `<div class="flex items-center gap-2 text-sm text-muted">${icon("filter_alt", { size: 18 })}Đang xem các quy tắc liên quan tới <b class="font-medium text-fg">${esc(sel)}</b></div>` : ""}
          <h2 class="text-sm font-medium text-fg">Cần xử lý (${shownIssues.length + (sel ? 0 : k.unlinked.length)})</h2>
          ${shownIssues.map(issueCard).join("")}${sel ? "" : unlinkedCard}
          ${!shownIssues.length && sel ? '<p class="rounded-2xl bg-ok-soft/50 px-4 py-3 text-sm text-fg">Giấy tờ này không có lỗi nào.</p>' : ""}
          <details class="group mt-2 rounded-2xl border border-line" ${sel ? "open" : ""}><summary class="flex min-h-12 cursor-pointer list-none items-center gap-2 px-4 text-sm font-medium text-fg">${icon("check_circle", { size: 20, fill: true, cls: "text-ok" })}<span class="flex-1">Đạt (${passes.filter(visible).length})</span>${icon("expand_more", { size: 20, cls: "text-muted transition-transform group-open:rotate-180" })}</summary>
            ${passes.filter(visible).map((r) => `<div class="border-t border-line"><div class="flex flex-wrap items-start gap-3 px-4 pt-3 pb-2"><p class="min-w-0 flex-1 text-sm leading-7 text-fg">${ruleSentence(rule(r), set)}</p><span class="text-xs text-muted">${r.rows.length}/${r.rows.length} giấy tờ khớp</span></div>${table(r)}</div>`).join("")}</details>
        </div></main></div></div>`;
  }

  // ------------------------------------------------------------ doc types (phòng thử kiểu Docsumo)
  // Mỗi loại giấy tờ: trường, prompt trích xuất có phiên bản, tài liệu mẫu đã
  // qua docpipe (markdown VLM chính, text TurboOCR tham khảo) và giá trị đúng
  // do người dùng duyệt để chấm điểm và tối ưu prompt.
  const DEFAULT_PROMPT = (title) => `Bạn đọc một ${title} và lấy ra các thông tin bên dưới.\nDựa vào phần Nội dung; chữ số nào không rõ thì kiểm lại ở phần Các dòng.\nChỉ ghi đúng giá trị có trên tài liệu. Thông tin nào không thấy thì để trống, không đoán.`;
  const toCode = (s) => s.normalize("NFD").replace(/[̀-ͯ]/g, "").replace(/đ/gi, "d").toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "").slice(0, 40);
  const lab = (t) => (M.LAB[t.name] ??= { current: 1, versions: [{ v: 1, created_at: new Date().toISOString(), by: S.user.name, note: "Bản đầu tiên", prompt: DEFAULT_PROMPT(t.title.toLowerCase()), rules: [], shots: [], ok: 1 }], samples: [], proposal: null });
  const ver = (L, v = L.current) => L.versions.find((x) => x.v === v);
  // Số phiên bản mới lấy từ số lớn nhất còn lại, để không trùng sau khi xoá.
  const nextV = (L) => Math.max(...L.versions.map((x) => x.v)) + 1;
  const norm = (v) => String(v ?? "").trim();
  // Mỗi phiên bản trỏ tới một bộ kết quả mẫu (ok); bản sửa tay giữ bộ của bản gốc.
  const okOf = (L, v = L.current) => ver(L, v).ok;
  const outOf = (s, k) => s.out[k] ?? s.out[1];
  const ran = (s) => s.status === "output" || s.status === "reviewed";
  function score(L, fields, k, only) {
    let ok = 0, n = 0;
    for (const s of L.samples)
      if (s.truth && ran(s))
        for (const f of fields)
          if (!only || only === f.key) n++, norm(outOf(s, k)[f.key]) === norm(s.truth[f.key]) && ok++;
    return n ? ok / n : null;
  }
  const pct = (x) => (x == null ? "—" : Math.round(x * 100) + "%");
  const pctTone = (x) => (x == null ? "text-subtle" : x >= 0.95 ? "text-ok" : x >= 0.8 ? "text-warn" : "text-err");
  const mdOf = (s) => s.content.map((p) => (p.title ? "## " + p.title + "\n\n" : "") + p.body.join("  \n")).join("\n\n");
  // So khớp theo dòng (LCS): "=" giữ nguyên, "-" chỉ có ở bản cũ, "+" chỉ có ở bản mới.
  function diffLines(a, b) {
    const dp = Array.from({ length: a.length + 1 }, () => new Array(b.length + 1).fill(0));
    for (let i = a.length - 1; i >= 0; i--) for (let j = b.length - 1; j >= 0; j--) dp[i][j] = a[i] === b[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
    const out = [];
    let i = 0, j = 0;
    while (i < a.length && j < b.length)
      if (a[i] === b[j]) out.push({ op: "=", a: ++i, b: ++j, t: a[i - 1] });
      else if (dp[i + 1][j] >= dp[i][j + 1]) out.push({ op: "-", a: ++i, t: a[i - 1] });
      else out.push({ op: "+", b: ++j, t: b[j - 1] });
    while (i < a.length) out.push({ op: "-", a: ++i, t: a[i - 1] });
    while (j < b.length) out.push({ op: "+", b: ++j, t: b[j - 1] });
    return out;
  }
  function buildPrompt(t, v, L) {
    // Viết bằng chữ thường, không có cú pháp kỹ thuật: người nghiệp vụ đọc được.
    let s = v.prompt + "\n\nCÁC THÔNG TIN CẦN LẤY:\n" + t.fields.map((f) => `- ${f.label}${f.description ? ": " + f.description : ""}`).join("\n");
    if (v.rules.length) s += "\n\nQUY TẮC THEO TRƯỜNG:\n" + v.rules.map((r) => "- " + r).join("\n");
    if (v.shots.length)
      s += "\n\nVÍ DỤ:\n" + v.shots.map((sh, i) => { const x = L.samples.find((y) => y.id === sh.sample); return `Ví dụ ${i + 1} (${x.file_name}):\nNội dung tài liệu:\n${x.content.map((p) => [p.title, ...p.body].filter(Boolean).join("\n")).join("\n")}\nKết quả đúng:\n${t.fields.map((f) => `- ${f.label}: ${x.truth[f.key] || "(để trống)"}`).join("\n")}`; }).join("\n\n");
    return s + "\n\nTÀI LIỆU CẦN ĐỌC:\nNội dung (nguồn chính):\n{{nội dung của tài liệu}}\nCác dòng (chỉ để kiểm lại chữ số):\n{{các dòng, bỏ dòng dưới 70%}}";
  }
  const SAMPLE_STATUS = {
    converting: ['<span class="badge">' + '<span class="spinner size-2.5 border-[1.5px]"></span> đang xử lý</span>'],
    converted: ['<span class="badge">chưa chạy thử</span>'],
    output: ['<span class="badge badge-warn">chưa duyệt</span>'],
  };

  // Thư viện nào đang dùng loại giấy tờ; thêm/bỏ loại khỏi một thư viện.
  const kbsOf = (t) => M.KBS.filter((k) => k.config.doc_types.includes(t));
  function toggleKB(t, k) {
    const a = k.config.doc_types, i = a.indexOf(t);
    if (i < 0) a.push(t), toast(`Đã thêm “${t.title}” vào ${k.name}`);
    else a.splice(i, 1), toast(`Đã bỏ “${t.title}” khỏi ${k.name}; trang mới sẽ không phân loại theo loại này`);
    render();
  }
  const kbMenu = (e, t) => openMenu(e, M.KBS.map((k) => ({ checked: k.config.doc_types.includes(t), label: esc(k.name), onClick: () => toggleKB(t, k) })), "right");

  // Cài đặt → Loại giấy tờ: danh mục chung, mỗi loại một dòng với chỉ số và
  // thư viện đang dùng; lọc theo thư viện, tìm theo tên.
  function docTypesTab() {
    const f = S.dtFilter ?? "all", q = (S.dtQ ?? "").trim().toLowerCase();
    let list = M.DOC_TYPES;
    if (f === "unused") list = list.filter((t) => !kbsOf(t).length);
    else if (f !== "all") list = list.filter((t) => M.KBS.find((k) => k.id === f).config.doc_types.includes(t));
    if (q) list = list.filter((t) => (t.title + " " + t.name).toLowerCase().includes(q));
    const chip = (v, label, n) => `<button class="chip ${f === v ? "chip-on" : ""}" aria-pressed="${f === v}" onclick="${on(() => ((S.dtFilter = v), render()))}">${f === v ? icon("check", { size: 18 }) : ""}${label} <span class="tabular-nums ${f === v ? "" : "text-subtle"}">${n}</span></button>`;
    const td = "border-b border-line px-3 py-2.5 align-middle";
    const row = (t) => {
      const L = M.LAB[t.name];
      const acc = L ? score(L, t.fields, okOf(L)) : null;
      const kbs = kbsOf(t);
      const href = `#/settings/doctypes/${t.name}`;
      return `<tr class="group cursor-pointer transition-colors hover:bg-surface-2" onclick="location.hash='${href}'">
        <td class="${td} pl-4"><div class="flex items-center gap-3"><i class="size-2.5 flex-none rounded-full" style="background:${typeColor(t.name)}" aria-hidden="true"></i><div class="min-w-0"><a href="${href}" class="block truncate font-medium text-fg">${esc(t.title)}</a><code class="font-mono text-xs text-subtle">${t.name}</code></div></div></td>
        <td class="${td} tabular-nums ${t.fields.length ? "text-muted" : "text-warn"}">${t.fields.length || "chưa có"}</td>
        <td class="${td} tabular-nums text-muted max-md:hidden">${L?.samples.length ? `${L.samples.filter((x) => x.truth).length}/${L.samples.length}` : '<span class="text-subtle">—</span>'}</td>
        <td class="${td}">${acc == null ? '<span class="text-subtle">—</span>' : `<div class="flex items-center gap-2"><span class="w-10 font-medium tabular-nums ${pctTone(acc)}">${pct(acc)}</span><span class="h-1.5 w-20 overflow-hidden rounded-full bg-surface-3 max-lg:hidden"><i class="block h-full rounded-full ${acc >= 0.95 ? "bg-ok" : acc >= 0.8 ? "bg-warn" : "bg-err"}" style="width:${acc * 100}%"></i></span></div>`}</td>
        <td class="${td} max-md:hidden"><div class="flex flex-wrap gap-1">${kbs.length ? kbs.map((k) => `<span class="tag">${icon("folder_shared", { size: 14 })}${esc(k.name)}</span>`).join("") : '<span class="text-xs text-subtle">Chưa dùng ở thư viện nào</span>'}</div></td>
        <td class="${td} pr-2 text-right whitespace-nowrap" onclick="event.stopPropagation()">
          <button class="btn-icon btn-sm" title="Thêm vào hoặc bỏ khỏi thư viện" aria-label="Thư viện dùng ${esc(t.title)}" onclick="${on((e) => kbMenu(e, t))}">${icon("library_add", { size: 20 })}</button>
          <button class="btn-icon btn-sm" aria-label="Thao tác với ${esc(t.title)}" onclick="${on((e) => openMenu(e, [
            { icon: "tune", label: "Cấu hình trường & hướng dẫn AI", onClick: () => go(href) },
            { icon: "content_copy", label: "Nhân bản", onClick: () => toast("Đã nhân bản thành “" + t.title + " (bản sao)”") },
            { divider: true },
            { icon: "delete", label: "Xoá", danger: true, onClick: () => confirm(kbs.length ? `“${t.title}” đang dùng ở ${kbs.map((k) => k.name).join(", ")}. Xoá sẽ bỏ khỏi các thư viện này. Tiếp tục?` : `Xoá “${t.title}”?`) && (M.DOC_TYPES.splice(M.DOC_TYPES.indexOf(t), 1), kbs.forEach((k) => k.config.doc_types.splice(k.config.doc_types.indexOf(t), 1)), toast("Đã xoá loại giấy tờ"), render()) },
          ], "right"))}">${icon("more_vert", { size: 20 })}</button></td></tr>`;
    };
    return sectionHead("Loại giấy tờ", "Danh mục dùng chung cho mọi thư viện. Mỗi loại có trường cần lấy, hướng dẫn AI và tài liệu mẫu để đo độ chính xác. Thư viện chỉ phân loại trang, tạo bảng Excel và đối chiếu theo các loại đã được thêm vào nó.",
        `<button class="btn btn-primary" onclick="${on(openNewDocType)}">${icon("add", { size: 18 })} Loại giấy tờ mới</button>`) +
      `<div class="flex flex-wrap items-center gap-2">
        ${chip("all", "Tất cả", M.DOC_TYPES.length)}${M.KBS.map((k) => chip(k.id, esc(k.name), k.config.doc_types.length)).join("")}${chip("unused", "Chưa dùng", M.DOC_TYPES.filter((t) => !kbsOf(t).length).length)}
        <span class="flex-1"></span>
        <label class="flex h-10 w-full items-center gap-2 rounded-full bg-surface-3 px-3 sm:w-64">${icon("search", { size: 20, cls: "text-muted" })}<input class="min-w-0 flex-1 bg-transparent text-sm text-fg outline-none placeholder:text-muted" placeholder="Tìm loại giấy tờ" aria-label="Tìm loại giấy tờ" value="${esc(S.dtQ ?? "")}" onchange="${on((e) => ((S.dtQ = e.target.value), render()))}"></label></div>
      <div class="overflow-x-auto rounded-2xl border border-line"><table class="w-full border-collapse text-sm"><thead><tr class="text-left text-[13px] text-muted">
        <th class="h-11 border-b border-line pl-4 font-medium">Loại giấy tờ</th><th class="border-b border-line px-3 font-medium">Trường</th><th class="border-b border-line px-3 font-medium max-md:hidden">Mẫu đã duyệt</th><th class="border-b border-line px-3 font-medium">Độ chính xác</th><th class="border-b border-line px-3 font-medium max-md:hidden">Dùng ở thư viện</th><th class="w-24 border-b border-line"></th></tr></thead>
        <tbody>${list.map(row).join("")}</tbody></table>
        ${!list.length ? `<p class="px-4 py-6 text-center text-sm text-muted">Không có loại nào khớp${q ? ` “${esc(S.dtQ)}”` : ""}. <button class="text-accent hover:underline" onclick="${on(() => ((S.dtQ = ""), (S.dtFilter = "all"), render()))}">Xem tất cả</button> hoặc tạo loại mới.</p>` : ""}</div>`;
  }


  function openNewDocType() {
    modal({
      title: "Loại giấy tờ mới",
      icon: "category",
      body: `<label class="field">Tên loại giấy tờ<input id="dt-title" class="input" autofocus placeholder="VD: Giấy nộp tiền vào NSNN" oninput="${on((e) => (document.getElementById("dt-code").textContent = toCode(e.target.value) || "sinh từ tên"))}"></label>
        <div class="-mt-2 text-xs">Mã: <code id="dt-code" class="font-mono">sinh từ tên</code></div>
        <label class="field">Mô tả (giúp phân loại trang)<input class="input" placeholder="Đặc điểm nhận biết: tiêu đề, mẫu số, cơ quan phát hành…"></label>
        <fieldset class="flex flex-col gap-1"><legend class="text-xs font-medium text-muted">Dùng ở thư viện</legend>${M.KBS.map((k) => `<label class="flex h-9 items-center gap-3 text-sm text-fg"><input type="checkbox" data-kb="${k.id}" ${k.id === S.kb ? "checked" : ""} class="size-4 accent-accent">${esc(k.name)}</label>`).join("")}</fieldset>
        <p class="text-xs">Tạo xong, tải lên vài tài liệu mẫu khác bố cục (3–5 là đủ để bắt đầu) để chạy thử và đo độ chính xác.</p>`,
      footer: `<button class="btn btn-text" onclick="A.closeModal()">Huỷ</button><button class="btn btn-primary" onclick="${on(() => {
        const title = document.getElementById("dt-title").value.trim();
        if (!title) return toast("Nhập tên loại giấy tờ");
        const t = { name: toCode(title), title, fields: [] };
        M.DOC_TYPES.push(t);
        for (const k of M.KBS) if (document.querySelector(`#modal [data-kb="${k.id}"]`)?.checked) k.config.doc_types.push(t);
        closeModal();
        go(`#/settings/doctypes/${t.name}`);
        setTimeout(() => uploadSamples(t), 50);
      })}">Tạo</button>`,
    });
  }

  // Tải mẫu lên: docpipe chạy nền, mỗi mẫu qua 4 bước rồi thành "chưa chạy".
  function uploadSamples(t) {
    const files = [["UNC_SHB_0915.pdf", 640_000], ["UNC_VietinBank_1002.jpg", 520_000]];
    modal({
      title: "Tải tài liệu mẫu",
      icon: "upload_file",
      width: 600,
      body: `<p class="text-sm">Mẫu của loại <b class="font-medium text-fg">${esc(t.title)}</b>. Nên chọn các mẫu khác ngân hàng, khác bố cục, có cả bản scan mờ.</p>
        <div class="flex cursor-pointer flex-col items-center gap-2 rounded-2xl border-2 border-dashed border-line px-6 py-7 text-center text-muted hover:bg-fg/4">${icon("cloud_upload", { size: 36, cls: "text-accent" })}<div class="text-sm">Kéo thả file vào đây hoặc <span class="font-medium text-accent">chọn từ máy</span></div><div class="text-xs text-subtle">PDF, ảnh scan · chọn được nhiều file</div></div>
        <div class="overflow-hidden rounded-xl border border-line">${files.map(([n, s]) => `<div class="flex h-11 items-center gap-3 border-t border-line pr-1 pl-4 text-sm text-fg first:border-t-0">${fileIcon(n, 20)}<span class="flex-1 truncate">${n}</span><span class="text-xs text-muted">${fmtSize(s)}</span><button class="btn-icon btn-sm">${icon("close", { size: 18 })}</button></div>`).join("")}</div>
        <p class="text-xs">Sau khi tải lên, hệ thống tự xử lý từng file. Xong là xem và chạy thử được.</p>`,
      footer: `<button class="btn btn-text" onclick="A.closeModal()">Huỷ</button><button class="btn btn-primary" onclick="${on(() => {
        const L = lab(t);
        const base = M.LAB.unc.samples.find((s) => s.id === "s5");
        const added = files.map(([n], i) => ({ ...base, id: "n" + Date.now() + i, file_name: n, status: "converting", truth: null, ran_at: null }));
        L.samples.push(...added);
        closeModal();
        toast(`Đã nhận ${files.length} mẫu, đang xử lý`);
        go(`#/settings/doctypes/${t.name}?tab=samples&s=${added[0].id}`);
        added.forEach((s, i) => setTimeout(() => {
          s.status = "converted";
          if (location.hash.startsWith("#/settings/doctypes/")) render();
        }, 2500 + i * 1200));
      })}">Tải lên ${files.length} file</button>`,
    });
  }

  function runSamples(L, ids) {
    S.dtRunning = new Set(ids);
    render();
    setTimeout(() => {
      for (const s of L.samples) if (ids.includes(s.id)) (s.status = s.truth ? "reviewed" : "output"), (s.ran_at = new Date().toISOString());
      S.dtRunning = null;
      toast(`Đã chạy thử hướng dẫn v${L.current} trên ${ids.length} mẫu`);
      render();
    }, 1300);
  }

  function viewDocType(name, q) {
    const t = M.DOC_TYPES.find((x) => x.name === name);
    if (!t) return empty("category", "Không có loại giấy tờ này");
    const L = lab(t);
    const cur = ver(L);
    // "optimize" là tab cũ, nay gộp vào Prompt.
    const tab = q.get("tab") === "optimize" ? "prompt" : q.get("tab") || "samples";
    const acc = score(L, t.fields, okOf(L));
    const reviewed = L.samples.filter((s) => s.truth);
    const url = (o = {}) => `#/settings/doctypes/${name}?tab=${o.tab ?? tab}${(o.s ?? q.get("s")) ? "&s=" + (o.s ?? q.get("s")) : ""}`;
    let body;
    if (tab === "samples") body = dtSamples(t, L, q.get("s"), url);
    else body = dtPromptWs(t, L);
    return `<div class="flex min-h-0 flex-1 flex-col">
      ${subHeader("#/settings/doctypes", icon("description", { size: 24, fill: true, cls: "text-[#4285f4]" }), esc(t.title),
        `<code class="font-mono">${t.name}</code> · ${t.fields.length} trường · ${L.samples.length} mẫu, ${reviewed.length} đã duyệt · hướng dẫn v${cur.v} đang dùng · độ chính xác <b class="${pctTone(acc)}">${pct(acc)}</b>`,
        `<button class="btn btn-text" title="Thêm vào hoặc bỏ khỏi thư viện" onclick="${on((e) => kbMenu(e, t))}">${icon("library_add", { size: 18 })} ${kbsOf(t).length ? `Dùng ở ${kbsOf(t).length} thư viện` : "Chưa dùng ở thư viện nào"}</button>`)}
      <nav class="flex flex-none gap-1 overflow-x-auto border-b border-line px-4">${[["samples", "fact_check", `Tài liệu mẫu (${L.samples.length})`], ["prompt", "edit_note", "Hướng dẫn AI"]].map(([k, ic, l]) => `<a href="${url({ tab: k })}" class="tab inline-flex items-center gap-2 ${tab === k ? "tab-active" : ""}">${icon(ic, { size: 20, fill: tab === k })}${l}</a>`).join("")}</nav>
      ${body}</div>`;
  }

  // Tab 1: mẫu · trình xem (ảnh / markdown / text OCR) · kết quả trích xuất.
  function dtSamples(t, L, sid, url) {
    if (!L.samples.length)
      return empty("upload_file", "Chưa có tài liệu mẫu", `Tải lên vài ${esc(t.title.toLowerCase())} khác bố cục, rồi chạy thử và duyệt kết quả AI đọc được.<div><button class="btn btn-primary mt-5" onclick="${on(() => uploadSamples(t))}">${icon("upload_file", { size: 18 })} Tải mẫu lên</button></div>`);
    const s = L.samples.find((x) => x.id === sid) ?? L.samples[0];
    const v = L.current, k = okOf(L);
    const edits = (S.dtEdits[s.id] ??= {});
    const running = S.dtRunning?.has(s.id);
    const focus = S.dtFocus && s.src[S.dtFocus];
    const view = S.dtView ?? "image";
    const list = L.samples.map((x) => {
      const sc = x.truth && ran(x) ? score({ samples: [x] }, t.fields, k) : null;
      const badge = x.status === "reviewed" ? `<span class="badge ${sc === 1 ? "badge-ok" : "badge-err"}">đã duyệt · ${pct(sc)}</span>` : SAMPLE_STATUS[x.status][0];
      return `<a href="${url({ s: x.id })}" class="flex flex-col gap-1 rounded-xl px-3 py-2 ${x.id === s.id ? "bg-accent-soft text-on-accent-soft" : "text-fg hover:bg-fg/5"}"><span class="flex items-center gap-2 text-sm">${fileIcon(x.file_name, 16)}<span class="min-w-0 flex-1 truncate">${esc(x.file_name)}</span></span><span class="flex items-center gap-2 text-xs">${badge}</span></a>`;
    });
    // Trình xem.
    let viewer;
    if (s.status === "converting")
      viewer = `<div class="mx-auto mt-10 flex max-w-sm flex-col items-center gap-3 rounded-2xl bg-surface-2 p-6 text-center">${spinner("size-6")}<div class="text-sm font-medium text-fg">Đang xử lý ${esc(s.file_name)}</div><div class="text-xs text-muted">Xong là xem và chạy thử được.</div></div>`;
    // Hai bản được lưu riêng: Nội dung (markdown VLM, nguồn chính) và Các dòng
    // (TurboOCR từng dòng, chỉ để đối chiếu chữ số).
    else if (view === "md" || view === "ocr") {
      const role = (ic, title, tag, tone, text) => `<div class="mb-3 flex gap-3 rounded-xl bg-surface p-3 shadow-1">${icon(ic, { size: 22, cls: "mt-0.5 flex-none text-accent" })}<div class="min-w-0 flex-1"><div class="flex flex-wrap items-center gap-2 text-sm font-medium text-fg">${title}<span class="badge ${tone}">${tag}</span></div><p class="mt-0.5 text-xs text-muted">${text}</p></div></div>`;
      viewer = view === "md"
        ? `<div class="mx-auto max-w-180">${role("auto_awesome", "Nội dung", "nguồn chính", "badge-ok", "AI xem ảnh trang rồi chép lại toàn bộ nội dung, giữ tiêu đề và bảng. Khi lấy thông tin, AI dựa vào bản này trước.")}
            <div class="rounded-xl border border-line bg-surface p-6"><div class="md text-[14px]">${md(mdOf(s))}</div></div></div>`
        : `<div class="mx-auto max-w-180">${role("text_fields", "Các dòng", "để đối chiếu", "", "Máy nhận dạng chữ theo từng dòng, kèm mức độ chắc chắn. AI chỉ dùng để kiểm lại chữ số khi phần Nội dung không rõ. Dòng dưới 70% bị bỏ qua.")}
            <div class="overflow-hidden rounded-xl border border-line bg-surface">${linesOf(s, 1).map((l) => { const low = l.conf < 0.7; return `<div class="grid grid-cols-[56px_1fr_auto] items-center gap-3 border-b border-line px-4 py-2 text-[13.5px] last:border-b-0 ${focus === l.line_no ? "bg-warn-soft" : ""}"><span class="text-xs text-subtle">Dòng ${l.line_no}</span><span class="${low ? "text-subtle line-through" : "text-fg"}">${esc(l.text)}</span><span class="flex items-center gap-2 text-xs ${low ? "text-err" : "text-muted"}">${low ? '<span class="badge badge-err">bỏ qua</span>' : ""}<span class="w-9 text-right tabular-nums">${Math.round(l.conf * 100)}%</span></span></div>`; }).join("")}</div></div>`;
    }
    else viewer = `<div class="mx-auto max-w-180 overflow-hidden rounded-xl border border-line shadow-1">${pageSVG(s, 1, { boxes: true, hl: focus ? [focus] : [], hideBoxes: true })}</div>`;
    // Kết quả.
    const out = outOf(s, k);
    const expected = (k) => edits[k] ?? (s.truth ? s.truth[k] : out[k]);
    const nEdit = Object.keys(edits).filter((k) => norm(edits[k]) !== norm(s.truth ? s.truth[k] : out[k])).length;
    let result;
    if (s.status === "converting") result = '<div class="px-4 py-10 text-center text-sm text-subtle">Đợi xử lý xong để chạy thử.</div>';
    else if (running) result = `<div class="flex flex-col items-center gap-3 px-4 py-10 text-center text-sm text-muted">${spinner("size-6")}AI đang đọc theo hướng dẫn v${v}…</div>`;
    else if (s.status === "converted") result = `<div class="px-4 py-10 text-center text-sm text-muted">Chưa chạy thử trên mẫu này.<div><button class="btn btn-primary mt-4" onclick="${on(() => runSamples(L, [s.id]))}">${icon("play_arrow", { size: 18 })} Chạy thử</button></div></div>`;
    else
      result = `<div class="flex flex-col gap-2 p-3">${t.fields.map((f) => {
        const judged = s.truth || edits[f.key] !== undefined;
        const ok = norm(out[f.key]) === norm(expected(f.key));
        const st = !judged ? icon("radio_button_unchecked", { size: 20, cls: "text-subtle" }) : ok ? icon("check_circle", { size: 20, fill: true, cls: "text-ok" }) : icon("cancel", { size: 20, fill: true, cls: "text-err" });
        return `<div class="rounded-xl border px-3 py-2 ${S.dtFocus === f.key ? "border-accent bg-accent/5" : "border-line"}" onclick="${on(() => { if (S.dtFocus !== f.key) (S.dtFocus = f.key), render(); })}">
          <div class="flex items-center gap-2 text-xs"><span class="font-medium text-fg">${esc(f.label)}</span><code class="font-mono text-subtle">${f.key}</code><span class="flex-1"></span><span title="${!judged ? "Chưa duyệt" : ok ? "Khớp giá trị đúng" : "Khác giá trị đúng"}">${st}</span></div>
          <input class="input mt-1 h-9 w-full" value="${esc(expected(f.key))}" onchange="${on((e) => ((edits[f.key] = e.target.value), render()))}">
          ${judged && !ok ? `<div class="mt-1 text-xs text-err">AI trả về: <s>${esc(out[f.key] ?? "(để trống)")}</s></div>` : ""}</div>`;
      }).join("")}</div>`;
    const sc = s.truth ? score({ samples: [s] }, t.fields, k) : null;
    const canSave = ran(s) && !running && (!s.truth || nEdit);
    return `<div class="flex min-h-0 flex-1 max-lg:flex-col">
      <aside class="flex flex-none flex-col gap-1 overflow-auto border-r border-line p-2 lg:w-64 max-lg:max-h-48">
        <div class="flex items-center pb-1 pl-2"><span class="flex-1 text-xs font-medium text-muted">Tài liệu mẫu</span><button class="btn-icon btn-sm" title="Tải mẫu lên" onclick="${on(() => uploadSamples(t))}">${icon("add", { size: 20 })}</button></div>
        ${list.join("")}
        <div class="mt-3 rounded-xl bg-surface-2 p-3 text-xs text-muted">Mẫu <b class="font-medium text-fg">đã duyệt</b> là dữ liệu đúng để chấm điểm và cải thiện hướng dẫn AI. Nên có từ 3 mẫu khác bố cục trở lên.</div></aside>
      <section class="flex min-h-0 min-w-0 flex-1 flex-col">
        <div class="flex flex-none flex-wrap items-center gap-2 px-4 py-2"><span class="min-w-0 flex-1 truncate text-sm text-fg">${esc(s.file_name)}</span>
          <div class="flex rounded-full border border-outline">${[["image", "Ảnh gốc"], ["md", "Nội dung"], ["ocr", "Các dòng"]].map(([k, l]) => `<button class="h-8 px-4 text-[13px] first:rounded-l-full last:rounded-r-full ${view === k ? "bg-accent-soft font-medium text-on-accent-soft" : "text-muted hover:bg-fg/8"}" ${s.status === "converting" ? "disabled" : ""} onclick="${on(() => ((S.dtView = k), render()))}">${l}</button>`).join("")}</div></div>
        <div class="min-h-0 flex-1 overflow-auto bg-surface-2/50 p-4">${viewer}</div></section>
      <aside class="flex w-full flex-none flex-col border-l border-line lg:w-96">
        <div class="flex flex-none items-center gap-2 border-b border-line px-4 py-2"><div class="min-w-0 flex-1"><div class="text-sm font-medium text-fg">Kết quả trích xuất</div><div class="truncate text-xs text-muted">hướng dẫn v${v}${s.ran_at && ran(s) ? " · chạy " + fmtDate(s.ran_at) : ""}</div></div>
          <button class="btn btn-sm" ${s.status === "converting" || running ? "disabled" : ""} onclick="${on(() => runSamples(L, [s.id]))}">${icon("play_arrow", { size: 18 })} Chạy thử</button></div>
        <div class="min-h-0 flex-1 overflow-auto">${result}</div>
        <div class="flex flex-none items-center gap-2 border-t border-line px-4 py-3">
          <span class="min-w-0 flex-1 text-xs text-muted">${s.truth ? `<b class="${pctTone(sc)}">${Math.round(sc * t.fields.length)}/${t.fields.length}</b> trường đúng với v${v}${nEdit ? ` · ${nEdit} giá trị đúng vừa sửa` : ""}` : ran(s) ? "Sửa các ô sai rồi lưu để thành dữ liệu mẫu" : ""}</span>
          <button class="btn btn-primary btn-sm" ${canSave ? "" : "disabled"} onclick="${on(() => {
            s.truth = Object.fromEntries(t.fields.map((f) => [f.key, norm(expected(f.key))]));
            s.status = "reviewed";
            S.dtEdits[s.id] = {};
            toast(`Đã lưu làm dữ liệu mẫu · độ chính xác v${v}: ${pct(score(L, t.fields, k))}`);
            render();
          })}">${icon("verified", { size: 18 })} Lưu làm dữ liệu mẫu</button></div></aside></div>`;
  }

  // Màn Prompt: một vòng sửa → chạy thử → xem trường sai → tối ưu → so sánh →
  // áp dụng. Trái: soạn (trường, hướng dẫn, quy tắc, ví dụ) hoặc prompt gửi
  // model; khi có đề xuất thì là so sánh v_cũ → v_mới. Phải: điểm từng trường
  // × mẫu và phiên bản; khi có đề xuất thì là lý do thay đổi và điểm trước/sau.
  function startOptimize(L) {
    S.dtOpt = {};
    S.dtHl = null;
    setTimeout(() => { if (S.dtOpt) (S.dtOpt = { ready: true }), render(); }, 3000);
  }
  function dtPromptWs(t, L) {
    const v = ver(L), cur = L.current, kc = okOf(L);
    const ready = L.samples.filter((s) => s.status !== "converting");
    const rev = L.samples.filter((s) => s.truth && ran(s));
    const P = L.proposal, opt = S.dtOpt;
    const proposing = !!(opt?.ready && P);
    const nv = nextV(L), kn = P?.v;
    const acc = score(L, t.fields, kc);
    const label = (k) => t.fields.find((f) => f.key === k)?.label ?? k;
    const fileOf = (id) => L.samples.find((x) => x.id === id).file_name;
    const done =(note, pv) => { L.versions.push({ ...pv, created_at: new Date().toISOString(), by: S.user.name, note }); L.current = pv.v; S.dtOpt = null; S.dtHl = null; L.proposal = null; };

    // --- thanh công cụ
    const toolbar = `<div class="flex flex-none flex-wrap items-center gap-2 border-b border-line px-4 py-2">
      <span class="text-sm font-medium text-fg">Hướng dẫn v${cur}</span>
      <span class="text-sm text-muted">· độ chính xác <b class="tabular-nums ${pctTone(acc)}">${pct(acc)}</b> <span class="text-xs">trên ${rev.length} mẫu × ${t.fields.length} trường</span></span>
      <span class="flex-1"></span>
      ${proposing ? "" : `<button class="btn btn-tonal btn-sm" ${rev.length && !opt ? "" : 'disabled title="Cần mẫu đã duyệt"'} onclick="${on(() => startOptimize(L))}">${icon("auto_awesome", { size: 18 })} Tối ưu bằng AI</button>
      <button class="btn btn-primary btn-sm" ${opt || S.dtRunning ? "disabled" : ""} title="Lưu thành v${nv} rồi chạy trên ${ready.length} mẫu" onclick="${on(() => { const n = nextV(L); L.versions.push({ ...v, v: n, ok: v.ok, created_at: new Date().toISOString(), by: S.user.name, note: "Sửa tay" }); L.current = n; runSamples(L, ready.map((s) => s.id)); })}">${icon(S.dtRunning ? "progress_activity" : "play_arrow", { size: 18 })} Lưu &amp; chạy thử</button>`}</div>`;

    // --- bên trái
    let left, banner = "", right;
    if (proposing) {
      const pv = { ...v, v: nv, ok: kn, rules: P.patterns.map((x) => x.rule), shots: P.shots };
      const before = score(L, t.fields, kc), after = score(L, t.fields, kn);
      const ops = diffLines(buildPrompt(t, v, L).split("\n"), buildPrompt(t, pv, L).split("\n"));
      // Gắn mỗi dòng thêm vào lý do của nó: quy tắc của trường nào, ví dụ của mẫu nào.
      let shot = null;
      for (const o of ops) {
        const m = o.t.match(/^Ví dụ \d+ \((.+)\):$/);
        if (m) shot = P.shots.find((x) => fileOf(x.sample) === m[1])?.sample ?? null;
        else if (o.t.endsWith(":") && o.t === o.t.toUpperCase()) shot = null;
        if (o.op !== "+") continue;
        const pat = P.patterns.find((x) => o.t.includes(x.rule));
        o.why = pat ? "f:" + pat.field : shot ? "s:" + shot : null;
      }
      const hl = S.dtHl;
      const nAdd = ops.filter((o) => o.op === "+").length, nDel = ops.filter((o) => o.op === "-").length;
      const tag = (o) => (o.why?.startsWith("f:") ? `<span class="ml-2 rounded bg-surface px-1.5 font-sans text-[10.5px] text-muted">${esc(label(o.why.slice(2)))}</span>` : "");
      const lineCls = (o) => (o.op === "+" ? "bg-ok-soft/70 text-fg" : o.op === "-" ? "bg-err-soft/70 text-fg line-through decoration-err/40" : "text-muted") + (hl && o.why === hl ? " !bg-warn-soft outline outline-1 outline-warn" : "");
      const num = (n) => `<span class="w-8 flex-none pr-2 text-right text-subtle select-none">${n ?? ""}</span>`;
      const mark = (o) => `<span class="w-4 flex-none select-none ${o.op === "+" ? "text-ok" : o.op === "-" ? "text-err" : ""}">${o.op === "=" ? "" : o.op}</span>`;
      const diff = ops.map((o) => `<div class="flex ${lineCls(o)}">${num(o.a)}${num(o.b)}${mark(o)}<span class="min-w-0 flex-1 pr-3 whitespace-pre-wrap">${esc(o.t) || " "}${tag(o)}</span></div>`).join("");
      banner = `<div class="flex flex-none flex-wrap items-center gap-3 border-b border-accent/30 bg-accent-soft/40 px-4 py-3">${icon("auto_awesome", { size: 22, cls: "text-accent" })}
        <div class="min-w-0 flex-1"><div class="text-sm font-medium text-fg">Đề xuất hướng dẫn v${nv}: độ chính xác <span class="${pctTone(before)}">${pct(before)}</span> → <span class="${pctTone(after)}">${pct(after)}</span></div><div class="text-xs text-muted"><span class="text-ok">+${nAdd} dòng</span>${nDel ? ` · <span class="text-err">−${nDel} dòng</span>` : ""} · ${P.patterns.length} quy tắc · ${P.shots.length} ví dụ · chấm trên ${rev.length} mẫu đã duyệt</div></div>
        <button class="btn btn-text btn-sm" onclick="${on(() => ((S.dtOpt = null), (S.dtHl = null), render()))}">Bỏ</button>
        <button class="btn btn-primary btn-sm" onclick="${on(() => (done(`Tối ưu tự động: ${P.patterns.length} quy tắc, ${P.shots.length} ví dụ`, pv), toast(`Đã áp dụng hướng dẫn v${nv} · độ chính xác ${pct(after)}`), render()))}">${icon("check", { size: 18 })} Áp dụng thành v${nv}</button></div>`;
      left = `<div id="prompt-diff" class="min-h-0 min-w-0 flex-1 overflow-auto py-2 text-[13px] leading-6">${diff}</div>`;
      const reason = (key, head, body) => `<button class="block w-full rounded-xl border p-3 text-left transition-colors ${hl === key ? "border-warn bg-warn-soft/50" : "border-line hover:bg-fg/4"}" aria-pressed="${hl === key}" onclick="${on(() => ((S.dtHl = hl === key ? null : key), render(), setTimeout(() => document.querySelector("#prompt-diff .\\!bg-warn-soft")?.scrollIntoView({ block: "center", behavior: "smooth" }))))}">${head}${body}</button>`;
      right = `<div><h4 class="mb-1 text-sm font-medium text-fg">Vì sao thay đổi</h4><p class="mb-2 text-xs text-muted">Bấm một mục để tô các dòng tương ứng trong hướng dẫn.</p>
          <div class="flex flex-col gap-2">${P.patterns.map((x) => reason("f:" + x.field, `<span class="flex flex-wrap items-center gap-2 text-sm"><b class="font-medium text-fg">${esc(label(x.field))}</b><span class="badge badge-err">sai ${x.hits.length}/${rev.length} mẫu</span></span>`, `<span class="mt-1 block text-xs text-muted">${esc(x.finding)}</span>`)).join("")}
          ${P.shots.map((x) => reason("s:" + x.sample, `<span class="flex items-center gap-2 text-sm">${fileIcon(fileOf(x.sample), 16)}<b class="min-w-0 truncate font-medium text-fg">Ví dụ: ${esc(fileOf(x.sample))}</b></span>`, `<span class="mt-1 block text-xs text-muted">${esc(x.why)}</span>`)).join("")}</div></div>
        <div><h4 class="mb-2 text-sm font-medium text-fg">Chạy thử trên mẫu đã duyệt</h4><table class="w-full text-sm"><thead><tr class="text-xs text-muted"><th class="h-8 border-b border-line text-left font-medium">Trường</th><th class="border-b border-line text-right font-medium">v${cur}</th><th class="border-b border-line text-right font-medium">v${nv}</th></tr></thead><tbody>
          ${t.fields.map((f) => { const a = score(L, t.fields, kc, f.key), b = score(L, t.fields, kn, f.key); const worse = rev.filter((s) => norm(outOf(s, kc)[f.key]) === norm(s.truth[f.key]) && norm(outOf(s, kn)[f.key]) !== norm(s.truth[f.key])); return `<tr><td class="h-8 border-b border-line text-fg">${esc(f.label)}${worse.length ? `<div class="pb-1 text-[11px] leading-4 text-err">giảm ở ${worse.map((s) => esc(s.file_name)).join(", ")}: “${esc(outOf(worse[0], kn)[f.key])}” thay vì “${esc(worse[0].truth[f.key])}”</div>` : ""}</td><td class="border-b border-line text-right align-top leading-8 tabular-nums ${pctTone(a)}">${pct(a)}</td><td class="border-b border-line text-right align-top leading-8 font-medium tabular-nums ${pctTone(b)}">${pct(b)}</td></tr>`; }).join("")}</tbody></table></div>`;
    } else {
      left = `<div class="flex min-h-0 min-w-0 flex-1 flex-col gap-5 overflow-auto p-6">
          <section class="rounded-2xl border border-line"><h3 class="px-5 pt-4 pb-3 text-sm font-medium text-fg">Trường cần lấy</h3>
            <div class="overflow-x-auto"><table class="w-full min-w-160 text-sm"><thead><tr class="text-left text-xs text-muted"><th class="w-44 py-2 pl-5 font-medium">Tên trường</th><th class="w-32 px-2 font-medium">Mã</th><th class="w-32 px-2 font-medium">Kiểu</th><th class="px-2 font-medium">Mô tả (hướng dẫn AI)</th></tr></thead><tbody>
            ${[...t.fields, null].map((f) => `<tr class="border-t border-line"><td class="py-1.5 pl-5"><input class="input h-9 w-full" value="${esc(f?.label ?? "")}" placeholder="${f ? "" : "Gõ tên trường tiếp theo…"}"></td><td class="px-2">${f ? `<code class="tag font-mono">${f.key}</code>` : '<span class="text-xs text-subtle">tự sinh</span>'}</td><td class="px-2">${f ? `<select class="input h-9 w-full">${VALUE_TYPES.map(([k, l]) => `<option ${k === f.value_type ? "selected" : ""}>${l}</option>`).join("")}</select>` : ""}</td><td class="px-2 pr-5">${f ? `<input class="input h-9 w-full" value="${esc(f.description)}" placeholder="Lấy ở đâu, ghi theo dạng nào">` : ""}</td></tr>`).join("")}</tbody></table></div></section>
          <section class="flex flex-col gap-2"><h3 class="text-sm font-medium text-fg">Hướng dẫn chung</h3><textarea class="input font-body" rows="5">${esc(v.prompt)}</textarea></section>
          <section class="flex flex-col gap-2"><div class="flex items-center"><h3 class="flex-1 text-sm font-medium text-fg">Quy tắc theo trường</h3><span class="text-xs text-subtle">thêm tay hoặc do “Tối ưu bằng AI” đề xuất</span></div>
            ${v.rules.map((r) => `<div class="flex items-center gap-2"><input class="input h-9 flex-1" value="${esc(r)}"><button class="btn-icon btn-sm" aria-label="Xoá quy tắc">${icon("delete", { size: 18 })}</button></div>`).join("")}<input class="input h-9" placeholder="Gõ một quy tắc, ví dụ: Số tiền chỉ gồm chữ số, bỏ “đồng”"></section>
          <section class="flex flex-col gap-2"><h3 class="text-sm font-medium text-fg">Ví dụ cho AI</h3>
            <div class="flex flex-wrap gap-2">${L.samples.filter((s) => s.truth).map((s) => { const sel = v.shots.some((x) => x.sample === s.id); return `<button class="chip ${sel ? "chip-on" : ""}" title="${esc(v.shots.find((x) => x.sample === s.id)?.why ?? "Dùng mẫu này làm ví dụ")}">${sel ? icon("check", { size: 18 }) : ""}${esc(s.file_name)}</button>`; }).join("") || '<span class="text-xs text-subtle">Chỉ mẫu đã duyệt mới dùng làm ví dụ.</span>'}</div>
            <p class="text-xs text-subtle">Mỗi ví dụ gồm nội dung một mẫu và kết quả đúng của nó. Khi chấm điểm chính mẫu đó, AI không được xem ví dụ này để điểm không bị cao giả.</p></section>
          <details class="group rounded-2xl border border-line"><summary class="flex h-11 cursor-pointer list-none items-center gap-2 px-5 text-sm font-medium text-fg">${icon("expand_more", { size: 20, cls: "text-muted transition-transform group-open:rotate-180" })}<span class="flex-1">Xem toàn văn gửi cho AI</span><span class="text-xs font-normal text-subtle">${buildPrompt(t, v, L).length.toLocaleString("vi-VN")} ký tự</span></summary>
            <pre class="mx-5 mb-5 rounded-xl bg-surface-2 p-4 font-body text-[13px] leading-6 whitespace-pre-wrap text-fg">${esc(buildPrompt(t, v, L))}</pre></details></div>`;
      // Điểm từng trường × mẫu của bản đang dùng.
      const dots = (f) => rev.map((s) => { const ok = norm(outOf(s, kc)[f.key]) === norm(s.truth[f.key]); return `<span title="${esc(s.file_name)}&#10;AI: ${esc(outOf(s, kc)[f.key] ?? "null")}&#10;Đúng: ${esc(s.truth[f.key])}">${icon(ok ? "check_circle" : "cancel", { size: 16, fill: true, cls: ok ? "text-ok" : "text-err" })}</span>`; }).join("");
      const progress = opt && !opt.ready
        ? `<div class="flex flex-col gap-2 rounded-xl bg-surface-2 p-4"><div class="flex items-center gap-2 text-sm font-medium text-fg">${spinner("size-4 border-2")} AI đang cải thiện hướng dẫn v${cur}…</div><div class="text-xs text-muted">Dựa trên ${rev.length} mẫu đã duyệt. Xong sẽ hiện đề xuất để bạn xem và quyết định.</div></div>`
        : opt?.ready && !P ? `<div class="rounded-xl border border-line p-3 text-sm text-muted">Không tìm thấy lỗi lặp lại nào đủ rõ trên ${rev.length} mẫu đã duyệt. Thêm mẫu khác bố cục rồi thử lại. <button class="text-accent hover:underline" onclick="${on(() => ((S.dtOpt = null), render()))}">Đóng</button></div>` : "";
      right = `${progress}
        <div><h4 class="mb-1 text-sm font-medium text-fg">Điểm theo trường · v${cur}</h4><p class="mb-2 text-xs text-muted">Mỗi dấu là một mẫu đã duyệt; rê chuột để xem giá trị AI và giá trị đúng.</p>
          ${rev.length ? t.fields.map((f) => { const a = score(L, t.fields, kc, f.key); return `<div class="flex items-center gap-2 border-b border-line py-2 last:border-b-0"><span class="min-w-0 flex-1 truncate text-sm text-fg">${esc(f.label)}</span><span class="flex gap-0.5">${dots(f)}</span><span class="w-11 text-right text-sm font-medium tabular-nums ${pctTone(a)}">${pct(a)}</span></div>`; }).join("") : '<div class="text-sm text-subtle">Chưa có mẫu đã duyệt. Duyệt kết quả ở tab Tài liệu mẫu để chấm điểm.</div>'}</div>
        <details class="rounded-xl border border-line" ${S.dtVerOpen !== false ? "open" : ""} ontoggle="${on((e) => (S.dtVerOpen = e.target.open))}"><summary class="flex h-10 cursor-pointer list-none items-center gap-2 px-3 text-sm font-medium text-fg">${icon("history", { size: 18, cls: "text-muted" })}<span class="flex-1">Phiên bản (${L.versions.length})</span>${icon("expand_more", { size: 20, cls: "text-muted" })}</summary>
          <p class="px-3 pb-2 text-xs text-muted">Bấm một phiên bản để dùng lại.</p>
          ${[...L.versions].reverse().map((x) => { const a = score(L, t.fields, x.ok), isCur = x.v === cur; return `<div class="group flex items-center gap-2 border-t border-line px-3 py-2 ${isCur ? "bg-accent-soft/40" : "cursor-pointer hover:bg-fg/4"}" ${isCur ? "" : `role="button" tabindex="0" title="Dùng hướng dẫn v${x.v}" onclick="${on(() => ((L.current = x.v), toast(`Đang dùng hướng dẫn v${x.v}`), render()))}"`}><span class="badge ${isCur ? "badge-info" : ""}">v${x.v}</span><div class="min-w-0 flex-1"><div class="truncate text-[13px] text-fg">${esc(x.note)}</div><div class="truncate text-[11px] text-muted">${isCur ? "đang dùng · " : ""}${esc(x.by)} · ${fmtDate(x.created_at)}</div></div><span class="text-[13px] font-medium tabular-nums ${pctTone(a)}">${pct(a)}</span>
            ${isCur ? '<span class="size-7"></span>' : `<button class="btn-icon size-7 opacity-0 group-hover:opacity-100 focus-visible:opacity-100" title="Xoá hướng dẫn v${x.v}" aria-label="Xoá hướng dẫn v${x.v}" onclick="${on((e) => { e.stopPropagation(); if (!confirm(`Xoá hướng dẫn v${x.v} (${x.note})? Không thể hoàn tác.`)) return; L.versions.splice(L.versions.indexOf(x), 1); toast(`Đã xoá hướng dẫn v${x.v}`); render(); })}">${icon("delete", { size: 16 })}</button>`}</div>`; }).join("")}</details>`;
    }
    return `<div class="flex min-h-0 flex-1 flex-col">${toolbar}${banner}
      <div class="flex min-h-0 flex-1 max-lg:flex-col">${left}<aside class="flex w-full flex-none flex-col gap-4 overflow-auto border-line p-4 max-lg:border-t lg:w-96 lg:border-l">${right}</aside></div></div>`;
  }

  // ------------------------------------------------------------ quy tắc đối chiếu
  // Bộ quy tắc dùng chung như loại giấy tờ; thư viện chọn bộ nào được dùng,
  // Studio chọn một bộ để chạy. Mỗi quy tắc: trường của giấy tờ A · cách so ·
  // trường của giấy tờ B (cùng kiểu), hoặc một câu viết bằng lời cho AI đánh giá.
  const CHECK_OPS = {
    text: [["same", "giống với"], ["contains", "có chứa"], ["diff", "khác với"]],
    money: [["eq", "bằng"], ["le", "không lớn hơn"], ["ge", "không nhỏ hơn"], ["sum_le", "cộng lại không vượt quá"]],
    date: [["before", "trước hoặc cùng ngày"], ["after", "sau hoặc cùng ngày"], ["same_day", "cùng ngày với"]],
  };
  const KIND_LABEL = { text: "Chữ", money: "Số / tiền", date: "Ngày" };
  const KIND_ICON = { text: "notes", money: "payments", date: "event" };
  const kindOf = (vt) => (vt === "money" || vt === "number" ? "money" : vt === "date" ? "date" : "text");
  const fieldOf = ([t, k]) => M.DOC_TYPES.find((x) => x.name === t)?.fields.find((f) => f.key === k);
  const opLabel = (op) => Object.values(CHECK_OPS).flat().find(([k]) => k === op)?.[1] ?? op;
  const fieldChip = (ref) => `<span class="inline-flex max-w-full items-center gap-1.5 rounded-lg bg-surface-2 px-2 py-0.5 align-middle text-fg"><i class="size-2 flex-none rounded-full" style="background:${typeColor(ref[0])}"></i><b class="font-medium">${esc(fieldOf(ref)?.label ?? ref[1])}</b><span class="text-muted">của ${esc(typeTitle(ref[0]))}</span></span>`;
  const setById = (id) => M.RULESETS.find((x) => x.id === id);
  const setsOfKB = (k) => M.RULESETS.filter((x) => x.kbs.includes(k.id));
  const setTypes = (set) => [...new Set(set.checks.flatMap((r) => (r.text ? [] : [r.a[0], r.b[0]])).concat(set.anchor))];
  function ruleSentence(r, set) {
    if (r.text) return esc(r.text);
    const sumB = r.b[0] !== set.anchor ? "tổng " : "";
    return r.op === "sum_le" ? `Tổng ${fieldChip(r.a)} ${opLabel(r.op)} ${sumB}${fieldChip(r.b)}` : `${fieldChip(r.a)} ${opLabel(r.op)} ${fieldChip(r.b)}`;
  }
  const setKbMenu = (e, set) => openMenu(e, M.KBS.map((k) => ({ checked: set.kbs.includes(k.id), label: esc(k.name), onClick: () => { const i = set.kbs.indexOf(k.id); i < 0 ? set.kbs.push(k.id) : set.kbs.splice(i, 1); toast(i < 0 ? `Đã thêm “${set.name}” vào ${k.name}` : `Đã bỏ “${set.name}” khỏi ${k.name}`); render(); } })), "right");

  // Cài đặt → Quy tắc đối chiếu: danh sách bộ quy tắc, cùng kiểu với Loại giấy tờ.
  function checksTab() {
    const f = S.rsFilter ?? "all";
    let list = M.RULESETS;
    if (f === "unused") list = list.filter((x) => !x.kbs.length);
    else if (f !== "all") list = list.filter((x) => x.kbs.includes(f));
    const chip = (v, label, n) => `<button class="chip ${f === v ? "chip-on" : ""}" aria-pressed="${f === v}" onclick="${on(() => ((S.rsFilter = v), render()))}">${f === v ? icon("check", { size: 18 }) : ""}${label} <span class="tabular-nums ${f === v ? "" : "text-subtle"}">${n}</span></button>`;
    const td = "border-b border-line px-3 py-2.5 align-middle";
    const row = (set) => {
      const href = `#/settings/checks/${set.id}`, nOn = set.checks.filter((r) => r.on).length;
      return `<tr class="cursor-pointer transition-colors hover:bg-surface-2" onclick="location.hash='${href}'">
        <td class="${td} pl-4"><a href="${href}" class="block font-medium text-fg">${esc(set.name)}</a><span class="block text-xs text-muted">${esc(set.desc)}</span></td>
        <td class="${td} tabular-nums whitespace-nowrap text-muted">${set.checks.length}${nOn < set.checks.length ? ` <span class="text-xs text-subtle">(${nOn} bật)</span>` : ""}</td>
        <td class="${td} max-lg:hidden"><div class="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted">${setTypes(set).map((t) => `<span class="inline-flex items-center gap-1.5"><i class="size-2 rounded-full" style="background:${typeColor(t)}"></i>${esc(typeTitle(t))}</span>`).join("")}</div></td>
        <td class="${td} max-md:hidden"><div class="flex flex-wrap gap-1">${set.kbs.length ? set.kbs.map((id) => `<span class="tag">${icon("folder_shared", { size: 14 })}${esc(M.KBS.find((k) => k.id === id).name)}</span>`).join("") : '<span class="text-xs text-subtle">Chưa dùng ở thư viện nào</span>'}</div></td>
        <td class="${td} pr-2 text-right whitespace-nowrap" onclick="event.stopPropagation()">
          <button class="btn-icon btn-sm" title="Thêm vào hoặc bỏ khỏi thư viện" aria-label="Thư viện dùng ${esc(set.name)}" onclick="${on((e) => setKbMenu(e, set))}">${icon("library_add", { size: 20 })}</button>
          <button class="btn-icon btn-sm" aria-label="Thao tác với ${esc(set.name)}" onclick="${on((e) => openMenu(e, [
            { icon: "edit", label: "Sửa quy tắc", onClick: () => go(href) },
            { icon: "content_copy", label: "Nhân bản", onClick: () => { M.RULESETS.push({ ...structuredClone(set), id: "rs" + Date.now(), name: set.name + " (bản sao)", kbs: [] }); toast("Đã nhân bản"); render(); } },
            { divider: true },
            { icon: "delete", label: "Xoá", danger: true, onClick: () => confirm(`Xoá bộ “${set.name}”?`) && (M.RULESETS.splice(M.RULESETS.indexOf(set), 1), toast("Đã xoá bộ quy tắc"), render()) },
          ], "right"))}">${icon("more_vert", { size: 20 })}</button></td></tr>`;
    };
    return sectionHead("Quy tắc đối chiếu", "Mỗi bộ quy tắc gồm các phép so thông tin giữa các giấy tờ, dùng chung cho mọi thư viện. Khi đối chiếu ở Studio, người dùng chọn một bộ để chạy.",
        `<button class="btn btn-primary" onclick="${on(openNewRuleSet)}">${icon("add", { size: 18 })} Bộ quy tắc mới</button>`) +
      `<div class="flex flex-wrap items-center gap-2">${chip("all", "Tất cả", M.RULESETS.length)}${M.KBS.map((k) => chip(k.id, esc(k.name), setsOfKB(k).length)).join("")}${chip("unused", "Chưa dùng", M.RULESETS.filter((x) => !x.kbs.length).length)}</div>
      <div class="overflow-x-auto rounded-2xl border border-line"><table class="w-full border-collapse text-sm"><thead><tr class="text-left text-[13px] text-muted">
        <th class="h-11 border-b border-line pl-4 font-medium">Bộ quy tắc</th><th class="border-b border-line px-3 font-medium whitespace-nowrap">Quy tắc</th><th class="border-b border-line px-3 font-medium max-lg:hidden">Giấy tờ được so</th><th class="border-b border-line px-3 font-medium max-md:hidden">Dùng ở thư viện</th><th class="w-24 border-b border-line"></th></tr></thead>
        <tbody>${list.map(row).join("")}</tbody></table>
        ${list.length ? "" : '<p class="px-4 py-6 text-center text-sm text-muted">Chưa có bộ quy tắc nào.</p>'}</div>`;
  }
  function openNewRuleSet() {
    modal({
      title: "Bộ quy tắc mới",
      icon: "rule",
      body: `<label class="field">Tên bộ quy tắc<input id="rs-name" class="input" autofocus placeholder="VD: Giải ngân theo hợp đồng thi công"></label>
        <label class="field">Mô tả<input id="rs-desc" class="input" placeholder="Bộ này kiểm những gì"></label>
        <fieldset class="flex flex-col gap-1"><legend class="text-xs font-medium text-muted">Dùng ở thư viện</legend>${M.KBS.map((k) => `<label class="flex h-9 items-center gap-3 text-sm text-fg"><input type="checkbox" data-kb="${k.id}" ${k.id === S.kb ? "checked" : ""} class="size-4 accent-accent">${esc(k.name)}</label>`).join("")}</fieldset>`,
      footer: `<button class="btn btn-text" onclick="A.closeModal()">Huỷ</button><button class="btn btn-primary" onclick="${on(() => {
        const name = document.getElementById("rs-name").value.trim();
        if (!name) return toast("Nhập tên bộ quy tắc");
        const set = { id: "rs" + Date.now(), name, desc: document.getElementById("rs-desc").value.trim(), anchor: "hop_dong", links: {}, checks: [], kbs: M.KBS.filter((k) => document.querySelector(`#modal [data-kb="${k.id}"]`)?.checked).map((k) => k.id) };
        M.RULESETS.push(set);
        closeModal();
        go(`#/settings/checks/${set.id}`);
        setTimeout(() => ruleBuilder(set), 50);
      })}">Tạo</button>`,
    });
  }
  // Trang của một bộ: các quy tắc dạng câu, bật/tắt, sửa, xoá; cách ghép thu gọn.
  function viewRuleSet(id) {
    const set = setById(id);
    if (!set) return empty("rule", "Không có bộ quy tắc này");
    const nOn = set.checks.filter((r) => r.on).length;
    const rows = set.checks.map((r, i) => `<div class="flex items-start gap-3 border-t border-line px-4 py-3 first:border-t-0 ${r.on ? "" : "opacity-60"}">
        <label class="relative mt-0.5 inline-flex flex-none cursor-pointer items-center" title="${r.on ? "Đang bật" : "Đang tắt"}"><input type="checkbox" class="peer sr-only" ${r.on ? "checked" : ""} aria-label="Bật quy tắc" onchange="${on(() => ((r.on = !r.on), render()))}"><span class="h-6 w-10 rounded-full bg-surface-3 transition-colors peer-checked:bg-accent"></span><span class="absolute left-1 size-4 rounded-full bg-surface shadow-1 transition-transform peer-checked:translate-x-4"></span></label>
        <div class="min-w-0 flex-1"><p class="text-sm leading-7 text-fg">${ruleSentence(r, set)}</p>${r.text ? `<span class="badge badge-vlm mt-1">${icon("auto_awesome", { size: 14 })} AI đánh giá mỗi lần chạy</span>` : ""}</div>
        <button class="btn-icon btn-sm" title="Sửa quy tắc" aria-label="Sửa quy tắc" onclick="${on(() => ruleBuilder(set, r))}">${icon("edit", { size: 18 })}</button>
        <button class="btn-icon btn-sm" title="Xoá quy tắc" aria-label="Xoá quy tắc" onclick="${on(() => { set.checks.splice(i, 1); toast("Đã xoá quy tắc"); render(); })}">${icon("delete", { size: 18 })}</button></div>`).join("");
    return `<div class="flex min-h-0 flex-1 flex-col">
      ${subHeader("#/settings/checks", icon("rule", { size: 24, cls: "text-[#5b47c9]" }), esc(set.name), `${esc(set.desc || "Chưa có mô tả")} · ${set.checks.length} quy tắc, ${nOn} đang bật`,
        `<button class="btn btn-text" onclick="${on((e) => setKbMenu(e, set))}">${icon("library_add", { size: 18 })} ${set.kbs.length ? `Dùng ở ${set.kbs.length} thư viện` : "Chưa dùng ở thư viện nào"}</button>`)}
      <div class="min-h-0 flex-1 overflow-auto"><div class="mx-auto flex max-w-5xl flex-col gap-4 px-4 py-6 sm:px-8">
        <div class="rounded-2xl border border-line">
          <div class="flex flex-wrap items-center gap-3 border-b border-line px-4 py-3"><span class="min-w-0 flex-1 text-sm font-medium text-fg">Các quy tắc</span><button class="btn btn-primary" onclick="${on(() => ruleBuilder(set))}">${icon("add", { size: 18 })} Thêm quy tắc</button></div>
          ${rows || `<div class="px-4 py-10 text-center text-sm text-subtle">Chưa có quy tắc nào. Bấm “Thêm quy tắc” để so hai giấy tờ.</div>`}</div>
        <details class="group rounded-2xl border border-line"><summary class="flex min-h-12 cursor-pointer list-none items-center gap-3 px-4 py-2 text-sm">${icon("link", { size: 20, cls: "text-muted" })}<span class="min-w-0 flex-1"><span class="font-medium text-fg">Cách ghép chứng từ với ${esc(typeTitle(set.anchor).toLowerCase())}</span><span class="block text-xs text-muted">Tự động: theo bên bán và số hợp đồng ghi trên chứng từ. Chỉ cần sửa khi máy ghép sai.</span></span>${icon("expand_more", { size: 20, cls: "text-muted transition-transform group-open:rotate-180" })}</summary>
          <div class="flex flex-col gap-3 border-t border-line px-4 py-3">
            <label class="field w-72">Giấy tờ gốc của mỗi bộ<select class="input" onchange="${on((e) => ((set.anchor = e.target.value), render()))}">${M.DOC_TYPES.filter((t) => t.fields.length).map((t) => `<option value="${t.name}" ${t.name === set.anchor ? "selected" : ""}>${esc(t.title)}</option>`).join("")}</select></label>
            ${Object.entries(set.links).map(([t, steps]) => `<div class="text-sm"><div class="mb-1 font-medium text-fg">${esc(typeTitle(t))}</div><ol class="list-decimal pl-5 text-muted">${steps.map((x) => `<li>${esc(x)}</li>`).join("")}</ol></div>`).join("")}
          </div></details>
      </div></div></div>`;
  }
  // Hộp thoại thêm/sửa: 1 giấy tờ A + trường, 2 giấy tờ B + trường, 3 cách so;
  // ngay dưới là ô viết bằng lời cho quy tắc không so được theo trường.
  function ruleBuilder(set, edit, d) {
    const types = M.DOC_TYPES.filter((t) => t.fields.length);
    d ??= edit && !edit.text ? { a: [...edit.a], b: [...edit.b], op: edit.op, text: "" } : { a: [types.find((t) => t.name !== set.anchor)?.name ?? types[0].name, null], b: [set.anchor, null], op: null, text: edit?.text ?? "" };
    const redo = (ch) => (ch(d), ruleBuilder(set, edit, d));
    const fa = d.a[1] && fieldOf(d.a), fb = d.b[1] && fieldOf(d.b);
    const kind = fa ? kindOf(fa.value_type) : null;
    const ops = kind ? CHECK_OPS[kind] : [];
    if (d.op && !ops.some(([k]) => k === d.op)) d.op = null;
    const byField = !!(fa && fb && d.op);
    const step = (n, title, ok) => `<div class="mb-2 flex items-center gap-2 text-sm font-medium text-fg"><span class="grid size-6 flex-none place-items-center rounded-full text-xs ${ok ? "bg-accent text-on-accent" : "bg-accent-soft text-on-accent-soft"}">${ok ? icon("check", { size: 16 }) : n}</span>${title}</div>`;
    const side = (n, label, key) => {
      const ref = d[key], f = ref[1] && fieldOf(ref);
      const t = M.DOC_TYPES.find((x) => x.name === ref[0]);
      return `<section class="flex min-w-0 flex-col rounded-2xl border ${f ? "border-accent/40" : "border-line"} p-4">${step(n, label, !!f)}
        <label class="field">Giấy tờ<select class="input" onchange="${on((e) => redo((x) => (x[key] = [e.target.value, null])))}">${types.map((x) => `<option value="${x.name}" ${x.name === ref[0] ? "selected" : ""}>${esc(x.title)}</option>`).join("")}</select></label>
        <div class="mt-3 mb-1 text-xs font-medium text-muted">Thông tin cần so</div>
        <div class="flex flex-col gap-1" role="radiogroup" aria-label="Thông tin của ${label}">${t.fields.map((x) => {
          const sel = x.key === ref[1];
          const bad = key === "b" && kind && kindOf(x.value_type) !== kind;
          return `<button role="radio" aria-checked="${sel}" ${bad ? `disabled title="Khác kiểu với ${esc(fa.label)}: cần ${KIND_LABEL[kind].toLowerCase()}"` : ""} class="flex min-h-10 items-center gap-3 rounded-xl px-3 text-left text-sm ${sel ? "bg-accent-soft font-medium text-on-accent-soft" : "text-fg hover:bg-fg/5"} disabled:cursor-not-allowed disabled:opacity-40" onclick="${on(() => redo((y) => (y[key] = [ref[0], x.key])))}">${icon(sel ? "radio_button_checked" : "radio_button_unchecked", { size: 20, cls: sel ? "" : "text-subtle" })}<span class="min-w-0 flex-1">${esc(x.label)}</span><span class="flex items-center gap-1 text-xs ${sel ? "" : "text-subtle"}">${icon(KIND_ICON[kindOf(x.value_type)], { size: 14 })}${KIND_LABEL[kindOf(x.value_type)]}</span></button>`;
        }).join("")}</div></section>`;
    };
    // Câu xem trước và nút Lưu đổi theo ô viết bằng lời mà không vẽ lại hộp thoại.
    const preview = () => {
      const txt = d.text.trim();
      const box = document.getElementById("rule-preview"), btn = document.getElementById("rule-save");
      if (!box) return;
      box.innerHTML = txt ? `${esc(txt)} <span class="badge badge-vlm align-middle">${icon("auto_awesome", { size: 14 })} AI đánh giá</span>` : byField ? ruleSentence(d, set) : '<span class="text-subtle">Chọn đủ hai thông tin và cách so, hoặc viết quy tắc bằng lời.</span>';
      btn.disabled = !txt && !byField;
    };
    modal({
      title: edit ? "Sửa quy tắc" : "Quy tắc mới",
      icon: "rule",
      width: 900,
      body: `<div class="grid gap-3 md:grid-cols-2">${side(1, "Giấy tờ A", "a")}${side(2, "Giấy tờ B", "b")}</div>
        <section class="rounded-2xl border ${d.op ? "border-accent/40" : "border-line"} p-4">${step(3, "Cách so", !!d.op)}
          ${kind ? `<div class="flex flex-wrap gap-2" role="radiogroup" aria-label="Cách so">${ops.map(([k, l]) => `<button role="radio" aria-checked="${d.op === k}" class="chip ${d.op === k ? "chip-on" : ""}" onclick="${on(() => redo((x) => (x.op = k)))}">${d.op === k ? icon("check", { size: 18 }) : ""}A ${l} B</button>`).join("")}</div>` : '<p class="text-sm text-subtle">Chọn thông tin của giấy tờ A trước; cách so phụ thuộc vào kiểu thông tin (chữ, số tiền, ngày).</p>'}
          <div class="mt-4 border-t border-line pt-4"><label class="field">Hoặc viết quy tắc bằng lời<textarea id="rule-text" class="input font-body" rows="2" placeholder="Ví dụ: Nếu hợp đồng thanh toán sau khi giao hàng thì phải có hoá đơn hoặc biên bản bàn giao">${esc(d.text)}</textarea></label>
            <p class="mt-1.5 flex items-start gap-1.5 text-xs text-muted">${icon("auto_awesome", { size: 14, cls: "mt-px text-vlm" })}Dùng khi không so được hai thông tin cụ thể. Có câu ở đây thì quy tắc do AI đọc các chứng từ và đánh giá mỗi lần chạy; phần chọn ở trên được bỏ qua. So được theo thông tin thì nên chọn ở trên để máy tự kiểm, nhanh và chính xác hơn.</p></div>
        </section>
        <div class="flex items-start gap-3 rounded-2xl bg-surface-2 p-4">${icon("fact_check", { size: 22, cls: "text-accent" })}<div class="min-w-0 flex-1"><div class="text-xs font-medium text-muted">Quy tắc sẽ đọc là</div><p id="rule-preview" class="mt-1 text-sm leading-7 text-fg"></p></div></div>`,
      footer: `<button class="btn btn-text" onclick="A.closeModal()">Huỷ</button><button id="rule-save" class="btn btn-primary" onclick="${on(() => {
        const txt = d.text.trim();
        const r = txt ? { text: txt } : { a: d.a, b: d.b, op: d.op };
        if (edit) { for (const k of ["a", "b", "op", "text"]) delete edit[k]; Object.assign(edit, r); } else set.checks.push({ ...r, on: true });
        closeModal(); toast(edit ? "Đã sửa quy tắc" : "Đã thêm quy tắc"); render();
      })}">Lưu quy tắc</button>`,
    });
    document.getElementById("rule-text").addEventListener("input", (e) => ((d.text = e.target.value), preview()));
    preview();
  }

  // Studio: chọn một bộ quy tắc của thư viện để chạy đối chiếu.
  function pickRuleSet(e, c) {
    openMenu(e, setsOfKB(kb()).map((x) => ({ checked: x.id === (S.caseSet[c.id] ?? setsOfKB(kb())[0]?.id), label: `${esc(x.name)} · ${x.checks.filter((r) => r.on).length} quy tắc`, onClick: () => ((S.caseSet[c.id] = x.id), render()) })));
  }

  // ------------------------------------------------------------ settings
  const VALUE_TYPES = [["string", "Văn bản"], ["number", "Số"], ["money", "Số tiền"], ["date", "Ngày"], ["bool", "Có / Không"]];
  const sectionHead = (title, text, action = "") => `<header class="flex flex-wrap items-end gap-x-6 gap-y-3"><div class="min-w-60 flex-1"><h2 class="text-base font-medium text-fg">${title}</h2><p class="mt-1 max-w-2xl text-sm text-muted">${text}</p></div>${action}</header>`;
  const kbSelect = () => `<label class="field w-full sm:w-72"><span>Thư viện</span><select class="input" onchange="${on((e) => ((S.kb = e.target.value), render()))}">${M.KBS.map((k) => `<option value="${k.id}" ${k.id === S.kb ? "selected" : ""}>${esc(k.name)}</option>`).join("")}</select></label>`;
  function viewSettings(tab) {
    const tabs = [["doctypes", "category", "Loại giấy tờ"], ["forms", "request_page", "Mẫu xuất Excel"], ["checks", "rule", "Quy tắc đối chiếu"], ["users", "group", "Người dùng"]];
    if (!tabs.some(([k]) => k === tab)) tab = "doctypes";
    let body = "";
    if (tab === "doctypes") body = docTypesTab();
    if (tab === "forms")
      body = sectionHead("Mẫu xuất Excel", "Nhập file Excel mẫu (bảng kê, biểu mẫu…): hệ thống tự nhận diện bảng và đề xuất cột nào lấy trường nào, công thức nào. Mỗi mẫu thành một nút xuất ở Studio của các hồ sơ trong thư viện.", kbSelect()) +
        `<div class="rounded-2xl border border-line"><div class="flex flex-wrap items-center gap-3 border-b border-line px-4 py-3"><span class="min-w-0 flex-1 text-sm text-muted">${M.FORMS.length} mẫu</span><button class="btn btn-primary" onclick="${on(() => toast("Đang nhận diện bảng và đề xuất cách điền…"))}">${icon("upload_file", { size: 18 })} Nhập mẫu Excel</button></div>
          ${M.FORMS.map((t) => `<div class="flex items-center gap-3 border-b border-line px-4 py-3 last:border-b-0">${icon("request_page", { size: 24, fill: true, cls: "text-[#107c41]" })}<div class="min-w-0 flex-1"><div class="truncate text-sm text-fg">${esc(t.name)}</div><div class="truncate text-xs text-muted">${esc(t.file_name)} · mỗi dòng một ${typeTitle(t.doc_type)} · ${t.columns} cột · cập nhật ${fmtDate(t.updated_at)}</div></div><button class="btn btn-text btn-sm" onclick="${on(() => openFormMap(t))}">${icon("tune", { size: 18 })} Sửa cách điền</button><button class="btn-icon btn-sm" title="Xoá mẫu">${icon("delete", { size: 18 })}</button></div>`).join("")}</div>`;
    if (tab === "checks") body = checksTab();
    if (tab === "users")
      body = sectionHead("Người dùng", "Phân quyền sửa hướng dẫn AI và đặt giới hạn chi phí theo tháng cho từng người. Thay đổi được lưu ngay, không cần bấm lưu.") +
        `<div class="overflow-x-auto rounded-2xl border border-line"><table class="w-full min-w-180 text-sm"><thead><tr class="text-left text-xs text-muted"><th class="py-2.5 pl-5 font-medium">Người dùng</th><th class="px-2 font-medium">Quyền</th><th class="px-2 font-medium">Đã dùng tháng này</th><th class="px-2 font-medium">Giới hạn / tháng</th><th class="w-14"></th></tr></thead><tbody>
          ${M.USERS.map((u) => `<tr class="border-t border-line"><td class="py-2.5 pl-5"><div class="flex items-center gap-3"><span class="grid size-8 place-items-center rounded-full bg-accent text-on-accent">${u.name[0]}</span><span><span class="block text-fg">${esc(u.name)}</span><span class="block text-xs text-muted">${u.email}</span></span></div></td>
            <td class="px-2"><div class="flex rounded-full border border-outline w-fit">${[["user", "Người dùng"], ["prompt_editor", "Sửa hướng dẫn AI"], ["admin", "Quản trị"]].map(([r, l]) => `<button class="h-8 px-3 text-xs first:rounded-l-full last:rounded-r-full ${u.role === r ? "bg-accent-soft text-on-accent-soft" : "text-muted hover:bg-fg/8"}" onclick="${on(() => ((u.role = r), render(), toast("Đã đổi quyền")))}">${l}</button>`).join("")}</div></td>
            <td class="px-2 tabular-nums">${fmtMoney(u.used)}</td><td class="px-2"><input class="input h-9 w-36" value="${u.limit ? new Intl.NumberFormat("vi-VN").format(u.limit) : ""}" placeholder="Không giới hạn"></td><td class="pr-3"><button class="btn-icon btn-sm" title="Đặt lại quyền truy cập cho người này">${icon("key", { size: 18 })}</button></td></tr>`).join("")}</tbody></table></div>`;
    return `${topBar("Cài đặt")}
      <nav class="flex flex-none gap-1 overflow-x-auto border-b border-line px-6">${tabs.map(([k, ic, l]) => `<a href="#/settings/${k}" class="tab inline-flex items-center gap-2 ${tab === k ? "tab-active" : ""}">${icon(ic, { size: 20, fill: tab === k })}${l}</a>`).join("")}</nav>
      <div class="min-h-0 flex-1 overflow-auto px-6 py-6"><div class="mx-auto flex max-w-5xl flex-col gap-5">${body}</div></div>`;
  }
  function openFormMap(t) {
    const type = M.DOC_TYPES.find((x) => x.name === t.doc_type);
    const cols = ["STT", ...type.fields.map((f) => f.label), "Ghi chú"].slice(0, t.columns);
    modal({
      title: "Mẫu xuất Excel",
      icon: "request_page",
      width: 820,
      body: `<div class="flex flex-wrap items-center gap-2 text-sm"><span class="text-fg">${esc(t.name)}</span><span class="text-xs">${esc(t.file_name)} · bảng ở A6:${colName(cols.length - 1)}6 · mỗi dòng một</span><select class="input h-8 w-56">${M.DOC_TYPES.map((x) => `<option ${x.name === t.doc_type ? "selected" : ""}>${x.title}</option>`).join("")}</select></div>
        <div class="overflow-x-auto rounded-xl border border-line"><table class="w-full text-sm"><thead><tr class="text-left text-xs"><th class="py-2 pl-4 font-medium">Cột</th><th class="px-2 font-medium">Tiêu đề trong mẫu</th><th class="px-2 font-medium">Lấy từ</th><th class="px-2 font-medium">Công thức</th></tr></thead><tbody>
          ${cols.map((h, i) => `<tr class="border-t border-line"><td class="py-1.5 pl-4 font-mono text-xs">${colName(i)}</td><td class="px-2 text-fg">${esc(h)}</td><td class="px-2"><select class="input h-8 w-full">${i === 0 ? "<option>Số thứ tự</option>" : ""}${type.fields.map((f) => `<option ${f.label === h ? "selected" : ""}>${f.label}</option>`).join("")}<option ${h === "Ghi chú" ? "selected" : ""}>— để trống —</option></select></td><td class="px-2"><input class="input h-8 w-full font-mono" placeholder="${i === cols.length - 1 ? "SUM(D{r},E{r})" : "—"}"></td></tr>`).join("")}</tbody></table></div>
        <p class="text-xs">AI đề xuất cách điền khi nhập mẫu; cột nào AI không chắc thì để trống cho bạn chọn. Trường mới tạo ở đây thành trường của loại giấy tờ.</p>`,
      footer: `<button class="btn btn-text" onclick="A.closeModal()">Huỷ</button><button class="btn btn-primary" onclick="${on(() => (closeModal(), toast("Đã lưu mẫu")))}">Lưu</button>`,
    });
  }

  // "Thông tin": tài khoản, key & chi phí.
  function openInfo(tab) {
    const bar = (label, spent, limit, reset) => `<div class="rounded-2xl bg-surface-2 p-4"><div class="flex items-baseline text-sm"><span class="flex-1 text-fg">${label}</span><span class="tabular-nums text-fg">${fmtMoney(spent)}</span>${limit ? `<span class="text-xs">&nbsp;/ ${fmtMoney(limit)}</span>` : ""}</div>${limit ? `<div class="mt-2 h-2 overflow-hidden rounded-full bg-surface-3"><i class="block h-full rounded-full bg-accent" style="width:${Math.min(100, (100 * spent) / limit)}%"></i></div>` : ""}<div class="mt-1 text-xs">${reset}</div></div>`;
    modal({
      title: "Thông tin",
      icon: "account_circle",
      width: 640,
      body: `<div class="-mx-6 -mt-2 flex overflow-x-auto border-b border-line px-3">${[["account", "Tài khoản"], ["keys", "Chi phí AI"]].map(([k, l]) => `<button class="tab ${tab === k ? "tab-active" : ""}" onclick="${on(() => openInfo(k))}">${l}</button>`).join("")}</div>` +
        (tab === "account"
          ? `<div class="flex items-center gap-4 rounded-2xl bg-surface-2 p-4"><div class="grid size-12 flex-none place-items-center rounded-full bg-accent text-xl text-on-accent">${S.user.name[0]}</div><div class="min-w-0 flex-1"><div class="truncate text-base text-fg">${S.user.name}</div><div class="truncate text-xs">${S.user.email}</div><div class="mt-1 flex gap-1"><span class="badge">Email + mật khẩu</span><span class="badge badge-info">Quản trị</span></div></div><button class="btn btn-sm" onclick="location.hash='#/login'">${icon("logout", { size: 18 })} Đăng xuất</button></div>
             <div class="flex items-center border-t border-line pt-4"><h3 class="flex-1 text-sm font-medium text-fg">Mật khẩu</h3><button class="btn btn-text btn-sm">Đổi mật khẩu</button></div>`
          : `<section class="flex flex-col gap-4"><div><h3 class="text-base font-medium text-fg">Chi phí đã sử dụng</h3><p class="text-xs">Tính theo lượng dùng AI thực tế của mọi người. Cập nhật lúc 09:12.</p></div>
              ${bar("Toàn hệ thống · tháng này", 1793000, 5000000, "Đặt lại vào 01/11/2026")}${bar("Hôm nay", 84500, 0, "Đặt lại lúc 00:00")}
              <div class="grid grid-cols-3 gap-2 text-center">${[[1102000, "Hỏi đáp"], [412000, "Xuất Excel"], [279000, "Xử lý file"]].map(([v, l]) => `<div class="rounded-xl bg-surface-2 px-2 py-3"><div class="text-base text-fg tabular-nums">${fmtMoney(v)}</div><div class="text-xs">${l}</div></div>`).join("")}</div></section>
             <section class="flex flex-col gap-3 border-t border-line pt-4"><h3 class="text-sm font-medium text-fg">Tài khoản AI dùng chung</h3><p class="text-xs">Một tài khoản AI cho mọi tính năng: hỏi đáp, đọc tài liệu, xuất Excel. Chi phí ở trên tính trên tài khoản này.</p>
              <div class="flex items-center gap-3 rounded-xl border border-line px-3 py-2">${icon("key", { size: 18, cls: "text-accent" })}<div class="min-w-0 flex-1"><div class="truncate text-sm text-fg">OpenRouter</div><div class="truncate text-[11px] text-subtle">Mã khoá …9f2c · đang dùng cho 4 tính năng</div></div><button class="btn btn-text btn-sm">Đổi tài khoản</button></div></section>`),
      footer: `<button class="btn btn-primary" onclick="A.closeModal()">Xong</button>`,
    });
  }

  // ------------------------------------------------------------ router
  function render() {
    handlers = [];
    closeMenu();
    const [path, qs] = location.hash.slice(1).split("?");
    const q = new URLSearchParams(qs);
    const p = (path || "/documents").split("/").filter(Boolean);
    let html;
    if (p[0] === "login") html = viewLogin();
    else {
      let content;
      if (p[0] === "documents" && p[1]) content = viewDocument(p[1], q);
      else if (p[0] === "chat") content = viewChat(p[1]);
      else if (p[0] === "cases" && p[2] === "split") content = viewSplit(p[1]);
      else if (p[0] === "cases" && p[2] === "sheets") content = viewSheet(p[1], p[3]);
      else if (p[0] === "cases" && p[2] === "checks") content = viewCheck(p[1]);
      else if (p[0] === "cases") content = viewCase(p[1] || "c1", q);
      else if (p[0] === "settings" && p[1] === "doctypes" && p[2]) content = viewDocType(p[2], q);
      else if (p[0] === "settings" && p[1] === "checks" && p[2]) content = viewRuleSet(p[2]);
      else if (p[0] === "settings") content = viewSettings(p[1]);
      else content = viewDocuments();
      html = layout("/" + p.join("/"), content);
    }
    // Giữ vị trí cuộn của cột trường khi vẽ lại (bấm trường, sửa giá trị).
    const fy = document.getElementById("case-fields")?.scrollTop;
    root.innerHTML = html;
    const fl = document.getElementById("case-fields");
    if (fy && fl) fl.scrollTop = fy;
    const sc = document.getElementById("scroller");
    if (sc) sc.scrollTop = sc.scrollHeight;
    document.querySelector(".pagebox.hl")?.scrollIntoView({ block: "center" });
    document.querySelector("[data-cur-group]")?.scrollIntoView({ block: "nearest" });
    // Tailwind (bản chạy trong trình duyệt) sinh CSS sau lần render đầu, nên
    // cuộn tới trang đầu của mục lại một lần nữa khi layout đã ổn định.
    const toFirst = () => {
      const h = document.querySelector("#case-pages .pagebox.hl"), f = document.getElementById("case-first");
      if (h) h.scrollIntoView({ block: "center" });
      else if (f) document.getElementById("case-pages").scrollTop = f.offsetTop - 8;
    };
    requestAnimationFrame(toFirst);
    setTimeout(toFirst, 300);
  }
  // Các handler đăng ký trong lúc render; modal có handler riêng nên giữ mảng
  // cho tới lần render sau.
  window.A = {
    h: (i, e) => handlers[i]?.(e),
    closeModal,
    cite,
  };
  addEventListener("hashchange", () => (closeModal(), track(), render()));
  addEventListener("keydown", (e) => {
    if (!location.hash.startsWith("#/documents/") || e.target.closest("input,textarea,select")) return;
    const m = location.hash.match(/page=(\d+)/);
    const n = m ? +m[1] : 1;
    if (e.key === "ArrowLeft" && n > 1) location.hash = location.hash.replace(/page=\d+/, "page=" + (n - 1)).replace(/^(#\/documents\/d\d+)$/, `$1?page=${n - 1}`);
    if (e.key === "ArrowRight") location.hash = m ? location.hash.replace(/page=\d+/, "page=" + (n + 1)) : location.hash + "?page=2";
  });
  track();
  render();
})();
