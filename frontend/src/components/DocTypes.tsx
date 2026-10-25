import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";

import {
  copyDocType,
  createDocType,
  createPrompt,
  deleteDocType,
  deletePrompt,
  deleteSample,
  dropDocTypeJob,
  getDocType,
  getPromptText,
  getSample,
  listDocTypeJobs,
  listDocTypes,
  listPrompts,
  listSamples,
  optimizeDocType,
  runSamples,
  samplePagePath,
  saveTruth,
  setCurrentPrompt,
  setDocTypeKBs,
  uploadSamples,
} from "../api/endpoints";
import { ApiError } from "../api/client";
import { canEditPrompts, type DocTypeJob, type DocTypePrompt, type DocTypeSample, type DocTypeView, type KnowledgeBase, type PromptShot, type SheetField } from "../api/types";
import { typeColor } from "../lib/doctype";
import { fmtDate, toCode, uniqueCode } from "../lib/format";
import { useApp } from "./AppContext";
import { useAuth } from "./AuthContext";
import { Markdown } from "./Markdown";
import { BackButton } from "./nav";
import { PageImage } from "./PageImage";
import { useToast } from "./toast";
import { Empty, FileIcon, Icon, Loading, Menu, Modal, Spinner } from "./ui";

// Cài đặt → Loại giấy tờ (§6.9.10, §7.11): the catalogue shared by every
// library of the user (U97). Each type has the fields to read, versioned AI
// instructions (U98) and sample documents to measure them on (U94).

const pct = (x?: number | null) => (x == null ? "—" : Math.round(x * 100) + "%");
const pctTone = (x?: number | null) => (x == null ? "text-subtle" : x >= 0.95 ? "text-ok" : x >= 0.8 ? "text-warn" : "text-err");
const norm = (v?: string | null) => (v ?? "").trim().replace(/\s+/g, " ").toLowerCase();

// The libraries that use a type; adding or removing one changes the types its
// pages are classified with.
function useKBToggle(reload: () => void) {
  const { kbs, reloadKBs } = useApp();
  const { toast, fail } = useToast();
  return {
    kbs,
    toggle: async (t: DocTypeView, k: KnowledgeBase) => {
      const on = t.kb_ids.includes(k.id);
      try {
        await setDocTypeKBs(t.name, on ? t.kb_ids.filter((x) => x !== k.id) : [...t.kb_ids, k.id]);
        toast(on ? `Đã bỏ “${t.title}” khỏi ${k.name}; trang mới sẽ không phân loại theo loại này` : `Đã thêm “${t.title}” vào ${k.name}`);
        reload();
        reloadKBs();
      } catch (e) {
        fail(e);
      }
    },
  };
}

function KBMenu({ t, reload, children }: { t: DocTypeView; reload: () => void; children: (open: (e: React.MouseEvent) => void) => ReactNode }) {
  const { kbs, toggle } = useKBToggle(reload);
  return <Menu align="right" trigger={children} items={kbs.length ? kbs.map((k) => ({ label: k.name, checked: t.kb_ids.includes(k.id), onClick: () => toggle(t, k) })) : [{ label: "Chưa có thư viện", disabled: true }]} />;
}

async function removeType(t: DocTypeView, kbs: KnowledgeBase[]) {
  const used = kbs.filter((k) => t.kb_ids.includes(k.id));
  const ok = confirm(used.length ? `“${t.title}” đang dùng ở ${used.map((k) => k.name).join(", ")}. Xoá sẽ bỏ khỏi các thư viện này, cùng tài liệu mẫu và hướng dẫn AI. Tiếp tục?` : `Xoá “${t.title}” cùng tài liệu mẫu và hướng dẫn AI?`);
  if (!ok) return false;
  await deleteDocType(t.name, used.length > 0);
  return true;
}

export function DocTypesTab() {
  const { kbs, reloadKBs } = useApp();
  const { user } = useAuth();
  const editor = canEditPrompts(user);
  const { toast, fail } = useToast();
  const nav = useNavigate();
  const [list, setList] = useState<DocTypeView[] | null>(null);
  const [filter, setFilter] = useState("all");
  const [q, setQ] = useState("");
  const [creating, setCreating] = useState(false);
  const load = useCallback(() => listDocTypes().then(setList, (e) => (fail(e), setList([]))), [fail]);
  useEffect(() => {
    load();
  }, [load]);

  const all = list ?? [];
  const fq = toCode(q);
  const shown = all
    .filter((t) => (filter === "all" ? true : filter === "unused" ? !t.kb_ids.length : t.kb_ids.includes(filter)))
    .filter((t) => !fq || toCode(t.title + " " + t.name).includes(fq));
  const chip = (v: string, label: string, n: number) => (
    <button key={v} className={"chip " + (filter === v ? "chip-on" : "")} aria-pressed={filter === v} onClick={() => setFilter(v)}>
      {filter === v && <Icon name="check" size={18} />}
      {label} <span className={"tabular-nums " + (filter === v ? "" : "text-subtle")}>{n}</span>
    </button>
  );
  const td = "border-b border-line px-3 py-2.5 align-middle";
  return (
    <>
      <header className="flex flex-wrap items-end gap-x-6 gap-y-3">
        <div className="min-w-60 flex-1">
          <h2 className="text-base font-medium text-fg">Loại giấy tờ</h2>
          <p className="mt-1 max-w-2xl text-sm text-muted">
            Danh mục dùng chung cho mọi thư viện. Mỗi loại có trường cần lấy, hướng dẫn AI và tài liệu mẫu để đo độ chính xác. Thư viện chỉ phân loại trang, tạo bảng Excel và đối chiếu theo các loại đã được thêm vào nó.
          </p>
        </div>
        {editor && (
          <button className="btn btn-primary" onClick={() => setCreating(true)}>
            <Icon name="add" size={18} /> Loại giấy tờ mới
          </button>
        )}
      </header>
      <div className="flex flex-wrap items-center gap-2">
        {chip("all", "Tất cả", all.length)}
        {kbs.map((k) => chip(k.id, k.name, all.filter((t) => t.kb_ids.includes(k.id)).length))}
        {chip("unused", "Chưa dùng", all.filter((t) => !t.kb_ids.length).length)}
        <span className="flex-1" />
        <label className="flex h-10 w-full items-center gap-2 rounded-full bg-surface-3 px-3 sm:w-64">
          <Icon name="search" size={20} className="text-muted" />
          <input className="min-w-0 flex-1 bg-transparent text-sm text-fg outline-none placeholder:text-muted" placeholder="Tìm loại giấy tờ" aria-label="Tìm loại giấy tờ" value={q} onChange={(e) => setQ(e.target.value)} />
        </label>
      </div>
      {!list ? (
        <Loading />
      ) : (
        <div className="overflow-x-auto rounded-2xl border border-line">
          <table className="w-full border-collapse text-sm">
            <thead>
              <tr className="text-left text-[13px] text-muted">
                <th className="h-11 border-b border-line pl-4 font-medium">Loại giấy tờ</th>
                <th className="border-b border-line px-3 font-medium">Trường</th>
                <th className="border-b border-line px-3 font-medium max-md:hidden">Mẫu đã duyệt</th>
                <th className="border-b border-line px-3 font-medium">Độ chính xác</th>
                <th className="border-b border-line px-3 font-medium max-md:hidden">Dùng ở thư viện</th>
                <th className="w-24 border-b border-line" />
              </tr>
            </thead>
            <tbody>
              {shown.map((t) => {
                const href = `/settings/doctypes/${encodeURIComponent(t.name)}`;
                return (
                  <tr key={t.name} className="cursor-pointer transition-colors hover:bg-surface-2" onClick={() => nav(href)}>
                    <td className={td + " pl-4"}>
                      <div className="flex items-center gap-3">
                        <i className="size-2.5 flex-none rounded-full" style={{ background: typeColor(t.name, all) }} aria-hidden="true" />
                        <div className="min-w-0">
                          <div className="truncate font-medium text-fg">{t.title}</div>
                          <code className="font-mono text-xs text-subtle">{t.name}</code>
                        </div>
                      </div>
                    </td>
                    <td className={td + " tabular-nums " + (t.fields.length ? "text-muted" : "text-warn")}>{t.fields.length || "chưa có"}</td>
                    <td className={td + " tabular-nums text-muted max-md:hidden"}>{t.samples ? `${t.reviewed}/${t.samples}` : <span className="text-subtle">—</span>}</td>
                    <td className={td}>
                      {t.accuracy == null ? (
                        <span className="text-subtle">—</span>
                      ) : (
                        <div className="flex items-center gap-2">
                          <span className={"w-10 font-medium tabular-nums " + pctTone(t.accuracy)}>{pct(t.accuracy)}</span>
                          <span className="h-1.5 w-20 overflow-hidden rounded-full bg-surface-3 max-lg:hidden">
                            <i className={"block h-full rounded-full " + (t.accuracy >= 0.95 ? "bg-ok" : t.accuracy >= 0.8 ? "bg-warn" : "bg-err")} style={{ width: t.accuracy * 100 + "%" }} />
                          </span>
                        </div>
                      )}
                    </td>
                    <td className={td + " max-md:hidden"}>
                      <div className="flex flex-wrap gap-1">
                        {t.kb_ids.length ? (
                          kbs
                            .filter((k) => t.kb_ids.includes(k.id))
                            .map((k) => (
                              <span key={k.id} className="tag">
                                <Icon name="folder_shared" size={14} />
                                {k.name}
                              </span>
                            ))
                        ) : (
                          <span className="text-xs text-subtle">Chưa dùng ở thư viện nào</span>
                        )}
                      </div>
                    </td>
                    <td className={td + " pr-2 text-right whitespace-nowrap"} onClick={(e) => e.stopPropagation()}>
                      {editor && (
                        <>
                          <KBMenu t={t} reload={load}>
                            {(open) => (
                              <button className="btn-icon btn-sm" title="Thêm vào hoặc bỏ khỏi thư viện" aria-label={`Thư viện dùng ${t.title}`} onClick={open}>
                                <Icon name="library_add" size={20} />
                              </button>
                            )}
                          </KBMenu>
                          <Menu
                            align="right"
                            trigger={(open) => (
                              <button className="btn-icon btn-sm" aria-label={`Thao tác với ${t.title}`} onClick={open}>
                                <Icon name="more_vert" size={20} />
                              </button>
                            )}
                            items={[
                              { icon: "tune", label: "Trường & hướng dẫn AI", onClick: () => nav(href + "?tab=prompt") },
                              { icon: "content_copy", label: "Nhân bản", onClick: () => copyDocType(t.name).then((c) => (toast(`Đã nhân bản thành “${c.title}”`), load()), fail) },
                              { divider: true, label: "" },
                              {
                                icon: "delete",
                                label: "Xoá",
                                danger: true,
                                onClick: () =>
                                  removeType(t, kbs).then((done) => {
                                    if (!done) return;
                                    toast("Đã xoá loại giấy tờ");
                                    load();
                                    reloadKBs();
                                  }, fail),
                              },
                            ]}
                          />
                        </>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
          {!shown.length && (
            <p className="px-4 py-6 text-center text-sm text-muted">
              {all.length ? (
                <>
                  Không có loại nào khớp.{" "}
                  <button className="text-accent hover:underline" onClick={() => (setQ(""), setFilter("all"))}>
                    Xem tất cả
                  </button>
                </>
              ) : (
                "Chưa có loại giấy tờ nào."
              )}
            </p>
          )}
        </div>
      )}
      {creating && (
        <NewDocTypeDialog
          onClose={() => setCreating(false)}
          onCreated={(t) => {
            setCreating(false);
            reloadKBs();
            nav(`/settings/doctypes/${encodeURIComponent(t.name)}?tab=samples`);
          }}
        />
      )}
    </>
  );
}

function NewDocTypeDialog({ onClose, onCreated }: { onClose: () => void; onCreated: (t: DocTypeView) => void }) {
  const { kbs, kb } = useApp();
  const { fail } = useToast();
  const [title, setTitle] = useState("");
  const [desc, setDesc] = useState("");
  const [ids, setIds] = useState<string[]>(kb ? [kb.id] : []);
  const [busy, setBusy] = useState(false);
  const create = async () => {
    setBusy(true);
    try {
      onCreated(await createDocType({ title: title.trim(), description: desc.trim() || undefined, kb_ids: ids }));
    } catch (e) {
      fail(e);
      setBusy(false);
    }
  };
  return (
    <Modal
      title="Loại giấy tờ mới"
      icon="category"
      onClose={onClose}
      footer={
        <>
          <button className="btn btn-text" onClick={onClose}>
            Huỷ
          </button>
          <button className="btn btn-primary" disabled={busy || !title.trim()} onClick={create}>
            Tạo
          </button>
        </>
      }
    >
      <label className="field">
        Tên loại giấy tờ
        <input className="input" autoFocus value={title} onChange={(e) => setTitle(e.target.value)} onKeyDown={(e) => e.key === "Enter" && title.trim() && create()} placeholder="VD: Giấy nộp tiền vào NSNN" />
      </label>
      <div className="-mt-2 text-xs">
        Mã: <code className="font-mono">{toCode(title) || "sinh từ tên"}</code>
      </div>
      <label className="field">
        Mô tả (giúp phân loại trang)
        <input className="input" value={desc} onChange={(e) => setDesc(e.target.value)} placeholder="Đặc điểm nhận biết: tiêu đề, mẫu số, cơ quan phát hành…" />
      </label>
      {kbs.length > 0 && (
        <fieldset className="flex flex-col gap-1">
          <legend className="text-xs font-medium text-muted">Dùng ở thư viện</legend>
          {kbs.map((k) => (
            <label key={k.id} className="flex h-9 items-center gap-3 text-sm text-fg">
              <input type="checkbox" className="size-4 accent-accent" checked={ids.includes(k.id)} onChange={(e) => setIds((xs) => (e.target.checked ? [...xs, k.id] : xs.filter((x) => x !== k.id)))} />
              {k.name}
            </label>
          ))}
        </fieldset>
      )}
      <p className="text-xs">Tạo xong, tải lên vài tài liệu mẫu khác bố cục (3–5 là đủ để bắt đầu) để chạy thử và đo độ chính xác.</p>
    </Modal>
  );
}

// ---------------------------------------------------------------- one type

// While the page is open it polls what runs in the background: samples being
// read and test runs or optimizations.
function usePoll(on: boolean, fn: () => void, ms = 2500) {
  const ref = useRef(fn);
  ref.current = fn;
  useEffect(() => {
    if (!on) return;
    const t = setInterval(() => ref.current(), ms);
    return () => clearInterval(t);
  }, [on, ms]);
}

export function DocTypePage() {
  const { code = "" } = useParams();
  const [sp, setSp] = useSearchParams();
  const { kbs } = useApp();
  const { user } = useAuth();
  const editor = canEditPrompts(user);
  const { fail } = useToast();
  const [t, setT] = useState<DocTypeView | null>(null);
  const [missing, setMissing] = useState(false);
  const [samples, setSamples] = useState<DocTypeSample[]>([]);
  const [prompts, setPrompts] = useState<DocTypePrompt[]>([]);
  const [jobs, setJobs] = useState<DocTypeJob[]>([]);
  const load = useCallback(async () => {
    try {
      const [a, b, c, d] = await Promise.all([getDocType(code), listSamples(code), listPrompts(code), listDocTypeJobs(code)]);
      setT(a);
      setSamples(b);
      setPrompts(c);
      setJobs(d);
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) setMissing(true);
      else fail(e);
    }
  }, [code, fail]);
  useEffect(() => {
    load();
  }, [load]);
  const running = jobs.filter((j) => j.status === "running");
  usePoll(running.length > 0 || samples.some((s) => s.status === "processing"), load);

  const tab = sp.get("tab") === "prompt" ? "prompt" : "samples";
  const setTab = (k: string) =>
    setSp(
      (p) => {
        p.set("tab", k);
        return p;
      },
      { replace: true },
    );

  if (missing)
    return (
      <div className="flex flex-1 flex-col">
        <div className="px-4 py-2">
          <BackButton fallback="/settings/doctypes" fallbackLabel="Loại giấy tờ" />
        </div>
        <Empty icon="category" title="Không có loại giấy tờ này" />
      </div>
    );
  if (!t) return <Loading />;
  const used = kbs.filter((k) => t.kb_ids.includes(k.id));
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex flex-none flex-wrap items-center gap-3 border-b border-line px-4 py-2">
        <BackButton fallback="/settings/doctypes" fallbackLabel="Loại giấy tờ" />
        <Icon name="description" size={24} fill className="text-[#4285f4]" />
        <div className="min-w-0 flex-1">
          <h1 className="truncate text-base font-medium text-fg">{t.title}</h1>
          <div className="truncate text-xs text-muted">
            <code className="font-mono">{t.name}</code> · {t.fields.length} trường · {t.samples} mẫu, {t.reviewed} đã duyệt · hướng dẫn v{t.prompt_version} đang dùng · độ chính xác{" "}
            <b className={pctTone(t.accuracy)}>{pct(t.accuracy)}</b>
          </div>
        </div>
        {editor ? (
          <KBMenu t={t} reload={load}>
            {(open) => (
              <button className="btn btn-text" title="Thêm vào hoặc bỏ khỏi thư viện" onClick={open}>
                <Icon name="library_add" size={18} /> {used.length ? `Dùng ở ${used.length} thư viện` : "Chưa dùng ở thư viện nào"}
              </button>
            )}
          </KBMenu>
        ) : (
          <span className="text-xs text-muted">{used.map((k) => k.name).join(", ") || "Chưa dùng ở thư viện nào"}</span>
        )}
      </div>
      <nav className="flex flex-none gap-1 overflow-x-auto border-b border-line px-4" aria-label="Mục của loại giấy tờ">
        {(
          [
            ["samples", "fact_check", `Tài liệu mẫu (${samples.length})`],
            ["prompt", "edit_note", "Trường & hướng dẫn AI"],
          ] as const
        ).map(([k, ic, l]) => (
          <button key={k} className={"tab inline-flex items-center gap-2 " + (tab === k ? "tab-active" : "")} aria-current={tab === k ? "page" : undefined} onClick={() => setTab(k)}>
            <Icon name={ic} size={20} fill={tab === k} />
            {l}
          </button>
        ))}
      </nav>
      {tab === "samples" ? (
        <SamplesTab t={t} samples={samples} jobs={jobs} editor={editor} reload={load} />
      ) : (
        <PromptTab t={t} samples={samples} prompts={prompts} jobs={jobs} editor={editor} reload={load} />
      )}
    </div>
  );
}

// ---------------------------------------------------------------- samples

function SampleBadge({ s }: { s: DocTypeSample }) {
  if (s.status === "processing")
    return (
      <span className="badge">
        <Spinner className="size-2.5 border-[1.5px]" /> đang xử lý
      </span>
    );
  if (s.status === "failed") return <span className="badge badge-err">không đọc được</span>;
  if (s.truth) return <span className={"badge " + (s.score === 1 ? "badge-ok" : s.score == null ? "" : "badge-err")}>đã duyệt{s.score != null && ` · ${pct(s.score)}`}</span>;
  if (s.ran) return <span className="badge badge-warn">chưa duyệt</span>;
  return <span className="badge">chưa chạy thử</span>;
}

function SamplesTab({ t, samples, jobs, editor, reload }: { t: DocTypeView; samples: DocTypeSample[]; jobs: DocTypeJob[]; editor: boolean; reload: () => void }) {
  const [sp, setSp] = useSearchParams();
  const { toast, fail } = useToast();
  const fileRef = useRef<HTMLInputElement>(null);
  const [uploading, setUploading] = useState<number | null>(null);
  const sid = sp.get("s");
  const cur = samples.find((x) => x.id === sid) ?? samples[0];
  const pick = (id: string) =>
    setSp(
      (p) => {
        p.set("s", id);
        return p;
      },
      { replace: true },
    );
  const upload = async (files: File[]) => {
    if (!files.length) return;
    setUploading(0);
    try {
      const added = await uploadSamples(t.name, files, setUploading);
      toast(`Đã nhận ${added.length} mẫu, đang xử lý`);
      reload();
      if (added[0]) pick(added[0].id);
    } catch (e) {
      fail(e);
    } finally {
      setUploading(null);
    }
  };
  const picker = (
    <input
      ref={fileRef}
      type="file"
      multiple
      accept="application/pdf,image/*"
      className="hidden"
      onChange={(e) => {
        upload([...(e.target.files ?? [])]);
        e.target.value = "";
      }}
    />
  );
  if (!samples.length)
    return (
      <Empty icon="upload_file" title="Chưa có tài liệu mẫu">
        Tải lên vài {t.title.toLowerCase()} khác bố cục, rồi chạy thử và duyệt kết quả AI đọc được.
        {editor && (
          <div>
            <button className="btn btn-primary mt-5" disabled={uploading != null} onClick={() => fileRef.current?.click()}>
              {uploading != null ? <Spinner className="size-4 border-2" /> : <Icon name="upload_file" size={18} />} Tải mẫu lên
            </button>
            {picker}
          </div>
        )}
      </Empty>
    );
  return (
    <div className="flex min-h-0 flex-1 max-lg:flex-col">
      <aside className="flex flex-none flex-col gap-1 overflow-auto border-r border-line p-2 lg:w-64 max-lg:max-h-48">
        <div className="flex items-center pb-1 pl-2">
          <span className="flex-1 text-xs font-medium text-muted">Tài liệu mẫu</span>
          {editor && (
            <button className="btn-icon btn-sm" title="Tải mẫu lên (PDF, ảnh scan; chọn được nhiều file)" aria-label="Tải mẫu lên" disabled={uploading != null} onClick={() => fileRef.current?.click()}>
              {uploading != null ? <Spinner className="size-4 border-2" /> : <Icon name="add" size={20} />}
            </button>
          )}
          {picker}
        </div>
        {samples.map((x) => (
          <button key={x.id} onClick={() => pick(x.id)} className={"flex flex-col gap-1 rounded-xl px-3 py-2 text-left " + (x.id === cur?.id ? "bg-accent-soft text-on-accent-soft" : "text-fg hover:bg-fg/5")}>
            <span className="flex w-full items-center gap-2 text-sm">
              <FileIcon name={x.file_name} mime={x.mime_type} size={16} />
              <span className="min-w-0 flex-1 truncate">{x.file_name}</span>
            </span>
            <span className="flex items-center gap-2 text-xs">
              <SampleBadge s={x} />
            </span>
          </button>
        ))}
        <div className="mt-3 rounded-xl bg-surface-2 p-3 text-xs text-muted">
          Mẫu <b className="font-medium text-fg">đã duyệt</b> là dữ liệu đúng để chấm điểm và cải thiện hướng dẫn AI. Nên có từ 3 mẫu khác bố cục trở lên.
        </div>
      </aside>
      {cur && (
        <SampleView
          key={cur.id}
          t={t}
          brief={cur}
          running={jobs.some((j) => j.kind === "run" && j.status === "running" && (j.sample_ids ?? []).includes(cur.id))}
          failed={jobs.find((j) => j.kind === "run" && j.status === "failed" && (j.sample_ids ?? []).includes(cur.id))}
          editor={editor}
          reload={reload}
        />
      )}
    </div>
  );
}

function SampleView({ t, brief, running, failed, editor, reload }: { t: DocTypeView; brief: DocTypeSample; running: boolean; failed?: DocTypeJob; editor: boolean; reload: () => void }) {
  const { toast, fail } = useToast();
  const [s, setS] = useState<DocTypeSample | null>(null);
  const [view, setView] = useState<"image" | "md" | "ocr">("image");
  const [page, setPage] = useState(1);
  const [focus, setFocus] = useState<string | null>(null);
  const [edits, setEdits] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  // The list says when the sample is read or a run ended: then the detail is read again.
  const stamp = `${brief.status}|${brief.ran}|${brief.score}|${running}|${brief.reviewed_at}`;
  useEffect(() => {
    getSample(t.name, brief.id).then(setS, fail);
  }, [t.name, brief.id, stamp, fail]);

  const v = t.prompt_version;
  const run = s?.runs?.find((r) => r.version === v);
  const out = run?.output ?? {};
  const expected = (k: string) => edits[k] ?? (s?.truth ? (s.truth[k] ?? "") : (out[k]?.value ?? ""));
  const nEdit = Object.keys(edits).filter((k) => norm(edits[k]) !== norm(s?.truth ? s.truth[k] : out[k]?.value)).length;
  const okCount = t.fields.filter((f) => norm(out[f.key]?.value) === norm(s?.truth?.[f.key])).length;
  const pg = s?.pages.find((p) => p.n === page) ?? s?.pages[0];
  const focusLine = focus && out[focus]?.page === pg?.n ? out[focus]?.line : undefined;
  const runFailed = failed && failed.version === v && (!run || (failed.finished_at ?? "") > run.ran_at) ? failed : undefined;

  const startRun = async () => {
    try {
      await runSamples(t.name, [brief.id]);
      reload();
    } catch (e) {
      fail(e);
    }
  };
  const save = async () => {
    setBusy(true);
    try {
      await saveTruth(t.name, brief.id, Object.fromEntries(t.fields.map((f) => [f.key, expected(f.key).trim()])));
      setEdits({});
      toast("Đã lưu làm dữ liệu mẫu");
      reload();
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  };
  const remove = async () => {
    if (!confirm(`Xoá mẫu “${brief.file_name}”?`)) return;
    try {
      await deleteSample(t.name, brief.id);
      toast("Đã xoá mẫu");
      reload();
    } catch (e) {
      fail(e);
    }
  };

  let viewer: ReactNode;
  if (brief.status === "processing" || !s)
    viewer = (
      <div className="mx-auto mt-10 flex max-w-sm flex-col items-center gap-3 rounded-2xl bg-surface-2 p-6 text-center">
        <Spinner className="size-6" />
        <div className="text-sm font-medium text-fg">Đang xử lý {brief.file_name}</div>
        <div className="text-xs text-muted">Xong là xem và chạy thử được.</div>
      </div>
    );
  else if (brief.status === "failed") viewer = <Empty icon="error" title="Không đọc được tài liệu này">Thử tải lại bản rõ hơn hoặc định dạng khác.</Empty>;
  else if (!pg) viewer = <Empty icon="draft" title="Tài liệu không có trang nào" />;
  else if (view === "md")
    viewer = (
      <div className="mx-auto max-w-180">
        <Role icon="auto_awesome" title="Nội dung" tag="nguồn chính" tone="badge-ok">
          AI xem ảnh trang rồi chép lại toàn bộ nội dung, giữ tiêu đề và bảng. Khi lấy thông tin, AI dựa vào bản này trước.
        </Role>
        <div className="rounded-xl border border-line bg-surface p-6">{pg.markdown ? <Markdown text={pg.markdown} className="md text-[14px]" /> : <p className="text-sm text-subtle">Trang này không có nội dung.</p>}</div>
      </div>
    );
  else if (view === "ocr")
    viewer = (
      <div className="mx-auto max-w-180">
        <Role icon="text_fields" title="Các dòng" tag="để đối chiếu" tone="">
          Máy nhận dạng chữ theo từng dòng, kèm mức độ chắc chắn. AI chỉ dùng để kiểm lại chữ số khi phần Nội dung không rõ. Dòng dưới 70% bị bỏ qua.
        </Role>
        <div className="overflow-hidden rounded-xl border border-line bg-surface">
          {pg.lines.map((l) => {
            const low = l.conf > 0 && l.conf < 0.7;
            return (
              <div key={l.n} className={"grid grid-cols-[56px_1fr_auto] items-center gap-3 border-b border-line px-4 py-2 text-[13.5px] last:border-b-0 " + (focusLine === l.n ? "bg-warn-soft" : "")}>
                <span className="text-xs text-subtle">Dòng {l.n}</span>
                <span className={low ? "text-subtle line-through" : "text-fg"}>{l.text}</span>
                <span className={"flex items-center gap-2 text-xs " + (low ? "text-err" : "text-muted")}>
                  {low && <span className="badge badge-err">bỏ qua</span>}
                  <span className="w-9 text-right tabular-nums">{Math.round(l.conf * 100)}%</span>
                </span>
              </div>
            );
          })}
          {!pg.lines.length && <p className="px-4 py-3 text-sm text-subtle">Trang này không có dòng nào.</p>}
        </div>
      </div>
    );
  else {
    const line = pg.lines.find((l) => l.n === focusLine);
    viewer = (
      <div className="mx-auto max-w-180 overflow-hidden rounded-xl border border-line bg-surface shadow-1">
        <PageImage
          docId=""
          pageNo={pg.n}
          path={samplePagePath(t.name, brief.id, pg.n)}
          width={pg.width}
          height={pg.height}
          boxes={line?.bbox ? [{ key: "hl", bbox: { x0: line.bbox[0], y0: line.bbox[1], x1: line.bbox[2], y1: line.bbox[3] }, className: "hl-box" }] : []}
          scrollTo="hl-box"
        />
      </div>
    );
  }

  let result: ReactNode;
  if (brief.status !== "ready") result = <div className="px-4 py-10 text-center text-sm text-subtle">Đợi xử lý xong để chạy thử.</div>;
  else if (running)
    result = (
      <div className="flex flex-col items-center gap-3 px-4 py-10 text-center text-sm text-muted">
        <Spinner className="size-6" />
        AI đang đọc theo hướng dẫn v{v}…
      </div>
    );
  else if (!run)
    result = (
      <div className="px-4 py-10 text-center text-sm text-muted">
        {runFailed ? (
          <div className="mb-2 rounded-xl bg-err-soft px-3 py-2 text-err">
            Lần chạy thử lúc {fmtDate(runFailed.finished_at)} không xong: {runFailed.error || "AI chưa trả lời được"}.
          </div>
        ) : null}
        Chưa chạy thử hướng dẫn v{v} trên mẫu này.
        {editor && (
          <div>
            <button className="btn btn-primary mt-4" onClick={startRun}>
              <Icon name="play_arrow" size={18} /> Chạy thử
            </button>
          </div>
        )}
      </div>
    );
  else
    result = (
      <div className="flex flex-col gap-2 p-3">
        {!t.fields.length && <p className="px-1 text-sm text-muted">Loại này chưa có trường nào. Thêm trường ở mục Trường &amp; hướng dẫn AI.</p>}
        {t.fields.map((f) => {
          const judged = !!s?.truth || edits[f.key] !== undefined;
          const ok = norm(out[f.key]?.value) === norm(expected(f.key));
          return (
            <div key={f.key} className={"rounded-xl border px-3 py-2 " + (focus === f.key ? "border-accent bg-accent/5" : "border-line")} onClick={() => (setFocus(f.key), out[f.key]?.page && setPage(out[f.key].page!))}>
              <div className="flex items-center gap-2 text-xs">
                <span className="font-medium text-fg">{f.label}</span>
                <span className="flex-1" />
                <span title={!judged ? "Chưa duyệt" : ok ? "Khớp giá trị đúng" : "Khác giá trị đúng"}>
                  {!judged ? <Icon name="radio_button_unchecked" size={20} className="text-subtle" /> : ok ? <Icon name="check_circle" size={20} fill className="text-ok" /> : <Icon name="cancel" size={20} fill className="text-err" />}
                </span>
              </div>
              <input className="input mt-1 h-9 w-full" value={expected(f.key)} readOnly={!editor} placeholder="(để trống)" onChange={(e) => setEdits((x) => ({ ...x, [f.key]: e.target.value }))} aria-label={`Giá trị đúng của ${f.label}`} />
              {judged && !ok && (
                <div className="mt-1 text-xs text-err">
                  AI trả về: <s>{out[f.key]?.value || "(để trống)"}</s>
                </div>
              )}
            </div>
          );
        })}
      </div>
    );

  return (
    <>
      <section className="flex min-h-0 min-w-0 flex-1 flex-col">
        <div className="flex flex-none flex-wrap items-center gap-2 px-4 py-2">
          <span className="min-w-0 flex-1 truncate text-sm text-fg">{brief.file_name}</span>
          {s && s.pages.length > 1 && (
            <select className="input h-8 w-28 text-[13px]" value={pg?.n ?? 1} onChange={(e) => setPage(+e.target.value)} aria-label="Trang">
              {s.pages.map((p) => (
                <option key={p.n} value={p.n}>
                  Trang {p.n}
                </option>
              ))}
            </select>
          )}
          <div className="flex rounded-full border border-outline">
            {(
              [
                ["image", "Ảnh gốc"],
                ["md", "Nội dung"],
                ["ocr", "Các dòng"],
              ] as const
            ).map(([k, l]) => (
              <button
                key={k}
                className={"h-8 px-4 text-[13px] first:rounded-l-full last:rounded-r-full " + (view === k ? "bg-accent-soft font-medium text-on-accent-soft" : "text-muted hover:bg-fg/8")}
                disabled={brief.status !== "ready"}
                aria-pressed={view === k}
                onClick={() => setView(k)}
              >
                {l}
              </button>
            ))}
          </div>
          {editor && (
            <button className="btn-icon btn-sm" title="Xoá mẫu" aria-label="Xoá mẫu" onClick={remove}>
              <Icon name="delete" size={18} />
            </button>
          )}
        </div>
        <div className="min-h-0 flex-1 overflow-auto bg-surface-2/50 p-4">{viewer}</div>
      </section>
      <aside className="flex w-full flex-none flex-col border-l border-line lg:w-96">
        <div className="flex flex-none items-center gap-2 border-b border-line px-4 py-2">
          <div className="min-w-0 flex-1">
            <div className="text-sm font-medium text-fg">Kết quả trích xuất</div>
            <div className="truncate text-xs text-muted">
              hướng dẫn v{v}
              {run && " · chạy " + fmtDate(run.ran_at)}
              {run && runFailed && <span className="text-err"> · lần chạy sau không xong</span>}
            </div>
          </div>
          {editor && (
            <button className="btn btn-sm" disabled={brief.status !== "ready" || running} onClick={startRun}>
              <Icon name="play_arrow" size={18} /> Chạy thử
            </button>
          )}
        </div>
        <div className="min-h-0 flex-1 overflow-auto">{result}</div>
        {editor && (
          <div className="flex flex-none items-center gap-2 border-t border-line px-4 py-3">
            <span className="min-w-0 flex-1 text-xs text-muted">
              {s?.truth && run ? (
                <>
                  <b className={pctTone(t.fields.length ? okCount / t.fields.length : null)}>
                    {okCount}/{t.fields.length}
                  </b>{" "}
                  trường đúng với v{v}
                  {nEdit > 0 && ` · ${nEdit} giá trị đúng vừa sửa`}
                </>
              ) : run ? (
                "Sửa các ô sai rồi lưu để thành dữ liệu mẫu"
              ) : (
                ""
              )}
            </span>
            <button className="btn btn-primary btn-sm" disabled={busy || !run || running || (!!s?.truth && !nEdit)} onClick={save}>
              <Icon name="verified" size={18} /> Lưu làm dữ liệu mẫu
            </button>
          </div>
        )}
      </aside>
    </>
  );
}

function Role({ icon, title, tag, tone, children }: { icon: string; title: string; tag: string; tone: string; children: ReactNode }) {
  return (
    <div className="mb-3 flex gap-3 rounded-xl bg-surface p-3 shadow-1">
      <Icon name={icon} size={22} className="mt-0.5 flex-none text-accent" />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2 text-sm font-medium text-fg">
          {title}
          <span className={"badge " + tone}>{tag}</span>
        </div>
        <p className="mt-0.5 text-xs text-muted">{children}</p>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------- instructions

function PromptTab({ t, samples, prompts, jobs, editor, reload }: { t: DocTypeView; samples: DocTypeSample[]; prompts: DocTypePrompt[]; jobs: DocTypeJob[]; editor: boolean; reload: () => void }) {
  const { toast, fail } = useToast();
  const cur = prompts.find((p) => p.current) ?? prompts[0];
  const [draft, setDraft] = useState<{ instruction: string; rules: string[]; shots: PromptShot[]; fields: NewField[] } | null>(null);
  const [busy, setBusy] = useState(false);
  const [newRule, setNewRule] = useState("");
  const [fullText, setFullText] = useState<string | null>(null);
  const [hl, setHl] = useState<string | null>(null);
  const reset = useCallback(() => {
    if (cur) setDraft({ instruction: cur.instruction, rules: [...cur.rules], shots: [...cur.shots], fields: cur.fields.map((f) => ({ ...f })) });
    setFullText(null);
  }, [cur]);
  useEffect(() => {
    reset();
  }, [reset]);

  const reviewed = samples.filter((s) => s.truth && s.status === "ready");
  const ready = samples.filter((s) => s.status === "ready");
  const optimizing = jobs.find((j) => j.kind === "optimize" && j.status === "running");
  const runningRun = jobs.some((j) => j.kind === "run" && j.status === "running");
  const lastOpt = jobs.find((j) => j.kind === "optimize" && j.status !== "running");
  const proposal = lastOpt?.status === "done" && lastOpt.result && !lastOpt.result.none && lastOpt.version === cur?.version ? lastOpt : undefined;
  const dirty = !!draft && !!cur && JSON.stringify([draft.instruction, draft.rules, draft.shots, draft.fields.map(({ key, label, value_type, description }) => ({ key, label, value_type, description: description || undefined }))]) !==
    JSON.stringify([cur.instruction, cur.rules, cur.shots, cur.fields.map(({ key, label, value_type, description }) => ({ key, label, value_type, description: description || undefined }))]);
  const label = (k: string) => (proposal?.result?.fields ?? t.fields).find((f) => f.key === k)?.label ?? k;
  const fileOf = (id: string) => samples.find((s) => s.id === id)?.file_name ?? "mẫu đã xoá";

  // Per field and reviewed sample, whether the version in use read it right.
  const [details, setDetails] = useState<Record<string, DocTypeSample>>({});
  const reviewedKey = reviewed.map((s) => s.id + s.score).join();
  useEffect(() => {
    Promise.all(reviewed.map((s) => getSample(t.name, s.id))).then((xs) => setDetails(Object.fromEntries(xs.map((x) => [x.id, x]))), () => {});
  }, [t.name, reviewedKey, cur?.version]); // eslint-disable-line react-hooks/exhaustive-deps

  const saveAndRun = async () => {
    if (!draft) return;
    setBusy(true);
    try {
      const fields = draft.fields.filter((f) => f.label.trim()).map((f) => ({ key: f.key, label: f.label.trim(), value_type: f.value_type || "string", description: f.description?.trim() || undefined }));
      const p = await createPrompt(t.name, { instruction: draft.instruction, rules: draft.rules, shots: draft.shots, fields, note: "Sửa tay", use: true });
      if (ready.length) await runSamples(t.name);
      toast(`Đã lưu hướng dẫn v${p.version}${ready.length ? ` và chạy thử trên ${ready.length} mẫu` : ""}`);
      reload();
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  };
  const optimize = async () => {
    try {
      await optimizeDocType(t.name, cur?.version ?? 0);
      reload();
    } catch (e) {
      fail(e);
    }
  };
  const apply = async () => {
    const r = proposal?.result;
    if (!r || !proposal) return;
    setBusy(true);
    try {
      const p = await createPrompt(t.name, { instruction: r.instruction, rules: r.rules, shots: r.shots, fields: r.fields, note: `Tối ưu tự động: ${r.patterns.length} quy tắc, ${r.shots.length - (cur?.shots.length ?? 0)} ví dụ`, use: true });
      await dropDocTypeJob(t.name, proposal.id);
      if (ready.length) await runSamples(t.name);
      toast(`Đã áp dụng hướng dẫn v${p.version} · độ chính xác ${pct(r.after)}`);
      setHl(null);
      reload();
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  };
  const drop = async () => {
    if (!proposal) return;
    await dropDocTypeJob(t.name, proposal.id).catch(fail);
    setHl(null);
    reload();
  };

  if (!cur || !draft) return <Loading />;
  const nextV = Math.max(...prompts.map((p) => p.version)) + 1;

  const toolbar = (
    <div className="flex flex-none flex-wrap items-center gap-2 border-b border-line px-4 py-2">
      <span className="text-sm font-medium text-fg">Hướng dẫn v{cur.version}</span>
      <span className="text-sm text-muted">
        · độ chính xác <b className={"tabular-nums " + pctTone(cur.accuracy)}>{pct(cur.accuracy)}</b>{" "}
        <span className="text-xs">
          trên {reviewed.length} mẫu × {cur.fields.length} trường
        </span>
      </span>
      <span className="flex-1" />
      {editor && !proposal && (
        <>
          <button className="btn btn-tonal btn-sm" disabled={!reviewed.length || !!optimizing || dirty} title={!reviewed.length ? "Cần mẫu đã duyệt" : dirty ? "Lưu thay đổi trước" : undefined} onClick={optimize}>
            <Icon name="auto_awesome" size={18} /> Tối ưu bằng AI
          </button>
          <button className="btn btn-primary btn-sm" disabled={busy || !!optimizing || runningRun || !dirty} title={`Lưu thành v${nextV}${ready.length ? ` rồi chạy trên ${ready.length} mẫu` : ""}`} onClick={saveAndRun}>
            {busy || runningRun ? <Spinner className="size-4 border-2" /> : <Icon name="play_arrow" size={18} />} Lưu &amp; chạy thử
          </button>
        </>
      )}
    </div>
  );

  let left: ReactNode, right: ReactNode, banner: ReactNode = null;
  if (proposal?.result) {
    const r = proposal.result;
    const nAdd = r.diff.filter((o) => o.op === "+").length,
      nDel = r.diff.filter((o) => o.op === "-").length;
    const newShots = r.shots.filter((x) => !cur.shots.some((y) => y.sample_id === x.sample_id));
    banner = (
      <div className="flex flex-none flex-wrap items-center gap-3 border-b border-accent/30 bg-accent-soft/40 px-4 py-3">
        <Icon name="auto_awesome" size={22} className="text-accent" />
        <div className="min-w-0 flex-1">
          <div className="text-sm font-medium text-fg">
            Đề xuất hướng dẫn v{nextV}: độ chính xác <span className={pctTone(r.before)}>{pct(r.before)}</span> → <span className={pctTone(r.after)}>{pct(r.after)}</span>
          </div>
          <div className="text-xs text-muted">
            <span className="text-ok">+{nAdd} dòng</span>
            {nDel > 0 && (
              <>
                {" "}
                · <span className="text-err">−{nDel} dòng</span>
              </>
            )}{" "}
            · {r.patterns.length} quy tắc · {newShots.length} ví dụ · chấm trên {reviewed.length} mẫu đã duyệt
          </div>
        </div>
        {editor && (
          <>
            <button className="btn btn-text btn-sm" disabled={busy} onClick={drop}>
              Bỏ
            </button>
            <button className="btn btn-primary btn-sm" disabled={busy} onClick={apply}>
              <Icon name="check" size={18} /> Áp dụng thành v{nextV}
            </button>
          </>
        )}
      </div>
    );
    const num = (n?: number) => <span className="w-8 flex-none pr-2 text-right text-subtle select-none">{n ?? ""}</span>;
    left = (
      <div className="min-h-0 min-w-0 flex-1 overflow-auto py-2 text-[13px] leading-6">
        {r.diff.map((o, i) => (
          <div
            key={i}
            ref={(el) => {
              if (el && hl && o.why === hl && !r.diff.slice(0, i).some((x) => x.why === hl)) el.scrollIntoView({ block: "center", behavior: "smooth" });
            }}
            className={"flex " + (o.op === "+" ? "bg-ok-soft/70 text-fg" : o.op === "-" ? "bg-err-soft/70 text-fg line-through decoration-err/40" : "text-muted") + (hl && o.why === hl ? " !bg-warn-soft outline outline-1 outline-warn" : "")}
          >
            {num(o.a)}
            {num(o.b)}
            <span className={"w-4 flex-none select-none " + (o.op === "+" ? "text-ok" : o.op === "-" ? "text-err" : "")}>{o.op === "=" ? "" : o.op}</span>
            <span className="min-w-0 flex-1 pr-3 whitespace-pre-wrap">
              {o.t || " "}
              {o.why?.startsWith("f:") && <span className="ml-2 rounded bg-surface px-1.5 font-sans text-[10.5px] text-muted">{label(o.why.slice(2))}</span>}
            </span>
          </div>
        ))}
      </div>
    );
    const reason = (key: string, head: ReactNode, body: ReactNode) => (
      <button key={key} className={"block w-full rounded-xl border p-3 text-left transition-colors " + (hl === key ? "border-warn bg-warn-soft/50" : "border-line hover:bg-fg/4")} aria-pressed={hl === key} onClick={() => setHl(hl === key ? null : key)}>
        {head}
        {body}
      </button>
    );
    right = (
      <>
        <div>
          <h4 className="mb-1 text-sm font-medium text-fg">Vì sao thay đổi</h4>
          <p className="mb-2 text-xs text-muted">Bấm một mục để tô các dòng tương ứng trong hướng dẫn.</p>
          <div className="flex flex-col gap-2">
            {r.patterns.map((x) =>
              reason(
                "f:" + x.field,
                <span className="flex flex-wrap items-center gap-2 text-sm">
                  <b className="font-medium text-fg">{label(x.field)}</b>
                  <span className="badge badge-err">
                    sai {new Set(x.samples).size}/{reviewed.length} mẫu
                  </span>
                </span>,
                <span className="mt-1 block text-xs text-muted">{x.finding}</span>,
              ),
            )}
            {newShots.map((x) =>
              reason(
                "s:" + x.sample_id,
                <span className="flex items-center gap-2 text-sm">
                  <FileIcon name={fileOf(x.sample_id)} size={16} />
                  <b className="min-w-0 truncate font-medium text-fg">Ví dụ: {fileOf(x.sample_id)}</b>
                </span>,
                <span className="mt-1 block text-xs text-muted">{x.why}</span>,
              ),
            )}
          </div>
        </div>
        <div>
          <h4 className="mb-2 text-sm font-medium text-fg">Chạy thử trên mẫu đã duyệt</h4>
          <table className="w-full text-sm">
            <thead>
              <tr className="text-xs text-muted">
                <th className="h-8 border-b border-line text-left font-medium">Trường</th>
                <th className="border-b border-line text-right font-medium">v{cur.version}</th>
                <th className="border-b border-line text-right font-medium">v{nextV}</th>
              </tr>
            </thead>
            <tbody>
              {r.by_field.map((f) => {
                const worse = r.worse.filter((w) => w.key === f.key);
                return (
                  <tr key={f.key}>
                    <td className="h-8 border-b border-line text-fg">
                      {label(f.key)}
                      {worse.length > 0 && (
                        <div className="pb-1 text-[11px] leading-4 text-err">
                          giảm ở {worse.map((w) => fileOf(w.sample_id)).join(", ")}: “{worse[0].value || "(để trống)"}” thay vì “{worse[0].truth || "(để trống)"}”
                        </div>
                      )}
                    </td>
                    <td className={"border-b border-line text-right align-top leading-8 tabular-nums " + pctTone(f.before)}>{pct(f.before)}</td>
                    <td className={"border-b border-line text-right align-top leading-8 font-medium tabular-nums " + pctTone(f.after)}>{pct(f.after)}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </>
    );
  } else {
    const shotOn = (id: string) => draft.shots.some((x) => x.sample_id === id);
    left = (
      <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-5 overflow-auto p-6">
        <section className="rounded-2xl border border-line">
          <h3 className="px-5 pt-4 pb-1 text-sm font-medium text-fg">Trường cần lấy</h3>
          <p className="px-5 pb-3 text-xs text-muted">Là các cột của bảng Excel. Gõ xong một trường, dòng mới tự hiện; dán nhiều dòng để thêm nhiều trường.</p>
          <FieldsTable fields={draft.fields} readOnly={!editor} onChange={(fields) => setDraft({ ...draft, fields })} />
        </section>
        <section className="flex flex-col gap-2">
          <h3 className="text-sm font-medium text-fg">Hướng dẫn chung</h3>
          <textarea className="input font-body" rows={5} readOnly={!editor} value={draft.instruction} onChange={(e) => setDraft({ ...draft, instruction: e.target.value })} />
        </section>
        <section className="flex flex-col gap-2">
          <div className="flex items-center">
            <h3 className="flex-1 text-sm font-medium text-fg">Quy tắc theo trường</h3>
            <span className="text-xs text-subtle">thêm tay hoặc do “Tối ưu bằng AI” đề xuất</span>
          </div>
          {draft.rules.map((r, i) => (
            <div key={i} className="flex items-center gap-2">
              <input className="input h-9 flex-1" readOnly={!editor} value={r} onChange={(e) => setDraft({ ...draft, rules: draft.rules.map((x, j) => (j === i ? e.target.value : x)) })} />
              {editor && (
                <button className="btn-icon btn-sm" aria-label="Xoá quy tắc" onClick={() => setDraft({ ...draft, rules: draft.rules.filter((_, j) => j !== i) })}>
                  <Icon name="delete" size={18} />
                </button>
              )}
            </div>
          ))}
          {editor && (
            <input
              className="input h-9"
              value={newRule}
              onChange={(e) => setNewRule(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && newRule.trim()) (setDraft({ ...draft, rules: [...draft.rules, newRule.trim()] }), setNewRule(""));
              }}
              onBlur={() => newRule.trim() && (setDraft({ ...draft, rules: [...draft.rules, newRule.trim()] }), setNewRule(""))}
              placeholder="Gõ một quy tắc rồi Enter, ví dụ: Số tiền chỉ gồm chữ số, bỏ “đồng”"
            />
          )}
        </section>
        <section className="flex flex-col gap-2">
          <h3 className="text-sm font-medium text-fg">Ví dụ cho AI</h3>
          <div className="flex flex-wrap gap-2">
            {reviewed.map((s) => (
              <button
                key={s.id}
                className={"chip " + (shotOn(s.id) ? "chip-on" : "")}
                disabled={!editor}
                aria-pressed={shotOn(s.id)}
                title={draft.shots.find((x) => x.sample_id === s.id)?.why || "Dùng mẫu này làm ví dụ"}
                onClick={() => setDraft({ ...draft, shots: shotOn(s.id) ? draft.shots.filter((x) => x.sample_id !== s.id) : [...draft.shots, { sample_id: s.id }] })}
              >
                {shotOn(s.id) && <Icon name="check" size={18} />}
                {s.file_name}
              </button>
            ))}
            {!reviewed.length && <span className="text-xs text-subtle">Chỉ mẫu đã duyệt mới dùng làm ví dụ.</span>}
          </div>
          <p className="text-xs text-subtle">Mỗi ví dụ gồm nội dung một mẫu và kết quả đúng của nó. Khi chấm điểm chính mẫu đó, AI không được xem ví dụ này để điểm không bị cao giả.</p>
        </section>
        <details className="group rounded-2xl border border-line" onToggle={(e) => (e.currentTarget as HTMLDetailsElement).open && fullText == null && getPromptText(t.name, cur.version).then(setFullText, fail)}>
          <summary className="flex h-11 cursor-pointer list-none items-center gap-2 px-5 text-sm font-medium text-fg">
            <Icon name="expand_more" size={20} className="text-muted transition-transform group-open:rotate-180" />
            <span className="flex-1">Xem toàn văn gửi cho AI (v{cur.version})</span>
            {fullText != null && <span className="text-xs font-normal text-subtle">{fullText.length.toLocaleString("vi-VN")} ký tự</span>}
          </summary>
          {fullText == null ? <Loading /> : <pre className="mx-5 mb-5 rounded-xl bg-surface-2 p-4 font-body text-[13px] leading-6 whitespace-pre-wrap text-fg">{fullText}</pre>}
        </details>
        {editor && dirty && (
          <div className="sticky bottom-0 -mx-6 -mb-6 flex items-center gap-2 border-t border-line bg-surface px-6 py-3">
            <span className="flex-1 text-xs text-muted">Có thay đổi chưa lưu. Lưu thành v{nextV} rồi chạy thử để xem độ chính xác.</span>
            <button className="btn btn-text btn-sm" onClick={reset}>
              Hoàn tác
            </button>
          </div>
        )}
      </div>
    );
    const progress = optimizing ? (
      <div className="flex flex-col gap-2 rounded-xl bg-surface-2 p-4">
        <div className="flex items-center gap-2 text-sm font-medium text-fg">
          <Spinner className="size-4 border-2" /> AI đang cải thiện hướng dẫn v{optimizing.version}…
        </div>
        <div className="text-xs text-muted">Dựa trên {reviewed.length} mẫu đã duyệt. Xong sẽ hiện đề xuất để bạn xem và quyết định.</div>
      </div>
    ) : lastOpt?.status === "done" && lastOpt.result?.none && lastOpt.version === cur.version ? (
      <div className="rounded-xl border border-line p-3 text-sm text-muted">
        Không tìm thấy lỗi lặp lại nào đủ rõ trên {reviewed.length} mẫu đã duyệt. Thêm mẫu khác bố cục rồi thử lại.{" "}
        {editor && (
          <button className="text-accent hover:underline" onClick={() => dropDocTypeJob(t.name, lastOpt.id).then(reload, fail)}>
            Đóng
          </button>
        )}
      </div>
    ) : lastOpt?.status === "failed" && lastOpt.version === cur.version ? (
      <div className="rounded-xl border border-err/40 p-3 text-sm text-err">{lastOpt.error || "Chưa cải thiện được, thử lại sau."}</div>
    ) : null;
    const out = (s: DocTypeSample, k: string) => details[s.id]?.runs?.find((r) => r.version === cur.version)?.output[k]?.value;
    right = (
      <>
        {progress}
        <div>
          <h4 className="mb-1 text-sm font-medium text-fg">Điểm theo trường · v{cur.version}</h4>
          <p className="mb-2 text-xs text-muted">Mỗi dấu là một mẫu đã duyệt; rê chuột để xem giá trị AI và giá trị đúng.</p>
          {reviewed.length ? (
            cur.fields.map((f) => {
              const ran = reviewed.filter((s) => details[s.id]?.runs?.some((r) => r.version === cur.version));
              const ok = ran.filter((s) => norm(out(s, f.key)) === norm(s.truth?.[f.key]));
              const a = ran.length ? ok.length / ran.length : null;
              return (
                <div key={f.key} className="flex items-center gap-2 border-b border-line py-2 last:border-b-0">
                  <span className="min-w-0 flex-1 truncate text-sm text-fg">{f.label}</span>
                  <span className="flex gap-0.5">
                    {ran.map((s) => {
                      const good = ok.includes(s);
                      return (
                        <span key={s.id} title={`${s.file_name}\nAI: ${out(s, f.key) || "(để trống)"}\nĐúng: ${s.truth?.[f.key] || "(để trống)"}`}>
                          <Icon name={good ? "check_circle" : "cancel"} size={16} fill className={good ? "text-ok" : "text-err"} />
                        </span>
                      );
                    })}
                  </span>
                  <span className={"w-11 text-right text-sm font-medium tabular-nums " + pctTone(a)}>{pct(a)}</span>
                </div>
              );
            })
          ) : (
            <div className="text-sm text-subtle">Chưa có mẫu đã duyệt. Duyệt kết quả ở tab Tài liệu mẫu để chấm điểm.</div>
          )}
        </div>
        <Versions t={t} prompts={prompts} editor={editor} reload={reload} />
      </>
    );
  }
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {toolbar}
      {banner}
      <div className="flex min-h-0 flex-1 max-lg:flex-col">
        {left}
        <aside className="flex w-full flex-none flex-col gap-4 overflow-auto border-line p-4 max-lg:border-t lg:w-96 lg:border-l">{right}</aside>
      </div>
    </div>
  );
}

function Versions({ t, prompts, editor, reload }: { t: DocTypeView; prompts: DocTypePrompt[]; editor: boolean; reload: () => void }) {
  const { toast, fail } = useToast();
  const [open, setOpen] = useState(true);
  return (
    <details className="rounded-xl border border-line" open={open} onToggle={(e) => setOpen((e.currentTarget as HTMLDetailsElement).open)}>
      <summary className="flex h-10 cursor-pointer list-none items-center gap-2 px-3 text-sm font-medium text-fg">
        <Icon name="history" size={18} className="text-muted" />
        <span className="flex-1">Phiên bản ({prompts.length})</span>
        <Icon name="expand_more" size={20} className="text-muted" />
      </summary>
      {editor && <p className="px-3 pb-2 text-xs text-muted">Bấm một phiên bản để dùng lại.</p>}
      {prompts.map((x) => {
        const use = () =>
          setCurrentPrompt(t.name, x.version).then(() => (toast(`Đang dùng hướng dẫn v${x.version}`), reload()), fail);
        return (
          <div
            key={x.version}
            className={"group flex items-center gap-2 border-t border-line px-3 py-2 " + (x.current ? "bg-accent-soft/40" : editor ? "cursor-pointer hover:bg-fg/4" : "")}
            role={x.current || !editor ? undefined : "button"}
            tabIndex={x.current || !editor ? undefined : 0}
            title={x.current || !editor ? undefined : `Dùng hướng dẫn v${x.version}`}
            onClick={x.current || !editor ? undefined : use}
            onKeyDown={(e) => !x.current && editor && e.key === "Enter" && use()}
          >
            <span className={"badge " + (x.current ? "badge-info" : "")}>v{x.version}</span>
            <div className="min-w-0 flex-1">
              <div className="truncate text-[13px] text-fg">{x.note}</div>
              <div className="truncate text-[11px] text-muted">
                {x.current ? "đang dùng · " : ""}
                {fmtDate(x.created_at)}
              </div>
            </div>
            <span className={"text-[13px] font-medium tabular-nums " + pctTone(x.accuracy)}>{pct(x.accuracy)}</span>
            {x.current || !editor ? (
              <span className="size-7" />
            ) : (
              <button
                className="btn-icon size-7 opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
                title={`Xoá hướng dẫn v${x.version}`}
                aria-label={`Xoá hướng dẫn v${x.version}`}
                onClick={(e) => {
                  e.stopPropagation();
                  if (confirm(`Xoá hướng dẫn v${x.version} (${x.note})? Không thể hoàn tác.`)) deletePrompt(t.name, x.version).then(() => (toast(`Đã xoá hướng dẫn v${x.version}`), reload()), fail);
                }}
              >
                <Icon name="delete" size={16} />
              </button>
            )}
          </div>
        );
      })}
    </details>
  );
}

// ---------------------------------------------------------------- fields

const VALUE_TYPES: [string, string][] = [
  ["string", "Văn bản"],
  ["number", "Số"],
  ["money", "Số tiền"],
  ["date", "Ngày"],
  ["bool", "Có / Không"],
];
// A field added here: its key follows its name (U65). A saved field keeps its
// key, so the values already extracted under it stay with the column. typed:
// the user picked the value type, so it no longer follows the name.
type NewField = SheetField & { auto?: boolean; typed?: boolean };

// guessType reads the value type off a field name ("Ngày ký" → date, "Tổng
// thanh toán" → money), so the type rarely has to be picked by hand.
const guessType = (label: string) => {
  const c = toCode(label);
  if (/(^|_)(so_luong|so_ngay|ty_le|khoi_luong|trong_luong)(_|$)/.test(c)) return "number";
  if (/(^|_)ngay(_|$)/.test(c)) return "date";
  if (/(^|_)(tien|gia_tri|thanh_toan|thanh_tien|tong|don_gia|doanh_thu)(_|$)/.test(c)) return "money";
  return "string";
};

// The fields of a type (§7.11): the table always ends with one empty row;
// typing in it makes it a field and the next empty row appears, so there is
// no "add" button. Several names pasted at once become one field each.
function FieldsTable({ fields, onChange, readOnly }: { fields: NewField[]; onChange: (f: NewField[]) => void; readOnly?: boolean }) {
  const keyOf = (label: string, taken: string[]) => {
    const k = toCode(label);
    return k ? uniqueCode(k.length < 2 ? k + "_1" : k, taken) : "";
  };
  const named = (f: NewField, label: string, taken: string[]): NewField =>
    f.auto ? { ...f, label, key: keyOf(label, taken), value_type: f.typed ? f.value_type : guessType(label) } : { ...f, label };
  const setLabel = (i: number, label: string) => {
    const taken = fields.filter((_, j) => j !== i).map((x) => x.key);
    if (i >= fields.length) return label && onChange([...fields, named({ key: "", label: "", auto: true }, label, taken)]);
    if (!label && fields[i].auto) return onChange(fields.filter((_, j) => j !== i));
    onChange(fields.map((f, k) => (k === i ? named(f, label, taken) : f)));
  };
  const pasteNames = (i: number, text: string) => {
    const names = text
      .split(/[\r\n\t]+/)
      .map((x) => x.trim())
      .filter(Boolean);
    if (names.length < 2) return false;
    const out = fields.slice(0, i);
    for (const label of names) out.push(named({ key: "", label: "", auto: true }, label, [...out, ...fields.slice(i)].map((x) => x.key)));
    onChange([...out, ...fields.slice(i).filter((f) => f.label)]);
    return true;
  };
  const focusRow = (i: number) => document.querySelector<HTMLInputElement>(`[data-field-name="${i}"]`)?.focus();
  const rows = useMemo(() => (readOnly ? fields : [...fields, null]), [fields, readOnly]);
  return (
    <div className="overflow-x-auto border-t border-line">
      <table className="w-full min-w-160 text-sm">
        <thead>
          <tr className="text-left text-xs text-muted">
            <th className="w-48 py-2 pl-5 font-medium">Tên trường</th>
            <th className="w-36 px-2 font-medium">Mã</th>
            <th className="w-32 px-2 font-medium">Kiểu</th>
            <th className="px-2 font-medium">Mô tả (hướng dẫn AI)</th>
            <th className="w-12" />
          </tr>
        </thead>
        <tbody>
          {rows.map((f, i) => (
            <tr key={i} className="border-t border-line">
              <td className="py-1.5 pl-5">
                <input
                  className="input h-9 w-full"
                  data-field-name={i}
                  readOnly={readOnly}
                  value={f?.label ?? ""}
                  onChange={(e) => setLabel(i, e.target.value)}
                  onPaste={(e) => pasteNames(i, e.clipboardData.getData("text")) && e.preventDefault()}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === "ArrowDown") (e.preventDefault(), focusRow(Math.min(i + 1, fields.length)));
                    if (e.key === "ArrowUp" && i > 0) (e.preventDefault(), focusRow(i - 1));
                  }}
                  placeholder={f ? "Tên trường" : fields.length ? "Gõ tên trường tiếp theo…" : "Gõ tên trường đầu tiên, vd Số hoá đơn"}
                  aria-label={f ? `Tên trường ${i + 1}` : "Tên trường mới"}
                />
              </td>
              <td className="px-2">{f?.key ? <code className="tag max-w-full truncate font-mono">{f.key}</code> : <span className="text-xs text-subtle">tự sinh</span>}</td>
              <td className="px-2">
                {f && (
                  <select className="input h-9 w-full" disabled={readOnly} value={f.value_type || "string"} onChange={(e) => onChange(fields.map((x, k) => (k === i ? { ...x, value_type: e.target.value, typed: true } : x)))} aria-label={`Kiểu của trường ${f.label || i + 1}`}>
                    {VALUE_TYPES.map(([v, l]) => (
                      <option key={v} value={v}>
                        {l}
                      </option>
                    ))}
                  </select>
                )}
              </td>
              <td className="px-2">
                {f && (
                  <input
                    className="input h-9 w-full"
                    readOnly={readOnly}
                    value={f.description ?? ""}
                    onChange={(e) => onChange(fields.map((x, k) => (k === i ? { ...x, description: e.target.value } : x)))}
                    placeholder="Lấy ở đâu, ghi theo dạng nào"
                    title={f.description || undefined}
                    aria-label={`Mô tả của trường ${f.label || i + 1}`}
                  />
                )}
              </td>
              <td className="pr-3 text-right">
                {f && !readOnly && (
                  <button className="btn-icon btn-sm" tabIndex={-1} title="Xoá trường" aria-label={`Xoá trường ${f.label || i + 1}`} onClick={() => onChange(fields.filter((_, j) => j !== i))}>
                    <Icon name="delete" size={18} />
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
