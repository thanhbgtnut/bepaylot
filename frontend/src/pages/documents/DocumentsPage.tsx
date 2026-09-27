import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";

import { deleteDocument, downloadOriginal, listDocuments, reparseDocument } from "../../api/endpoints";
import type { Case, Document } from "../../api/types";
import { useApp } from "../../components/AppContext";
import { caseLabel } from "../../components/CasePicker";
import { NeedKB, TopBar } from "../../components/Layout";
import { Thumb } from "../../components/Thumb";
import { useToast } from "../../components/toast";
import { Empty, FileIcon, Icon, Loading, Menu, MetaChips, StatusBadge, type MenuItem } from "../../components/ui";
import { fmtDate, fmtSize, isBusy, parseKeyValues, STATUS, TERMINAL } from "../../lib/format";
import { UploadDialog } from "./UploadDialog";

const STATUS_FILTERS: [string, string][] = [
  ["queued,splitting,parsing,assembling,indexing", "Đang xử lý"],
  ["completed", "Hoàn tất"],
  ["partial", "Một phần"],
  ["failed", "Lỗi"],
];
const TYPE_FILTERS: [string, string, string][] = [
  ["pdf", "PDF", "picture_as_pdf"],
  ["image", "Hình ảnh", "image"],
];
type Sort = "name" | "date" | "size" | "pages";
type View = "list" | "grid";

const loadView = (): View => {
  try {
    return localStorage.getItem("bp.docView") === "grid" ? "grid" : "list";
  } catch {
    return "list";
  }
};

export function DocumentsPage() {
  const { kb, cases, reloadCases } = useApp();
  const { toast, fail } = useToast();
  const caseById = useMemo(() => new Map((cases ?? []).map((c) => [c.id, c])), [cases]);
  // Every file belongs to exactly one case (hồ sơ); "" = all cases of the KB.
  const [caseFilter, setCaseFilter] = useState("");
  const nav = useNavigate();
  const [sp, setSp] = useSearchParams();
  const q = sp.get("q") ?? "";
  const [status, setStatus] = useState("");
  const [ftype, setFtype] = useState("");
  const [meta, setMeta] = useState("");
  const [docs, setDocs] = useState<Document[] | null>(null);
  const [cursor, setCursor] = useState<string | undefined>();
  const [err, setErr] = useState("");
  const [view, setView] = useState<View>(loadView);
  const [sort, setSort] = useState<{ by: Sort; desc: boolean }>({ by: "date", desc: true });
  const [upload, setUpload] = useState<File[] | null>(sp.get("upload") ? [] : null);
  const [dragging, setDragging] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);

  useEffect(() => {
    try {
      localStorage.setItem("bp.docView", view);
    } catch {
      /* ignore */
    }
  }, [view]);

  // "Tải file lên" from the New menu arrives as ?upload=1.
  useEffect(() => {
    if (sp.get("upload")) {
      setUpload((u) => u ?? []);
      const next = new URLSearchParams(sp);
      next.delete("upload");
      setSp(next, { replace: true });
    }
  }, [sp, setSp]);

  const load = useCallback(
    async (before?: string) => {
      if (!kb) return;
      clearTimeout(timer.current);
      try {
        const res = await listDocuments(kb.id, { caseId: caseFilter || undefined, q, status, metadata: parseKeyValues(meta), before });
        const list = res.data ?? [];
        setDocs((cur) => (before && cur ? [...cur, ...list] : list));
        setCursor(res.next_cursor);
        setErr("");
        if (list.some((d) => !TERMINAL.has(d.status))) timer.current = setTimeout(() => load(), 3000);
      } catch (e) {
        setErr((e as Error).message);
      }
    },
    [kb, caseFilter, q, status, meta],
  );

  useEffect(() => {
    setDocs(null);
    setCaseFilter("");
  }, [kb]);
  useEffect(() => {
    const t = setTimeout(() => load(), 150);
    return () => {
      clearTimeout(t);
      clearTimeout(timer.current);
    };
  }, [load]);

  const shown = useMemo(() => {
    let list = docs ?? [];
    if (ftype === "pdf") list = list.filter((d) => d.mime_type === "application/pdf");
    if (ftype === "image") list = list.filter((d) => d.mime_type.startsWith("image/"));
    const dir = sort.desc ? -1 : 1;
    const key: Record<Sort, (d: Document) => string | number> = {
      name: (d) => d.file_name.toLowerCase(),
      date: (d) => d.created_at,
      size: (d) => d.size_bytes,
      pages: (d) => d.page_count,
    };
    return [...list].sort((a, b) => (key[sort.by](a) < key[sort.by](b) ? -dir : key[sort.by](a) > key[sort.by](b) ? dir : 0));
  }, [docs, ftype, sort]);

  const actions = (d: Document): MenuItem[] => [
    { icon: "open_in_new", label: "Mở", onClick: () => nav(`/documents/${d.id}`) },
    { icon: "download", label: "Tải xuống", onClick: () => downloadOriginal(d).catch(fail) },
    { icon: "forum", label: "Hỏi đáp về knowledge base", onClick: () => nav("/chat") },
    { divider: true, label: "" },
    {
      icon: "refresh",
      label: "Parse lại",
      onClick: async () => {
        try {
          await reparseDocument(d.id);
          toast(`Đang parse lại “${d.file_name}”`);
          load();
        } catch (e) {
          fail(e);
        }
      },
    },
    {
      icon: "delete",
      label: "Xoá",
      danger: true,
      onClick: async () => {
        if (!confirm(`Xoá “${d.file_name}”? Không thể hoàn tác.`)) return;
        try {
          await deleteDocument(d.id);
          toast("Đã xoá 1 file");
          load();
        } catch (e) {
          fail(e);
        }
      },
    },
  ];

  const metaFilter = parseKeyValues(meta);
  const statusLabel = STATUS_FILTERS.find(([v]) => v === status)?.[1];
  const typeLabel = TYPE_FILTERS.find(([v]) => v === ftype)?.[1];

  if (!kb)
    return (
      <>
        <TopBar title="Tài liệu" />
        <NeedKB />
      </>
    );

  return (
    <div
      className="relative flex min-h-0 flex-1 flex-col"
      onDragEnter={(e) => {
        if (e.dataTransfer.types.includes("Files")) setDragging(true);
      }}
      onDragOver={(e) => {
        if (e.dataTransfer.types.includes("Files")) e.preventDefault();
      }}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node)) setDragging(false);
      }}
      onDrop={(e) => {
        e.preventDefault();
        setDragging(false);
        if (e.dataTransfer.files.length) setUpload(Array.from(e.dataTransfer.files));
      }}
    >
      <TopBar title={kb.name} subtitle={kb.description}>
        <div className="mr-2 flex rounded-full border border-outline" role="group" aria-label="Kiểu hiển thị">
          {(["list", "grid"] as View[]).map((v) => (
            <button
              key={v}
              onClick={() => setView(v)}
              title={v === "list" ? "Dạng danh sách" : "Dạng lưới"}
              className={"flex h-8 w-14 items-center justify-center first:rounded-l-full last:rounded-r-full " + (view === v ? "bg-accent-soft text-on-accent-soft" : "text-muted hover:bg-fg/8")}
            >
              {view === v && <Icon name="check" size={18} />}
              <Icon name={v === "list" ? "view_list" : "grid_view"} size={18} />
            </button>
          ))}
        </div>
        <button className="btn-icon" title="Làm mới" onClick={() => load()}>
          <Icon name="refresh" />
        </button>
      </TopBar>

      {/* filter chips */}
      <div className="flex flex-none flex-wrap items-center gap-2 px-6 pb-3">
        <Menu
          trigger={(open) => (
            <button className={"chip " + (caseFilter ? "chip-on" : "")} onClick={open}>
              <Icon name="folder_open" size={18} />
              {caseFilter ? caseLabel(caseById.get(caseFilter) ?? ({ code: "?" } as Case)) : "Hồ sơ"}
              <Icon name="arrow_drop_down" size={20} className="-mr-1.5" />
            </button>
          )}
          items={[
            { label: "Tất cả hồ sơ", checked: !caseFilter, onClick: () => setCaseFilter("") },
            { divider: true, label: "" },
            ...(cases ?? []).map((c) => ({ label: `${caseLabel(c)} · ${Object.values(c.documents ?? {}).reduce((a, b) => a + b, 0)} file`, checked: caseFilter === c.id, onClick: () => setCaseFilter(c.id) })),
          ]}
        />
        <Menu
          trigger={(open) => (
            <button className={"chip " + (ftype ? "chip-on" : "")} onClick={open}>
              {ftype && <Icon name="check" size={18} />}
              {typeLabel ?? "Loại"}
              <Icon name="arrow_drop_down" size={20} className="-mr-1.5" />
            </button>
          )}
          items={[
            ...TYPE_FILTERS.map(([v, l, icon]) => ({ icon, label: l, onClick: () => setFtype(v) })),
            ...(ftype ? [{ divider: true, label: "" }, { icon: "close", label: "Bỏ lọc", onClick: () => setFtype("") }] : []),
          ]}
        />
        <Menu
          trigger={(open) => (
            <button className={"chip " + (status ? "chip-on" : "")} onClick={open}>
              {status && <Icon name="check" size={18} />}
              {statusLabel ?? "Trạng thái"}
              <Icon name="arrow_drop_down" size={20} className="-mr-1.5" />
            </button>
          )}
          items={[
            ...STATUS_FILTERS.map(([v, l]) => ({ label: l, checked: status === v, onClick: () => setStatus(v) })),
            ...(status ? [{ divider: true, label: "" }, { icon: "close", label: "Bỏ lọc", onClick: () => setStatus("") }] : []),
          ]}
        />
        <MetaChip value={meta} onChange={setMeta} />
        {q && (
          <button
            className="chip chip-on"
            onClick={() => {
              const next = new URLSearchParams(sp);
              next.delete("q");
              setSp(next);
            }}
          >
            <Icon name="search" size={18} /> “{q}” <Icon name="close" size={18} className="-mr-1" />
          </button>
        )}
        {(caseFilter || status || ftype || Object.keys(metaFilter).length > 0) && (
          <button
            className="btn btn-text btn-sm"
            onClick={() => {
              setCaseFilter("");
              setStatus("");
              setFtype("");
              setMeta("");
            }}
          >
            Xoá bộ lọc
          </button>
        )}
      </div>

      <div className="min-h-0 flex-1 overflow-auto px-3 pb-4">
        {err && <Empty icon="error" title="Không tải được danh sách">{err}</Empty>}
        {!err && !docs && <Loading />}
        {!err && docs && !shown.length && (
          <Empty icon="cloud_upload" title={q || status || ftype || meta ? "Không có file nào khớp" : "Thả file vào đây"}>
            {q || status || ftype || meta ? "Thử bỏ bớt bộ lọc." : "hoặc dùng nút “Mới” để tải PDF, ảnh scan lên knowledge base này."}
            {!(q || status || ftype || meta) && (
              <div>
                <button className="btn btn-primary mt-5" onClick={() => setUpload([])}>
                  <Icon name="upload_file" size={18} /> Tải file lên
                </button>
              </div>
            )}
          </Empty>
        )}
        {!err && shown.length > 0 && view === "list" && (
          <table className="w-full border-collapse text-sm">
            <thead>
              <tr className="text-left text-[13px] font-medium text-muted">
                <SortTh by="name" sort={sort} setSort={setSort} className="pl-4">
                  Tên
                </SortTh>
                <th className="h-12 border-b border-line px-3 font-medium whitespace-nowrap">Hồ sơ</th>
                <th className="h-12 border-b border-line px-3 font-medium whitespace-nowrap">Trạng thái</th>
                <SortTh by="pages" sort={sort} setSort={setSort} className="max-lg:hidden">
                  Trang
                </SortTh>
                <th className="h-12 border-b border-line px-3 font-medium max-xl:hidden">Metadata</th>
                <SortTh by="date" sort={sort} setSort={setSort} className="max-md:hidden">
                  Ngày tải lên
                </SortTh>
                <SortTh by="size" sort={sort} setSort={setSort} className="max-md:hidden">
                  Kích thước
                </SortTh>
                <th className="h-12 w-12 border-b border-line" />
              </tr>
            </thead>
            <tbody>
              {shown.map((d) => (
                <ListRow key={d.id} d={d} kcase={caseById.get(d.case_id)} onCase={setCaseFilter} onOpen={() => nav(`/documents/${d.id}`)} actions={actions(d)} />
              ))}
            </tbody>
          </table>
        )}
        {!err && shown.length > 0 && view === "grid" && (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(220px,1fr))] gap-4 px-3 pt-1">
            {shown.map((d) => (
              <GridCard key={d.id} d={d} kcase={caseById.get(d.case_id)} onOpen={() => nav(`/documents/${d.id}`)} actions={actions(d)} />
            ))}
          </div>
        )}
        {cursor && (
          <div className="p-4 text-center">
            <button className="btn btn-sm" onClick={() => load(cursor)}>
              Tải thêm
            </button>
          </div>
        )}
      </div>

      {dragging && (
        <div className="pointer-events-none absolute inset-2 z-10 flex items-end justify-center rounded-2xl border-2 border-accent bg-accent/8 pb-10">
          <div className="flex items-center gap-3 rounded-full bg-accent px-6 py-3 text-on-accent shadow-3">
            <Icon name="cloud_upload" />
            Thả file để tải lên <b>{kb.name}</b>
          </div>
        </div>
      )}

      {upload && (
        <UploadDialog
          kb={kb}
          initialFiles={upload}
          onClose={() => setUpload(null)}
          onUploaded={() => {
            load();
            reloadCases();
          }}
          onOpen={(id) => {
            setUpload(null);
            nav(`/documents/${id}`);
          }}
        />
      )}
    </div>
  );
}

function SortTh({
  by,
  sort,
  setSort,
  className = "",
  children,
}: {
  by: Sort;
  sort: { by: Sort; desc: boolean };
  setSort: (s: { by: Sort; desc: boolean }) => void;
  className?: string;
  children: React.ReactNode;
}) {
  const on = sort.by === by;
  return (
    <th className={"h-12 border-b border-line px-3 font-medium whitespace-nowrap " + className}>
      <button className={"flex items-center gap-1 rounded-full py-1 hover:text-fg " + (on ? "text-fg" : "")} onClick={() => setSort({ by, desc: on ? !sort.desc : by !== "name" })}>
        {children}
        {on && <Icon name={sort.desc ? "arrow_downward" : "arrow_upward"} size={16} />}
      </button>
    </th>
  );
}

function Progress({ d }: { d: Document }) {
  if (!isBusy(d.status) || !d.page_count) return null;
  const pct = Math.round((100 * (d.pages_done + d.pages_failed)) / d.page_count);
  return (
    <div className="mt-1 h-1 w-24 overflow-hidden rounded-full bg-accent/20" title={pct + "%"}>
      <div className="h-full rounded-full bg-accent transition-all" style={{ width: pct + "%" }} />
    </div>
  );
}

function CaseChip({ c, onClick }: { c?: Case; onClick?: () => void }) {
  if (!c) return <span className="text-subtle">—</span>;
  return (
    <button
      className={"inline-flex h-6 max-w-44 items-center gap-1 rounded-md px-2 text-xs " + (c.code === "_UNASSIGNED" ? "bg-surface-3 text-muted" : "bg-accent-soft text-on-accent-soft")}
      title={(c.title ? c.title + " · " : "") + "Lọc theo hồ sơ này"}
      onClick={(e) => {
        e.stopPropagation();
        onClick?.();
      }}
    >
      <Icon name="folder_open" size={14} />
      <span className="truncate">{caseLabel(c)}</span>
    </button>
  );
}

function ListRow({ d, kcase, onCase, onOpen, actions }: { d: Document; kcase?: Case; onCase: (id: string) => void; onOpen: () => void; actions: MenuItem[] }) {
  const td = "border-b border-line px-3 py-2 align-middle";
  return (
    <tr className="group cursor-default hover:bg-surface-2" onDoubleClick={onOpen} onClick={onOpen}>
      <td className={td + " rounded-l-lg pl-4"}>
        <div className="flex items-center gap-4">
          <FileIcon mime={d.mime_type} name={d.file_name} />
          <div className="min-w-0">
            <div className="max-w-105 truncate font-medium text-fg" title={d.file_name}>
              {d.file_name}
            </div>
            {d.title && (
              <div className="max-w-105 truncate text-xs text-muted" title={d.title}>
                {d.title}
              </div>
            )}
          </div>
        </div>
      </td>
      <td className={td + " whitespace-nowrap"}>
        <CaseChip c={kcase} onClick={() => onCase(d.case_id)} />
      </td>
      <td className={td}>
        <StatusBadge status={d.status} />
        <Progress d={d} />
        {d.error && (
          <div className="mt-0.5 max-w-50 truncate text-xs text-err" title={d.error}>
            {d.error}
          </div>
        )}
      </td>
      <td className={td + " text-muted max-lg:hidden"}>
        {d.page_count || "—"}
        {d.pages_failed > 0 && <span className="badge badge-err ml-1.5">{d.pages_failed} lỗi</span>}
      </td>
      <td className={td + " max-xl:hidden"}>
        <div className="flex max-w-80 flex-wrap gap-1">
          <MetaChips meta={d.metadata} />
        </div>
      </td>
      <td className={td + " whitespace-nowrap text-muted max-md:hidden"}>{fmtDate(d.created_at)}</td>
      <td className={td + " whitespace-nowrap text-muted max-md:hidden"}>{fmtSize(d.size_bytes)}</td>
      <td className={td + " rounded-r-lg pr-2"} onClick={(e) => e.stopPropagation()}>
        <Menu
          align="right"
          trigger={(open, isOpen) => (
            <button className={"btn-icon btn-sm " + (isOpen ? "" : "opacity-0 group-hover:opacity-100")} onClick={open} title="Thao tác khác">
              <Icon name="more_vert" size={20} />
            </button>
          )}
          items={actions}
        />
      </td>
    </tr>
  );
}

function GridCard({ d, kcase, onOpen, actions }: { d: Document; kcase?: Case; onOpen: () => void; actions: MenuItem[] }) {
  const hasImage = d.page_count > 0 && !["queued", "splitting"].includes(d.status);
  const [label, tone] = STATUS[d.status] ?? [d.status, ""];
  return (
    <div className="group flex cursor-pointer flex-col rounded-xl bg-surface-2 p-1.5 pt-0 transition-colors hover:bg-surface-3" onClick={onOpen}>
      <div className="flex h-12 items-center gap-3 pr-0 pl-2.5">
        <FileIcon mime={d.mime_type} name={d.file_name} size={20} />
        <span className="min-w-0 flex-1 truncate text-sm font-medium" title={d.file_name}>
          {d.file_name}
        </span>
        <span onClick={(e) => e.stopPropagation()}>
          <Menu
            align="right"
            trigger={(open) => (
              <button className="btn-icon btn-sm" onClick={open} title="Thao tác khác">
                <Icon name="more_vert" size={20} />
              </button>
            )}
            items={actions}
          />
        </span>
      </div>
      <div className="relative aspect-4/3 overflow-hidden rounded-lg bg-surface">
        {hasImage ? (
          <Thumb docId={d.id} pageNo={1} className="size-full" />
        ) : (
          <div className="grid size-full place-items-center">
            <FileIcon mime={d.mime_type} name={d.file_name} size={64} />
          </div>
        )}
        {(isBusy(d.status) || tone === "err" || tone === "warn") && (
          <div className="absolute right-2 bottom-2">
            <StatusBadge status={d.status} />
          </div>
        )}
      </div>
      <div className="flex items-center gap-2 px-2.5 pt-2 pb-1.5 text-xs text-muted">
        <CaseChip c={kcase} />
        <span className="min-w-0 flex-1 truncate">{d.title || label}</span>
        <span className="whitespace-nowrap">{d.page_count ? `${d.page_count} trang` : ""}</span>
      </div>
    </div>
  );
}

function MetaChip({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const [draft, setDraft] = useState(value);
  const [open, setOpen] = useState(false);
  const active = Object.keys(parseKeyValues(value)).length > 0;
  useEffect(() => setDraft(value), [value]);
  return (
    <span className="relative">
      <button className={"chip " + (active ? "chip-on" : "")} onClick={() => setOpen((o) => !o)}>
        {active && <Icon name="check" size={18} />}
        {active ? value : "Metadata"}
        <Icon name="arrow_drop_down" size={20} className="-mr-1.5" />
      </button>
      {open && (
        <form
          className="menu absolute top-10 left-0 flex w-80 flex-col gap-3 p-4"
          onSubmit={(e) => {
            e.preventDefault();
            onChange(draft);
            setOpen(false);
          }}
        >
          <label className="field">
            Lọc theo metadata
            <input className="input" autoFocus value={draft} onChange={(e) => setDraft(e.target.value)} placeholder="group_code=G1, loai=GCN" />
          </label>
          <div className="flex justify-end gap-1">
            <button
              type="button"
              className="btn btn-text btn-sm"
              onClick={() => {
                onChange("");
                setOpen(false);
              }}
            >
              Xoá
            </button>
            <button className="btn btn-primary btn-sm">Áp dụng</button>
          </div>
        </form>
      )}
    </span>
  );
}
