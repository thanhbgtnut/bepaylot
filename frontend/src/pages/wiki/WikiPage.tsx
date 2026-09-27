import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { Link, useLocation, useNavigate, useParams, useSearchParams } from "react-router-dom";

import {
  decideWikiProposal,
  deleteWikiNote,
  editWikiPage,
  exportWiki,
  getWiki,
  getWikiPage,
  rebuildWiki,
  restoreWikiRevision,
  runWikiLint,
  searchWiki,
  setLintStatus,
  wikiLint,
  wikiLog,
  wikiRevisions,
} from "../../api/endpoints";
import type { Case, WikiAttribute, WikiFootnote, WikiLink, WikiLintIssue, WikiLogEntry, WikiPage as Page, WikiPageRef, WikiRevision, WikiTOC } from "../../api/types";
import { useApp } from "../../components/AppContext";
import { CasePicker, caseLabel, WIKI_STATUS } from "../../components/CasePicker";
import { useCitation } from "../../components/Citation";
import { NeedKB, TopBar } from "../../components/Layout";
import { Markdown } from "../../components/Markdown";
import { useToast } from "../../components/toast";
import { Badge, Empty, FileIcon, Icon, Loading, Menu, Modal, Spinner } from "../../components/ui";
import { fmtDate, fold, metaValue } from "../../lib/format";

// The wiki of one case (hồ sơ), compiled from the case's files (the LLM only
// extracts entities; pages are written by code):
// an overview, one source page per file, entity and topic pages, and notes
// saved from the chat. Every fact carries a footnote [^n] that opens the
// exact lines of the source page.

const KIND: Record<string, [string, string]> = {
  overview: ["Tổng quan", "home"],
  source: ["Tài liệu", "description"],
  entity: ["Thực thể", "person"],
  topic: ["Chủ đề", "topic"],
  note: ["Ghi chú", "sticky_note_2"],
};
const KIND_ORDER = ["overview", "source", "entity", "topic", "note"];

const LINT: Record<string, string> = {
  stale: "Chú thích lỗi thời",
  contradiction: "Nguồn mâu thuẫn",
  orphan: "Trang không có liên kết đến",
  missing_link: "Thiếu liên kết",
  gap: "Thiếu thông tin",
  index_drift: "Mục lục lệch",
};
const OP: Record<string, string> = {
  ingest: "Nạp file",
  retract: "Gỡ file",
  lint: "Kiểm tra",
  edit: "Sửa tay",
  note: "Ghi chú",
  rebuild: "Dựng lại",
  query: "Truy vấn",
};

const wikiPath = (caseId: string, slug?: string) => `/wiki/${caseId}` + (slug ? "/" + slug.split("/").map(encodeURIComponent).join("/") : "");

export function WikiPage() {
  const params = useParams();
  const caseId = params.caseId;
  const slug = params["*"] ?? "";
  const [sp, setSp] = useSearchParams();
  const view = sp.get("view") as "log" | "issues" | null;
  const nav = useNavigate();
  const loc = useLocation();
  const { kb, kcase, cases, selectCase, reloadCases } = useApp();
  const [toc, setToc] = useState<WikiTOC | null>(null);
  const [err, setErr] = useState("");
  const [reload, setReload] = useState(0);
  const refresh = useCallback(() => setReload((r) => r + 1), []);

  // The URL names the case; without one, open the case picked last.
  useEffect(() => {
    if (!caseId && kcase) nav(wikiPath(kcase.id), { replace: true });
  }, [caseId, kcase, nav]);
  useEffect(() => {
    if (caseId && cases?.some((c) => c.id === caseId) && kcase?.id !== caseId) selectCase(caseId);
  }, [caseId, cases]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!caseId) return;
    let alive = true;
    setErr("");
    getWiki(caseId).then(
      (t) => alive && setToc(t),
      (e) => alive && setErr((e as Error).message),
    );
    return () => {
      alive = false;
    };
  }, [caseId, reload]);
  useEffect(() => setToc(null), [caseId]);

  // While files are being folded in, poll the table of contents.
  const busy = !!toc && (toc.case.wiki_status === "building" || !!toc.pending?.length);
  useEffect(() => {
    if (!busy) return;
    const t = setInterval(refresh, 5000);
    return () => clearInterval(t);
  }, [busy, refresh]);

  // Open the overview (or first) page when none is selected.
  useEffect(() => {
    if (!caseId || slug || view || !toc?.pages.length) return;
    const first = toc.pages.find((p) => p.kind === "overview") ?? toc.pages[0];
    nav(wikiPath(caseId, first.slug) + loc.search, { replace: true });
  }, [caseId, slug, view, toc, nav, loc.search]);

  const open = useCallback((s: string) => caseId && nav(wikiPath(caseId, s)), [caseId, nav]);
  const setView = (v: string | null) => {
    const n = new URLSearchParams(sp);
    if (v) n.set("view", v);
    else n.delete("view");
    setSp(n);
  };

  if (!kb)
    return (
      <>
        <TopBar title="Wiki" />
        <NeedKB />
      </>
    );

  const c = toc?.case ?? kcase;
  const [statusLabel, statusTone] = WIKI_STATUS[c?.wiki_status ?? "none"] ?? ["", ""];

  return (
    <>
      <TopBar
        title="Wiki hồ sơ"
        subtitle={
          c && toc ? (
            <span className="flex items-center gap-2">
              <Badge tone={statusTone}>
                {busy && <span className="spinner size-2.5 border-[1.5px]" />}
                {statusLabel}
              </Badge>
              {toc.pages.length} trang · {c.wiki_docs_covered}/{toc.documents_total} file đã vào wiki
              {c.wiki_built_at ? ` · cập nhật ${fmtDate(c.wiki_built_at)}` : ""}
            </span>
          ) : undefined
        }
      >
        <CasePicker value={c} onChange={(x) => nav(wikiPath(x.id))} />
        {c && <WikiActions c={c} onDone={refresh} />}
      </TopBar>
      {cases && !cases.length ? (
        <Empty icon="folder_off" title="Chưa có hồ sơ">
          Wiki được dựng riêng cho từng hồ sơ. Tải file lên kèm mã hồ sơ để tạo hồ sơ đầu tiên.
          <div>
            <Link className="btn btn-primary mt-5" to="/documents?upload=1">
              <Icon name="upload_file" size={18} /> Tải file lên
            </Link>
          </div>
        </Empty>
      ) : err ? (
        <Empty icon="error" title="Không tải được wiki">
          {err}
        </Empty>
      ) : !toc || !caseId ? (
        <Loading />
      ) : (
        <div className="flex min-h-0 flex-1 border-t border-line">
          <Sidebar toc={toc} caseId={caseId} current={view ? "" : slug} view={view} onOpen={open} onView={setView} />
          {view === "log" ? (
            <LogView caseId={caseId} onOpen={open} />
          ) : view === "issues" ? (
            <IssuesView caseId={caseId} toc={toc} onOpen={open} onChanged={refresh} />
          ) : !toc.pages.length ? (
            <EmptyWiki toc={toc} onDone={() => (refresh(), reloadCases())} />
          ) : slug ? (
            <Article key={caseId + slug} caseId={caseId} slug={slug} toc={toc} onOpen={open} onSaved={refresh} />
          ) : (
            <Loading />
          )}
        </div>
      )}
    </>
  );
}

// ---------------------------------------------------------------- actions

function WikiActions({ c, onDone }: { c: Case; onDone: () => void }) {
  const { toast, fail } = useToast();
  const run = (fn: () => Promise<unknown>, msg: string) => async () => {
    try {
      await fn();
      toast(msg);
      onDone();
    } catch (e) {
      fail(e);
    }
  };
  return (
    <Menu
      align="right"
      trigger={(open) => (
        <button className="btn-icon" title="Thao tác wiki" onClick={open}>
          <Icon name="more_vert" />
        </button>
      )}
      items={[
        { icon: "rule", label: "Kiểm tra wiki ngay", onClick: run(() => runWikiLint(c.id), "Đã đưa việc kiểm tra vào hàng đợi") },
        {
          icon: "autorenew",
          label: "Dựng lại toàn bộ wiki",
          onClick: () => {
            if (!confirm(`Dựng lại wiki hồ sơ ${caseLabel(c)} từ đầu? Trang sửa tay được giữ, còn lại dựng lại từ mục lục và 1 lần trích xuất bằng LLM cho mỗi file.`)) return;
            run(() => rebuildWiki(c.id), "Đã đưa việc dựng lại wiki vào hàng đợi")();
          },
        },
        { divider: true, label: "" },
        { icon: "folder_zip", label: "Xuất Markdown (.zip)", onClick: () => exportWiki(c.id, c.code, "md").catch(fail) },
        { icon: "html", label: "Xuất HTML", onClick: () => exportWiki(c.id, c.code, "html").catch(fail) },
      ]}
    />
  );
}

// ---------------------------------------------------------------- sidebar

const rowCls = (active: boolean) =>
  "flex h-8 w-full items-center gap-2 rounded-full pr-3 pl-3 text-left text-sm " + (active ? "bg-accent-soft font-medium text-on-accent-soft" : "text-fg hover:bg-fg/8");

function Sidebar({
  toc,
  caseId,
  current,
  view,
  onOpen,
  onView,
}: {
  toc: WikiTOC;
  caseId: string;
  current: string;
  view: string | null;
  onOpen: (s: string) => void;
  onView: (v: string | null) => void;
}) {
  const [q, setQ] = useState("");
  const [hits, setHits] = useState<{ slug: string; title: string; kind: string; snippet: string }[] | null>(null);
  useEffect(() => {
    const text = q.trim();
    if (!text) return setHits(null);
    const t = setTimeout(() => searchWiki(caseId, text).then(setHits, () => setHits([])), 250);
    return () => clearTimeout(t);
  }, [q, caseId]);

  // Pages grouped by kind; entities further by entity type.
  const groups = useMemo(() => {
    const out: { kind: string; sub?: string; pages: WikiPageRef[] }[] = [];
    for (const k of KIND_ORDER) {
      const list = toc.pages.filter((p) => p.kind === k);
      if (!list.length) continue;
      if (k === "entity") {
        const byType = new Map<string, WikiPageRef[]>();
        for (const p of list) byType.set(p.entity_type || "khác", [...(byType.get(p.entity_type || "khác") ?? []), p]);
        for (const [t, ps] of [...byType.entries()].sort(([a], [b]) => a.localeCompare(b)))
          out.push({ kind: k, sub: t, pages: ps.sort((a, b) => a.title.localeCompare(b.title, "vi")) });
      } else out.push({ kind: k, pages: k === "overview" ? list : list.sort((a, b) => a.title.localeCompare(b.title, "vi")) });
    }
    return out;
  }, [toc.pages]);

  return (
    <aside className="hidden w-76 flex-none flex-col border-r border-line md:flex">
      <div className="p-3">
        <div className="flex h-10 items-center gap-2 rounded-full bg-surface-2 px-3 focus-within:bg-surface-3">
          <Icon name="search" size={20} className="text-muted" />
          <input className="min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-muted" placeholder="Tìm trong wiki" value={q} onChange={(e) => setQ(e.target.value)} />
          {q && (
            <button className="btn-icon btn-sm -mr-1" onClick={() => setQ("")} aria-label="Xoá">
              <Icon name="close" size={18} />
            </button>
          )}
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-auto px-2 pb-3">
        {hits ? (
          <div className="flex flex-col gap-1">
            {!hits.length && <div className="px-3 py-2 text-sm text-subtle">Không có trang khớp</div>}
            {hits.map((h) => (
              <button key={h.slug} onClick={() => onOpen(h.slug)} className="rounded-xl px-3 py-2 text-left hover:bg-fg/8">
                <div className="flex items-center gap-2 text-sm text-fg">
                  <Icon name={KIND[h.kind]?.[1] ?? "article"} size={18} className="text-muted" />
                  <span className="truncate">{h.title}</span>
                </div>
                {h.snippet && <div className="mt-0.5 line-clamp-2 text-xs text-muted" dangerouslySetInnerHTML={{ __html: h.snippet }} />}
              </button>
            ))}
          </div>
        ) : (
          <div className="flex flex-col gap-px">
            {groups.map((g) => (
              <Group key={g.kind + (g.sub ?? "")} title={g.sub ? `${KIND.entity[0]} · ${g.sub}` : KIND[g.kind][0]} count={g.pages.length} hideHeader={g.kind === "overview"}>
                {g.pages.map((p) => (
                  <button key={p.slug} onClick={() => onOpen(p.slug)} title={p.summary || p.title} className={rowCls(p.slug === current)}>
                    <Icon name={KIND[p.kind]?.[1] ?? "article"} size={18} fill={p.slug === current} className={p.slug === current ? "" : "text-muted"} />
                    <span className="min-w-0 flex-1 truncate">{p.title}</span>
                    {p.proposed && <i className="size-2 flex-none rounded-full bg-warn" title="Có bản cập nhật đề xuất" />}
                  </button>
                ))}
              </Group>
            ))}
            {!!toc.pending?.length && (
              <Group title="Đang chờ vào wiki" count={toc.pending.length}>
                {toc.pending.map((d) => (
                  <Link key={d.document_id} to={`/documents/${d.document_id}`} className="flex h-8 items-center gap-2 rounded-full px-3 text-sm text-muted hover:bg-fg/8">
                    <FileIcon name={d.file_name} size={18} />
                    <span className="min-w-0 flex-1 truncate">{d.file_name}</span>
                    {d.wiki_status === "failed" ? <Badge tone="err">lỗi</Badge> : <Spinner className="size-3.5 border-2" />}
                  </Link>
                ))}
              </Group>
            )}
          </div>
        )}
      </div>
      <div className="flex flex-col gap-px border-t border-line p-2">
        <button className={rowCls(view === "issues")} onClick={() => onView("issues")}>
          <Icon name="rule" size={18} className={view === "issues" ? "" : "text-muted"} />
          <span className="flex-1">Vấn đề cần xem</span>
          {toc.open_lint_issues > 0 && <span className="min-w-5 rounded-full bg-warn-soft px-1.5 text-center text-[11px] text-warn">{toc.open_lint_issues}</span>}
        </button>
        <button className={rowCls(view === "log")} onClick={() => onView("log")}>
          <Icon name="history" size={18} className={view === "log" ? "" : "text-muted"} />
          <span className="flex-1">Nhật ký wiki</span>
        </button>
      </div>
    </aside>
  );
}

function Group({ title, count, hideHeader, children }: { title: string; count: number; hideHeader?: boolean; children: ReactNode }) {
  const [open, setOpen] = useState(true);
  if (hideHeader) return <div className="mb-1">{children}</div>;
  return (
    <div className="mt-2">
      <button className="flex h-7 w-full items-center gap-1 rounded-full pr-3 pl-1 text-left text-xs font-medium text-muted hover:bg-fg/8" onClick={() => setOpen((v) => !v)}>
        <Icon name="arrow_right" size={20} className={"transition-transform " + (open ? "rotate-90" : "")} />
        <span className="flex-1 truncate">{title}</span>
        <span className="text-[11px] text-subtle">{count}</span>
      </button>
      {open && <div className="flex flex-col gap-px">{children}</div>}
    </div>
  );
}

// ---------------------------------------------------------------- article

function Article({ caseId, slug, toc, onOpen, onSaved }: { caseId: string; slug: string; toc: WikiTOC; onOpen: (s: string) => void; onSaved: () => void }) {
  const { toast, fail } = useToast();
  const [page, setPage] = useState<Page | null>(null);
  const [err, setErr] = useState("");
  const [editing, setEditing] = useState(false);
  const [showRevs, setShowRevs] = useState(false);
  const [showProposal, setShowProposal] = useState(false);
  const known = useMemo(() => new Set(toc.pages.map((p) => p.slug)), [toc.pages]);
  const titles = useMemo(() => new Map(toc.pages.map((p) => [p.slug, p.title])), [toc.pages]);

  const load = useCallback(() => {
    let alive = true;
    getWikiPage(caseId, slug).then(
      (p) => alive && setPage(p),
      (e) => alive && setErr((e as Error).message),
    );
    return () => {
      alive = false;
    };
  }, [caseId, slug]);
  useEffect(load, [load]);

  const fns = useMemo(() => {
    const m = new Map<number, { citation: string; stale?: boolean; label?: string }>();
    for (const f of page?.footnotes ?? []) m.set(f.n, { citation: f.citation_id, stale: f.status === "stale", label: fnLabel(f) });
    return m;
  }, [page]);

  if (err)
    return (
      <div className="flex-1">
        <Empty icon="search_off" title="Không có trang này">
          {err}
        </Empty>
      </div>
    );
  if (!page)
    return (
      <div className="flex-1">
        <Loading />
      </div>
    );

  const decide = async (action: "accept" | "reject") => {
    try {
      setPage(await decideWikiProposal(caseId, page.slug, action));
      setShowProposal(false);
      toast(action === "accept" ? "Đã nhận bản cập nhật" : "Đã bỏ bản cập nhật");
      onSaved();
    } catch (e) {
      fail(e);
    }
  };
  const [kindLabel] = KIND[page.kind] ?? [page.kind];

  return (
    <>
      <main className="min-h-0 min-w-0 flex-1 overflow-auto bg-surface-2">
        <div className="sticky top-0 z-1 flex h-12 items-center gap-2 bg-surface-2/90 px-4 backdrop-blur">
          <Badge>{page.entity_type ? `${kindLabel} · ${page.entity_type}` : kindLabel}</Badge>
          <span className="truncate text-xs text-muted">
            Phiên bản {page.version} · {page.last_edit_source === "user" ? "sửa tay" : "sinh tự động"} · {fmtDate(page.updated_at)}
          </span>
          <div className="flex-1" />
          {!editing && (
            <>
              <button className="btn-icon btn-sm" title="Lịch sử phiên bản" onClick={() => setShowRevs(true)}>
                <Icon name="history" size={20} />
              </button>
              {page.kind === "note" && (
                <button
                  className="btn-icon btn-sm"
                  title="Xoá ghi chú"
                  onClick={async () => {
                    if (!confirm("Xoá ghi chú này khỏi wiki?")) return;
                    try {
                      await deleteWikiNote(caseId, page.slug);
                      toast("Đã xoá ghi chú");
                      onSaved();
                    } catch (e) {
                      fail(e);
                    }
                  }}
                >
                  <Icon name="delete" size={20} />
                </button>
              )}
              <button className="btn btn-tonal btn-sm" onClick={() => setEditing(true)}>
                <Icon name="edit" size={18} /> Sửa
              </button>
            </>
          )}
        </div>

        {page.proposed_content != null && !editing && (
          <div className="mx-auto mb-3 flex max-w-204 flex-wrap items-center gap-3 rounded-xl bg-warn-soft px-4 py-3 text-sm text-warn">
            <Icon name="published_with_changes" />
            <span className="min-w-0 flex-1">Trang đã được sửa tay nên tài liệu mới không ghi đè. Có một bản cập nhật đề xuất đang chờ bạn duyệt.</span>
            <button className="btn btn-sm" onClick={() => setShowProposal(true)}>
              Xem đề xuất
            </button>
          </div>
        )}

        <div className="mx-auto mb-10 max-w-204 rounded-sm bg-surface px-8 py-10 shadow-1 sm:px-14 sm:py-12">
          {editing ? (
            <Editor
              caseId={caseId}
              page={page}
              onCancel={() => setEditing(false)}
              onSaved={(p) => {
                setPage(p);
                setEditing(false);
                onSaved();
              }}
            />
          ) : (
            <>
              <h1 className="mb-1 text-[30px] leading-9 font-normal text-fg">{page.title}</h1>
              {!!page.aliases?.length && <div className="mb-4 text-sm text-muted">Còn gọi là: {page.aliases.join(", ")}</div>}
              {page.summary && <p className="mb-6 border-l-3 border-accent/40 pl-3 text-[15px] leading-6 text-muted">{page.summary}</p>}
              <Markdown text={stripTitle(page.content, page.title)} wikiLinks knownSlugs={known} slugTitles={titles} footnotes={fns} onWikiLink={onOpen} className="text-[15px]" />
              <Footnotes list={page.footnotes ?? []} />
            </>
          )}
        </div>
      </main>
      <aside className="hidden w-80 flex-none flex-col gap-6 overflow-auto border-l border-line p-5 xl:flex">
        {page.kind === "source" && page.document_id && (
          <Link to={`/documents/${page.document_id}`} className="btn btn-sm self-start">
            <Icon name="open_in_new" size={18} /> Mở tài liệu gốc
          </Link>
        )}
        <Attributes attrs={page.attributes} fns={page.footnotes ?? []} />
        <Links title="Liên kết đến" links={page.links_out ?? []} dir="out" known={known} onOpen={onOpen} />
        <Links title="Được nhắc từ" links={page.links_in ?? []} dir="in" known={known} onOpen={onOpen} />
        <Link className="btn btn-text btn-sm -ml-3 self-start" to={`/graph?case=${caseId}&page=${encodeURIComponent(page.slug)}`}>
          <Icon name="hub" size={18} /> Xem trên graph
        </Link>
      </aside>
      {showRevs && (
        <Revisions
          caseId={caseId}
          page={page}
          onClose={() => setShowRevs(false)}
          onRestored={(p) => {
            setPage(p);
            setShowRevs(false);
            onSaved();
          }}
        />
      )}
      {showProposal && (
        <Modal
          title="Bản cập nhật đề xuất"
          icon="published_with_changes"
          width={820}
          onClose={() => setShowProposal(false)}
          footer={
            <>
              <button className="btn btn-text" onClick={() => decide("reject")}>
                Bỏ đề xuất
              </button>
              <button className="btn btn-primary" onClick={() => decide("accept")}>
                Nhận bản này
              </button>
            </>
          }
        >
          <p className="text-sm text-muted">Nội dung dựng lại từ tài liệu mới. Nhận sẽ thay nội dung hiện tại (bản cũ vẫn còn trong lịch sử).</p>
          <div className="max-h-[60vh] overflow-auto rounded-xl border border-line p-5 text-fg">
            <Markdown text={page.proposed_content ?? ""} wikiLinks knownSlugs={known} slugTitles={titles} footnotes={fns} />
          </div>
        </Modal>
      )}
    </>
  );
}

// Pages may repeat their title as a leading "# Title".
function stripTitle(md: string, title: string) {
  const m = /^\s*#\s+(.+)\n/.exec(md);
  return m && fold(m[1].trim()) === fold(title.trim()) ? md.slice(m[0].length) : md;
}

const fnLabel = (f: WikiFootnote) => `${f.file_name ?? "tài liệu"}, tr. ${f.page_no}, dòng ${f.line_from}–${f.line_to}${f.status === "stale" ? " (lỗi thời)" : ""}`;

function Footnotes({ list }: { list: WikiFootnote[] }) {
  const cite = useCitation();
  if (!list.length) return null;
  return (
    <section className="mt-10 border-t border-line pt-4">
      <h2 className="mb-2 text-sm font-medium text-muted">Chú thích</h2>
      <ol className="flex flex-col gap-1.5 text-[13px] leading-5">
        {[...list]
          .sort((a, b) => a.n - b.n)
          .map((f) => (
            <li key={f.n} className="flex gap-2">
              <button className={"cite fn flex-none" + (f.status === "stale" ? " stale" : "")} onClick={(e) => cite.open(f.citation_id, e.currentTarget.getBoundingClientRect())}>
                {f.n}
              </button>
              <span className="min-w-0">
                <Link to={`/documents/${f.document_id}?page=${f.page_no}`} className="text-fg hover:underline">
                  {f.file_name ?? f.document_id.slice(0, 8)}
                </Link>
                <span className="text-muted">
                  {" "}
                  · tr. {f.page_no}, dòng {f.line_from}–{f.line_to}
                </span>
                {f.status === "stale" && (
                  <Badge tone="warn" title="Dòng gốc đã đổi, chú thích không còn khớp">
                    lỗi thời
                  </Badge>
                )}
                {f.quote && <span className="block text-muted italic">“{f.quote}”</span>}
              </span>
            </li>
          ))}
      </ol>
    </section>
  );
}

function FnRefs({ ns, fns }: { ns?: number[]; fns: WikiFootnote[] }) {
  const cite = useCitation();
  return (
    <>
      {(ns ?? []).map((n) => {
        const f = fns.find((x) => x.n === n);
        return f ? (
          <button key={n} className={"cite fn" + (f.status === "stale" ? " stale" : "")} title={fnLabel(f)} onClick={(e) => cite.open(f.citation_id, e.currentTarget.getBoundingClientRect())}>
            {n}
          </button>
        ) : null;
      })}
    </>
  );
}

function Attributes({ attrs, fns }: { attrs?: Record<string, WikiAttribute>; fns: WikiFootnote[] }) {
  const entries = Object.entries(attrs ?? {});
  if (!entries.length) return null;
  return (
    <Section title="Thuộc tính">
      <dl className="flex flex-col gap-2.5 text-[13px]">
        {entries.map(([k, a]) => (
          <div key={k}>
            <dt className="text-xs text-muted">{k}</dt>
            <dd className="text-fg">
              {a.conflict ? (
                <div className="mt-1 rounded-lg bg-warn-soft/60 p-2">
                  <div className="mb-1 flex items-center gap-1 text-xs font-medium text-warn">
                    <Icon name="warning" size={16} /> Các nguồn ghi khác nhau
                  </div>
                  {(a.history ?? []).map((h, i) => (
                    <div key={i}>
                      {metaValue(h.value)} <FnRefs ns={h.footnotes} fns={fns} />
                    </div>
                  ))}
                </div>
              ) : (
                <>
                  {metaValue(a.value)} <FnRefs ns={a.footnotes} fns={fns} />
                </>
              )}
            </dd>
          </div>
        ))}
      </dl>
    </Section>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div>
      <h3 className="mb-2 text-sm font-medium text-fg">{title}</h3>
      {children}
    </div>
  );
}

function Links({ title, links, dir, known, onOpen }: { title: string; links: WikiLink[]; dir: "in" | "out"; known: Set<string>; onOpen: (s: string) => void }) {
  return (
    <Section title={`${title} (${links.length})`}>
      {!links.length && <span className="text-sm text-subtle">—</span>}
      <div className="flex flex-col gap-1">
        {links.map((l, i) => {
          const slug = dir === "out" ? l.to : l.from;
          const title = (dir === "out" ? l.to_title : l.from_title) || slug;
          return (
            <button key={i} className="flex items-baseline gap-2 rounded-lg px-2 py-1 text-left text-sm hover:bg-fg/8 disabled:opacity-50" disabled={!known.has(slug)} onClick={() => onOpen(slug)}>
              <span className="min-w-0 flex-1 truncate text-fg">{title}</span>
              {l.relation && <span className="font-mono text-[11px] text-muted">{l.relation}</span>}
            </button>
          );
        })}
      </div>
    </Section>
  );
}

function Editor({ caseId, page, onCancel, onSaved }: { caseId: string; page: Page; onCancel: () => void; onSaved: (p: Page) => void }) {
  const { toast, fail } = useToast();
  const [title, setTitle] = useState(page.title);
  const [content, setContent] = useState(page.content);
  const [busy, setBusy] = useState(false);
  return (
    <div className="flex flex-col gap-4">
      <input className="border-b border-line bg-transparent pb-2 text-[28px] text-fg outline-none focus:border-accent" value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Tiêu đề" />
      <label className="field">
        Nội dung (Markdown · liên kết [[slug]] · chú thích [^n] giữ nguyên số)
        <textarea className="input min-h-[55vh] font-mono text-[13px] leading-relaxed" value={content} onChange={(e) => setContent(e.target.value)} />
      </label>
      <p className="text-xs text-subtle">Trang sửa tay không bị lần nạp file sau ghi đè: nội dung mới từ tài liệu sẽ thành bản đề xuất để bạn duyệt.</p>
      <div className="flex justify-end gap-2">
        <button className="btn btn-text" onClick={onCancel}>
          Huỷ
        </button>
        <button
          className="btn btn-primary"
          disabled={busy}
          onClick={async () => {
            setBusy(true);
            try {
              const r = await editWikiPage(caseId, page.slug, { title, content });
              if (r.invalid_footnotes?.length) toast(`Đã lưu; ${r.invalid_footnotes.length} chú thích không khớp nguồn bị đánh dấu lỗi thời`, "err");
              else toast("Đã lưu phiên bản " + r.page.version);
              onSaved(r.page);
            } catch (e) {
              fail(e);
              setBusy(false);
            }
          }}
        >
          Lưu
        </button>
      </div>
    </div>
  );
}

function Revisions({ caseId, page, onClose, onRestored }: { caseId: string; page: Page; onClose: () => void; onRestored: (p: Page) => void }) {
  const { fail, toast } = useToast();
  const [revs, setRevs] = useState<WikiRevision[] | null>(null);
  const [err, setErr] = useState("");
  const [view, setView] = useState<WikiRevision | null>(null);
  useEffect(() => {
    wikiRevisions(caseId, page.slug).then(setRevs, (e) => setErr((e as Error).message));
  }, [caseId, page.slug]);
  return (
    <Modal title="Lịch sử phiên bản" icon="history" onClose={onClose} width={780}>
      {err && <Empty>{err}</Empty>}
      {!revs && !err && <Loading />}
      {view ? (
        <div className="text-fg">
          <div className="mb-3 flex items-center">
            <button className="btn btn-text btn-sm -ml-3" onClick={() => setView(null)}>
              <Icon name="arrow_back" size={18} /> Tất cả phiên bản
            </button>
            <div className="flex-1" />
            {view.version !== page.version && (
              <button
                className="btn btn-tonal btn-sm"
                onClick={async () => {
                  try {
                    const p = await restoreWikiRevision(caseId, page.slug, view.version);
                    toast(`Đã khôi phục phiên bản ${view.version}`);
                    onRestored(p);
                  } catch (e) {
                    fail(e);
                  }
                }}
              >
                <Icon name="restore" size={18} /> Khôi phục bản này
              </button>
            )}
          </div>
          <Markdown text={view.content} wikiLinks />
        </div>
      ) : (
        <div className="-mx-2 flex flex-col">
          {revs?.map((r) => (
            <button key={r.version} className="flex items-center gap-3 rounded-xl px-3 py-2.5 text-left hover:bg-fg/8" onClick={() => setView(r)}>
              <Icon name={r.edit_source === "user" ? "edit_note" : "auto_awesome"} className="text-muted" />
              <div className="min-w-0 flex-1">
                <div className="text-sm text-fg">
                  Phiên bản {r.version} · {fmtDate(r.edited_at)}
                </div>
                <div className="truncate text-xs">{r.title}</div>
              </div>
              <Badge>{r.edit_source === "user" ? "sửa tay" : "tự động"}</Badge>
            </button>
          ))}
        </div>
      )}
      {revs && !revs.length && <Empty icon="history">Chưa có phiên bản cũ.</Empty>}
    </Modal>
  );
}

// ---------------------------------------------------------------- log / issues

function LogView({ caseId, onOpen }: { caseId: string; onOpen: (s: string) => void }) {
  const [list, setList] = useState<WikiLogEntry[] | null>(null);
  const [err, setErr] = useState("");
  useEffect(() => {
    wikiLog(caseId).then(setList, (e) => setErr((e as Error).message));
  }, [caseId]);
  return (
    <main className="min-h-0 flex-1 overflow-auto px-6 py-4">
      <h2 className="mb-4 text-lg text-fg">Nhật ký wiki</h2>
      {err && <div className="text-err">{err}</div>}
      {!list && !err && <Loading />}
      {list && !list.length && <Empty icon="history">Chưa có hoạt động nào.</Empty>}
      <ol className="relative flex max-w-200 flex-col gap-4 border-l border-line pl-5">
        {list?.map((e) => (
          <li key={e.id} className="relative">
            <span className="absolute top-1.5 -left-[25px] size-2.5 rounded-full bg-accent" />
            <div className="flex flex-wrap items-center gap-2 text-sm">
              <Badge tone={e.op === "ingest" ? "info" : e.op === "retract" ? "warn" : ""}>{OP[e.op] ?? e.op}</Badge>
              <span className="font-medium text-fg">{e.ref}</span>
              <span className="text-xs text-muted">
                {fmtDate(e.at)} · {e.actor}
                {e.llm_calls ? ` · ${e.llm_calls} lần gọi LLM` : ""}
              </span>
            </div>
            <div className="mt-1 text-sm text-fg">{e.summary}</div>
            {!!e.pages?.length && (
              <div className="mt-1.5 flex flex-wrap gap-1">
                {e.pages.slice(0, 12).map((s) => (
                  <button key={s} className="chip h-6 text-xs" onClick={() => onOpen(s)}>
                    {s}
                  </button>
                ))}
                {e.pages.length > 12 && <span className="text-xs text-muted">+{e.pages.length - 12}</span>}
              </div>
            )}
          </li>
        ))}
      </ol>
    </main>
  );
}

function IssuesView({ caseId, toc, onOpen, onChanged }: { caseId: string; toc: WikiTOC; onOpen: (s: string) => void; onChanged: () => void }) {
  const { fail } = useToast();
  const [status, setStatus] = useState<"open" | "fixed" | "dismissed">("open");
  const [list, setList] = useState<WikiLintIssue[] | null>(null);
  const [reload, setReload] = useState(0);
  useEffect(() => {
    setList(null);
    wikiLint(caseId, status).then(setList, fail);
  }, [caseId, status, reload, fail]);
  const titleOf = (s: string) => toc.pages.find((p) => p.slug === s)?.title ?? s;
  const act = async (id: number, s: "fixed" | "dismissed" | "open") => {
    try {
      await setLintStatus(caseId, id, s);
      setReload((r) => r + 1);
      onChanged();
    } catch (e) {
      fail(e);
    }
  };
  return (
    <main className="min-h-0 flex-1 overflow-auto px-6 py-4">
      <div className="mb-4 flex flex-wrap items-center gap-2">
        <h2 className="mr-3 text-lg text-fg">Vấn đề cần xem</h2>
        {(["open", "fixed", "dismissed"] as const).map((s) => (
          <button key={s} className={"chip " + (status === s ? "chip-on" : "")} onClick={() => setStatus(s)}>
            {status === s && <Icon name="check" size={16} />}
            {s === "open" ? "Đang mở" : s === "fixed" ? "Đã xử lý" : "Đã bỏ qua"}
          </button>
        ))}
      </div>
      {!list && <Loading />}
      {list && !list.length && <Empty icon="task_alt">{status === "open" ? "Wiki không có vấn đề nào cần xem." : "Không có mục nào."}</Empty>}
      <div className="flex max-w-220 flex-col gap-2">
        {list?.map((it) => {
          const d = it.detail ?? {};
          return (
            <div key={it.id} className="rounded-xl border border-line p-3">
              <div className="flex flex-wrap items-center gap-2">
                <Badge tone={it.kind === "contradiction" ? "err" : it.kind === "stale" || it.kind === "gap" ? "warn" : ""}>{LINT[it.kind] ?? it.kind}</Badge>
                <span className="text-xs text-muted">{fmtDate(it.found_at)}</span>
                <div className="flex-1" />
                {status === "open" ? (
                  <>
                    <button className="btn btn-text btn-sm" onClick={() => act(it.id, "dismissed")}>
                      Bỏ qua
                    </button>
                    <button className="btn btn-sm" onClick={() => act(it.id, "fixed")}>
                      Đã xử lý
                    </button>
                  </>
                ) : (
                  <button className="btn btn-text btn-sm" onClick={() => act(it.id, "open")}>
                    Mở lại
                  </button>
                )}
              </div>
              <div className="mt-2 text-sm text-fg">
                {typeof d.reason === "string" && <div>{d.reason}</div>}
                {typeof d.attribute === "string" && (
                  <div>
                    Thuộc tính <b>{d.attribute}</b>:{" "}
                    {Array.isArray(d.values) && (d.values as { value: unknown }[]).map((v) => metaValue(v.value)).join(" ≠ ")}
                  </div>
                )}
                {typeof d.file_name === "string" && (
                  <div className="text-muted">
                    File:{" "}
                    {typeof d.document_id === "string" ? (
                      <Link className="text-accent hover:underline" to={`/documents/${d.document_id}`}>
                        {d.file_name}
                      </Link>
                    ) : (
                      d.file_name
                    )}
                  </div>
                )}
                {Array.isArray(d.footnotes) && <div className="text-muted">Chú thích lỗi thời: {(d.footnotes as number[]).map((n) => `[${n}]`).join(" ")}</div>}
                {typeof d.error === "string" && <div className="mt-1 font-mono text-xs break-all text-err">{d.error}</div>}
              </div>
              {!!(it.pages?.length || d.slug) && (
                <div className="mt-2 flex flex-wrap gap-1">
                  {(it.pages?.length ? it.pages : [d.slug as string]).map((s) => (
                    <button key={s} className="chip h-7" onClick={() => onOpen(s)}>
                      {titleOf(s)}
                    </button>
                  ))}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </main>
  );
}

// ---------------------------------------------------------------- empty

function EmptyWiki({ toc, onDone }: { toc: WikiTOC; onDone: () => void }) {
  const { toast, fail } = useToast();
  const c = toc.case;
  const waiting = toc.pending?.length ?? 0;
  return (
    <div className="flex-1">
      <Empty icon="menu_book" title={`Wiki hồ sơ ${caseLabel(c)} chưa có trang nào`}>
        {toc.documents_total === 0 ? (
          "Hồ sơ chưa có file. Tải file lên hồ sơ này, wiki sẽ tự được dựng khi file xử lý xong."
        ) : waiting ? (
          <>
            {waiting} file đang được đưa vào wiki. Trang sẽ hiện ra khi xong — trang này tự làm mới.
            <div className="mt-4 flex justify-center">
              <Spinner />
            </div>
          </>
        ) : (
          <>
            Có {toc.documents_total} file nhưng chưa có trang wiki.
            <div>
              <button
                className="btn btn-primary mt-5"
                onClick={async () => {
                  try {
                    await rebuildWiki(c.id);
                    toast("Đã đưa việc dựng wiki vào hàng đợi");
                    onDone();
                  } catch (e) {
                    fail(e);
                  }
                }}
              >
                <Icon name="auto_awesome" size={18} /> Dựng wiki
              </button>
            </div>
          </>
        )}
      </Empty>
    </div>
  );
}
