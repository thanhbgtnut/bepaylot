import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { NavLink, useParams } from "react-router-dom";

import { changePassword, createAPIKey, listAPIKeys, revokeAPIKey } from "../api/auth";
import { getAdminUsage, getLLMKey, getMyUsage, issueAPIKey, listAdminUsers, setLLMKey, updateAdminUser, updateKB } from "../api/endpoints";
import { isAdmin, type AdminUser, type APIKey, type DocType, type LLMKeyView, type SheetField, type UsageSummary } from "../api/types";
import { fmtMoney, ROLE_LABEL, toCode, uniqueCode } from "../lib/format";
import { useApp } from "./AppContext";
import { useAuth } from "./AuthContext";
import { TopBar } from "./Layout";
import { useToast } from "./toast";
import { Empty, Icon, Modal, Spinner } from "./ui";

export type SettingsTab = "account" | "doctypes" | "keys" | "users";

// "Thông tin" (§7.8), opened from the signed-in user's menu: account, key &
// cost (a usage page like ChatGPT/Claude's, next to the key setup; cost is
// shown only here).
export function SettingsDialog({ tab: initial, onClose }: { tab: "account" | "keys"; onClose: () => void }) {
  const { user } = useAuth();
  const [tab, setTab] = useState(initial);
  return (
    <Modal
      title="Thông tin"
      icon="account_circle"
      onClose={onClose}
      width={640}
      footer={
        <button className="btn btn-primary" onClick={onClose}>
          Xong
        </button>
      }
    >
      <div className="-mx-6 -mt-2 flex overflow-x-auto border-b border-line px-3">
        {(
          [
            ["account", "Tài khoản"],
            ["keys", "Key & chi phí"],
          ] as const
        ).map(([k, l]) => (
          <button key={k} className={"tab " + (tab === k ? "tab-active" : "")} onClick={() => setTab(k)}>
            {l}
          </button>
        ))}
      </div>
      {tab === "account" && <AccountTab />}
      {tab === "keys" && <KeysTab admin={isAdmin(user)} />}
    </Modal>
  );
}

// "Cài đặt" (§7.8), a page opened from the bottom of the sidebar: the fields
// of each document type of a knowledge base (§7.11) and users for admins.
// Every section is a centred column of the same width, opened by a heading
// that says what the section is for.
export function SettingsPage() {
  const { user } = useAuth();
  const admin = isAdmin(user);
  const { tab: param } = useParams();
  const tabs: [string, string, string][] = [["doctypes", "description", "Loại giấy tờ"], ...(admin ? [["users", "group", "Người dùng"] as [string, string, string]] : [])];
  const tab = tabs.some(([k]) => k === param) ? param : "doctypes";
  return (
    <>
      <TopBar title="Cài đặt" />
      <nav className="flex flex-none gap-1 overflow-x-auto border-b border-line px-6" aria-label="Mục cài đặt">
        {tabs.map(([k, icon, l]) => (
          <NavLink key={k} to={`/settings/${k}`} replace aria-current={tab === k ? "page" : undefined} className={"tab inline-flex items-center gap-2 " + (tab === k ? "tab-active" : "")}>
            <Icon name={icon} size={20} fill={tab === k} />
            {l}
          </NavLink>
        ))}
      </nav>
      <div className="min-h-0 flex-1 overflow-auto px-6 py-6">
        <div className="mx-auto flex max-w-5xl flex-col gap-5">
          {tab === "doctypes" && <DocTypesTab />}
          {tab === "users" && <UsersTab />}
        </div>
      </div>
    </>
  );
}

// The heading of a settings section: what it is, one line on what it does,
// and the section's main action on the right.
function SectionHead({ title, children, action }: { title: string; children: ReactNode; action?: ReactNode }) {
  return (
    <header className="flex flex-wrap items-end gap-x-6 gap-y-3">
      <div className="min-w-60 flex-1">
        <h2 className="text-base font-medium text-fg">{title}</h2>
        <p className="mt-1 max-w-2xl text-sm text-muted">{children}</p>
      </div>
      {action}
    </header>
  );
}

function AccountTab() {
  const { user, signOut, signIn, config } = useAuth();
  const { toast, fail } = useToast();
  const [pwOpen, setPwOpen] = useState(false);
  const [cur, setCur] = useState("");
  const [next, setNext] = useState("");
  const [busy, setBusy] = useState(false);
  const save = async () => {
    setBusy(true);
    try {
      signIn(await changePassword(cur, next));
      toast(user?.has_password ? "Đã đổi mật khẩu, các phiên khác đã bị đăng xuất" : "Đã đặt mật khẩu");
      setPwOpen(false);
      setCur("");
      setNext("");
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  };
  if (!user) return null;
  const role = user.role ?? (user.is_admin ? "admin" : "user");
  return (
    <>
      <div className="flex items-center gap-4 rounded-2xl bg-surface-2 p-4">
        <div className="grid size-12 flex-none place-items-center rounded-full bg-accent text-xl text-on-accent">{(user.name || user.email)[0]?.toUpperCase()}</div>
        <div className="min-w-0 flex-1">
          <div className="truncate text-base text-fg">{user.name || user.email}</div>
          <div className="truncate text-xs">{user.email}</div>
          <div className="mt-1 flex gap-1">
            <span className="badge">{user.auth_provider === "local" ? "Email + mật khẩu" : "OIDC · " + user.auth_provider}</span>
            <span className={"badge " + (role === "admin" ? "badge-info" : role === "prompt_editor" ? "badge-vlm" : "")}>{ROLE_LABEL[role]}</span>
            {config?.auth_bypass && <span className="badge badge-warn">auth_bypass</span>}
          </div>
        </div>
        {!config?.auth_bypass && (
          <button className="btn btn-sm" onClick={signOut}>
            <Icon name="logout" size={18} /> Đăng xuất
          </button>
        )}
      </div>
      {!config?.auth_bypass && (
        <section className="flex flex-col gap-3">
          <div className="flex items-center">
            <h3 className="flex-1 text-sm font-medium text-fg">Mật khẩu</h3>
            {!pwOpen && (
              <button className="btn btn-text btn-sm" onClick={() => setPwOpen(true)}>
                {user.has_password ? "Đổi mật khẩu" : "Đặt mật khẩu"}
              </button>
            )}
          </div>
          {!pwOpen && !user.has_password && <p className="text-xs">Tài khoản chưa có mật khẩu (tạo qua OIDC hoặc make seed). Đặt mật khẩu để đăng nhập bằng email.</p>}
          {pwOpen && (
            <div className="flex flex-col gap-3">
              {user.has_password && (
                <label className="field">
                  <span>Mật khẩu hiện tại</span>
                  <input className="input" type="password" autoComplete="current-password" value={cur} onChange={(e) => setCur(e.target.value)} />
                </label>
              )}
              <label className="field">
                <span>Mật khẩu mới (ít nhất {config?.password_min_length ?? 8} ký tự)</span>
                <input className="input" type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
              </label>
              <div className="flex justify-end gap-2">
                <button className="btn btn-text btn-sm" onClick={() => setPwOpen(false)}>
                  Huỷ
                </button>
                <button className="btn btn-tonal btn-sm" onClick={save} disabled={busy || !next}>
                  Lưu mật khẩu
                </button>
              </div>
            </div>
          )}
        </section>
      )}
    </>
  );
}

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

// Fields of each document type of a knowledge base (§7.11): pick the KB, pick
// the document type on the left, edit its fields on the right. They are the
// columns of the type's Excel sheet and are extracted when a sheet is made;
// the description of a field guides the agent that extracts it.
function DocTypesTab() {
  const { kbs, kb: current, updateKBLocal, openEditKB } = useApp();
  const { toast, fail } = useToast();
  const [picked, setPick] = useState(current?.id ?? kbs[0]?.id ?? "");
  // After a reload the KBs arrive after the tab opens: until one is picked,
  // the current KB (or the first one) is shown.
  const pick = kbs.some((k) => k.id === picked) ? picked : (current?.id ?? kbs[0]?.id ?? "");
  const kb = kbs.find((k) => k.id === pick);
  const [types, setTypes] = useState<(DocType & { fields?: NewField[] })[]>([]);
  const [active, setActive] = useState(0);
  const [dirty, setDirty] = useState(false);
  const [busy, setBusy] = useState(false);
  const reset = useCallback(() => {
    setTypes((kb?.config.doc_types ?? []).map((t) => ({ ...t, fields: (t.fields ?? []).map((f) => ({ ...f })) })));
    setDirty(false);
  }, [kb]);
  useEffect(() => {
    reset();
    setActive(0);
  }, [reset]);

  const type = types[active];
  const fields: NewField[] = type?.fields ?? [];
  const setFields = (fn: (xs: NewField[]) => NewField[]) => {
    setTypes((ts) => ts.map((t, i) => (i === active ? { ...t, fields: fn(t.fields ?? []) } : t)));
    setDirty(true);
  };
  // Field keys need two characters or more.
  const keyOf = (label: string, taken: string[]) => {
    const k = toCode(label);
    return k ? uniqueCode(k.length < 2 ? k + "_1" : k, taken) : "";
  };
  // named gives a new field the key and value type of its name.
  const named = (f: NewField, label: string, taken: string[]): NewField =>
    f.auto ? { ...f, label, key: keyOf(label, taken), value_type: f.typed ? f.value_type : guessType(label) } : { ...f, label };
  // The table always ends with one empty row: typing in it makes it a field
  // and the next empty row appears, so there is no "add" button. Row i ===
  // fields.length is that empty row. A new field whose name is cleared goes.
  const setLabel = (i: number, label: string) =>
    setFields((xs) => {
      const taken = xs.filter((_, j) => j !== i).map((x) => x.key);
      if (i >= xs.length) return label ? [...xs, named({ key: "", label: "", auto: true }, label, taken)] : xs;
      if (!label && xs[i].auto) return xs.filter((_, j) => j !== i);
      return xs.map((f, k) => (k === i ? named(f, label, taken) : f));
    });
  const setType = (i: number, value_type: string) =>
    setFields((xs) => (i >= xs.length ? xs : xs.map((f, k) => (k === i ? { ...f, value_type, typed: true } : f))));
  const setDesc = (i: number, description: string) => setFields((xs) => xs.map((f, k) => (k === i ? { ...f, description } : f)));
  // Several names pasted at once (one per line, or tab-separated cells from
  // a spreadsheet) become one field each.
  const pasteNames = (i: number, text: string) => {
    const names = text
      .split(/[\r\n\t]+/)
      .map((x) => x.trim())
      .filter(Boolean);
    if (names.length < 2) return false;
    setFields((xs) => {
      const out = xs.slice(0, i);
      for (const label of names) out.push(named({ key: "", label: "", auto: true }, label, [...out, ...xs.slice(i)].map((x) => x.key)));
      return [...out, ...xs.slice(i).filter((f) => f.label)];
    });
    return true;
  };
  const focusRow = (i: number) => document.querySelector<HTMLInputElement>(`[data-field-name="${i}"]`)?.focus();
  const focusDesc = (i: number) => document.querySelector<HTMLInputElement>(`[data-field-desc="${i}"]`)?.focus();
  const save = async () => {
    if (!kb) return;
    const doc_types = types.map((t) => ({
      name: t.name,
      title: t.title,
      description: t.description,
      fields: (t.fields ?? []).filter((f) => f.key && f.label.trim()).map((f) => ({ key: f.key, label: f.label.trim(), value_type: f.value_type || "string", description: f.description?.trim() || undefined })),
    }));
    setBusy(true);
    try {
      // PATCH replaces the whole config, so it sends what the KB has.
      updateKBLocal(await updateKB(kb.id, { config: { ...kb.config, doc_types } }));
      toast("Đã lưu trường của loại giấy tờ; trường mới được bóc tách khi tạo bảng Excel");
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  };

  // Ctrl/⌘+S saves from anywhere on the page.
  const saveRef = useRef(save);
  saveRef.current = save;
  useEffect(() => {
    if (!dirty) return;
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "s") (e.preventDefault(), saveRef.current());
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [dirty]);

  if (!kbs.length)
    return (
      <Empty icon="create_new_folder" title="Chưa có thư viện">
        Tạo thư viện và khai báo loại giấy tờ trước khi thêm trường cần lấy.
      </Empty>
    );
  return (
    <>
      <SectionHead
        title="Trường cần lấy theo loại giấy tờ"
        action={
          <label className="field w-full sm:w-72">
            <span>Thư viện</span>
            <select className="input" value={pick} onChange={(e) => setPick(e.target.value)} disabled={dirty} title={dirty ? "Lưu hoặc hoàn tác thay đổi trước khi đổi thư viện" : undefined}>
              {kbs.map((k) => (
                <option key={k.id} value={k.id}>
                  {k.name}
                </option>
              ))}
            </select>
          </label>
        }
      >
        Các trường là cột của sheet tương ứng khi xuất Excel và được bóc tách khi bạn tạo bảng. Mô tả hướng dẫn AI cách lấy từng trường.
      </SectionHead>

      {!types.length ? (
        <div className="rounded-2xl border border-line">
          <Empty icon="category" title="Thư viện này chưa có loại giấy tờ">
            Khai báo các loại giấy tờ (Hợp đồng, Hoá đơn…) rồi quay lại đây để thêm trường cho từng loại.
            <div>
              <button className="btn btn-primary mt-5" onClick={() => kb && openEditKB(kb)}>
                <Icon name="edit" size={18} /> Thêm loại giấy tờ
              </button>
            </div>
          </Empty>
        </div>
      ) : (
        <div className="flex flex-col overflow-hidden rounded-2xl border border-line lg:flex-row">
          {/* Master: the document types of the knowledge base. */}
          <div className="flex flex-none flex-col gap-1 border-b border-line bg-surface-2/60 p-3 lg:w-64 lg:border-r lg:border-b-0">
            <div className="flex items-center pr-1 pl-3">
              <span className="flex-1 text-xs font-medium text-muted">Loại giấy tờ</span>
              <button className="btn-icon btn-sm" title="Thêm, đổi tên hoặc xoá loại giấy tờ" aria-label="Sửa loại giấy tờ của thư viện" onClick={() => kb && openEditKB(kb)}>
                <Icon name="edit" size={18} />
              </button>
            </div>
            <div className="flex gap-1 overflow-x-auto lg:flex-col lg:overflow-visible" role="tablist" aria-label="Loại giấy tờ" aria-orientation="vertical">
              {types.map((t, i) => {
                const on = i === active;
                return (
                  <button
                    key={t.name}
                    role="tab"
                    aria-selected={on}
                    onClick={() => setActive(i)}
                    className={"flex h-10 flex-none cursor-pointer items-center gap-3 rounded-full pr-2 pl-3 text-left text-sm " + (on ? "bg-accent-soft font-medium text-on-accent-soft" : "text-fg hover:bg-fg/8")}
                  >
                    <Icon name="description" size={20} fill={on} className={on ? "" : "text-muted"} />
                    <span className="min-w-0 flex-1 truncate">{t.title}</span>
                    <span className={"grid h-5 min-w-5 place-items-center rounded-full px-1.5 text-[11px] tabular-nums " + (on ? "bg-surface text-on-accent-soft" : "bg-surface-3 text-muted")} title={`${t.fields?.length ?? 0} trường`}>
                      {t.fields?.length ?? 0}
                    </span>
                  </button>
                );
              })}
            </div>
          </div>

          {/* Detail: the fields of the chosen type. */}
          <div className="flex min-w-0 flex-1 flex-col" role="tabpanel" aria-label={type?.title}>
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1 px-5 pt-4 pb-3">
              <h3 className="text-[15px] font-medium text-fg">{type?.title}</h3>
              <code className="tag font-mono">{type?.name}</code>
              {type?.description && <span className="w-full text-xs text-muted">{type.description}</span>}
            </div>
            <div className="overflow-x-auto border-t border-line">
              <table className="w-full min-w-200 text-sm">
                <thead>
                  <tr className="text-left text-xs text-muted">
                    <th className="w-12 py-2.5 pl-5 font-medium">#</th>
                    <th className="w-56 px-2 py-2.5 font-medium">Tên trường</th>
                    <th className="w-44 px-2 py-2.5 font-medium">Key (tự sinh)</th>
                    <th className="w-36 px-2 py-2.5 font-medium">Kiểu dữ liệu</th>
                    <th className="px-2 py-2.5 font-medium">Mô tả (hướng dẫn AI)</th>
                    <th className="w-14" />
                  </tr>
                </thead>
                <tbody>
                  {[...fields, null].map((f, i) => (
                    <tr key={i} className="border-t border-line hover:bg-fg/3">
                      <td className="py-1.5 pl-5 text-xs text-muted tabular-nums">{f ? i + 1 : <Icon name="add" size={16} className="text-subtle" />}</td>
                      <td className="px-2 py-1.5">
                        <input
                          className="input h-9 w-full"
                          data-field-name={i}
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
                      <td className="px-2 py-1.5">{f?.key ? <code className="tag max-w-full truncate font-mono">{f.key}</code> : <span className="text-xs text-subtle">sinh từ tên trường</span>}</td>
                      <td className="px-2 py-1.5">
                        {f && (
                          <select className="input h-9 w-full" value={f.value_type || "string"} onChange={(e) => setType(i, e.target.value)} aria-label={`Kiểu dữ liệu của trường ${f.label || i + 1}`}>
                            {VALUE_TYPES.map(([v, l]) => (
                              <option key={v} value={v}>
                                {l}
                              </option>
                            ))}
                          </select>
                        )}
                      </td>
                      <td className="px-2 py-1.5">
                        {f && (
                          <input
                            className="input h-9 w-full"
                            data-field-desc={i}
                            value={f.description ?? ""}
                            onChange={(e) => setDesc(i, e.target.value)}
                            onKeyDown={(e) => {
                              if ((e.key === "Enter" || e.key === "ArrowDown") && i + 1 < fields.length) (e.preventDefault(), focusDesc(i + 1));
                              if (e.key === "ArrowUp" && i > 0) (e.preventDefault(), focusDesc(i - 1));
                            }}
                            placeholder="Lấy ở đâu, ghi theo dạng nào (không bắt buộc)"
                            title={f.description || undefined}
                            aria-label={`Mô tả của trường ${f.label || i + 1}`}
                          />
                        )}
                      </td>
                      <td className="pr-3 text-right">
                        {f && (
                          <button className="btn-icon btn-sm" tabIndex={-1} title="Xoá trường" aria-label={`Xoá trường ${f.label || i + 1}`} onClick={() => setFields((xs) => xs.filter((_, j) => j !== i))}>
                            <Icon name="delete" size={18} />
                          </button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {/* One action bar for the whole knowledge base: edits of every type are saved together. */}
            <div className="mt-auto flex flex-wrap items-center gap-2 border-t border-line px-5 py-3">
              <span className="min-w-0 flex-1 text-xs text-muted">Gõ xong một trường, dòng mới tự hiện. Enter xuống dòng kế; dán nhiều dòng để thêm nhiều trường cùng lúc.</span>
              <span className="text-xs text-muted" role="status">
                {dirty ? "Có thay đổi chưa lưu" : "Đã lưu"}
              </span>
              <button className="btn btn-text btn-sm" onClick={reset} disabled={busy || !dirty}>
                Hoàn tác
              </button>
              <button className="btn btn-primary btn-sm" onClick={save} disabled={busy || !dirty}>
                {busy ? <Spinner className="size-4 border-2" /> : <Icon name="save" size={18} />} Lưu
                <kbd className="ml-1 text-[11px] opacity-70">{/Mac/.test(navigator.platform) ? "⌘S" : "Ctrl+S"}</kbd>
              </button>
            </div>
          </div>
        </div>
      )}
      <p className="flex items-start gap-2 text-xs text-muted">
        <Icon name="info" size={16} className="mt-px flex-none" />
        Thông tin chỉ được bóc tách khi tạo bảng Excel và được lưu lại: lần tạo sau chỉ bóc tách trường mới thêm, và hỏi lại một lần các trường chưa tìm thấy.
      </p>
    </>
  );
}

// A usage bar like the usage pages of ChatGPT and Claude.
export function UsageBar({ label, spent, limit, reset, currency }: { label: string; spent: number; limit: number; reset?: string; currency: string }) {
  const p = limit > 0 ? Math.min(100, Math.round((spent / limit) * 100)) : 0;
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-baseline gap-2">
        <span className="flex-1 text-sm text-fg">{label}</span>
        {limit > 0 && <span className="text-xs">{p}% đã dùng</span>}
      </div>
      {limit > 0 && (
        <div className="h-2 overflow-hidden rounded-full bg-surface-3">
          <i className={"block h-full rounded-full " + (p >= 90 ? "bg-err" : "bg-accent")} style={{ width: `${p}%` }} />
        </div>
      )}
      <div className="flex text-xs">
        <span className="flex-1 text-fg tabular-nums">
          {fmtMoney(spent, currency)}
          {limit > 0 ? " / " + fmtMoney(limit, currency) : ""}
        </span>
        {reset && <span>{reset}</span>}
      </div>
    </div>
  );
}

const dmy = (s?: string) => (s ? new Date(s).toLocaleDateString("vi-VN", { day: "2-digit", month: "2-digit", year: "numeric" }) : "");

function KeysTab({ admin }: { admin: boolean }) {
  const { toast, fail } = useToast();
  const [usage, setUsage] = useState<UsageSummary | null>(null);
  const [keys, setKeys] = useState<APIKey[] | null>(null);
  const [llm, setLLM] = useState<LLMKeyView | null>(null);
  const [editKey, setEditKey] = useState<string | null>(null);
  const [keyName, setKeyName] = useState("");
  const [created, setCreated] = useState("");
  const load = useCallback(() => {
    (admin ? getAdminUsage() : getMyUsage()).then(setUsage, fail);
    listAPIKeys().then(setKeys, fail);
    if (admin) getLLMKey().then(setLLM, fail);
  }, [admin, fail]);
  useEffect(load, [load]);

  const kinds: [string, string][] = [
    ["chat", "Hỏi đáp"],
    ["sheet", "Xuất Excel"],
    ["parse", "Xử lý file"],
  ];
  return (
    <>
      <section className="flex flex-col gap-4">
        <div>
          <h3 className="text-base font-medium text-fg">Chi phí đã sử dụng</h3>
          <p className="text-xs">
            Tính từ token thực tế của mọi model dùng chung một key LLM.
            {usage && ` Cập nhật lúc ${new Date(usage.updated_at).toLocaleTimeString("vi-VN", { hour: "2-digit", minute: "2-digit" })}.`}
          </p>
        </div>
        {!usage ? (
          <Spinner />
        ) : (
          <>
            <UsageBar label={admin ? "Toàn hệ thống · tháng này" : "Của bạn · tháng này"} spent={usage.month.spent} limit={usage.month.limit} reset={`Đặt lại vào ${dmy(usage.month.resets_at)}`} currency={usage.currency} />
            <UsageBar label="Hôm nay" spent={usage.today.spent} limit={0} reset="Đặt lại lúc 00:00" currency={usage.currency} />
            <div className="grid grid-cols-3 gap-2 text-center">
              {kinds.map(([k, l]) => (
                <div key={k} className="rounded-xl bg-surface-2 px-2 py-3">
                  <div className="text-base text-fg tabular-nums">{fmtMoney(usage.by_kind?.[k] ?? 0, usage.currency)}</div>
                  <div className="text-xs">{l}</div>
                </div>
              ))}
            </div>
          </>
        )}
      </section>

      {admin && (
        <section className="flex flex-col gap-3 border-t border-line pt-4">
          <h3 className="text-sm font-medium text-fg">Key LLM dùng chung</h3>
          <p className="text-xs">Một key cho mọi model (hỏi đáp, VLM, thẻ tài liệu, bảng Excel). Chi phí ở trên tính trên key này.</p>
          {llm && (
            <div className="flex items-center gap-3 rounded-xl border border-line px-3 py-2">
              <Icon name="key" size={18} className="text-accent" />
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm text-fg">
                  {llm.kind} · {llm.base_url || llm.provider}
                </div>
                <div className="truncate font-mono text-[11px] text-subtle">
                  {llm.masked_key || "(chưa có key)"} · {llm.models.length} model đang dùng{llm.overridden ? " · đặt từ Cài đặt" : " · từ .env"}
                </div>
              </div>
              {editKey === null && (
                <button className="btn btn-text btn-sm" onClick={() => setEditKey("")}>
                  Đổi key
                </button>
              )}
            </div>
          )}
          {editKey !== null && (
            <div className="flex gap-2">
              <input className="input flex-1 font-mono" type="password" autoComplete="off" placeholder="Key mới; để trống = dùng key trong .env" value={editKey} onChange={(e) => setEditKey(e.target.value)} />
              <button className="btn btn-text" onClick={() => setEditKey(null)}>
                Huỷ
              </button>
              <button
                className="btn btn-tonal"
                onClick={async () => {
                  try {
                    setLLM(await setLLMKey(editKey));
                    setEditKey(null);
                    toast("Đã đổi key LLM, áp dụng ngay");
                  } catch (e) {
                    fail(e);
                  }
                }}
              >
                Lưu key
              </button>
            </div>
          )}
        </section>
      )}

      <section className="flex flex-col gap-3 border-t border-line pt-4">
        <h3 className="text-sm font-medium text-fg">{admin ? "API key của bạn" : "Key được cấp"}</h3>
        <p className="text-xs">
          {admin ? "Key cho script, gửi qua header x-api-key. Cấp key cho người khác ở tab Người dùng." : "Key do quản trị cấp. Chi phí ở trên tính theo key này; giới hạn do quản trị đặt."}
        </p>
        {created && <NewKey value={created} />}
        {admin && (
          <div className="flex gap-2">
            <input className="input flex-1" value={keyName} onChange={(e) => setKeyName(e.target.value)} placeholder="Tên key, ví dụ: curl, n8n" />
            <button
              className="btn btn-tonal"
              onClick={async () => {
                try {
                  const r = await createAPIKey(keyName.trim() || "curl");
                  setCreated(r.api_key);
                  setKeyName("");
                  load();
                } catch (e) {
                  fail(e);
                }
              }}
            >
              <Icon name="key" size={18} /> Tạo key
            </button>
          </div>
        )}
        {keys === null ? (
          <Spinner />
        ) : keys.length === 0 ? (
          <span className="text-xs text-subtle">{admin ? "Chưa có API key" : "Chưa được cấp key. Web vẫn dùng bình thường; cần key cho script thì liên hệ quản trị."}</span>
        ) : (
          <ul className="flex flex-col divide-y divide-line rounded-xl border border-line">
            {keys.map((k) => (
              <li key={k.id} className="flex items-center gap-3 px-3 py-2">
                <Icon name="key" size={18} className={k.revoked_at ? "text-subtle" : "text-accent"} />
                <div className="min-w-0 flex-1">
                  <div className={"truncate text-sm " + (k.revoked_at ? "text-subtle line-through" : "text-fg")}>{k.name}</div>
                  <div className="truncate font-mono text-[11px] text-subtle">
                    {k.prefix}… · {k.issued_by ? "cấp bởi " + k.issued_by : "tạo " + dmy(k.created_at)}
                    {k.last_used_at ? " · dùng " + new Date(k.last_used_at).toLocaleString("vi-VN") : ""}
                  </div>
                </div>
                {k.revoked_at ? (
                  <span className="badge">Đã thu hồi</span>
                ) : (
                  <button
                    className="btn btn-text btn-sm btn-danger"
                    onClick={async () => {
                      try {
                        await revokeAPIKey(k.id);
                        load();
                      } catch (e) {
                        fail(e);
                      }
                    }}
                  >
                    Thu hồi
                  </button>
                )}
              </li>
            ))}
          </ul>
        )}
        {!admin && (
          <div className="text-xs text-subtle">
            <Icon name="lock" size={14} className="align-[-2px]" /> Bạn không tự tạo key. Cần thêm key hoặc tăng giới hạn thì liên hệ quản trị.
          </div>
        )}
      </section>
    </>
  );
}

function NewKey({ value }: { value: string }) {
  const { toast, fail } = useToast();
  return (
    <div className="flex flex-col gap-2 rounded-xl bg-ok-soft p-3 text-ok">
      <span className="text-xs font-medium">Key mới — chỉ hiện một lần, hãy sao chép ngay:</span>
      <div className="flex items-center gap-2">
        <code className="min-w-0 flex-1 truncate rounded-md bg-surface px-2 py-1 font-mono text-xs text-fg">{value}</code>
        <button className="btn-icon btn-sm" title="Sao chép" onClick={() => navigator.clipboard.writeText(value).then(() => toast("Đã sao chép API key"), fail)}>
          <Icon name="content_copy" size={18} />
        </button>
      </div>
    </div>
  );
}

// Admin: roles, monthly limits, keys (§7.8).
function UsersTab() {
  const { toast, fail } = useToast();
  const { user: me, reload } = useAuth();
  const [users, setUsers] = useState<AdminUser[] | null>(null);
  const [currency, setCurrency] = useState("VND");
  const [limits, setLimits] = useState<Record<string, string>>({});
  const [issued, setIssued] = useState<{ email: string; key: string } | null>(null);
  const load = useCallback(
    () =>
      listAdminUsers().then((r) => {
        setUsers(r.data);
        setCurrency(r.currency);
        setLimits(Object.fromEntries(r.data.map((u) => [u.id, u.monthly_limit != null ? String(u.monthly_limit) : ""])));
      }, fail),
    [fail],
  );
  useEffect(() => {
    load();
  }, [load]);

  // A limit is saved when its box is left (or Enter): no save button.
  const saveLimit = async (u: AdminUser) => {
    const want = limits[u.id]?.replace(/[^\d]/g, "") ?? "";
    if (want === (u.monthly_limit != null ? String(u.monthly_limit) : "")) return;
    try {
      await updateAdminUser(u.id, { monthly_limit: want === "" ? -1 : Number(want) });
      toast(`Đã lưu giới hạn của ${u.name || u.email}`);
      load();
    } catch (e) {
      fail(e);
    }
  };

  return (
    <>
      <SectionHead title="Người dùng">Gán quyền đặt prompt, cấp key và đặt giới hạn chi phí theo tháng cho từng người. Thay đổi được lưu ngay, không cần bấm lưu.</SectionHead>
      {issued && (
        <div className="flex flex-col gap-1">
          <span className="text-xs text-muted">Key cho {issued.email}:</span>
          <NewKey value={issued.key} />
        </div>
      )}
      {!users ? (
        <div className="grid h-40 place-items-center rounded-2xl border border-line">
          <Spinner />
        </div>
      ) : (
        <div className="overflow-x-auto rounded-2xl border border-line">
          <table className="w-full min-w-190 text-sm">
            <thead>
              <tr className="bg-surface-2/60 text-left text-xs text-muted">
                <th className="py-2.5 pr-2 pl-5 font-medium">Người dùng</th>
                <th className="w-48 px-2 py-2.5 font-medium">Quyền</th>
                <th className="w-52 px-2 py-2.5 font-medium">Đã dùng tháng này</th>
                <th className="w-44 px-2 py-2.5 font-medium">Giới hạn / tháng ({currency})</th>
                <th className="w-36 py-2.5 pr-5 pl-2 text-right font-medium">Key</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => {
                const lim = u.monthly_limit ?? 0;
                const p = lim > 0 ? Math.min(100, Math.round((u.spent / lim) * 100)) : 0;
                return (
                  <tr key={u.id} className="border-t border-line hover:bg-fg/3">
                    <td className="py-2.5 pr-2 pl-5">
                      <div className="flex items-center gap-3">
                        <span className="grid size-8 flex-none place-items-center rounded-full bg-accent-soft text-[13px] font-medium text-on-accent-soft" aria-hidden="true">
                          {(u.name || u.email)[0]?.toUpperCase()}
                        </span>
                        <span className="min-w-0">
                          <span className="block truncate text-fg">
                            {u.name || u.email}
                            {u.id === me?.id && <span className="badge ml-2 align-middle">bạn</span>}
                          </span>
                          <span className="block truncate text-xs text-muted">{u.email}</span>
                        </span>
                      </div>
                    </td>
                    <td className="px-2 py-2.5">
                      <select
                        className="input h-9 w-full"
                        value={u.role ?? "user"}
                        disabled={u.id === me?.id}
                        aria-label={`Quyền của ${u.email}`}
                        onChange={async (e) => {
                          try {
                            await updateAdminUser(u.id, { role: e.target.value });
                            toast("Đã đổi quyền");
                            load();
                            if (u.id === me?.id) reload();
                          } catch (err) {
                            fail(err);
                          }
                        }}
                      >
                        {(["user", "prompt_editor", "admin"] as const).map((r) => (
                          <option key={r} value={r}>
                            {ROLE_LABEL[r]}
                          </option>
                        ))}
                      </select>
                    </td>
                    <td className="px-2 py-2.5">
                      <div className="text-[13px] text-fg tabular-nums">
                        {fmtMoney(u.spent, currency)}
                        {lim > 0 && <span className="text-xs text-muted"> · {p}%</span>}
                      </div>
                      {lim > 0 && (
                        <div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-surface-3" role="progressbar" aria-valuenow={p} aria-valuemin={0} aria-valuemax={100} aria-label={`Đã dùng ${p}% giới hạn`}>
                          <i className={"block h-full rounded-full " + (p >= 100 ? "bg-err" : p >= 80 ? "bg-warn" : "bg-accent")} style={{ width: `${p}%` }} />
                        </div>
                      )}
                    </td>
                    <td className="px-2 py-2.5">
                      <input
                        className="input h-9 w-full tabular-nums"
                        inputMode="numeric"
                        placeholder="Không giới hạn"
                        value={limits[u.id] ?? ""}
                        aria-label={`Giới hạn mỗi tháng của ${u.email}`}
                        onChange={(e) => setLimits((m) => ({ ...m, [u.id]: e.target.value }))}
                        onBlur={() => saveLimit(u)}
                        onKeyDown={(e) => e.key === "Enter" && e.currentTarget.blur()}
                      />
                    </td>
                    <td className="py-2.5 pr-3 pl-2 text-right whitespace-nowrap">
                      <span className="badge mr-1 tabular-nums" title={`${u.keys} key đang có`}>
                        <Icon name="key" size={14} /> {u.keys}
                      </span>
                      <button
                        className="btn btn-text btn-sm"
                        title="Cấp key mới cho người này"
                        onClick={async () => {
                          try {
                            const r = await issueAPIKey(u.id, "web + script");
                            setIssued({ email: u.email, key: r.api_key });
                            load();
                          } catch (e) {
                            fail(e);
                          }
                        }}
                      >
                        Cấp key
                      </button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      <dl className="grid gap-x-8 gap-y-2 text-xs text-muted sm:grid-cols-2">
        <div>
          <dt className="font-medium text-fg">Được đặt prompt</dt>
          <dd>Viết prompt tuỳ chỉnh, tạo và phát hành mẫu chat dùng chung.</dd>
        </div>
        <div>
          <dt className="font-medium text-fg">Người dùng</dt>
          <dd>Chọn mẫu có sẵn, hỏi đáp, xuất và sửa Excel.</dd>
        </div>
      </dl>
    </>
  );
}
