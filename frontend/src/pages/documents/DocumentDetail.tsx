import { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";

import {
  deleteDocument,
  downloadOriginal,
  getDocument,
  getMarkdown,
  getPage,
  getTree,
  listPages,
  reparseDocument,
} from "../../api/endpoints";
import type { Document, DocumentDetail as Doc, DocumentPage, PageView, TreeNode } from "../../api/types";
import { Markdown } from "../../components/Markdown";
import { PageImage } from "../../components/PageImage";
import { PageSteps, pageSummary, pagesWith, skippedRun } from "../../components/PageState";
import { BackButton } from "../../components/nav";
import { Thumb } from "../../components/Thumb";
import { useToast } from "../../components/toast";
import { useApp } from "../../components/AppContext";
import { Empty, FileIcon, Icon, Loading, MetaChips, Modal, Spinner, StatusBadge } from "../../components/ui";
import { typeColor, typeTitle } from "../../lib/doctype";
import { fmtDate, fmtSize, TERMINAL } from "../../lib/format";
import { PagePicker } from "./PagePicker";
import { BlocksPanel, FindPanel, LinesPanel, TreePanel } from "./panels";

type Tab = "info" | "lines" | "md" | "blocks" | "find" | "tree";
const TABS: [Tab, string][] = [
  ["info", "Chi tiết"],
  ["lines", "Dòng"],
  ["md", "Nội dung"],
  ["blocks", "Bố cục"],
  ["find", "Tìm"],
  ["tree", "Mục lục"],
];
const ZOOMS = [50, 67, 80, 100, 125, 150, 200];

// Icon button of the viewer chrome.
const barBtn = "btn-icon text-muted disabled:opacity-30";

export function DocumentDetail() {
  const { id = "" } = useParams();
  const [sp, setSp] = useSearchParams();
  const nav = useNavigate();
  const { toast, fail } = useToast();
  const { kbs } = useApp();
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
  // Khoảng điền sẵn khi mở Đọc lại từ một trang chưa xử lý.
  const [reparseRange, setReparseRange] = useState<[number, number] | undefined>();
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

  // The documents of the file by type (the doctype nodes of its tree, §6.5),
  // to break the thumbnails; while the file is still processing the type of
  // each page stands in.
  const [docNodes, setDocNodes] = useState<TreeNode[]>([]);
  const indexed = doc?.status === "completed" || doc?.status === "partial";
  useEffect(() => {
    let alive = true;
    setDocNodes([]);
    if (indexed)
      getTree(id).then(
        (ns) => alive && setDocNodes(ns.filter((n) => n.doc_type)),
        () => {},
      );
    return () => {
      alive = false;
    };
  }, [id, indexed, reloadKey]);
  const types = kbs.find((k) => k.id === doc?.kb_id)?.config.doc_types ?? [];
  // Pages grouped by document (a doctype node of the tree; while processing,
  // the type read from each page); pages outside every document form an
  // unclassified group.
  const groupOf = (p: DocumentPage) => {
    const n = docNodes.find((n) => p.page_no >= n.page_start && p.page_no <= n.page_end);
    if (n) return { key: n.id, type: n.doc_type, title: n.title || typeTitle(n.doc_type, types) };
    if (p.status === "skipped") return { key: "skipped", type: "", title: "Chưa xử lý" };
    if (docNodes.length || !p.doc_type) return { key: "none", type: "", title: "Chưa phân loại" };
    return { key: "p:" + p.doc_type, type: p.doc_type, title: typeTitle(p.doc_type, types) };
  };
  const blocks: { key: string; type?: string; title: string; pages: DocumentPage[] }[] = [];
  for (const p of pages) {
    const g = groupOf(p);
    const last = blocks.at(-1);
    if (last?.key === g.key) last.pages.push(p);
    else blocks.push({ ...g, pages: [p] });
  }
  // The not-processed spans of the tree (pages not picked, U111) are not documents.
  const docOnly = docNodes.filter((n) => n.doc_type !== "not_processed");
  const typeCount = new Map<string, number>();
  for (const n of docOnly) typeCount.set(n.doc_type!, (typeCount.get(n.doc_type!) ?? 0) + 1);
  const curPage = pages.find((p) => p.page_no === pageNo);
  const summary = pageSummary(pages);
  const reparse = (range?: [number, number]) => (setReparseRange(range), setModal("reparse"));
  const pagesLabel = (a: number, b: number) => (a === b ? `tr. ${a}` : `tr. ${a}–${b}`);

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
    <div className="flex min-h-0 flex-1 flex-col bg-surface-2/60 text-fg">
      {/* viewer chrome */}
      <header className="flex min-h-16 flex-none items-center gap-1 px-2 py-2">
        <BackButton fallback="/documents" fallbackLabel="Tài liệu" className="btn btn-text max-w-64 flex-none" />
        <FileIcon mime={doc.mime_type} name={doc.file_name} />
        <div className="ml-2 min-w-0 flex-1">
          <div className="truncate text-base">{doc.file_name}</div>
          {!TERMINAL.has(doc.status) ? (
            <div className="flex items-center gap-2 text-xs text-muted">
              <Spinner className="size-3 border-2" />
              {Math.round((doc.progress || 0) * 100)}% · {doc.pages_done}/{doc.page_count - (doc.pages_skipped ?? 0)} trang
              {doc.pages_skipped > 0 && ` · ${doc.pages_skipped} trang không chọn xử lý`}
            </div>
          ) : (
            // Pages, documents and their types, each type jumping to its first document.
            <div className="mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-muted">
              <span>
                {doc.page_count} trang{docOnly.length ? ` · ${docOnly.length} giấy tờ` : ""}
              </span>
              {summary && <span>{summary}</span>}
              {[...typeCount].map(([t, c]) => (
                <button key={t} className="inline-flex items-center gap-1.5 text-fg hover:underline" title={`Tới ${typeTitle(t, types)} đầu tiên`} onClick={() => go(docNodes.find((n) => n.doc_type === t)!.page_start)}>
                  <i className="size-2 rounded-full" style={{ background: typeColor(t, types) }} />
                  {typeTitle(t, types)}
                  {c > 1 && <span className="text-muted">×{c}</span>}
                </button>
              ))}
            </div>
          )}
        </div>
        <button className={barBtn} title="Tìm trong file" onClick={() => (setPanel(true), go(pageNo, { tab: "find" }))}>
          <Icon name="search" />
        </button>
        <button className={barBtn} title="Tải xuống" onClick={() => downloadOriginal(doc).catch(fail)}>
          <Icon name="download" />
        </button>
        <button className={barBtn} title="Toàn văn" onClick={() => setModal("md")}>
          <Icon name="article" />
        </button>
        <button className={barBtn} title="Đọc lại" onClick={() => reparse()}>
          <Icon name="refresh" />
        </button>
        <button className={barBtn} title="Xoá" onClick={remove}>
          <Icon name="delete" />
        </button>
        <button className={barBtn + (panel ? " bg-accent-soft text-on-accent-soft" : "")} title="Chi tiết" onClick={() => setPanel((p) => !p)}>
          <Icon name="info" fill={panel} />
        </button>
      </header>

      <div className="flex min-h-0 flex-1">
        {/* thumbnails, one block per document with its type's color */}
        <div className="hidden w-40 flex-none flex-col gap-1.5 overflow-y-auto px-2 pt-1 pb-6 lg:flex" aria-label="Các trang theo giấy tờ">
          {blocks.map((b) => {
            const cur = b.pages.some((p) => p.page_no === pageNo);
            return (
              <section key={b.key + b.pages[0].page_no} className={"rounded-md border-l-[3px] py-1.5 pr-0.5 pl-1.5 " + (cur ? "bg-accent-soft/40" : "")} style={{ borderColor: typeColor(b.type, types) }}>
                <div className="mb-1.5">
                  <div className="truncate text-[11px] leading-4 font-medium text-fg" title={b.title}>
                    {b.title}
                  </div>
                  <div className="truncate text-[10.5px] text-muted">{pagesLabel(b.pages[0].page_no, b.pages.at(-1)!.page_no)}</div>
                </div>
                <div className="grid grid-cols-2 gap-x-1 gap-y-1">
                  {b.pages.map((p) => {
                    const on = p.page_no === pageNo;
                    return (
                      <button
                        key={p.page_no}
                        ref={(el) => {
                          if (el && on) el.scrollIntoView({ block: "nearest" });
                        }}
                        onClick={() => go(p.page_no)}
                        className="group flex flex-col items-center gap-1"
                        title={p.text_plain?.slice(0, 160) || `Trang ${p.page_no}`}
                      >
                        <div className={"relative w-full overflow-hidden rounded border-2 transition-colors " + (on ? "border-accent" : "border-line group-hover:border-outline")}>
                          {p.status === "done" || p.status === "rendered" || p.status === "ocr" ? (
                            <Thumb docId={doc.id} pageNo={p.page_no} className="aspect-3/4 w-full bg-white" />
                          ) : p.status === "skipped" ? (
                            // Not picked: never rendered, so there is no image (U111).
                            <div className="grid aspect-3/4 w-full place-items-center bg-surface-3">
                              <Icon name="pending" className="text-subtle" />
                            </div>
                          ) : (
                            <div className="grid aspect-3/4 w-full place-items-center bg-surface-3">{p.status === "failed" ? <Icon name="error" className="text-err" /> : <Spinner className="size-4 border-2" />}</div>
                          )}
                          {p.status === "skipped" ? (
                            <span className="absolute inset-x-0 bottom-0 bg-black/60 text-[10px] leading-4 text-white">chưa xử lý</span>
                          ) : (
                            p.is_blank && <span className="absolute inset-x-0 bottom-0 bg-black/60 text-[10px] leading-4 text-white">trắng</span>
                          )}
                        </div>
                        <span className={"text-[11px] tabular-nums " + (on ? "font-medium text-accent" : "text-muted")}>{p.page_no}</span>
                      </button>
                    );
                  })}
                </div>
              </section>
            );
          })}
        </div>

        {/* page */}
        <div className="relative min-w-0 flex-1">
          <div className="absolute inset-0 overflow-auto px-6 pt-2 pb-24">
            <div className="mx-auto" style={{ width: `${Math.min(zoom, 400)}%`, maxWidth: zoom <= 100 ? 980 : undefined }}>
              {curPage?.status === "skipped" ? (
                <div className="grid aspect-3/4 place-items-center rounded bg-surface-3 p-6 text-center text-sm text-muted">
                  <span className="flex flex-col items-center gap-3">
                    <Icon name="pending" size={40} className="text-subtle" />
                    Trang {pageNo} chưa được đọc và phân loại
                    <button className="btn btn-primary btn-sm" onClick={() => reparse(skippedRun(pages, pageNo))}>
                      <Icon name="play_arrow" size={18} /> Xử lý từ trang này…
                    </button>
                  </span>
                </div>
              ) : pageErr ? (
                <div className="grid aspect-3/4 place-items-center rounded bg-surface-3 p-6 text-center text-sm text-muted">
                  <span>
                    <Icon name="hourglass_empty" size={40} className="mb-2 text-subtle" />
                    <br />
                    Trang {pageNo} chưa sẵn sàng
                  </span>
                </div>
              ) : !page ? (
                <div className="grid aspect-3/4 place-items-center">
                  <Spinner />
                </div>
              ) : (
                <div className="overflow-hidden rounded-md border border-line shadow-1">
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
          <div className="absolute bottom-5 left-1/2 flex h-12 -translate-x-1/2 items-center gap-0.5 rounded-full border border-line bg-surface/95 px-2 text-sm text-fg shadow-2 backdrop-blur">
            <button className={barBtn} disabled={pageNo <= 1} onClick={() => go(pageNo - 1)} title="Trang trước (←)">
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
                className="h-7 w-10 rounded bg-surface-2 text-center outline-none focus:ring-2 focus:ring-accent"
                aria-label="Số trang"
              />
              / {doc.page_count}
            </span>
            <button className={barBtn} disabled={pageNo >= doc.page_count} onClick={() => go(pageNo + 1)} title="Trang sau (→)">
              <Icon name="chevron_right" />
            </button>
            <span className="mx-1 h-6 w-px bg-line" />
            <button className={barBtn} disabled={zoom <= ZOOMS[0]} onClick={() => setZoom(ZOOMS[Math.max(0, ZOOMS.indexOf(zoom) - 1)])} title="Thu nhỏ">
              <Icon name="zoom_out" />
            </button>
            <button className="h-8 w-14 rounded-full hover:bg-fg/8" onClick={() => setZoom(100)} title="Vừa khung">
              {zoom}%
            </button>
            <button className={barBtn} disabled={zoom >= ZOOMS.at(-1)!} onClick={() => setZoom(ZOOMS[Math.min(ZOOMS.length - 1, ZOOMS.indexOf(zoom) + 1)])} title="Phóng to">
              <Icon name="zoom_in" />
            </button>
            <span className="mx-1 h-6 w-px bg-line" />
            <button className={barBtn + (showBoxes ? " bg-accent-soft text-on-accent-soft" : "")} onClick={() => setShowBoxes((b) => !b)} title="Khung từng dòng chữ">
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
              {tab === "info" && <InfoPanel doc={doc} page={page} pageRow={curPage} classify={types.length > 0} typeName={(c) => typeTitle(c, types)} onGo={(n) => go(n)} />}
              {tab === "find" && <FindPanel doc={doc} onPick={(n, line) => go(n, { hl: [line], tab: "find" })} />}
              {tab === "tree" && <TreePanel doc={doc} current={pageNo} onPick={(n) => go(n, { tab: "tree" })} />}
              {(tab === "lines" || tab === "md" || tab === "blocks") && !page && !pageErr && <Loading />}
              {(tab === "lines" || tab === "md" || tab === "blocks") && pageErr && <Empty icon="hourglass_empty">Trang chưa sẵn sàng</Empty>}
              {tab === "lines" && page && (
                <LinesPanel lines={lines} highlighted={highlighted} hover={hover} onHover={setHover} onPick={(n) => setFocus(n)} scrollTarget={focus ?? hl[0]} />
              )}
              {tab === "md" && page && (
                <div className="px-5 py-4">
                  <Markdown text={page.markdown_vlm || page.markdown || "_Trang trống_"} />
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
          pages={pages}
          range={reparseRange}
          onClose={() => setModal(null)}
          onDone={() => {
            setModal(null);
            toast("Đã đưa vào hàng đợi đọc lại");
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

function InfoPanel({
  doc,
  page,
  pageRow,
  classify,
  typeName,
  onGo,
}: {
  doc: Doc;
  page: PageView | null;
  pageRow?: DocumentPage;
  classify: boolean;
  typeName: (code: string) => string;
  onGo: (n: number) => void;
}) {
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
      {doc.metadata && Object.keys(doc.metadata).length > 0 && (
        <Row label="Thông tin thêm">
          <span className="flex flex-wrap gap-1.5">
            <MetaChips meta={doc.metadata} />
          </span>
        </Row>
      )}
      <Row label="Tải lên">{fmtDate(doc.created_at)}</Row>
      <Row label="Cập nhật">{fmtDate(doc.updated_at)}</Row>
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
      {pageRow && !page && (
        <>
          <h3 className="pt-4 pb-1 text-sm font-medium">Trang {pageRow.page_no}</h3>
          <Row label="Đã xử lý">
            <span className="flex flex-wrap gap-1">
              <PageSteps page={pageRow} classify={classify} />
            </span>
          </Row>
        </>
      )}
      {page && (
        <>
          <h3 className="pt-4 pb-1 text-sm font-medium">Trang {page.page_no}</h3>
          {pageRow && (
            <Row label="Đã xử lý">
              <span className="flex flex-wrap gap-1">
                <PageSteps page={pageRow} classify={classify} />
              </span>
            </Row>
          )}
          {page.doc_type && (
            <Row label="Loại giấy tờ">
              {typeName(page.doc_type)} · {Math.round((page.doc_type_confidence ?? 0) * 100)}%
            </Row>
          )}
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
    <Modal title="Toàn văn" icon="article" onClose={onClose} width={900}>
      <div className="text-fg">{err ? <Empty>{err}</Empty> : md === null ? <Loading /> : <Markdown text={md} />}</div>
    </Modal>
  );
}

// Đọc lại cùng hộp chọn trang như lúc tải lên (U111); còn trang chưa xử lý
// thì điền sẵn khoảng đó. Chỉ trang trong khoảng được xử lý; chọn đủ mọi
// trang thì đọc lại cả file như trước.
export function ReparseDialog({ doc, pages, range, onClose, onDone }: { doc: Document; pages: DocumentPage[]; range?: [number, number]; onClose: () => void; onDone: () => void }) {
  const skipped = pagesWith(pages, "skipped");
  const fresh = skipped.length === doc.page_count;
  return (
    <PagePicker
      title={fresh ? "Xử lý tài liệu" : "Đọc lại tài liệu"}
      icon={fresh ? "play_arrow" : "refresh"}
      files={[{ id: doc.id, name: doc.file_name, mime: doc.mime_type, pages: doc.page_count, note: pageSummary(pages) }]}
      range={range ?? (skipped.length && !fresh ? [skipped[0], skipped.at(-1)!] : undefined)}
      ok={fresh ? "Đọc & phân loại" : "Đọc lại"}
      onClose={onClose}
      onSubmit={async ([p]) => {
        await reparseDocument(doc.id, { pages: p.pages.length === doc.page_count && !fresh ? undefined : p.pages });
        onDone();
      }}
    />
  );
}
