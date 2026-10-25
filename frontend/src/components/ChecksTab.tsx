import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";

import { compileRuleset, copyRuleset, createRuleset, deleteRuleset, getRuleset, listRulesets, updateRuleset } from "../api/endpoints";
import { canEditPrompts, type CheckRule, type CheckRuleset, type CheckStep, type DocType, type FieldRef, type KnowledgeBase } from "../api/types";
import { typeColor, typeTitle } from "../lib/doctype";
import { useApp } from "./AppContext";
import { useAuth } from "./AuthContext";
import { BackButton } from "./nav";
import { useToast } from "./toast";
import { Empty, Icon, Loading, Menu, Modal, Spinner } from "./ui";

// Cài đặt → Quy tắc đối chiếu (§6.9.9, §7.13): rule sets shared by every
// library, like document types (U105). A rule compares field A of one
// document with field B of another (U104); what is not one comparison is a
// sentence the AI judges. Studio picks one set to run.

// Comparisons by the kind of field A.
const OPS: Record<string, [string, string][]> = {
  text: [
    ["same", "giống với"],
    ["contains", "có chứa"],
    ["diff", "khác với"],
  ],
  money: [
    ["eq", "bằng"],
    ["le", "không lớn hơn"],
    ["ge", "không nhỏ hơn"],
    ["sum_le", "cộng lại không vượt quá"],
  ],
  date: [
    ["before", "trước hoặc cùng ngày"],
    ["after", "sau hoặc cùng ngày"],
    ["same_day", "cùng ngày với"],
  ],
};
const KIND_LABEL: Record<string, string> = { text: "Chữ", money: "Số / tiền", date: "Ngày" };
const KIND_ICON: Record<string, string> = { text: "notes", money: "payments", date: "event" };
const kindOf = (vt?: string) => (vt === "money" || vt === "number" ? "money" : vt === "date" ? "date" : "text");
const opLabel = (op?: string) => Object.values(OPS).flat().find(([k]) => k === op)?.[1] ?? op ?? "";
const LINK_HINTS = ["Cùng tên bên bán với hợp đồng", "Nội dung có nhắc số hoặc tên hợp đồng", "Mặt hàng và số lượng phù hợp với hợp đồng"];
const newId = () => Math.random().toString(36).slice(2, 10);

// The document types a set may meet: those of the libraries that use it (all
// of the user's libraries when it is not used yet), by code.
function typesOf(kbs: KnowledgeBase[], ids: string[]): DocType[] {
  const out: DocType[] = [];
  for (const k of ids.length ? kbs.filter((k) => ids.includes(k.id)) : kbs)
    for (const t of k.config.doc_types ?? []) {
      const have = out.find((x) => x.name === t.name);
      if (!have) out.push({ ...t, fields: [...(t.fields ?? [])] });
      else for (const f of t.fields ?? []) if (!have.fields?.some((x) => x.key === f.key)) have.fields = [...(have.fields ?? []), f];
    }
  return out;
}
const fieldOf = (types: DocType[], r?: FieldRef) => types.find((t) => t.name === r?.type)?.fields?.find((f) => f.key === r?.key);

function FieldChip({ r, types }: { r: FieldRef; types: DocType[] }) {
  return (
    <span className="inline-flex max-w-full items-center gap-1.5 rounded-lg bg-surface-2 px-2 py-0.5 align-middle text-fg">
      <i className="size-2 flex-none rounded-full" style={{ background: typeColor(r.type, types) }} />
      <b className="font-medium">{fieldOf(types, r)?.label ?? r.key}</b>
      <span className="text-muted">của {typeTitle(r.type, types)}</span>
    </span>
  );
}

// A rule as one readable sentence.
export function RuleSentence({ rule, types, anchor }: { rule: CheckRule; types: DocType[]; anchor: string }) {
  if (!rule.a || !rule.b) return <>{rule.text || rule.name}</>;
  if (rule.op === "sum_le")
    return (
      <>
        Tổng <FieldChip r={rule.a} types={types} /> {opLabel(rule.op)} {rule.b.type !== anchor ? "tổng " : ""}
        <FieldChip r={rule.b} types={types} />
      </>
    );
  return (
    <>
      <FieldChip r={rule.a} types={types} /> {opLabel(rule.op)} <FieldChip r={rule.b} types={types} />
    </>
  );
}

// ---------------------------------------------------------------- list

export function ChecksTab() {
  const { kbs } = useApp();
  const { user } = useAuth();
  const editor = canEditPrompts(user);
  const { toast, fail } = useToast();
  const nav = useNavigate();
  const [list, setList] = useState<CheckRuleset[] | null>(null);
  const [filter, setFilter] = useState("all");
  const [creating, setCreating] = useState(false);
  const load = () => listRulesets().then(setList, (e) => (fail(e), setList([])));
  useEffect(() => {
    load();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const shown = (list ?? []).filter((x) => (filter === "all" ? true : filter === "unused" ? !x.kb_ids.length : x.kb_ids.includes(filter)));
  const chip = (v: string, label: string, n: number) => (
    <button key={v} className={"chip " + (filter === v ? "chip-on" : "")} aria-pressed={filter === v} onClick={() => setFilter(v)}>
      {filter === v && <Icon name="check" size={18} />}
      {label} <span className={"tabular-nums " + (filter === v ? "" : "text-subtle")}>{n}</span>
    </button>
  );
  const toggleKB = async (rs: CheckRuleset, k: KnowledgeBase) => {
    const on = rs.kb_ids.includes(k.id);
    try {
      await updateRuleset({ ...rs, kb_ids: on ? rs.kb_ids.filter((x) => x !== k.id) : [...rs.kb_ids, k.id] });
      toast(on ? `Đã bỏ “${rs.name}” khỏi ${k.name}` : `Đã thêm “${rs.name}” vào ${k.name}`);
      load();
    } catch (e) {
      fail(e);
    }
  };

  return (
    <>
      <header className="flex flex-wrap items-end gap-x-6 gap-y-3">
        <div className="min-w-60 flex-1">
          <h2 className="text-base font-medium text-fg">Quy tắc đối chiếu</h2>
          <p className="mt-1 max-w-2xl text-sm text-muted">Mỗi bộ quy tắc gồm các phép so thông tin giữa các giấy tờ, dùng chung cho mọi thư viện. Khi đối chiếu ở Studio, người dùng chọn một bộ để chạy.</p>
        </div>
        {editor && (
          <button className="btn btn-primary" onClick={() => setCreating(true)}>
            <Icon name="add" size={18} /> Bộ quy tắc mới
          </button>
        )}
      </header>
      {!editor && (
        <div className="rounded-xl bg-surface-2 px-4 py-3 text-sm text-muted">
          <Icon name="lock" size={16} className="mr-1 align-[-3px]" />
          Chỉ người được sửa hướng dẫn AI hoặc quản trị mới sửa được quy tắc. Bạn vẫn chạy đối chiếu ở Studio của hồ sơ.
        </div>
      )}
      <div className="flex flex-wrap items-center gap-2">
        {chip("all", "Tất cả", list?.length ?? 0)}
        {kbs.map((k) => chip(k.id, k.name, (list ?? []).filter((x) => x.kb_ids.includes(k.id)).length))}
        {chip("unused", "Chưa dùng", (list ?? []).filter((x) => !x.kb_ids.length).length)}
      </div>
      {!list ? (
        <Loading />
      ) : (
        <div className="overflow-x-auto rounded-2xl border border-line">
          <table className="w-full border-collapse text-sm">
            <thead>
              <tr className="text-left text-[13px] text-muted">
                <th className="h-11 border-b border-line pl-4 font-medium">Bộ quy tắc</th>
                <th className="border-b border-line px-3 font-medium whitespace-nowrap">Quy tắc</th>
                <th className="border-b border-line px-3 font-medium max-lg:hidden">Giấy tờ được so</th>
                <th className="border-b border-line px-3 font-medium max-md:hidden">Dùng ở thư viện</th>
                <th className="w-24 border-b border-line" />
              </tr>
            </thead>
            <tbody>
              {shown.map((rs) => {
                const types = typesOf(kbs, rs.kb_ids);
                const used = [...new Set([rs.anchor, ...rs.rules.flatMap((r) => (r.a && r.b ? [r.a.type, r.b.type] : []))].filter(Boolean))];
                const on = rs.rules.filter((r) => !r.off).length;
                return (
                  <tr key={rs.id} className="cursor-pointer transition-colors hover:bg-surface-2" onClick={() => nav(`/settings/checks/${rs.id}`)}>
                    <td className="border-b border-line py-2.5 pl-4 align-middle">
                      <div className="font-medium text-fg">{rs.name}</div>
                      {rs.description && <div className="text-xs text-muted">{rs.description}</div>}
                    </td>
                    <td className="border-b border-line px-3 align-middle whitespace-nowrap text-muted tabular-nums">
                      {rs.rules.length}
                      {on < rs.rules.length && <span className="text-xs text-subtle"> ({on} bật)</span>}
                    </td>
                    <td className="border-b border-line px-3 align-middle max-lg:hidden">
                      <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted">
                        {used.map((t) => (
                          <span key={t} className="inline-flex items-center gap-1.5">
                            <i className="size-2 rounded-full" style={{ background: typeColor(t, types) }} />
                            {typeTitle(t, types)}
                          </span>
                        ))}
                      </div>
                    </td>
                    <td className="border-b border-line px-3 align-middle max-md:hidden">
                      <div className="flex flex-wrap gap-1">
                        {rs.kb_ids.length ? (
                          kbs
                            .filter((k) => rs.kb_ids.includes(k.id))
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
                    <td className="border-b border-line pr-2 text-right align-middle whitespace-nowrap" onClick={(e) => e.stopPropagation()}>
                      {editor && (
                        <>
                          <Menu
                            align="right"
                            trigger={(open) => (
                              <button className="btn-icon btn-sm" title="Thêm vào hoặc bỏ khỏi thư viện" aria-label={`Thư viện dùng ${rs.name}`} onClick={open}>
                                <Icon name="library_add" size={20} />
                              </button>
                            )}
                            items={kbs.map((k) => ({ label: k.name, checked: rs.kb_ids.includes(k.id), onClick: () => toggleKB(rs, k) }))}
                          />
                          <Menu
                            align="right"
                            trigger={(open) => (
                              <button className="btn-icon btn-sm" aria-label={`Thao tác với ${rs.name}`} onClick={open}>
                                <Icon name="more_vert" size={20} />
                              </button>
                            )}
                            items={[
                              { icon: "edit", label: "Sửa quy tắc", onClick: () => nav(`/settings/checks/${rs.id}`) },
                              { icon: "content_copy", label: "Nhân bản", onClick: () => copyRuleset(rs.id).then(() => (toast("Đã nhân bản"), load()), fail) },
                              { divider: true, label: "" },
                              {
                                icon: "delete",
                                label: "Xoá",
                                danger: true,
                                onClick: () => confirm(`Xoá bộ “${rs.name}”? Các lần đối chiếu cũ vẫn giữ kết quả.`) && deleteRuleset(rs.id).then(() => (toast("Đã xoá bộ quy tắc"), load()), fail),
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
          {!shown.length && <p className="px-4 py-6 text-center text-sm text-muted">Chưa có bộ quy tắc nào.</p>}
        </div>
      )}
      {creating && (
        <NewRulesetDialog
          onClose={() => setCreating(false)}
          onCreated={(rs) => {
            setCreating(false);
            nav(`/settings/checks/${rs.id}?add=1`);
          }}
        />
      )}
    </>
  );
}

function NewRulesetDialog({ onClose, onCreated }: { onClose: () => void; onCreated: (rs: CheckRuleset) => void }) {
  const { kbs, kb } = useApp();
  const { fail } = useToast();
  const [name, setName] = useState("");
  const [desc, setDesc] = useState("");
  const [ids, setIds] = useState<string[]>(kb ? [kb.id] : []);
  const [busy, setBusy] = useState(false);
  const types = typesOf(kbs, ids);
  const anchor = types.find((t) => /hop_dong|hopdong|contract/.test(t.name))?.name ?? types[0]?.name ?? "";
  return (
    <Modal
      title="Bộ quy tắc mới"
      icon="rule"
      onClose={onClose}
      footer={
        <>
          <button className="btn btn-text" onClick={onClose}>
            Huỷ
          </button>
          <button
            className="btn btn-primary"
            disabled={busy || !name.trim()}
            onClick={async () => {
              setBusy(true);
              try {
                onCreated(await createRuleset({ name: name.trim(), description: desc.trim(), kb_ids: ids, anchor, links: [], rules: [] }));
              } catch (e) {
                fail(e);
                setBusy(false);
              }
            }}
          >
            Tạo
          </button>
        </>
      }
    >
      <label className="field">
        Tên bộ quy tắc
        <input className="input" autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="VD: Giải ngân theo hợp đồng thi công" />
      </label>
      <label className="field">
        Mô tả
        <input className="input" value={desc} onChange={(e) => setDesc(e.target.value)} placeholder="Bộ này kiểm những gì" />
      </label>
      <fieldset className="flex flex-col gap-1">
        <legend className="text-xs font-medium text-muted">Dùng ở thư viện</legend>
        {kbs.map((k) => (
          <label key={k.id} className="flex h-9 items-center gap-3 text-sm text-fg">
            <input type="checkbox" className="size-4 accent-accent" checked={ids.includes(k.id)} onChange={(e) => setIds((xs) => (e.target.checked ? [...xs, k.id] : xs.filter((x) => x !== k.id)))} />
            {k.name}
          </label>
        ))}
      </fieldset>
    </Modal>
  );
}

// ---------------------------------------------------------------- one set

export function RulesetPage() {
  const { id = "" } = useParams();
  const { kbs } = useApp();
  const { user } = useAuth();
  const editor = canEditPrompts(user);
  const { toast, fail } = useToast();
  const nav = useNavigate();
  const [rs, setRs] = useState<CheckRuleset | null>(null);
  const [err, setErr] = useState("");
  const [editing, setEditing] = useState<CheckRule | "new" | null>(new URLSearchParams(location.search).has("add") ? "new" : null);
  const [working, setWorking] = useState<Record<string, boolean>>({});
  useEffect(() => {
    getRuleset(id).then(setRs, (e) => setErr((e as Error).message));
  }, [id]);
  const types = useMemo(() => typesOf(kbs, rs?.kb_ids ?? []), [kbs, rs?.kb_ids]);

  // Every change is saved at once (U76); the server checks it and answers
  // with the stored set (formulas, names).
  const save = async (next: CheckRuleset, msg?: string) => {
    setRs(next);
    try {
      setRs(await updateRuleset(next));
      if (msg) toast(msg);
    } catch (e) {
      fail(e);
      getRuleset(id).then(setRs, () => {});
    }
  };

  if (err)
    return (
      <Empty icon="rule" title="Không có bộ quy tắc này">
        {err}
      </Empty>
    );
  if (!rs) return <Loading />;
  const anchorTitle = typeTitle(rs.anchor, types) || "giấy tờ gốc";
  const on = rs.rules.filter((r) => !r.off).length;
  const linkTypes = [...new Set(rs.rules.flatMap((r) => (r.a && r.b ? [r.a.type, r.b.type] : [])).concat(rs.links.map((l) => l.doc_type)))].filter((t) => t !== rs.anchor);

  const setSteps = (dt: string, fn: (xs: CheckStep[]) => CheckStep[], persist: boolean) => {
    const has = rs.links.some((l) => l.doc_type === dt);
    const links = has ? rs.links.map((l) => (l.doc_type === dt ? { ...l, steps: fn(l.steps) } : l)) : [...rs.links, { doc_type: dt, steps: fn([]) }];
    const next = { ...rs, links };
    if (persist) save(next);
    else setRs(next);
  };
  const compileStep = async (dt: string, stepId: string, text: string) => {
    if (!text.trim()) return;
    setWorking((w) => ({ ...w, [stepId]: true }));
    try {
      const r = await compileRuleset(rs.id, { kind: "link", doc_type: dt, anchor: rs.anchor, text });
      const patch = { formula: r.formula ?? "", ai: r.ai, explanation: r.explanation + (r.missing ? ` Còn thiếu: ${r.missing}.` : "") };
      setSteps(dt, (xs) => xs.map((x) => (x.id === stepId ? { ...x, text, ...patch } : x)), true);
    } catch (e) {
      fail(e);
    } finally {
      setWorking((w) => ({ ...w, [stepId]: false }));
    }
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="flex min-h-16 flex-none flex-wrap items-center gap-2 px-4 pt-3 pb-2">
        <BackButton fallback="/settings/checks" fallbackLabel="Cài đặt" />
        <span className="h-6 border-l border-line max-sm:hidden" />
        <div className="min-w-0 flex-1 px-2">
          <h1 className="flex items-center gap-2 truncate text-[22px] leading-7 font-normal text-fg">
            <Icon name="rule" size={24} className="text-[#5b47c9]" /> {rs.name}
          </h1>
          <div className="truncate text-xs text-muted">
            {rs.description || "Chưa có mô tả"} · {rs.rules.length} quy tắc, {on} đang bật
          </div>
        </div>
        {editor && (
          <Menu
            align="right"
            trigger={(open) => (
              <button className="btn btn-text" onClick={open}>
                <Icon name="library_add" size={18} /> {rs.kb_ids.length ? `Dùng ở ${rs.kb_ids.length} thư viện` : "Chưa dùng ở thư viện nào"}
              </button>
            )}
            items={kbs.map((k) => ({
              label: k.name,
              checked: rs.kb_ids.includes(k.id),
              onClick: () => save({ ...rs, kb_ids: rs.kb_ids.includes(k.id) ? rs.kb_ids.filter((x) => x !== k.id) : [...rs.kb_ids, k.id] }, "Đã cập nhật thư viện dùng bộ này"),
            }))}
          />
        )}
      </header>
      <div className="min-h-0 flex-1 overflow-auto">
        <div className="mx-auto flex max-w-5xl flex-col gap-4 px-4 py-6 sm:px-8">
          <div className="rounded-2xl border border-line">
            <div className="flex flex-wrap items-center gap-3 border-b border-line px-4 py-3">
              <span className="min-w-0 flex-1 text-sm font-medium text-fg">Các quy tắc</span>
              {editor && (
                <button className="btn btn-primary" disabled={!types.length} onClick={() => setEditing("new")}>
                  <Icon name="add" size={18} /> Thêm quy tắc
                </button>
              )}
            </div>
            {!rs.rules.length && <div className="px-4 py-10 text-center text-sm text-subtle">Chưa có quy tắc nào. Bấm “Thêm quy tắc” để so hai giấy tờ.</div>}
            {rs.rules.map((r, i) => (
              <div key={r.id ?? i} className={"flex items-start gap-3 border-t border-line px-4 py-3 first:border-t-0 " + (r.off ? "opacity-60" : "")}>
                <label className="relative mt-0.5 inline-flex flex-none cursor-pointer items-center" title={r.off ? "Đang tắt" : "Đang bật"}>
                  <input
                    type="checkbox"
                    className="peer sr-only"
                    checked={!r.off}
                    disabled={!editor}
                    aria-label="Bật quy tắc"
                    onChange={() => save({ ...rs, rules: rs.rules.map((x) => (x === r ? { ...x, off: !x.off } : x)) })}
                  />
                  <span className="h-6 w-10 rounded-full bg-surface-3 transition-colors peer-checked:bg-accent" />
                  <span className="absolute left-1 size-4 rounded-full bg-surface shadow-1 transition-transform peer-checked:translate-x-4" />
                </label>
                <div className="min-w-0 flex-1">
                  <p className="text-sm leading-7 text-fg">
                    <RuleSentence rule={r} types={types} anchor={rs.anchor} />
                  </p>
                  {!r.a && (
                    <span className="badge badge-vlm mt-1">
                      <Icon name="auto_awesome" size={14} /> AI đánh giá mỗi lần chạy
                    </span>
                  )}
                </div>
                {editor && (
                  <>
                    <button className="btn-icon btn-sm" title="Sửa quy tắc" aria-label="Sửa quy tắc" onClick={() => setEditing(r)}>
                      <Icon name="edit" size={18} />
                    </button>
                    <button className="btn-icon btn-sm" title="Xoá quy tắc" aria-label="Xoá quy tắc" onClick={() => save({ ...rs, rules: rs.rules.filter((x) => x !== r) }, "Đã xoá quy tắc")}>
                      <Icon name="delete" size={18} />
                    </button>
                  </>
                )}
              </div>
            ))}
          </div>

          <details className="group rounded-2xl border border-line">
            <summary className="flex min-h-12 cursor-pointer list-none items-center gap-3 px-4 py-2 text-sm [&::-webkit-details-marker]:hidden">
              <Icon name="link" size={20} className="text-muted" />
              <span className="min-w-0 flex-1">
                <span className="font-medium text-fg">Cách ghép chứng từ với {anchorTitle.toLowerCase()}</span>
                <span className="block text-xs text-muted">Tự động: theo bên bán và số hợp đồng ghi trên chứng từ. Chỉ cần sửa khi máy ghép sai.</span>
              </span>
              <Icon name="expand_more" size={20} className="text-muted transition-transform group-open:rotate-180" />
            </summary>
            <div className="flex flex-col gap-3 border-t border-line px-4 py-3">
              <label className="field w-72">
                Giấy tờ gốc của mỗi bộ
                <select className="input" value={rs.anchor} disabled={!editor} onChange={(e) => save({ ...rs, anchor: e.target.value })}>
                  <option value="">— chọn loại giấy tờ —</option>
                  {types.map((t) => (
                    <option key={t.name} value={t.name}>
                      {t.title}
                    </option>
                  ))}
                </select>
              </label>
              {linkTypes.map((t) => (
                <div key={t} className="rounded-xl bg-surface-2/60 p-3">
                  <div className="mb-1 text-sm font-medium text-fg">{typeTitle(t, types)}</div>
                  <Steps
                    items={rs.links.find((l) => l.doc_type === t)?.steps ?? []}
                    disabled={!editor || !rs.anchor}
                    working={working}
                    onChange={(fn, persist) => setSteps(t, fn, persist)}
                    onCompile={(sid, text) => compileStep(t, sid, text)}
                  />
                </div>
              ))}
              {!linkTypes.length && <p className="text-xs text-subtle">Thêm quy tắc so giấy tờ khác với {anchorTitle.toLowerCase()} để cài cách ghép.</p>}
            </div>
          </details>
          {!editor && <p className="text-xs text-subtle">Bạn đang xem ở dạng chỉ đọc.</p>}
          <button className="self-start text-sm text-accent hover:underline" onClick={() => nav("/settings/checks")}>
            Xem mọi bộ quy tắc
          </button>
        </div>
      </div>
      {editing && (
        <RuleDialog
          rule={editing === "new" ? undefined : editing}
          types={types}
          anchor={rs.anchor}
          onClose={() => setEditing(null)}
          onSave={(rule) => {
            const rules = editing === "new" ? [...rs.rules, rule] : rs.rules.map((x) => (x === editing ? { ...rule, id: x.id, off: x.off } : x));
            setEditing(null);
            save({ ...rs, rules }, editing === "new" ? "Đã thêm quy tắc" : "Đã sửa quy tắc");
          }}
        />
      )}
    </div>
  );
}

// The steps that tell which contract a document belongs to: sentences, the
// list ending with an empty row (U76); leaving a changed sentence asks the
// server to turn it into a formula once.
function Steps({
  items,
  disabled,
  working,
  onChange,
  onCompile,
}: {
  items: CheckStep[];
  disabled: boolean;
  working: Record<string, boolean>;
  onChange: (fn: (xs: CheckStep[]) => CheckStep[], persist: boolean) => void;
  onCompile: (id: string, text: string) => void;
}) {
  const [draft, setDraft] = useState("");
  const compiled = useRef<Record<string, string>>({});
  useEffect(() => {
    for (const x of items) if (x.id && compiled.current[x.id] === undefined) compiled.current[x.id] = x.text;
  }, [items]);
  const add = (text: string) => {
    if (!text.trim()) return;
    const id = newId();
    compiled.current[id] = text;
    onChange((xs) => [...xs, { id, text }], false);
    onCompile(id, text);
  };
  const unused = LINK_HINTS.filter((h) => !items.some((x) => x.text === h));
  return (
    <div className="flex flex-col">
      {items.map((x, i) => (
        <div key={x.id ?? i} className="border-b border-line py-1.5 last:border-b-0">
          <div className="flex items-center gap-2">
            <span className="w-14 flex-none text-xs text-muted">Bước {i + 1}</span>
            <input
              className="input h-9 min-w-0 flex-1"
              value={x.text}
              disabled={disabled}
              aria-label={`Bước ${i + 1}`}
              onChange={(e) => onChange((xs) => xs.map((y) => (y === x ? { ...y, text: e.target.value } : y)), false)}
              onBlur={() => x.id && compiled.current[x.id] !== x.text && ((compiled.current[x.id] = x.text), onCompile(x.id, x.text))}
              onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
            />
            {x.id && working[x.id] ? (
              <span className="badge h-7 gap-1.5 px-2">
                <Spinner className="size-3.5 border-2" /> Đang hiểu…
              </span>
            ) : x.formula ? (
              <span className="badge badge-ok h-7 px-2" title="Máy tự kiểm, không tốn AI khi chạy">
                <Icon name="check" size={14} /> Máy tự kiểm
              </span>
            ) : (
              <span className="badge badge-vlm h-7 px-2" title="AI đọc thông tin và đánh giá mỗi lần chạy">
                <Icon name="auto_awesome" size={14} /> AI đánh giá
              </span>
            )}
            {!disabled && (
              <button className="btn-icon btn-sm" tabIndex={-1} onClick={() => onChange((xs) => xs.filter((y) => y !== x), true)} aria-label={`Xoá bước ${i + 1}`} title="Xoá">
                <Icon name="close" size={18} />
              </button>
            )}
          </div>
          {x.explanation && <div className="mt-1 pl-16 text-xs text-muted">{x.explanation}</div>}
        </div>
      ))}
      {!disabled && (
        <div className="flex items-center gap-2 py-1.5">
          <span className="w-14 flex-none text-xs text-subtle">Bước {items.length + 1}</span>
          <input
            className="input h-9 flex-1"
            placeholder={`Gõ một bước, ví dụ: ${LINK_HINTS[0]}`}
            value={draft}
            aria-label="Thêm bước"
            onChange={(e) => setDraft(e.target.value)}
            onBlur={() => (add(draft), setDraft(""))}
            onKeyDown={(e) => e.key === "Enter" && (add(draft), setDraft(""))}
          />
        </div>
      )}
      {!disabled && unused.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5 pt-1 pl-16">
          <span className="text-xs text-subtle">Gợi ý:</span>
          {unused.map((h) => (
            <button key={h} className="chip h-7 text-xs" onClick={() => add(h)}>
              <Icon name="add" size={14} /> {h}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------- rule dialog

// Three steps (U104): document A and its field, document B and a field of the
// same kind, the comparison; under the comparison, in the same box, a
// sentence for what is not one comparison (U105). The sentence wins.
function RuleDialog({ rule, types, anchor, onClose, onSave }: { rule?: CheckRule; types: DocType[]; anchor: string; onClose: () => void; onSave: (r: CheckRule) => void }) {
  const withFields = types.filter((t) => t.fields?.length);
  const [a, setA] = useState<{ type: string; key?: string }>(rule?.a ?? { type: withFields.find((t) => t.name !== anchor)?.name ?? withFields[0]?.name ?? "" });
  const [b, setB] = useState<{ type: string; key?: string }>(rule?.b ?? { type: anchor || withFields[0]?.name || "" });
  const [op, setOp] = useState(rule?.op ?? "");
  const [text, setText] = useState(rule && !rule.a ? (rule.text ?? "") : "");
  const fa = fieldOf(types, a as FieldRef);
  const fb = fieldOf(types, b as FieldRef);
  const kind = fa ? kindOf(fa.value_type) : "";
  const ops = kind ? OPS[kind] : [];
  const opOk = ops.some(([k]) => k === op) ? op : "";
  const byField = !!(fa && fb && opOk && kindOf(fb.value_type) === kind);
  const draft: CheckRule = text.trim() ? { text: text.trim() } : { a: a as FieldRef, op: opOk, b: b as FieldRef };
  const step = (n: number, title: string, ok: boolean) => (
    <div className="mb-2 flex items-center gap-2 text-sm font-medium text-fg">
      <span className={"grid size-6 flex-none place-items-center rounded-full text-xs " + (ok ? "bg-accent text-on-accent" : "bg-accent-soft text-on-accent-soft")}>{ok ? <Icon name="check" size={16} /> : n}</span>
      {title}
    </div>
  );
  const side = (n: number, label: string, v: { type: string; key?: string }, set: (x: { type: string; key?: string }) => void, other?: string) => {
    const t = types.find((x) => x.name === v.type);
    return (
      <section className={"flex min-w-0 flex-col rounded-2xl border p-4 " + (v.key ? "border-accent/40" : "border-line")}>
        {step(n, label, !!v.key)}
        <label className="field">
          Giấy tờ
          <select className="input" value={v.type} onChange={(e) => set({ type: e.target.value })}>
            {withFields.map((x) => (
              <option key={x.name} value={x.name}>
                {x.title}
              </option>
            ))}
          </select>
        </label>
        <div className="mt-3 mb-1 text-xs font-medium text-muted">Thông tin cần so</div>
        <div className="flex flex-col gap-1" role="radiogroup" aria-label={`Thông tin của ${label}`}>
          {(t?.fields ?? []).map((f) => {
            const sel = f.key === v.key;
            const bad = !!other && !!kind && kindOf(f.value_type) !== kind;
            const k = kindOf(f.value_type);
            return (
              <button
                key={f.key}
                role="radio"
                aria-checked={sel}
                disabled={bad}
                title={bad ? `Khác kiểu với ${other}: cần ${KIND_LABEL[kind].toLowerCase()}` : undefined}
                className={"flex min-h-10 items-center gap-3 rounded-xl px-3 text-left text-sm disabled:cursor-not-allowed disabled:opacity-40 " + (sel ? "bg-accent-soft font-medium text-on-accent-soft" : "text-fg hover:bg-fg/5")}
                onClick={() => set({ type: v.type, key: f.key })}
              >
                <Icon name={sel ? "radio_button_checked" : "radio_button_unchecked"} size={20} className={sel ? "" : "text-subtle"} />
                <span className="min-w-0 flex-1">{f.label}</span>
                <span className={"flex items-center gap-1 text-xs " + (sel ? "" : "text-subtle")}>
                  <Icon name={KIND_ICON[k]} size={14} />
                  {KIND_LABEL[k]}
                </span>
              </button>
            );
          })}
        </div>
      </section>
    );
  };
  return (
    <Modal
      title={rule ? "Sửa quy tắc" : "Quy tắc mới"}
      icon="rule"
      width={900}
      onClose={onClose}
      footer={
        <>
          <button className="btn btn-text" onClick={onClose}>
            Huỷ
          </button>
          <button className="btn btn-primary" disabled={!text.trim() && !byField} onClick={() => onSave(draft)}>
            Lưu quy tắc
          </button>
        </>
      }
    >
      <div className="grid gap-3 md:grid-cols-2">
        {side(1, "Giấy tờ A", a, (x) => setA(x))}
        {side(2, "Giấy tờ B", b, (x) => setB(x), fa?.label)}
      </div>
      <section className={"rounded-2xl border p-4 " + (opOk ? "border-accent/40" : "border-line")}>
        {step(3, "Cách so", !!opOk)}
        {kind ? (
          <div className="flex flex-wrap gap-2" role="radiogroup" aria-label="Cách so">
            {ops.map(([k, l]) => (
              <button key={k} role="radio" aria-checked={opOk === k} className={"chip " + (opOk === k ? "chip-on" : "")} onClick={() => setOp(k)}>
                {opOk === k && <Icon name="check" size={18} />}A {l} B
              </button>
            ))}
          </div>
        ) : (
          <p className="text-sm text-subtle">Chọn thông tin của giấy tờ A trước; cách so phụ thuộc vào kiểu thông tin (chữ, số tiền, ngày).</p>
        )}
        <div className="mt-4 border-t border-line pt-4">
          <label className="field">
            Hoặc viết quy tắc bằng lời
            <textarea className="input font-body" rows={2} value={text} onChange={(e) => setText(e.target.value)} placeholder="Ví dụ: Nếu hợp đồng thanh toán sau khi giao hàng thì phải có hoá đơn hoặc biên bản bàn giao" />
          </label>
          <p className="mt-1.5 flex items-start gap-1.5 text-xs text-muted">
            <Icon name="auto_awesome" size={14} className="mt-px text-vlm" />
            Dùng khi không so được hai thông tin cụ thể. Có câu ở đây thì quy tắc do AI đọc các chứng từ và đánh giá mỗi lần chạy; phần chọn ở trên được bỏ qua. So được theo thông tin thì nên chọn ở trên để máy tự kiểm, nhanh và chính xác hơn.
          </p>
        </div>
      </section>
      <div className="flex items-start gap-3 rounded-2xl bg-surface-2 p-4">
        <Icon name="fact_check" size={22} className="text-accent" />
        <div className="min-w-0 flex-1">
          <div className="text-xs font-medium text-muted">Quy tắc sẽ đọc là</div>
          <p className="mt-1 text-sm leading-7 text-fg">
            {text.trim() ? (
              <>
                {text.trim()}{" "}
                <span className="badge badge-vlm align-middle">
                  <Icon name="auto_awesome" size={14} /> AI đánh giá
                </span>
              </>
            ) : byField ? (
              <RuleSentence rule={draft} types={types} anchor={anchor} />
            ) : (
              <span className="text-subtle">Chọn đủ hai thông tin và cách so, hoặc viết quy tắc bằng lời.</span>
            )}
          </p>
          {byField && !text.trim() && <p className="mt-1 text-xs text-muted">Kiểm trên từng bộ hồ sơ: mỗi giấy tờ “{typeTitle(a.type, types)}” được so với “{typeTitle(b.type, types)}” cùng bộ.</p>}
        </div>
      </div>
    </Modal>
  );
}
