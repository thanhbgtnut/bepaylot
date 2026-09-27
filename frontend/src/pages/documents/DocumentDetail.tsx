import { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";

import {
  deleteDocument,
  downloadOriginal,
  getDocument,
  getMarkdown,
  getPage,
  listEngines,
  listPages,
  reparseDocument,
} from "../../api/endpoints";
import type { DocumentDetail as Doc, DocumentPage, EngineInfo, PageView } from "../../api/types";
import { Markdown } from "../../components/Markdown";
import { PageImage } from "../../components/PageImage";
import { Thumb } from "../../components/Thumb";
import { useToast } from "../../components/toast";
import { Empty, FileIcon, Icon, Loading, MetaChips, Modal, SourceBadge, Spinner, StatusBadge } from "../../components/ui";
import { fmtDate, fmtSize, TERMINAL } from "../../lib/format";
import { BlocksPanel, FindPanel, LinesPanel, TreePanel } from "./panels";

type Tab = "info" | "lines" | "md" | "blocks" | "find" | "tree";
const TABS: [Tab, string][] = [
  ["info", "Chi tiết"],
  ["lines", "Dòng"],
  ["md", "Markdown"],
  ["blocks", "Blocks"],
  ["find", "Tìm"],
  ["tree", "Mục lục"],
];
const ZOOMS = [50, 67, 80, 100, 125, 150, 200];

// Icon button on the dark viewer chrome.
const darkBtn = "btn-icon text-[#e3e3e3] hover:bg-white/10 disabled:opacity-30";

export function DocumentDetail() {
  const { id = "" } = useParams();
  const [sp, setSp] = useSearchParams();
  const nav = useNavigate();
  const { toast, fail } = useToast();
  const pageNo = Math.max(1, +(sp.get("page") ?? 1) || 1);
  const hl = useMemo(() => (sp.get("hl") ?? "").split(",").filter(Boolean).map(Number), [sp]);
  const tab = (sp.get("tab") as Tab) || (hl.length ? "lines" : "info");

  const [doc, setDoc] = useState<Doc | null>(null);
  const [pages, setPages] = useState<DocumentPage[]>([]);
  const [page, setPage] = useState<PageView | null>(null);
  const [pageErr, setPageErr] = useState("");
  const [err, setErr] = useState("");
  const [showBoxes, setShowBoxes] = useState(true);
  const [panel, setPanel] = useState(true);
  const [zoom, setZoom] = useState(100);
  const [hover, setHover] = useState<number | null>(null);
  const [focus, setFocus] = useState<number | null>(null);
  const [modal, setModal] = useState<"md" | "reparse" | null>(null);
  const [reloadKey, setReloadKey] = useState(0);

  const go = useCallback(
    (n: number, opts: { hl?: number[]; tab?: Tab } = {}) => {
      const next = new URLSearchParams();
      next.set("page", String(n));
      if (opts.hl?.length) next.set("hl", opts.hl.join(","));
      next.set("tab", opts.tab ?? tab);
      setSp(next, { replace: true });
    },
    [setSp, tab],
  );

  // Document + pages, polled while processing.
  useEffect(() => {
    let alive = true;
    let timer: ReturnType<typeof setTimeout>;
    const load = async () => {
      try {
        const [d, ps] = await Promise.all([getDocument(id), listPages(id)]);
        if (!alive) return;
        setDoc(d);
        setPages(ps);
        setErr("");
        if (!TERMINAL.has(d.status)) timer = setTimeout(load, 3000);
      } catch (e) {
        if (alive) setErr((e as Error).message);
      }
    };
    load();
    return () => {
      alive = false;
      clearTimeout(timer);
    };
  }, [id, reloadKey]);

  const pageReady = pages.find((p) => p.page_no === pageNo)?.status;
  useEffect(() => {
    let alive = true;
    setPage(null);
    setPageErr("");
    setFocus(null);
    getPage(id, pageNo).then(
      (p) => alive && setPage(p),
      (e) => alive && setPageErr((e as Error).message),
    );
    return () => {
      alive = false;
    };
  }, [id, pageNo, pageReady]);

  // Keyboard: ←/→ change page.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.target as HTMLElement)?.closest("input,textarea,select")) return;
      if (e.key === "ArrowLeft" && pageNo > 1) go(pageNo - 1);
      if (e.key === "ArrowRight" && doc && pageNo < doc.page_count) go(pageNo + 1);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [go, pageNo, doc]);

  const highlighted = useMemo(() => new Set([...hl, ...(focus != null ? [focus] : [])]), [hl, focus]);
  const lines = useMemo(() => (page?.lines ?? []).filter((l) => l.text?.trim()), [page]);
  const boxes = useMemo(
    () =>
      lines.map((l) => ({
        key: l.line_no,
        bbox: l.bbox,
        title: `L${l.line_no}: ${l.text}`,
        className: [l.text_source === "vlm" ? "vlm" : "", l.low_confidence ? "low" : "", highlighted.has(l.line_no) || hover === l.line_no ? "hl" : ""].join(" "),
      })),
    [lines, highlighted, hover],
  );

  if (err)
    return (
      <Empty icon="error" title="Không mở được tài liệu">
        {err}
        <div>
          <button className="btn mt-5" onClick={() => nav("/documents")}>
            Quay lại danh sách
          </button>
        </div>
      </Empty>
    );
  if (!doc) return <Loading />;

  const remove = async () => {
    if (!confirm(`Xoá “${doc.file_name}”? Không thể hoàn tác.`)) return;
    try {
      await deleteDocument(doc.id);
      toast("Đã xoá 1 file");
      nav("/documents");
    } catch (e) {
      fail(e);
    }
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col bg-[#1e1f20] text-[#e3e3e3]">
      {/* viewer chrome */}
      <header className="flex h-16 flex-none items-center gap-1 px-2">
        <button className={darkBtn} onClick={() => nav("/documents")} title="Đóng">
          <Icon name="arrow_back" />
        </button>
        <FileIcon mime={doc.mime_type} name={doc.file_name} />
        <div className="ml-2 min-w-0 flex-1">
          <div className="truncate text-base">{doc.file_name}</div>
          {!TERMINAL.has(doc.status) && (
            <div className="flex items-center gap-2 text-xs text-[#c4c7c5]">
              <Spinner className="size-3 border-2 border-white/25 border-t-[#a8c7fa]" />
              {Math.round((doc.progress || 0) * 100)}% · {doc.pages_done}/{doc.page_count} trang
            </div>
          )}
        </div>
        <button className={darkBtn} title="Tìm trong file" onClick={() => (setPanel(true), go(pageNo, { tab: "find" }))}>
          <Icon name="search" />
        </button>
        <button className={darkBtn} title="Tải xuống" onClick={() => downloadOriginal(doc).catch(fail)}>
          <Icon name="download" />
        </button>
        <button className={darkBtn} title="Markdown toàn văn" onClick={() => setModal("md")}>
          <Icon name="article" />
        </button>
        <button className={darkBtn} title="Parse lại" onClick={() => setModal("reparse")}>
          <Icon name="refresh" />
        </button>
        <button className={darkBtn} title="Xoá" onClick={remove}>
          <Icon name="delete" />
        </button>
        <button className={darkBtn + (panel ? " bg-white/15" : "")} title="Chi tiết" onClick={() => setPanel((p) => !p)}>
          <Icon name="info" fill={panel} />
        </button>
      </header>

      <div className="flex min-h-0 flex-1">
        {/* thumbnails */}
        <div className="hidden w-36 flex-none flex-col gap-3 overflow-y-auto px-4 pt-1 pb-6 lg:flex">
          {pages.map((p) => {
            const on = p.page_no === pageNo;
            return (
              <button
                key={p.page_no}
                ref={(el) => {
                  if (el && on) el.scrollIntoView({ block: "nearest" });
                }}
                onClick={() => go(p.page_no)}
                className="group flex flex-col items-center gap-1.5"
                title={p.text_plain?.slice(0, 160)}
              >
                <div className={"relative w-full overflow-hidden rounded-md border-2 transition-colors " + (on ? "border-[#a8c7fa]" : "border-transparent group-hover:border-white/30")}>
                  {p.status === "done" || p.status === "rendered" || p.status === "ocr" ? (
                    <Thumb docId={doc.id} pageNo={p.page_no} className="aspect-3/4 w-full bg-white" />
                  ) : (
                    <div className="grid aspect-3/4 w-full place-items-center bg-white/5">
                      {p.status === "failed" ? <Icon name="error" className="text-[#f28b82]" /> : <Spinner className="size-4 border-2 border-white/25 border-t-[#a8c7fa]" />}
                    </div>
                  )}
                  {p.is_blank && <span className="absolute inset-x-0 bottom-0 bg-black/60 text-[10px] leading-4">trắng</span>}
                </div>
                <span className={"text-xs " + (on ? "font-medium text-white" : "text-[#c4c7c5]")}>{p.page_no}</span>
              </button>
            );
          })}
        </div>

        {/* page */}
        <div className="relative min-w-0 flex-1">
          <div className="absolute inset-0 overflow-auto px-6 pt-2 pb-24">
            <div className="mx-auto" style={{ width: `${Math.min(zoom, 400)}%`, maxWidth: zoom <= 100 ? 980 : undefined }}>
              {pageErr ? (
                <div className="grid aspect-3/4 place-items-center rounded bg-white/5 p-6 text-center text-sm text-[#c4c7c5]">
                  <span>
                    <Icon name="hourglass_empty" size={40} className="mb-2 text-[#8e918f]" />
                    <br />
                    Trang {pageNo} chưa sẵn sàng
                  </span>
                </div>
              ) : !page ? (
                <div className="grid aspect-3/4 place-items-center">
                  <Spinner className="border-white/20 border-t-[#a8c7fa]" />
                </div>
              ) : (
                <div className="shadow-3">
                  <PageImage
                    docId={doc.id}
                    pageNo={pageNo}
                    width={page.width}
                    height={page.height}
                    boxes={boxes}
                    hideBoxes={!showBoxes}
                    scrollTo={hl.length ? "hl" : undefined}
                    onBoxClick={(k) => {
                      setFocus(+k);
                      setPanel(true);
                      if (tab !== "lines") go(pageNo, { tab: "lines" });
                    }}
                    onBoxHover={(k) => setHover(k == null ? null : +k)}
                  />
                </div>
              )}
            </div>
          </div>

          {/* floating page / zoom bar */}
          <div className="absolute bottom-5 left-1/2 flex h-12 -translate-x-1/2 items-center gap-0.5 rounded-full bg-[#303134]/95 px-2 text-sm shadow-3 backdrop-blur">
            <button className={darkBtn} disabled={pageNo <= 1} onClick={() => go(pageNo - 1)} title="Trang trước (←)">
              <Icon name="chevron_left" />
            </button>
            <span className="flex items-center gap-1.5 px-1 whitespace-nowrap">
              Trang
              <input
                key={pageNo}
                defaultValue={pageNo}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    const n = +e.currentTarget.value;
                    if (n >= 1 && n <= doc.page_count) go(n);
                  }
                }}
                className="h-7 w-10 rounded bg-white/10 text-center outline-none focus:ring-2 focus:ring-[#a8c7fa]"
                aria-label="Số trang"
              />
              / {doc.page_count}
            </span>
            <button className={darkBtn} disabled={pageNo >= doc.page_count} onClick={() => go(pageNo + 1)} title="Trang sau (→)">
              <Icon name="chevron_right" />
            </button>
            <span className="mx-1 h-6 w-px bg-white/20" />
            <button className={darkBtn} disabled={zoom <= ZOOMS[0]} onClick={() => setZoom(ZOOMS[Math.max(0, ZOOMS.indexOf(zoom) - 1)])} title="Thu nhỏ">
              <Icon name="zoom_out" />
            </button>
            <button className="h-8 w-14 rounded-full hover:bg-white/10" onClick={() => setZoom(100)} title="Vừa khung">
              {zoom}%
            </button>
            <button className={darkBtn} disabled={zoom >= ZOOMS.at(-1)!} onClick={() => setZoom(ZOOMS[Math.min(ZOOMS.length - 1, ZOOMS.indexOf(zoom) + 1)])} title="Phóng to">
              <Icon name="zoom_in" />
            </button>
            <span className="mx-1 h-6 w-px bg-white/20" />
            <button className={darkBtn + (showBoxes ? " bg-white/15" : "")} onClick={() => setShowBoxes((b) => !b)} title="Khung toạ độ từng dòng">
              <Icon name="select" />
            </button>
          </div>
        </div>

        {/* details panel */}
        {panel && (
          <aside className="flex w-105 max-w-[90vw] flex-none flex-col overflow-hidden rounded-tl-2xl bg-surface text-fg max-md:absolute max-md:inset-y-16 max-md:right-0 max-md:z-10 max-md:shadow-3">
            <div className="flex items-center gap-2 px-5 pt-4 pb-1">
              <FileIcon mime={doc.mime_type} name={doc.file_name} size={20} />
              <span className="min-w-0 flex-1 truncate text-base">{doc.file_name}</span>
              <button className="btn-icon btn-sm" onClick={() => setPanel(false)} aria-label="Đóng">
                <Icon name="close" size={20} />
              </button>
            </div>
            <div className="flex flex-none overflow-x-auto border-b border-line px-2">
              {TABS.map(([k, label]) => (
                <button key={k} className={"tab" + (tab === k ? " tab-active" : "")} onClick={() => go(pageNo, { tab: k })}>
                  {label}
                </button>
              ))}
            </div>
            <div className="min-h-0 flex-1 overflow-auto">
              {tab === "info" && <InfoPanel doc={doc} page={page} onGo={(n) => go(n)} />}
              {tab === "find" && <FindPanel doc={doc} onPick={(n, line) => go(n, { hl: [line], tab: "find" })} />}
              {tab === "tree" && <TreePanel doc={doc} current={pageNo} onPick={(n) => go(n, { tab: "tree" })} />}
              {(tab === "lines" || tab === "md" || tab === "blocks") && !page && !pageErr && <Loading />}
              {(tab === "lines" || tab === "md" || tab === "blocks") && pageErr && <Empty icon="hourglass_empty">Trang chưa sẵn sàng</Empty>}
              {tab === "lines" && page && (
                <LinesPanel lines={lines} highlighted={highlighted} hover={hover} onHover={setHover} onPick={(n) => setFocus(n)} scrollTarget={focus ?? hl[0]} />
              )}
              {tab === "md" && page && (
                <div className="px-5 py-4">
                  <Markdown text={page.markdown || "_Trang trống_"} />
                </div>
              )}
              {tab === "blocks" && page && <BlocksPanel blocks={page.blocks ?? []} />}
            </div>
          </aside>
        )}
      </div>

      {modal === "md" && <FullMarkdown docId={doc.id} onClose={() => setModal(null)} />}
      {modal === "reparse" && (
        <ReparseDialog
          doc={doc}
          pageNo={pageNo}
          onClose={() => setModal(null)}
          onDone={() => {
            setModal(null);
            toast("Đã đưa vào hàng đợi parse lại");
            setTimeout(() => setReloadKey((k) => k + 1), 500);
          }}
        />
      )}
    </div>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="py-2">
      <div className="text-xs text-muted">{label}</div>
      <div className="mt-0.5 text-sm wrap-break-word text-fg">{children}</div>
    </div>
  );
}

function InfoPanel({ doc, page, onGo }: { doc: Doc; page: PageView | null; onGo: (n: number) => void }) {
  return (
    <div className="px-5 py-3">
      {(doc.title || doc.summary) && (
        <div className="mb-3 rounded-xl bg-surface-2 p-4">
          {doc.title && <div className="font-medium">{doc.title}</div>}
          {doc.summary && <p className="mt-2 font-body text-[13px] leading-5 text-muted">{doc.summary}</p>}
        </div>
      )}
      <h3 className="pt-2 pb-1 text-sm font-medium">Chi tiết tệp</h3>
      <Row label="Trạng thái">
        <StatusBadge status={doc.status} />
      </Row>
      <Row label="Số trang">
        {doc.page_count}
        {doc.pages_failed > 0 && <span className="ml-2 text-err">{doc.pages_failed} trang lỗi</span>}
      </Row>
      <Row label="Kích thước">{fmtSize(doc.size_bytes)}</Row>
      <Row label="OCR engine">{doc.engine || "mặc định"}</Row>
      <Row label="Các bước">
        <span className="flex flex-wrap gap-1.5">
          <span className="tag">parse: {doc.parse_status}</span>
          <span className="tag">index: {doc.index_status}</span>
          <span className="tag">wiki: {doc.wiki_status}</span>
        </span>
      </Row>
      {doc.metadata && Object.keys(doc.metadata).length > 0 && (
        <Row label="Metadata">
          <span className="flex flex-wrap gap-1.5">
            <MetaChips meta={doc.metadata} />
          </span>
        </Row>
      )}
      <Row label="Tải lên">{fmtDate(doc.created_at)}</Row>
      <Row label="Cập nhật">{fmtDate(doc.updated_at)}</Row>
      {doc.callback_url && <Row label="Callback">{doc.callback_url}</Row>}
      {doc.error && <Row label="Lỗi">{<span className="text-err">{doc.error}</span>}</Row>}
      {!!doc.failed_pages?.length && (
        <Row label="Trang lỗi">
          <span className="flex flex-wrap gap-1.5">
            {doc.failed_pages.map((f) => (
              <button key={f.page_no} className="chip h-7" title={f.error} onClick={() => onGo(f.page_no)}>
                {f.page_no}
              </button>
            ))}
          </span>
        </Row>
      )}
      {page && (
        <>
          <h3 className="pt-4 pb-1 text-sm font-medium">Trang {page.page_no}</h3>
          <Row label="Nguồn chữ">
            <SourceBadge source={page.text_source} />
          </Row>
          <Row label="Ảnh">
            {page.width}×{page.height}px · {Math.round(page.dpi)} dpi
          </Row>
          {page.ocr_ms > 0 && <Row label="Thời gian OCR">{page.ocr_ms} ms</Row>}
          {page.error && <Row label="Lỗi trang">{<span className="text-err">{page.error}</span>}</Row>}
        </>
      )}
    </div>
  );
}

function FullMarkdown({ docId, onClose }: { docId: string; onClose: () => void }) {
  const [md, setMd] = useState<string | null>(null);
  const [err, setErr] = useState("");
  useEffect(() => {
    getMarkdown(docId).then(
      (m) => setMd(m.replace(/<!-- page:(\d+) -->/g, "\n\n---\n\n**— Trang $1 —**\n\n")),
      (e) => setErr((e as Error).message),
    );
  }, [docId]);
  return (
    <Modal title="Markdown toàn văn" icon="article" onClose={onClose} width={900}>
      <div className="text-fg">{err ? <Empty>{err}</Empty> : md === null ? <Loading /> : <Markdown text={md} />}</div>
    </Modal>
  );
}

function ReparseDialog({ doc, pageNo, onClose, onDone }: { doc: Doc; pageNo: number; onClose: () => void; onDone: () => void }) {
  const { fail } = useToast();
  const [engines, setEngines] = useState<EngineInfo[]>([]);
  const [engine, setEngine] = useState("");
  const [scope, setScope] = useState<"all" | "page">("all");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    listEngines().then(setEngines, () => {});
  }, []);
  return (
    <Modal
      title="Parse lại"
      icon="refresh"
      onClose={onClose}
      footer={
        <>
          <button className="btn btn-text" onClick={onClose}>
            Huỷ
          </button>
          <button
            className="btn btn-primary"
            disabled={busy}
            onClick={async () => {
              setBusy(true);
              try {
                await reparseDocument(doc.id, { engine: engine || undefined, pages: scope === "page" ? [pageNo] : undefined });
                onDone();
              } catch (e) {
                fail(e);
                setBusy(false);
              }
            }}
          >
            Parse lại
          </button>
        </>
      }
    >
      <label className="flex items-center gap-3 text-sm text-fg">
        <input type="radio" className="size-4 accent-accent" checked={scope === "all"} onChange={() => setScope("all")} /> Toàn bộ tài liệu ({doc.page_count} trang)
      </label>
      <label className="flex items-center gap-3 text-sm text-fg">
        <input type="radio" className="size-4 accent-accent" checked={scope === "page"} onChange={() => setScope("page")} /> Chỉ trang {pageNo}
      </label>
      <label className="field">
        OCR engine
        <select className="input" value={engine} onChange={(e) => setEngine(e.target.value)}>
          <option value="">Giữ nguyên ({doc.engine || "mặc định"})</option>
          {engines.map((e) => (
            <option key={e.name} value={e.name} disabled={!e.available}>
              {e.name}
              {e.available ? "" : " — không khả dụng"}
            </option>
          ))}
        </select>
      </label>
    </Modal>
  );
}
