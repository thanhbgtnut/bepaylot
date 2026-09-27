import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";

import { getCaseTOC, getTree } from "../../api/endpoints";
import type { CaseTOC, TOCDoc, TreeNode } from "../../api/types";
import { useApp } from "../../components/AppContext";
import { CasePicker } from "../../components/CasePicker";
import { NeedKB, TopBar } from "../../components/Layout";
import { PageImage } from "../../components/PageImage";
import { Empty, FileIcon, Icon, Loading, MetaChips, Spinner, StatusBadge } from "../../components/ui";

// One case (hồ sơ) read through its tables of contents (§7): the case lists
// its files, each file opens into its tree (title, page range, summary), and
// a node shows its pages. Everything comes from the stored cards and trees;
// nothing here calls the LLM.

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

// Tree helpers: children by parent, in order, and the path to a node.
function childrenOf(nodes: TreeNode[]) {
  const m = new Map<string, TreeNode[]>();
  for (const n of nodes) if (n.parent_id) m.set(n.parent_id, [...(m.get(n.parent_id) ?? []), n]);
  for (const list of m.values()) list.sort((a, b) => a.ord - b.ord);
  return m;
}
function pathTo(nodes: TreeNode[], id: string) {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const out: TreeNode[] = [];
  for (let n = byId.get(id); n; n = n.parent_id ? byId.get(n.parent_id) : undefined) out.unshift(n);
  return out;
}

export function CasePage() {
  const { caseId } = useParams();
  const [sp, setSp] = useSearchParams();
  const docId = sp.get("doc");
  const nodeId = sp.get("node"); // short id (n<k>); none = the file itself
  const nav = useNavigate();
  const { kb, kcase, cases, selectCase } = useApp();
  const [toc, setToc] = useState<CaseTOC | null>(null);
  const [err, setErr] = useState("");
  const [reload, setReload] = useState(0);

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

  // While files are still being parsed or indexed, poll the TOC.
  const busy = !!toc?.pending?.length;
  useEffect(() => {
    if (!busy) return;
    const t = setInterval(() => setReload((r) => r + 1), 5000);
    return () => clearInterval(t);
  }, [busy]);

  // Open the first file when none is selected.
  useEffect(() => {
    if (!docId && toc?.documents.length) setSp({ doc: toc.documents[0].document_id }, { replace: true });
  }, [docId, toc, setSp]);

  const select = useCallback((doc: string, node?: string) => setSp(node ? { doc, node } : { doc }), [setSp]);

  if (!kb)
    return (
      <>
        <TopBar title="Hồ sơ" />
        <NeedKB />
      </>
    );

  const c = toc?.case ?? kcase;
  const doc = toc?.documents.find((d) => d.document_id === docId) ?? null;

  return (
    <>
      <TopBar
        title="Hồ sơ"
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
        <CasePicker value={c} onChange={(x) => nav(casePath(x.id))} />
        {c && (
          <Link className="btn btn-tonal" to="/chat" onClick={() => selectCase(c.id)} title="Mở phiên hỏi đáp gắn với hồ sơ này">
            <Icon name="forum" size={18} /> Hỏi về hồ sơ
          </Link>
        )}
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
      ) : !toc || !caseId ? (
        <Loading />
      ) : !toc.documents.length && !toc.pending?.length ? (
        <Empty icon="draft" title="Hồ sơ chưa có file">
          <Link className="btn btn-primary mt-5" to="/documents?upload=1">
            <Icon name="upload_file" size={18} /> Tải file lên
          </Link>
        </Empty>
      ) : (
        <div className="flex min-h-0 flex-1 border-t border-line">
          <Sidebar toc={toc} docId={docId} nodeId={nodeId} onSelect={select} />
          <div className="min-w-0 flex-1 overflow-auto">
            {doc ? <NodeView key={doc.document_id} doc={doc} nodeId={nodeId} onSelect={select} /> : <Empty icon="account_tree">Chọn một file ở cột trái.</Empty>}
          </div>
        </div>
      )}
    </>
  );
}

// ---------------------------------------------------------------- sidebar

const rowCls = (active: boolean) =>
  "flex min-h-8 w-full items-center gap-2 rounded-full py-1 pr-3 text-left text-sm " + (active ? "bg-accent-soft font-medium text-on-accent-soft" : "text-fg hover:bg-fg/8");

function Sidebar({ toc, docId, nodeId, onSelect }: { toc: CaseTOC; docId: string | null; nodeId: string | null; onSelect: (doc: string, node?: string) => void }) {
  return (
    <aside className="hidden w-80 flex-none flex-col overflow-auto border-r border-line px-2 py-3 md:flex">
      <div className="px-3 pb-1 text-xs font-medium text-muted">Mục lục hồ sơ</div>
      {toc.documents.map((d) => (
        <FileEntry key={d.document_id} d={d} open={d.document_id === docId} nodeId={d.document_id === docId ? nodeId : null} onSelect={onSelect} />
      ))}
      {!!toc.pending?.length && (
        <>
          <div className="mt-3 px-3 pb-1 text-xs font-medium text-muted">Đang xử lý</div>
          {toc.pending.map((p) => (
            <Link key={p.document_id} to={`/documents/${p.document_id}`} className="flex h-8 items-center gap-2 rounded-full px-3 text-sm text-muted hover:bg-fg/8">
              <FileIcon name={p.file_name} size={18} />
              <span className="min-w-0 flex-1 truncate">{p.file_name}</span>
              {p.status === "failed" ? <StatusBadge status={p.status} /> : <Spinner className="size-3.5 border-2" />}
            </Link>
          ))}
        </>
      )}
    </aside>
  );
}

function FileEntry({ d, open, nodeId, onSelect }: { d: TOCDoc; open: boolean; nodeId: string | null; onSelect: (doc: string, node?: string) => void }) {
  const [expanded, setExpanded] = useState(open);
  useEffect(() => {
    if (open) setExpanded(true);
  }, [open]);
  const { nodes } = useTree(expanded ? d.document_id : null);
  const kids = useMemo(() => (nodes ? childrenOf(nodes) : null), [nodes]);
  const root = nodes?.find((n) => !n.parent_id);

  return (
    <div className="mb-px">
      <div className={rowCls(open && !nodeId)}>
        <button className="btn-icon btn-sm -my-1 ml-0.5 flex-none" onClick={() => setExpanded((v) => !v)} aria-label={expanded ? "Thu gọn" : "Mở mục lục"}>
          <Icon name="arrow_right" size={20} className={"transition-transform " + (expanded ? "rotate-90" : "")} />
        </button>
        <button className="flex min-w-0 flex-1 items-center gap-2 text-left" onClick={() => (onSelect(d.document_id), setExpanded(true))} title={d.summary || d.file_name}>
          <FileIcon name={d.file_name} size={18} />
          <span className="min-w-0 flex-1 truncate">{d.file_name}</span>
          <span className="text-xs whitespace-nowrap text-muted">{d.page_count} tr.</span>
        </button>
      </div>
      {expanded &&
        (!nodes ? (
          <div className="py-1 pl-12">
            <Spinner className="size-3.5 border-2" />
          </div>
        ) : root && kids ? (
          <NodeList parent={root.id} kids={kids} depth={1} docId={d.document_id} nodeId={nodeId} onSelect={onSelect} />
        ) : null)}
    </div>
  );
}

function NodeList({
  parent,
  kids,
  depth,
  docId,
  nodeId,
  onSelect,
}: {
  parent: string;
  kids: Map<string, TreeNode[]>;
  depth: number;
  docId: string;
  nodeId: string | null;
  onSelect: (doc: string, node?: string) => void;
}) {
  return (
    <>
      {(kids.get(parent) ?? []).map((n) => (
        <div key={n.id}>
          <button
            onClick={() => onSelect(docId, n.short_id)}
            title={n.summary}
            className={rowCls(n.short_id === nodeId)}
            style={{ paddingLeft: 12 + depth * 16 }}
          >
            <span className="min-w-0 flex-1 truncate">{n.title || n.short_id}</span>
            <span className="text-xs whitespace-nowrap text-muted">{pages(n.page_start, n.page_end)}</span>
          </button>
          <NodeList parent={n.id} kids={kids} depth={depth + 1} docId={docId} nodeId={nodeId} onSelect={onSelect} />
        </div>
      ))}
    </>
  );
}

// ---------------------------------------------------------------- node view

// A file (its card) or one node of its tree: summary, sub-sections and the
// pages of the node, one page image at a time.
function NodeView({ doc, nodeId, onSelect }: { doc: TOCDoc; nodeId: string | null; onSelect: (doc: string, node?: string) => void }) {
  const { nodes, err } = useTree(doc.document_id);
  const kids = useMemo(() => (nodes ? childrenOf(nodes) : null), [nodes]);
  const node = nodes?.find((n) => (nodeId ? n.short_id === nodeId : !n.parent_id)) ?? null;
  const path = useMemo(() => (nodes && node ? pathTo(nodes, node.id).slice(1) : []), [nodes, node]);
  const from = node?.page_start ?? 1;
  const to = node?.page_end ?? doc.page_count;
  const [page, setPage] = useState(from);
  useEffect(() => setPage(from), [from, nodeId]);

  if (err) return <Empty icon="error">{err}</Empty>;
  if (!nodes || !kids) return <Loading />;
  const isFile = !node?.parent_id;
  const children = node ? (kids.get(node.id) ?? []) : [];

  return (
    <div className="mx-auto flex max-w-240 flex-col gap-5 px-6 py-5">
      <div>
        <div className="flex flex-wrap items-center gap-1 text-xs text-muted">
          <button className="hover:text-fg hover:underline" onClick={() => onSelect(doc.document_id)}>
            {doc.file_name}
          </button>
          {path.map((p) => (
            <span key={p.id} className="flex items-center gap-1">
              <Icon name="chevron_right" size={16} />
              <button className="hover:text-fg hover:underline" onClick={() => onSelect(doc.document_id, p.short_id)}>
                {p.title || p.short_id}
              </button>
            </span>
          ))}
        </div>
        <h2 className="mt-1 text-[22px] leading-8 text-fg">{isFile ? doc.title || doc.file_name : node?.title || node?.short_id}</h2>
        <div className="mt-1 flex flex-wrap items-center gap-2 text-sm text-muted">
          <span>{pages(from, to)}</span>
          {isFile && <StatusBadge status={doc.status} />}
          {isFile && <MetaChips meta={doc.metadata} />}
          <Link className="ml-auto flex items-center gap-1 text-accent hover:underline" to={`/documents/${doc.document_id}?page=${page}`}>
            <Icon name="open_in_new" size={16} /> Mở trong trình xem tài liệu
          </Link>
        </div>
        {(isFile ? doc.summary : node?.summary) && <p className="mt-3 text-[15px] leading-6 text-fg">{isFile ? doc.summary : node?.summary}</p>}
      </div>

      {children.length > 0 && (
        <section>
          <h3 className="mb-2 text-sm font-medium text-muted">Mục con</h3>
          <div className="grid gap-2 sm:grid-cols-2">
            {children.map((n) => (
              <button key={n.id} onClick={() => onSelect(doc.document_id, n.short_id)} className="rounded-xl border border-line px-4 py-3 text-left hover:bg-fg/5">
                <div className="flex items-baseline gap-2">
                  <span className="min-w-0 flex-1 truncate text-sm font-medium text-fg">{n.title || n.short_id}</span>
                  <span className="text-xs whitespace-nowrap text-muted">{pages(n.page_start, n.page_end)}</span>
                </div>
                {n.summary && <div className="mt-1 line-clamp-2 text-xs text-muted">{n.summary}</div>}
              </button>
            ))}
          </div>
        </section>
      )}

      <section>
        <div className="mb-2 flex items-center gap-1">
          <h3 className="flex-1 text-sm font-medium text-muted">
            Trang {page} <span className="text-subtle">({pages(from, to)})</span>
          </h3>
          <button className="btn-icon btn-sm" disabled={page <= from} onClick={() => setPage((p) => p - 1)} aria-label="Trang trước">
            <Icon name="chevron_left" />
          </button>
          <button className="btn-icon btn-sm" disabled={page >= to} onClick={() => setPage((p) => p + 1)} aria-label="Trang sau">
            <Icon name="chevron_right" />
          </button>
        </div>
        <div className="overflow-hidden rounded-xl border border-line bg-surface-2">
          <PageImage docId={doc.document_id} pageNo={page} />
        </div>
      </section>
    </div>
  );
}
