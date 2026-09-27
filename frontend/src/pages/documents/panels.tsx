import { useEffect, useRef, useState } from "react";

import { getTree, searchInDocument } from "../../api/endpoints";
import type { Document, PageBlock, PageLine, PageSearchHit, TreeNode } from "../../api/types";
import { Badge, Empty, Loading, SourceBadge } from "../../components/ui";
import { splitMatch } from "../../lib/format";

// ---------------------------------------------------------------- lines
export function LinesPanel({
  lines,
  highlighted,
  hover,
  onHover,
  onPick,
  scrollTarget,
}: {
  lines: PageLine[];
  highlighted: Set<number>;
  hover: number | null;
  onHover: (n: number | null) => void;
  onPick: (n: number) => void;
  scrollTarget?: number | null;
}) {
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (scrollTarget == null) return;
    const el = box.current?.querySelector(`[data-no="${scrollTarget}"]`);
    if (el) setTimeout(() => el.scrollIntoView({ block: "center", behavior: "smooth" }), 60);
  }, [scrollTarget, lines]);
  if (!lines.length) return <Empty>Trang không có dòng chữ</Empty>;
  return (
    <div ref={box} onMouseLeave={() => onHover(null)}>
      {lines.map((l) => {
        const on = highlighted.has(l.line_no) || hover === l.line_no;
        return (
          <div
            key={l.line_no}
            data-no={l.line_no}
            onMouseEnter={() => onHover(l.line_no)}
            onClick={() => onPick(l.line_no)}
            className={"grid cursor-pointer grid-cols-[40px_1fr] gap-2 border-b border-line px-5 py-2 " + (on ? "bg-warn-soft" : "")}
          >
            <div className="pt-0.5 font-mono text-[11.5px] text-muted">L{l.line_no}</div>
            <div className="min-w-0">
              <div className={"wrap-break-word " + (l.low_confidence ? "underline decoration-warn decoration-wavy underline-offset-4" : "")}>{l.text}</div>
              <div className="mt-0.5 flex flex-wrap items-center gap-1.5 text-xs">
                <SourceBadge source={l.text_source} />
                <span className="font-mono text-muted" title="bbox (px)">
                  [{[l.bbox.x0, l.bbox.y0, l.bbox.x1, l.bbox.y1].map((v) => Math.round(v)).join(", ")}]
                </span>
                {l.confidence > 0 && l.confidence < 1 && <span className="text-muted">conf {(l.confidence * 100).toFixed(0)}%</span>}
              </div>
              {l.text_ocr && l.text_ocr !== l.text && <div className="text-xs text-muted">OCR gốc: {l.text_ocr}</div>}
            </div>
          </div>
        );
      })}
    </div>
  );
}

// ---------------------------------------------------------------- blocks
export function BlocksPanel({ blocks }: { blocks: PageBlock[] }) {
  if (!blocks.length) return <Empty>Không có block</Empty>;
  return (
    <div>
      {blocks.map((b) => (
        <div key={b.block_no} className="border-b border-line px-3 py-2">
          <div className="flex flex-wrap items-center gap-1.5">
            <b className="text-xs">#{b.block_no}</b>
            <span className="tag">{b.type}</span>
            {b.raw_class && <span className="tag font-mono">{b.raw_class}</span>}
            <SourceBadge source={b.text_source} />
            {b.is_furniture && <Badge tone="warn">header/footer</Badge>}
          </div>
          <div className="mt-1 text-xs whitespace-pre-wrap">{(b.text || "").slice(0, 600)}</div>
        </div>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------- find in document
// State survives tab switches within one document.
const findCache = new Map<string, { q: string; from: string; to: string; mode: "keyword" | "reasoning"; result: PageSearchHit[] | null }>();

export function FindPanel({ doc, onPick }: { doc: Document; onPick: (page: number, line: number) => void }) {
  const init = findCache.get(doc.id) ?? { q: "", from: "", to: "", mode: "keyword" as const, result: null };
  const [q, setQ] = useState(init.q);
  const [from, setFrom] = useState(init.from);
  const [to, setTo] = useState(init.to);
  const [mode, setMode] = useState<"keyword" | "reasoning">(init.mode);
  const [result, setResult] = useState<PageSearchHit[] | null>(init.result);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  useEffect(() => {
    findCache.set(doc.id, { q, from, to, mode, result });
  }, [doc.id, q, from, to, mode, result]);

  const run = async () => {
    if (!q.trim()) return;
    setBusy(true);
    setErr("");
    try {
      setResult(
        await searchInDocument(doc.id, {
          query: q.trim(),
          mode,
          page_from: from ? +from : undefined,
          page_to: to ? +to : undefined,
        }),
      );
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const total = result?.reduce((n, p) => n + p.hits.length, 0) ?? 0;
  return (
    <div>
      <form
        className="flex flex-col gap-2 px-5 py-4"
        onSubmit={(e) => {
          e.preventDefault();
          run();
        }}
      >
        <input className="input" type="search" placeholder="Từ khoá, số hiệu, số tiền…" value={q} onChange={(e) => setQ(e.target.value)} />
        <div className="flex flex-wrap items-center gap-2">
          <input className="input w-24" type="number" min={1} max={doc.page_count} placeholder="Từ trang" value={from} onChange={(e) => setFrom(e.target.value)} />
          <input className="input w-24" type="number" min={1} max={doc.page_count} placeholder="Đến trang" value={to} onChange={(e) => setTo(e.target.value)} />
          <select className="input" value={mode} onChange={(e) => setMode(e.target.value as "keyword" | "reasoning")}>
            <option value="keyword">Keyword</option>
            <option value="reasoning">Reasoning (LLM)</option>
          </select>
          <button className="btn btn-primary" disabled={busy}>
            {busy ? "Đang tìm…" : "Tìm"}
          </button>
        </div>
        <div className="text-xs text-muted">Để trống trang = tìm cả file.</div>
      </form>
      {err && <Empty>{err}</Empty>}
      {busy && <Loading />}
      {!busy && result && !result.length && <Empty>Không thấy “{q}”</Empty>}
      {!busy && result && result.length > 0 && (
        <>
          <div className="px-5 pb-1.5 text-xs text-muted">
            {total} kết quả trên {result.length} trang
          </div>
          {result.map((p) => (
            <div key={p.page_no} className="border-b border-line px-5 py-2.5">
              <div className="flex items-center gap-2">
                <b>Trang {p.page_no}</b>
                <span className="text-xs text-muted">{p.hits.length} dòng</span>
              </div>
              {p.hits.map((l) => {
                const parts = splitMatch(l.snippet, q);
                return (
                  <button
                    key={l.line_no}
                    className="block w-full rounded-lg px-2 py-1.5 text-left text-[13px] hover:bg-fg/8"
                    onClick={() => onPick(p.page_no, l.line_no)}
                  >
                    <span className="mr-1 font-mono text-xs text-muted">L{l.line_no}</span>
                    {parts ? (
                      <>
                        {parts[0]}
                        <mark className="rounded bg-warn-soft px-0.5 text-fg">{parts[1]}</mark>
                        {parts[2]}
                      </>
                    ) : (
                      l.snippet
                    )}
                  </button>
                );
              })}
            </div>
          ))}
        </>
      )}
    </div>
  );
}

// ---------------------------------------------------------------- tree
const treeCache = new Map<string, TreeNode[]>();

export function TreePanel({ doc, current, onPick }: { doc: Document; current?: number; onPick: (page: number) => void }) {
  const [nodes, setNodes] = useState<TreeNode[] | null>(treeCache.get(doc.id) ?? null);
  const [err, setErr] = useState("");
  useEffect(() => {
    if (treeCache.has(doc.id)) return;
    getTree(doc.id).then(
      (n) => {
        treeCache.set(doc.id, n);
        setNodes(n);
      },
      (e) => setErr((e as Error).message),
    );
  }, [doc.id]);
  if (err) return <Empty>{err}</Empty>;
  if (!nodes) return <Loading />;
  if (!nodes.length) return <Empty>Chưa có mục lục (tài liệu đang lập chỉ mục?)</Empty>;
  return (
    <div className="py-1.5">
      {nodes.map((n) => {
        // The deepest node containing the current page is highlighted.
        const on = current != null && current >= n.page_start && current <= n.page_end && !nodes.some((c) => c.parent_id === n.id && current >= c.page_start && current <= c.page_end);
        return (
        <button
          key={n.id}
          onClick={() => onPick(n.page_start)}
          title={n.summary}
          className={"mx-2 flex w-[calc(100%-16px)] items-center gap-2 rounded-full py-1.5 pr-3 text-left text-sm " + (on ? "bg-accent-soft text-on-accent-soft" : "hover:bg-fg/8")}
          style={{ paddingLeft: 12 + n.level * 14 }}
        >
          <span className={"min-w-0 flex-1 truncate " + (n.level === 0 ? "font-medium" : "")}>{n.title || (n.level === 0 ? doc.file_name : n.short_id)}</span>
          <span className="text-xs whitespace-nowrap text-muted">
            {n.page_start === n.page_end ? `tr.${n.page_start}` : `tr.${n.page_start}–${n.page_end}`}
          </span>
        </button>
        );
      })}
    </div>
  );
}
