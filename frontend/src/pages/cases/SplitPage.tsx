import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";

import { classifyCase, getSplit, pageImageURL, saveSplit } from "../../api/endpoints";
import type { SplitFile, SplitView } from "../../api/types";
import { PageImage } from "../../components/PageImage";
import { useToast } from "../../components/toast";
import { Empty, FileIcon, Icon, Loading, Spinner } from "../../components/ui";

// One document of the draft: a page range of a file, its type and bundle.
interface Doc {
  key: string;
  document_id: string;
  page_start: number;
  page_end: number;
  label: string;
  confidence: number;
  source: string;
  bundle: string;
}

let seq = 0;
const newKey = () => "s" + ++seq;
const pages = (a: number, b: number) => (a === b ? `tr. ${a}` : `tr. ${a}–${b}`);
const bundleNo = (b: string) => parseInt(b.slice(1), 10) || 0;
const bundleCode = (n: number) => "B" + String(n).padStart(2, "0");

function draftOf(files: SplitFile[]): Doc[] {
  return files.flatMap((f) =>
    f.segments.map((g) => ({
      key: newKey(),
      document_id: f.document_id,
      page_start: g.page_start,
      page_end: g.page_end,
      label: g.label,
      confidence: g.confidence,
      source: g.source,
      bundle: g.bundle || "B01",
    })),
  );
}

// Tách & gom trang (§7.9, U49): the AI's proposal — documents found by code
// cut hints and one title-only classification call per file, bundles grouped
// by code — for the user to fix and confirm. Only the confirmed split feeds
// the sheets.
export function SplitPage() {
  const { caseId = "" } = useParams();
  const nav = useNavigate();
  const { toast, fail } = useToast();
  const [view, setView] = useState<SplitView | null>(null);
  const [err, setErr] = useState("");
  const [draft, setDraft] = useState<Doc[]>([]);
  const [file, setFile] = useState("");
  const [sel, setSel] = useState<{ doc: string; page: number } | null>(null);
  const [running, setRunning] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [menu, setMenu] = useState(false);

  useEffect(() => {
    getSplit(caseId).then(
      (v) => {
        setView(v);
        setDraft(draftOf(v.files.filter((f) => f.indexed)));
        const first = v.files.find((f) => f.indexed && f.status !== "reviewed") ?? v.files.find((f) => f.indexed);
        if (first) {
          setFile(first.document_id);
          const low = [...first.segments].sort((a, b) => a.confidence - b.confidence)[0];
          setSel({ doc: first.document_id, page: low?.page_start ?? 1 });
        }
      },
      (e) => setErr((e as Error).message),
    );
  }, [caseId]);

  // While files are being classified, poll and take the new proposal of
  // those files only; edits of the other files stay.
  useEffect(() => {
    if (!running.length) return;
    const t = setInterval(async () => {
      try {
        const v = await getSplit(caseId);
        const done = v.files.filter((f) => running.includes(f.document_id) && f.classify_status !== "running");
        if (!done.length) return;
        const ids = new Set(done.map((f) => f.document_id));
        setView(v);
        setDraft((d) => [...d.filter((x) => !ids.has(x.document_id)), ...draftOf(done)]);
        setRunning((r) => r.filter((id) => !ids.has(id)));
        toast(`AI đã đề xuất lại cho ${done.map((f) => f.file_name).join(", ")}`);
      } catch (e) {
        fail(e);
        setRunning([]);
      }
    }, 2000);
    return () => clearInterval(t);
  }, [running, caseId, toast, fail]);

  const files = useMemo(() => view?.files.filter((f) => f.indexed) ?? [], [view]);
  const order = useMemo(() => new Map(files.map((f, i) => [f.document_id, i])), [files]);
  const titleOf = useCallback((l: string) => view?.labels.find((x) => x.name === l)?.title ?? (l === "other" ? "Khác" : l === "unknown" ? "Chưa phân loại" : l), [view]);
  const bundles = useMemo(() => [...new Set(draft.map((d) => d.bundle))].sort((a, b) => bundleNo(a) - bundleNo(b)), [draft]);
  const sorted = (list: Doc[]) => [...list].sort((a, b) => (order.get(a.document_id) ?? 0) - (order.get(b.document_id) ?? 0) || a.page_start - b.page_start);

  const update = (key: string, patch: Partial<Doc>) => setDraft((d) => d.map((x) => (x.key === key ? { ...x, ...patch, confidence: 1, source: "user" } : x)));
  const cut = (g: Doc, page: number) =>
    setDraft((d) => d.flatMap((x) => (x.key !== g.key ? [x] : [{ ...x, page_end: page - 1, source: "user", confidence: 1 }, { ...x, key: newKey(), page_start: page, source: "user", confidence: 1 }])));
  const merge = (g: Doc, prev: Doc) =>
    setDraft((d) => d.filter((x) => x.key !== g.key).map((x) => (x.key === prev.key ? { ...x, page_end: g.page_end, source: "user", confidence: 1 } : x)));
  const move = (g: Doc, to: string) => update(g.key, { bundle: to === "new" ? bundleCode(Math.max(0, ...draft.map((x) => bundleNo(x.bundle))) + 1) : to });

  const rerun = async (mode: "titles" | "pages") => {
    setMenu(false);
    if (!file) return;
    try {
      await classifyCase(caseId, { mode, document_ids: [file] });
      setRunning((r) => [...new Set([...r, file])]);
      toast(mode === "pages" ? "AI đang đọc đầu mỗi trang (tốn hơn)…" : "AI đang đề xuất lại: 1 lần gọi, chỉ đọc tiêu đề và dấu điểm cắt…");
    } catch (e) {
      fail(e);
    }
  };

  const confirm = async () => {
    const unknown = draft.find((d) => d.label === "unknown");
    if (unknown) {
      setSel({ doc: unknown.document_id, page: unknown.page_start });
      return fail(new Error("Còn giấy tờ chưa phân loại: chọn loại (hoặc Khác) trước khi xác nhận"));
    }
    setSaving(true);
    try {
      const body = bundles.map((b) => ({
        segments: sorted(draft.filter((d) => d.bundle === b)).map((d) => ({ document_id: d.document_id, page_start: d.page_start, page_end: d.page_end, label: d.label })),
      }));
      await saveSplit(caseId, body);
      toast(`Đã xác nhận: ${draft.filter((d) => d.label !== "other").length} giấy tờ trong ${bundles.length} bộ`);
      nav(`/cases/${caseId}`);
    } catch (e) {
      fail(e);
    } finally {
      setSaving(false);
    }
  };

  if (err)
    return (
      <Empty icon="error" title="Không mở được màn tách & gom">
        {err}
      </Empty>
    );
  if (!view) return <Loading />;
  if (!view.labels.length)
    return (
      <Empty icon="content_cut" title="Loại hồ sơ này không phân loại giấy tờ">
        Loại case chưa khai báo classification.labels, nên không có tách & gom trang.
      </Empty>
    );

  const fileOf = (id: string) => files.find((f) => f.document_id === id);
  const selFile = sel ? fileOf(sel.doc) : undefined;
  const selMark = sel && selFile ? selFile.marks[String(sel.page)] : undefined;
  const nDocs = draft.filter((d) => d.label !== "other").length;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="flex min-h-16 flex-none flex-wrap items-center gap-2 px-4 pt-3 pb-2">
        <button className="btn btn-text" onClick={() => nav(`/cases/${caseId}`)} title="Quay lại phương án">
          <Icon name="arrow_back" size={20} /> Phương án
        </button>
        <span className="h-6 border-l border-line max-sm:hidden" />
        <div className="min-w-0 flex-1 px-2">
          <h1 className="flex items-center gap-2 truncate text-[22px] leading-7 font-normal text-fg">
            <Icon name="content_cut" size={24} className="text-accent" /> Tách & gom trang
          </h1>
          <div className="truncate text-xs text-muted">
            {files.length} file · {nDocs} giấy tờ · {bundles.length} bộ
            {view.opens_with.length ? ` · ${view.opens_with.map(titleOf).join(", ")} mở bộ mới, giấy tờ sau nối vào bộ đang mở (sửa được)` : ""}
          </div>
        </div>
        <div className="relative">
          <button className="btn" onClick={() => setMenu((m) => !m)} disabled={!file || running.includes(file)}>
            {running.includes(file) ? <Spinner className="size-4 border-2" /> : <Icon name="auto_awesome" size={18} />} AI đề xuất lại
            <Icon name="arrow_drop_down" size={18} className="-mr-2" />
          </button>
          {menu && (
            <div className="absolute right-0 z-10 mt-1 w-72 rounded-lg bg-surface py-2 shadow-2">
              <button className="menu-item" onClick={() => rerun("titles")}>
                <Icon name="title" size={18} /> Chỉ đọc tiêu đề (mặc định)
              </button>
              <button className="menu-item" onClick={() => rerun("pages")}>
                <Icon name="article" size={18} /> Đọc cả đầu mỗi trang (tốn hơn)
              </button>
            </div>
          )}
        </div>
        <button className="btn btn-primary" onClick={confirm} disabled={saving || !draft.length || running.length > 0}>
          <Icon name="check" size={18} /> Xác nhận tách & gom
        </button>
      </header>

      <div className="flex min-h-0 flex-1 border-t border-line">
        <aside className="hidden w-60 flex-none flex-col gap-1 overflow-auto border-r border-line p-2 lg:flex">
          <div className="px-2 pb-1 text-xs font-medium text-muted">File</div>
          {files.map((f) => (
            <button
              key={f.document_id}
              onClick={() => {
                setFile(f.document_id);
                document.getElementById("doc-" + f.document_id)?.scrollIntoView({ block: "start", behavior: "smooth" });
              }}
              className={"flex flex-col gap-1 rounded-xl px-3 py-2 text-left " + (file === f.document_id ? "bg-accent-soft text-on-accent-soft" : "hover:bg-fg/5")}
            >
              <span className="flex items-center gap-2 text-sm">
                <FileIcon name={f.file_name} size={16} />
                <span className="min-w-0 flex-1 truncate">{f.file_name}</span>
              </span>
              <span className="flex items-center gap-2 text-xs">
                {running.includes(f.document_id) ? (
                  <span className="badge">
                    <Spinner className="size-3 border-[1.5px]" /> AI đang đọc
                  </span>
                ) : f.status === "reviewed" ? (
                  <span className="badge badge-ok">đã duyệt</span>
                ) : f.status === "proposed" ? (
                  <span className="badge badge-warn">AI đề xuất</span>
                ) : (
                  <span className="badge">chưa tách</span>
                )}
                <span className="text-muted">{f.page_count} trang</span>
              </span>
            </button>
          ))}
          <div className="mt-3 rounded-xl bg-surface-2 p-3 text-xs text-muted">
            <div className="mb-1 font-medium text-fg">Cách AI đề xuất</div>
            Code đánh dấu chỗ "Trang 1/n", trang trắng, đổi khổ giấy (<Icon name="content_cut" size={12} className="align-[-2px] text-warn" />
            ). Một lần gọi LLM mỗi file chỉ đọc tiêu đề trang để gán loại. Gom bộ bằng quy tắc của loại hồ sơ, không gọi LLM.
          </div>
        </aside>

        <div className="min-h-0 min-w-0 flex-1 overflow-auto p-4">
          <div className="mx-auto flex max-w-230 flex-col gap-5">
            {!draft.length && <div className="py-10 text-center text-sm text-subtle">Hồ sơ chưa có file nào đã dựng mục lục.</div>}
            {bundles.map((b) => {
              const list = sorted(draft.filter((d) => d.bundle === b));
              return (
                <section key={b}>
                  <div className="mb-2 flex items-center gap-2">
                    <span className="rounded-lg bg-surface-3 px-2.5 py-1 text-sm font-medium text-fg">Bộ {b}</span>
                    <span className="truncate text-xs text-muted">
                      {list
                        .filter((d) => d.label !== "other")
                        .map((d) => titleOf(d.label))
                        .join(" · ")}
                    </span>
                  </div>
                  <div className="flex flex-col gap-2">
                    {list.map((g) => {
                      const f = fileOf(g.document_id);
                      const prev = draft.find((x) => x.document_id === g.document_id && x.page_end === g.page_start - 1);
                      const first = sorted(draft).find((x) => x.document_id === g.document_id)?.key === g.key;
                      return (
                        <DocRow
                          key={g.key}
                          id={first ? "doc-" + g.document_id : undefined}
                          g={g}
                          file={f}
                          labels={view.labels}
                          bundles={bundles}
                          sel={sel}
                          onSelect={(page) => (setSel({ doc: g.document_id, page }), setFile(g.document_id))}
                          onLabel={(l) => update(g.key, { label: l })}
                          onMove={(to) => move(g, to)}
                          onCut={(p) => cut(g, p)}
                          onMerge={prev ? () => merge(g, prev) : undefined}
                        />
                      );
                    })}
                  </div>
                </section>
              );
            })}
          </div>
        </div>

        <aside className="hidden w-80 flex-none flex-col gap-3 overflow-auto border-l border-line p-3 xl:flex">
          {sel && selFile ? (
            <>
              <div className="truncate text-sm font-medium text-fg">
                {selFile.file_name} · trang {sel.page}
              </div>
              <div className="overflow-hidden rounded-xl border border-line bg-surface-2">
                <PageImage docId={sel.doc} pageNo={sel.page} />
              </div>
              {selMark && (
                <div className="flex gap-2 rounded-lg bg-warn-soft p-2 text-xs text-warn">
                  <Icon name="content_cut" size={16} /> Dấu điểm cắt: {selMark}
                </div>
              )}
            </>
          ) : (
            <div className="py-6 text-center text-sm text-subtle">Chọn một trang để xem lớn.</div>
          )}
        </aside>
      </div>
    </div>
  );
}

function DocRow({
  id,
  g,
  file,
  labels,
  bundles,
  sel,
  onSelect,
  onLabel,
  onMove,
  onCut,
  onMerge,
}: {
  id?: string;
  g: Doc;
  file?: SplitFile;
  labels: { name: string; title: string }[];
  bundles: string[];
  sel: { doc: string; page: number } | null;
  onSelect: (page: number) => void;
  onLabel: (l: string) => void;
  onMove: (to: string) => void;
  onCut: (page: number) => void;
  onMerge?: () => void;
}) {
  const low = g.source === "pipeline" && (g.label === "unknown" || g.confidence < 0.75);
  const thumbs: number[] = [];
  for (let p = g.page_start; p <= g.page_end; p++) thumbs.push(p);
  return (
    <div id={id} className={"flex gap-3 rounded-xl border p-2.5 max-sm:flex-col " + (low ? "border-[#f9ab00]" : "border-line")}>
      <div className="flex w-52 flex-none flex-col gap-1.5">
        <select className="input h-8 px-2 text-[13px]" value={g.label} onChange={(e) => onLabel(e.target.value)} aria-label="Loại giấy tờ">
          {g.label === "unknown" && <option value="unknown">Chưa phân loại…</option>}
          {labels.map((l) => (
            <option key={l.name} value={l.name}>
              {l.title}
            </option>
          ))}
          <option value="other">Khác</option>
        </select>
        <div className="flex items-center gap-1 text-xs text-muted">
          <FileIcon name={file?.file_name} size={14} />
          <span className="min-w-0 truncate" title={file?.file_name}>
            {file?.file_name}
          </span>
          <span className="whitespace-nowrap">· {pages(g.page_start, g.page_end)}</span>
        </div>
        <div className="flex flex-wrap items-center gap-1">
          {g.source === "user" ? (
            <span className="badge">đã chỉnh</span>
          ) : low ? (
            <span className="badge badge-warn">AI {Math.round(g.confidence * 100)}% · cần xem</span>
          ) : (
            <span className="badge badge-info">AI {Math.round(g.confidence * 100)}%</span>
          )}
          <select className="input h-7 px-1.5 text-xs" value={g.bundle} onChange={(e) => onMove(e.target.value)} aria-label="Chuyển sang bộ">
            {bundles.map((b) => (
              <option key={b} value={b}>
                Bộ {b}
              </option>
            ))}
            <option value="new">+ Bộ mới</option>
          </select>
          {onMerge && (
            <button className="btn btn-text btn-sm h-7 px-2 text-xs" onClick={onMerge} title="Gộp với giấy tờ ngay trước trong cùng file">
              <Icon name="merge" size={16} /> Gộp lên
            </button>
          )}
        </div>
      </div>
      <div className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto pb-1">
        {thumbs.map((p) => (
          <span key={p} className="flex flex-none items-center gap-1">
            {p > g.page_start && (
              <button
                className={"grid h-19 w-4.5 place-items-center rounded-md hover:bg-fg/8 " + (file?.marks[String(p)] ? "text-warn" : "text-subtle opacity-40 hover:opacity-100")}
                onClick={() => onCut(p)}
                title={"Tách tại đây" + (file?.marks[String(p)] ? " · dấu điểm cắt: " + file.marks[String(p)] : "")}
              >
                <Icon name="content_cut" size={16} />
              </button>
            )}
            <Thumb docId={g.document_id} page={p} on={sel?.doc === g.document_id && sel.page === p} onClick={() => onSelect(p)} />
          </span>
        ))}
      </div>
    </div>
  );
}

// A page thumbnail, loaded when it scrolls into view.
function Thumb({ docId, page, on, onClick }: { docId: string; page: number; on: boolean; onClick: () => void }) {
  const ref = useRef<HTMLButtonElement>(null);
  const [visible, setVisible] = useState(false);
  const [src, setSrc] = useState<string | null>(null);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const io = new IntersectionObserver((es) => es.some((e) => e.isIntersecting) && (setVisible(true), io.disconnect()), { rootMargin: "200px" });
    io.observe(el);
    return () => io.disconnect();
  }, []);
  useEffect(() => {
    if (!visible) return;
    let alive = true;
    pageImageURL(docId, page).then(
      (u) => alive && setSrc(u),
      () => {},
    );
    return () => {
      alive = false;
    };
  }, [visible, docId, page]);
  return (
    <button
      ref={ref}
      onClick={onClick}
      title={`Trang ${page}`}
      className={"relative h-19 w-14.5 flex-none overflow-hidden rounded-md border border-line bg-surface " + (on ? "outline-2 outline-offset-1 outline-accent" : "")}
    >
      {src && <img src={src} alt="" className="size-full object-cover object-top" />}
      <span className="absolute right-1 bottom-0.5 rounded bg-surface/80 px-0.5 text-[10px] text-subtle">{page}</span>
    </button>
  );
}
