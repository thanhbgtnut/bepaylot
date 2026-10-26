import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";

import { getCaseFields, getCaseTOC, getPage, getSplit, getTree, listPages, reparseDocument, saveCaseField } from "../../api/endpoints";
import type { CaseDocFields, CaseFieldView, CaseTOC, DocType, DocumentPage, SplitFile, SplitView, TOCDoc, TreeNode } from "../../api/types";
import { useApp } from "../../components/AppContext";
import { CasePicker, caseLabel } from "../../components/CasePicker";
import { NeedKB, TopBar } from "../../components/Layout";
import { PageImage } from "../../components/PageImage";
import { PageSteps, pageSummary, pagesWith, skippedRun } from "../../components/PageState";
import { PagePicker } from "../documents/PagePicker";
import { useToast } from "../../components/toast";
import { Badge, Empty, FileIcon, Icon, Loading, Spinner, StatusBadge } from "../../components/ui";
import { typeColor, typeTitle } from "../../lib/doctype";
import { Studio } from "./Studio";

// One case (hồ sơ, §7.2): Nguồn (the files, their documents and sections),
// the pages of the chosen source, the fields read from its documents
// (editable) and Studio. The chat lives on the Hỏi đáp page (U96).

const casePath = (caseId: string) => `/cases/${caseId}`;
const pages = (a: number, b: number) => (a === b ? `tr. ${a}` : `tr. ${a}–${b}`);

// Trees are fetched once per document and kept for the session.
const treeCache = new Map<string, TreeNode[]>();
function useTree(docId: string | null) {
  const [nodes, setNodes] = useState<TreeNode[] | null>(docId ? (treeCache.get(docId) ?? null) : null);
  const [err, setErr] = useState("");
  useEffect(() => {
    setErr("");
    if (!docId) return setNodes(null);
    const cached = treeCache.get(docId);
    if (cached) return setNodes(cached);
    setNodes(null);
    let alive = true;
    getTree(docId).then(
      (n) => {
        treeCache.set(docId, n);
        if (alive) setNodes(n);
      },
      (e) => alive && setErr((e as Error).message),
    );
    return () => {
      alive = false;
    };
  }, [docId]);
  return { nodes, err };
}

// Tree helpers: children by parent, in order, and the ids on the path to a node.
function childrenOf(nodes: TreeNode[]) {
  const m = new Map<string, TreeNode[]>();
  for (const n of nodes) if (n.parent_id) m.set(n.parent_id, [...(m.get(n.parent_id) ?? []), n]);
  for (const list of m.values()) list.sort((a, b) => a.ord - b.ord);
  return m;
}
function pathIds(nodes: TreeNode[], shortId: string | null) {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const out = new Set<string>();
  for (let n = nodes.find((x) => x.short_id === shortId); n; n = n.parent_id ? byId.get(n.parent_id) : undefined) out.add(n.id);
  return out;
}

// Whether the field column is folded, remembered per browser.
const FIELDS_KEY = "bp.caseFieldsOpen";
function readOpen() {
  try {
    return localStorage.getItem(FIELDS_KEY) !== "0";
  } catch {
    return true;
  }
}

type Col = "sources" | "view" | "fields" | "studio";

export function CasePage() {
  const { caseId } = useParams();
  const [sp, setSp] = useSearchParams();
  const nodeId = sp.get("node"); // short id (n<k>); none = the whole file
  const hl = sp.get("hl"); // "page:line" of the field the user picked
  const nav = useNavigate();
  const { kb, kbs, kcase, cases, selectCase } = useApp();
  const [toc, setToc] = useState<CaseTOC | null>(null);
  const [err, setErr] = useState("");
  const [reload, setReload] = useState(0);
  // How the files split into documents and bundles (§6.9.7); null when the
  // case type has no classification.
  const [split, setSplit] = useState<SplitView | null>(null);
  // Narrow screens show one column at a time.
  const [col, setCol] = useState<Col>("view");
  const [fieldsOpen, setFieldsOpen] = useState(readOpen);

  // The URL names the case; without one, open the case picked last.
  useEffect(() => {
    if (!caseId && kcase) nav(casePath(kcase.id), { replace: true });
  }, [caseId, kcase, nav]);
  useEffect(() => {
    if (caseId && cases?.some((c) => c.id === caseId) && kcase?.id !== caseId) selectCase(caseId);
  }, [caseId, cases]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!caseId) return;
    let alive = true;
    setErr("");
    getCaseTOC(caseId).then(
      (t) => alive && setToc(t),
      (e) => alive && setErr((e as Error).message),
    );
    return () => {
      alive = false;
    };
  }, [caseId, reload]);
  useEffect(() => setToc(null), [caseId]);
  useEffect(() => {
    if (!caseId) return;
    let alive = true;
    getSplit(caseId).then(
      (v) => alive && setSplit(v.labels.length ? v : null),
      () => alive && setSplit(null),
    );
    return () => {
      alive = false;
    };
  }, [caseId, reload]);

  // While files are still being parsed or indexed, poll the TOC. Files
  // waiting for the user to pick pages (U111) or failed do not move.
  const busy = !!toc?.pending?.some((p) => p.status !== "failed" && p.status !== "not_started");
  useEffect(() => {
    if (!busy) return;
    const t = setInterval(() => setReload((r) => r + 1), 5000);
    return () => clearInterval(t);
  }, [busy]);

  useEffect(() => {
    try {
      localStorage.setItem(FIELDS_KEY, fieldsOpen ? "1" : "0");
    } catch {
      /* private mode */
    }
  }, [fieldsOpen]);

  // The source shown: the file (and section) in the URL, else the first file.
  const docId = sp.get("doc") || toc?.documents[0]?.document_id || null;
  const select = useCallback((doc: string, node?: string) => setSp(node ? { doc, node } : { doc }), [setSp]);
  const pickLine = useCallback(
    (page: number, line: number) => {
      const next = new URLSearchParams(sp);
      next.set("doc", docId ?? "");
      next.set("hl", `${page}:${line}`);
      setSp(next, { replace: true });
      setCol("view");
    },
    [sp, setSp, docId],
  );

  if (!kb)
    return (
      <>
        <TopBar title="Hồ sơ" />
        <NeedKB />
      </>
    );

  const c = toc?.case ?? kcase;
  const doc = toc?.documents.find((d) => d.document_id === docId) ?? null;
  const types = kbs.find((k) => k.id === c?.kb_id)?.config.doc_types ?? kb.config.doc_types ?? [];
  const show = (k: Col) => (col === k ? "flex" : "hidden lg:flex");

  return (
    <>
      <TopBar
        title={c ? caseLabel(c) : "Hồ sơ"}
        subtitle={
          toc ? (
            <span className="flex items-center gap-2">
              {busy && <Spinner className="size-3 border-[1.5px]" />}
              {toc.documents.length} file đã có mục lục
              {toc.pending?.length ? ` · ${toc.pending.length} file đang xử lý` : ""}
              {c?.title ? ` · ${c.title}` : ""}
            </span>
          ) : undefined
        }
      >
        {c && (
          <button className="btn btn-text" title="Hỏi trợ lý AI về hồ sơ này" onClick={() => (selectCase(c.id), nav("/chat"))}>
            <Icon name="forum" size={18} /> Hỏi đáp
          </button>
        )}
        <CasePicker value={c} onChange={(x) => nav(casePath(x.id))} />
      </TopBar>
      {cases && !cases.length ? (
        <Empty icon="folder_off" title="Chưa có hồ sơ">
          Tải file lên kèm mã hồ sơ để tạo hồ sơ đầu tiên.
          <div>
            <Link className="btn btn-primary mt-5" to="/documents?upload=1">
              <Icon name="upload_file" size={18} /> Tải file lên
            </Link>
          </div>
        </Empty>
      ) : err ? (
        <Empty icon="error" title="Không tải được hồ sơ">
          {err}
        </Empty>
      ) : !toc || !caseId || !c ? (
        <Loading />
      ) : (
        <>
          <div className="flex flex-none border-t border-line lg:hidden">
            {(
              [
                ["sources", "Nguồn"],
                ["view", "Xem"],
                ["fields", "Trường"],
                ["studio", "Studio"],
              ] as const
            ).map(([k, l]) => (
              <button key={k} className={"tab flex-1 " + (col === k ? "tab-active" : "")} onClick={() => setCol(k)}>
                {l}
              </button>
            ))}
          </div>
          <div className="flex min-h-0 flex-1 border-t border-line">
            <Sidebar toc={toc} split={split} types={types} caseId={c.id} docId={docId} nodeId={nodeId} onSelect={(d, n) => (select(d, n), setCol("view"))} className={show("sources")} />
            <section className={show("view") + " min-w-0 flex-1 flex-col"}>
              {doc ? (
                <Viewer key={doc.document_id} doc={doc} nodeId={nodeId} hl={hl} classify={types.length > 0} onReparsed={() => setReload((r) => r + 1)} />
              ) : (
                <Empty icon="folder_off">Hồ sơ chưa có nguồn nào có mục lục</Empty>
              )}
            </section>
            {doc && (fieldsOpen || col === "fields") ? (
              <FieldsColumn key={doc.document_id} caseId={c.id} doc={doc} nodeId={nodeId} hl={hl} types={types} onPick={pickLine} onFold={() => setFieldsOpen(false)} className={show("fields")} />
            ) : doc ? (
              <aside className="hidden w-12 flex-none flex-col items-center gap-2 border-l border-line py-3 lg:flex">
                <button className="btn-icon" title="Hiện trường trích xuất" onClick={() => setFieldsOpen(true)}>
                  <Icon name="right_panel_open" />
                </button>
                <span className="text-xs text-muted [writing-mode:vertical-rl]">Trường trích xuất</span>
              </aside>
            ) : null}
            <Studio key={c.id} kcase={c} split={split} className={show("studio")} />
          </div>
        </>
      )}
    </>
  );
}

// ---------------------------------------------------------------- sources

function Sidebar({
  toc,
  split,
  types,
  caseId,
  docId,
  nodeId,
  onSelect,
  className,
}: {
  toc: CaseTOC;
  split: SplitView | null;
  types: DocType[];
  caseId: string;
  docId: string | null;
  nodeId: string | null;
  onSelect: (doc: string, node?: string) => void;
  className: string;
}) {
  return (
    <aside className={className + " w-full flex-none flex-col overflow-auto border-r border-line px-2 py-3 lg:w-72"}>
      <div className="flex items-center pr-1 pb-1 pl-3">
        <span className="flex-1 text-sm font-medium text-fg">Nguồn</span>
        <Link className="btn-icon btn-sm" to="/documents?upload=1" title="Thêm nguồn">
          <Icon name="add" size={20} />
        </Link>
      </div>
      {split && (
        <Link className="btn btn-tonal btn-sm mx-2 mb-2 flex-none" to={`/cases/${caseId}/split`}>
          <Icon name="content_cut" size={18} /> Tách & gom trang
          {!split.reviewed && <span className="badge badge-warn">{split.files.filter((f) => f.indexed && f.status !== "reviewed").length}</span>}
        </Link>
      )}
      {!toc.documents.length && !toc.pending?.length && <div className="px-3 py-2 text-sm text-subtle">Hồ sơ chưa có file</div>}
      {toc.documents.map((d) => (
        <FileEntry
          key={d.document_id}
          d={d}
          sf={split?.files.find((f) => f.document_id === d.document_id)}
          types={types}
          open={d.document_id === docId}
          nodeId={d.document_id === docId ? nodeId : null}
          onSelect={onSelect}
        />
      ))}
      {!!toc.pending?.length && (
        <>
          <div className="mt-3 px-3 pb-1 text-xs font-medium text-muted">Chưa có mục lục</div>
          {toc.pending.map((p) => (
            <Link
              key={p.document_id}
              to={`/documents/${p.document_id}`}
              title={p.status === "not_started" ? "Mở để chọn trang cần đọc & phân loại" : undefined}
              className="flex h-8 items-center gap-2 rounded-full px-3 text-sm text-muted hover:bg-fg/8"
            >
              <FileIcon name={p.file_name} size={18} />
              <span className="min-w-0 flex-1 truncate">{p.file_name}</span>
              {p.status === "failed" || p.status === "not_started" ? <StatusBadge status={p.status} /> : <Spinner className="size-3.5 border-2" />}
            </Link>
          ))}
        </>
      )}
    </aside>
  );
}

// A file is a group header; under it its documents (a dot of the type's
// color), whose sections stay folded until opened and open by themselves along
// the path of the chosen section (U96).
function FileEntry({
  d,
  sf,
  types,
  open,
  nodeId,
  onSelect,
}: {
  d: TOCDoc;
  sf?: SplitFile;
  types: DocType[];
  open: boolean;
  nodeId: string | null;
  onSelect: (doc: string, node?: string) => void;
}) {
  const [expanded, setExpanded] = useState(open);
  useEffect(() => {
    if (open) setExpanded(true);
  }, [open]);
  const { nodes } = useTree(expanded ? d.document_id : null);
  const kids = useMemo(() => (nodes ? childrenOf(nodes) : null), [nodes]);
  const onPath = useMemo(() => (nodes ? pathIds(nodes, nodeId) : new Set<string>()), [nodes, nodeId]);
  const root = nodes?.find((n) => !n.parent_id);
  const status = sf?.status === "reviewed" ? (
    <span title="Tách & gom đã duyệt">
      <Icon name="check_circle" size={16} fill className="text-ok" />
    </span>
  ) : sf?.status === "proposed" ? (
    <span title="AI đề xuất tách, cần duyệt">
      <Icon name="auto_awesome" size={16} className="text-warn" />
    </span>
  ) : null;

  return (
    <div className="mb-2">
      <div className={"flex h-9 items-center gap-1 rounded-lg pr-2 " + (open && !nodeId ? "bg-accent-soft text-on-accent-soft" : "text-fg hover:bg-fg/6")}>
        <button className="btn-icon size-7 flex-none" onClick={() => setExpanded((v) => !v)} aria-label={expanded ? "Thu gọn" : "Mở mục lục"}>
          <Icon name="arrow_right" size={20} className={"transition-transform " + (expanded ? "rotate-90" : "")} />
        </button>
        <button className="flex min-w-0 flex-1 items-center gap-2 text-left" onClick={() => (onSelect(d.document_id), setExpanded(true))} title={d.file_name}>
          <FileIcon name={d.file_name} size={18} />
          <span className="min-w-0 flex-1 truncate text-sm font-medium">{d.file_name}</span>
        </button>
        {d.pages_skipped ? (
          <span className="text-xs whitespace-nowrap text-warn" title={`${d.pages_skipped} trang chưa xử lý`}>
            {d.page_count - d.pages_skipped}/{d.page_count} tr
          </span>
        ) : (
          <span className="text-xs whitespace-nowrap text-muted">{d.page_count} tr</span>
        )}
        {status}
      </div>
      {expanded &&
        (!nodes ? (
          <div className="py-1 pl-10">
            <Spinner className="size-3.5 border-2" />
          </div>
        ) : root && kids ? (
          <div className="ml-5 border-l border-line pl-2">
            {(kids.get(root.id) ?? []).map((n) => (
              <NodeRow key={n.id} n={n} kids={kids} top types={types} docId={d.document_id} nodeId={nodeId} onPath={onPath} onSelect={onSelect} />
            ))}
          </div>
        ) : null)}
    </div>
  );
}

function NodeRow({
  n,
  kids,
  top = false,
  types,
  docId,
  nodeId,
  onPath,
  onSelect,
}: {
  n: TreeNode;
  kids: Map<string, TreeNode[]>;
  top?: boolean;
  types: DocType[];
  docId: string;
  nodeId: string | null;
  onPath: Set<string>;
  onSelect: (doc: string, node?: string) => void;
}) {
  const sub = kids.get(n.id) ?? [];
  const [open, setOpen] = useState<boolean | null>(null);
  const expanded = open ?? onPath.has(n.id);
  const cur = n.short_id === nodeId;
  // Pages not picked for processing (U111): a hollow dot, muted.
  const todo = n.doc_type === "not_processed";
  return (
    <>
      <div className={"group flex h-8 items-center rounded-lg pr-1 " + (cur ? "bg-accent-soft text-on-accent-soft" : "text-fg hover:bg-fg/6")}>
        <button onClick={() => onSelect(docId, n.short_id)} title={todo ? "Trang chưa được đọc và phân loại" : n.summary || n.title} className="flex h-full min-w-0 flex-1 items-center gap-2 pl-2 text-left">
          {top && todo && <i className="size-2 flex-none rounded-full border border-outline" />}
          {top && n.doc_type && !todo && <i className="size-2 flex-none rounded-full" style={{ background: typeColor(n.doc_type, types) }} title={typeTitle(n.doc_type, types)} />}
          <span className={"min-w-0 flex-1 truncate " + (top ? "text-sm" : "text-[13px] " + (cur ? "" : "text-muted")) + (todo && !cur ? " text-muted italic" : "")}>{n.title || n.short_id}</span>
          <span className={"text-[11px] whitespace-nowrap tabular-nums " + (cur ? "" : "text-subtle")}>{n.page_start === n.page_end ? n.page_start : `${n.page_start}–${n.page_end}`}</span>
        </button>
        {sub.length ? (
          <button className="btn-icon size-6 flex-none" onClick={() => setOpen(!expanded)} aria-label={expanded ? "Thu gọn" : "Mục con"}>
            <Icon name="chevron_right" size={18} className={"transition-transform " + (expanded ? "rotate-90" : "")} />
          </button>
        ) : (
          <span className="w-6 flex-none" />
        )}
      </div>
      {sub.length > 0 && expanded && (
        <div className="ml-3 border-l border-line pl-2">
          {sub.map((x) => (
            <NodeRow key={x.id} n={x} kids={kids} types={types} docId={docId} nodeId={nodeId} onPath={onPath} onSelect={onSelect} />
          ))}
        </div>
      )}
    </>
  );
}

// ---------------------------------------------------------------- viewer

// The pages of the chosen file, light and without a summary (U96): the chosen
// section's pages are outlined and scrolled to; a field's line is highlighted.
function Viewer({ doc, nodeId, hl, classify, onReparsed }: { doc: TOCDoc; nodeId: string | null; hl: string | null; classify: boolean; onReparsed: () => void }) {
  const { toast } = useToast();
  const { nodes } = useTree(doc.document_id);
  // The state of every page (U111): which pages were read and classified,
  // which were not picked for processing.
  const [rows, setRows] = useState<DocumentPage[]>([]);
  const [pick, setPick] = useState<[number, number] | null>(null);
  useEffect(() => {
    let alive = true;
    listPages(doc.document_id).then(
      (ps) => alive && setRows(ps),
      () => {},
    );
    return () => {
      alive = false;
    };
  }, [doc.document_id]);
  const row = (n: number) => rows.find((p) => p.page_no === n);
  const summary = rows.length ? pageSummary(rows) : "";
  const skipped = pagesWith(rows, "skipped");
  const failed = pagesWith(rows, "failed");
  const node = nodeId ? nodes?.find((n) => n.short_id === nodeId) : undefined;
  const from = node?.page_start ?? 1;
  const to = node?.page_end ?? doc.page_count;
  const [hlPage, hlLine] = (hl ?? "").split(":").map(Number);
  const box = useRef<HTMLDivElement>(null);

  // Scroll to the first page of the section (or the highlighted page), again
  // once the images above it have their real height.
  const target = hlPage || from;
  useLayoutEffect(() => {
    const go = () => box.current?.querySelector<HTMLElement>(`[data-page="${target}"]`)?.scrollIntoView({ block: "start" });
    go();
    const t = setTimeout(go, 400);
    return () => clearTimeout(t);
  }, [target, nodeId]);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex h-11 flex-none items-center gap-2 border-b border-line px-4 text-sm text-fg">
        <FileIcon name={doc.file_name} size={18} />
        <span className="min-w-0 flex-1 truncate">
          {doc.file_name}
          {node && (
            <span className="text-muted">
              {" "}
              · {node.title || node.short_id} · {pages(from, to)}
            </span>
          )}
        </span>
        <Link className="btn-icon btn-sm" to={`/documents/${doc.document_id}?page=${target}`} title="Mở trong trình xem tài liệu">
          <Icon name="open_in_new" size={18} />
        </Link>
      </div>
      {summary && (
        <div className="flex flex-none flex-wrap items-center gap-x-3 gap-y-1 border-b border-line bg-surface-2/60 px-4 py-2 text-xs text-muted">
          <span className="min-w-0 flex-[1_1_12rem]">{summary}</span>
          {(skipped.length > 0 || failed.length > 0) && (
            <button className="btn btn-tonal btn-sm" onClick={() => setPick(skipped.length ? [skipped[0], skipped.at(-1)!] : [failed[0], failed.at(-1)!])}>
              <Icon name="play_arrow" size={18} /> Xử lý thêm trang…
            </button>
          )}
        </div>
      )}
      <div ref={box} className="min-h-0 flex-1 overflow-auto bg-surface-2/50 px-6 py-4">
        <div className="mx-auto flex max-w-200 flex-col gap-4">
          {Array.from({ length: doc.page_count }, (_, i) => i + 1).map((n) => {
            const r = row(n);
            return (
              <div key={n} data-page={n} className="relative scroll-mt-2">
                <div className={"overflow-hidden rounded-md border border-line bg-white " + (node && n >= from && n <= to ? "outline-3 outline-offset-2 outline-accent" : "")}>
                  {r?.status === "skipped" ? (
                    // Not picked: never rendered, so there is no image (U111).
                    <div className="grid aspect-[1/1.414] place-items-center bg-surface-3 text-sm text-muted">
                      <span className="flex flex-col items-center gap-3">
                        <Icon name="pending" size={36} className="text-subtle" />
                        Trang {n} chưa được đọc và phân loại
                        <button className="btn btn-sm bg-surface" onClick={() => setPick(skippedRun(rows, n))}>
                          <Icon name="play_arrow" size={18} /> Xử lý từ trang này…
                        </button>
                      </span>
                    </div>
                  ) : (
                    <LazyPage docId={doc.document_id} pageNo={n} line={n === hlPage ? hlLine : undefined} />
                  )}
                </div>
                {r && (
                  <div className="absolute top-2 left-2 flex flex-wrap gap-1 rounded-lg bg-surface/90 p-1 shadow-1">
                    <PageSteps page={r} classify={classify} compact />
                  </div>
                )}
                <span className="absolute right-2 bottom-2 rounded bg-black/60 px-1.5 text-xs text-white">
                  {n} / {doc.page_count}
                </span>
              </div>
            );
          })}
        </div>
      </div>
      {pick && (
        <PagePicker
          title="Xử lý thêm trang"
          icon="play_arrow"
          files={[{ id: doc.document_id, name: doc.file_name, pages: doc.page_count, note: summary }]}
          range={pick}
          onClose={() => setPick(null)}
          onSubmit={async ([p]) => {
            await reparseDocument(doc.document_id, { pages: p.pages });
            treeCache.delete(doc.document_id);
            setPick(null);
            toast(`Đang đọc & phân loại ${pages(p.pages[0], p.pages.at(-1)!)} của “${doc.file_name}”`);
            onReparsed();
          }}
        />
      )}
    </div>
  );
}

// A page image mounted only near the viewport; with a line, that line's box
// is highlighted.
function LazyPage({ docId, pageNo, line }: { docId: string; pageNo: number; line?: number }) {
  const ref = useRef<HTMLDivElement>(null);
  const [visible, setVisible] = useState(!!line);
  const [box, setBox] = useState<{ width: number; height: number; bbox: { x0: number; y0: number; x1: number; y1: number } } | null>(null);
  useEffect(() => {
    const el = ref.current;
    if (!el || visible) return;
    const io = new IntersectionObserver((es) => es.some((e) => e.isIntersecting) && setVisible(true), { rootMargin: "600px" });
    io.observe(el);
    return () => io.disconnect();
  }, [visible]);
  useEffect(() => {
    setBox(null);
    if (!line) return;
    setVisible(true);
    let alive = true;
    getPage(docId, pageNo).then(
      (p) => {
        const l = p.lines?.find((x) => x.line_no === line);
        if (alive && l) setBox({ width: p.width, height: p.height, bbox: l.bbox });
      },
      () => {},
    );
    return () => {
      alive = false;
    };
  }, [docId, pageNo, line]);
  return (
    <div ref={ref} className={visible ? "" : "aspect-[1/1.414]"}>
      {visible && (
        <PageImage
          docId={docId}
          pageNo={pageNo}
          width={box?.width}
          height={box?.height}
          boxes={box ? [{ key: String(line), bbox: box.bbox, className: "hl" }] : []}
          hideBoxes={!!box}
          scrollTo={box ? "hl" : undefined}
        />
      )}
    </div>
  );
}

// ---------------------------------------------------------------- fields

// The fields read from the documents in view (the file, or the section
// chosen): label above, value below and wrapping; edited in place (U99).
// Reading costs no model call: fields are extracted when a sheet is built.
function FieldsColumn({
  caseId,
  doc,
  nodeId,
  hl,
  types,
  onPick,
  onFold,
  className,
}: {
  caseId: string;
  doc: TOCDoc;
  nodeId: string | null;
  hl: string | null;
  types: DocType[];
  onPick: (page: number, line: number) => void;
  onFold: () => void;
  className: string;
}) {
  const { fail, toast } = useToast();
  const { nodes } = useTree(doc.document_id);
  const [list, setList] = useState<CaseDocFields[] | null>(null);
  const [editing, setEditing] = useState<string | null>(null);
  useEffect(() => {
    let alive = true;
    getCaseFields(caseId, doc.document_id).then(
      (l) => alive && setList(l),
      (e) => alive && (fail(e), setList([])),
    );
    return () => {
      alive = false;
    };
  }, [caseId, doc.document_id, fail]);

  const node = nodeId ? nodes?.find((n) => n.short_id === nodeId) : undefined;
  const shown = (list ?? []).filter((g) => !node || (g.page_start <= node.page_end && g.page_end >= node.page_start));
  const titleOf = (g: CaseDocFields) => nodes?.find((n) => n.doc_type === g.label && n.page_start === g.page_start)?.title || typeTitle(g.label, types);

  const save = async (g: CaseDocFields, f: CaseFieldView, value: string) => {
    setEditing(null);
    if (value.trim() === f.value.trim()) return;
    try {
      const v = await saveCaseField(caseId, { segment_id: g.segment_id, key: f.key, value });
      setList((l) => l?.map((x) => (x.segment_id === g.segment_id ? { ...x, fields: x.fields.map((y) => (y.key === f.key ? v : y)) } : x)) ?? null);
      toast(`Đã lưu ${f.label}`);
    } catch (e) {
      fail(e);
    }
  };

  return (
    <aside className={className + " w-full flex-none flex-col border-l border-line lg:w-80"}>
      <div className="flex h-11 flex-none items-center gap-2 border-b border-line pr-2 pl-4">
        <span className="flex-1 text-sm font-medium text-fg">Trường trích xuất</span>
        <button className="btn-icon btn-sm max-lg:hidden" title="Thu gọn" onClick={onFold}>
          <Icon name="right_panel_close" size={20} />
        </button>
      </div>
      <div className="min-h-0 flex-1 overflow-auto px-3">
        {!list ? (
          <Loading />
        ) : !shown.length ? (
          <div className="px-2 py-8 text-center text-sm text-subtle">
            Chưa có trường nào được trích xuất cho {node ? "mục" : "file"} này.
            <div className="mt-1 text-xs">Trường được lấy khi tạo bảng ở Studio → Xuất Excel.</div>
          </div>
        ) : (
          shown.map((g) => (
            <section key={g.segment_id} className="border-b border-line pb-3 last:border-b-0">
              <div className="flex items-start gap-2 px-1 pt-3 pb-2">
                <i className="mt-1.5 size-2 flex-none rounded-full" style={{ background: typeColor(g.label, types) }} />
                <div className="min-w-0 flex-1">
                  <div className="text-sm font-medium wrap-break-word text-fg">{titleOf(g)}</div>
                  <div className="text-xs text-muted">
                    {typeTitle(g.label, types)} · {pages(g.page_start, g.page_end)}
                  </div>
                </div>
              </div>
              <dl className="flex flex-col">
                {g.fields.map((f) => {
                  const id = g.segment_id + ":" + f.key;
                  if (editing === id) return <FieldEditor key={id} f={f} onSave={(v) => save(g, f, v)} onCancel={() => setEditing(null)} />;
                  const cur = !!f.page_no && hl === `${f.page_no}:${f.line_no}`;
                  const canSee = !!f.page_no && !!f.line_no;
                  return (
                    <div key={id} className="group relative">
                      <div
                        role={canSee ? "button" : undefined}
                        tabIndex={canSee ? 0 : undefined}
                        title={canSee ? `Xem trên trang ${f.page_no} · nháy đúp để sửa` : "Nháy đúp để sửa"}
                        onClick={() => canSee && onPick(f.page_no!, f.line_no!)}
                        onDoubleClick={() => setEditing(id)}
                        className={"rounded-lg px-2 py-1.5 " + (cur ? "bg-warn-soft" : "hover:bg-fg/5") + (canSee ? " cursor-pointer" : "")}
                      >
                        <dt className="pr-7 text-xs text-muted">{f.label}</dt>
                        <dd className={"mt-0.5 text-sm wrap-break-word whitespace-pre-wrap " + (f.value ? "text-fg" : "text-subtle italic")}>
                          {f.value || (f.asked ? "không thấy" : "chưa lấy")}{" "}
                          {f.edited ? <Badge tone="info">đã sửa</Badge> : f.low ? <Badge tone="warn">kiểm tra lại</Badge> : null}
                        </dd>
                      </div>
                      <button
                        className="btn-icon absolute top-1 right-1 size-7 opacity-0 group-hover:opacity-100 focus-visible:opacity-100 max-lg:opacity-100"
                        title={"Sửa " + f.label}
                        aria-label={"Sửa " + f.label}
                        onClick={() => setEditing(id)}
                      >
                        <Icon name="edit" size={16} />
                      </button>
                      {f.edited && (
                        <div className="flex items-center gap-2 px-2 pb-1 text-[11px] text-muted">
                          <span className="min-w-0 wrap-break-word">
                            AI: <s>{f.ai_value || "không thấy"}</s>
                          </span>
                          <button className="flex-none text-accent hover:underline" onClick={() => save(g, f, f.ai_value)}>
                            Hoàn tác
                          </button>
                        </div>
                      )}
                    </div>
                  );
                })}
              </dl>
            </section>
          ))
        )}
      </div>
    </aside>
  );
}

// Edit in place: Enter or leaving the box saves, Esc cancels, Shift+Enter
// starts a new line.
function FieldEditor({ f, onSave, onCancel }: { f: CaseFieldView; onSave: (v: string) => void; onCancel: () => void }) {
  const done = useRef(false);
  return (
    <div className="rounded-lg bg-accent/5 px-2 py-1.5">
      <dt className="text-xs font-medium text-accent">{f.label}</dt>
      <textarea
        autoFocus
        rows={1}
        aria-label={f.label}
        defaultValue={f.value}
        className="input field-sizing-content mt-1 h-auto w-full resize-none py-1.5 text-sm"
        onFocus={(e) => e.currentTarget.setSelectionRange(e.currentTarget.value.length, e.currentTarget.value.length)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
            e.preventDefault();
            e.currentTarget.blur();
          }
          if (e.key === "Escape") {
            done.current = true;
            onCancel();
          }
        }}
        onBlur={(e) => {
          if (done.current) return;
          done.current = true;
          onSave(e.currentTarget.value);
        }}
      />
      <div className="mt-1 text-[11px] text-subtle">Enter để lưu · Esc để huỷ</div>
    </div>
  );
}
