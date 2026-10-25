import { useCallback, useEffect, useState, type ReactNode } from "react";
import { NavLink, useParams } from "react-router-dom";

import { changePassword, createAPIKey, listAPIKeys, revokeAPIKey } from "../api/auth";
import { getAdminUsage, getLLMKey, getMyUsage, issueAPIKey, listAdminUsers, setLLMKey, updateAdminUser } from "../api/endpoints";
import { isAdmin, type AdminUser, type APIKey, type LLMKeyView, type UsageSummary } from "../api/types";
import { fmtMoney, ROLE_LABEL } from "../lib/format";
import { useAuth } from "./AuthContext";
import { ChecksTab } from "./ChecksTab";
import { DocTypesTab } from "./DocTypes";
import { FormsTab } from "./FormsTab";
import { TopBar } from "./Layout";
import { useToast } from "./toast";
import { Icon, Modal, Spinner } from "./ui";

export type SettingsTab = "account" | "checks" | "doctypes" | "forms" | "keys" | "users";

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
            ["keys", "Chi phí AI"],
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
// of each document type of a knowledge base (§7.11), its Excel forms
// (§7.12) and users for admins.
// Every section is a centred column of the same width, opened by a heading
// that says what the section is for.
export function SettingsPage() {
  const { user } = useAuth();
  const admin = isAdmin(user);
  const { tab: param } = useParams();
  const tabs: [string, string, string][] = [["doctypes", "description", "Loại giấy tờ"], ["forms", "request_page", "Mẫu xuất Excel"], ["checks", "rule", "Quy tắc đối chiếu"], ...(admin ? [["users", "group", "Người dùng"] as [string, string, string]] : [])];
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
          {tab === "forms" && <FormsTab />}
          {tab === "checks" && <ChecksTab />}
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
          <h3 className="text-sm font-medium text-fg">Tài khoản AI dùng chung</h3>
          <p className="text-xs">Một tài khoản AI cho mọi tính năng: hỏi đáp, đọc tài liệu, xuất Excel. Chi phí ở trên tính trên tài khoản này.</p>
          {llm && (
            <div className="flex items-center gap-3 rounded-xl border border-line px-3 py-2">
              <Icon name="key" size={18} className="text-accent" />
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm text-fg">{llm.provider || llm.kind}</div>
                <div className="truncate text-[11px] text-subtle">{llm.masked_key ? "Mã khoá " + llm.masked_key : "Chưa có khoá"}</div>
              </div>
              {editKey === null && (
                <button className="btn btn-text btn-sm" onClick={() => setEditKey("")}>
                  Đổi khoá
                </button>
              )}
            </div>
          )}
          {editKey !== null && (
            <div className="flex gap-2">
              <input className="input flex-1 font-mono" type="password" autoComplete="off" placeholder="Khoá mới; để trống = dùng khoá mặc định của hệ thống" value={editKey} onChange={(e) => setEditKey(e.target.value)} />
              <button className="btn btn-text" onClick={() => setEditKey(null)}>
                Huỷ
              </button>
              <button
                className="btn btn-tonal"
                onClick={async () => {
                  try {
                    setLLM(await setLLMKey(editKey));
                    setEditKey(null);
                    toast("Đã đổi khoá, áp dụng ngay");
                  } catch (e) {
                    fail(e);
                  }
                }}
              >
                Lưu khoá
              </button>
            </div>
          )}
        </section>
      )}

      <section className="flex flex-col gap-3 border-t border-line pt-4">
        <h3 className="text-sm font-medium text-fg">{admin ? "Khoá truy cập của bạn" : "Khoá truy cập được cấp"}</h3>
        <p className="text-xs">
          {admin ? "Dùng khi kết nối hệ thống khác vào BePaylot. Cấp khoá cho người khác ở tab Người dùng." : "Khoá do quản trị cấp. Chi phí ở trên tính theo khoá này; giới hạn do quản trị đặt."}
        </p>
        {created && <NewKey value={created} />}
        {admin && (
          <div className="flex gap-2">
            <input className="input flex-1" value={keyName} onChange={(e) => setKeyName(e.target.value)} placeholder="Tên khoá, ví dụ: hệ thống báo cáo" />
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
              <Icon name="key" size={18} /> Tạo khoá
            </button>
          </div>
        )}
        {keys === null ? (
          <Spinner />
        ) : keys.length === 0 ? (
          <span className="text-xs text-subtle">{admin ? "Chưa có khoá truy cập" : "Chưa được cấp khoá. Web vẫn dùng bình thường; cần kết nối hệ thống khác thì liên hệ quản trị."}</span>
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
      <span className="text-xs font-medium">Khoá mới — chỉ hiện một lần, hãy sao chép ngay:</span>
      <div className="flex items-center gap-2">
        <code className="min-w-0 flex-1 truncate rounded-md bg-surface px-2 py-1 font-mono text-xs text-fg">{value}</code>
        <button className="btn-icon btn-sm" title="Sao chép" onClick={() => navigator.clipboard.writeText(value).then(() => toast("Đã sao chép khoá"), fail)}>
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
      <SectionHead title="Người dùng">Phân quyền sửa hướng dẫn AI, cấp khoá truy cập và đặt giới hạn chi phí theo tháng cho từng người. Thay đổi được lưu ngay, không cần bấm lưu.</SectionHead>
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
                      <span className="badge mr-1 tabular-nums" title={`${u.keys} khoá đang có`}>
                        <Icon name="key" size={14} /> {u.keys}
                      </span>
                      <button
                        className="btn btn-text btn-sm"
                        title="Cấp khoá mới cho người này"
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
          <dt className="font-medium text-fg">Sửa hướng dẫn AI</dt>
          <dd>Viết hướng dẫn riêng cho trợ lý, phát hành mẫu trả lời dùng chung; sửa loại giấy tờ, mẫu Excel và quy tắc đối chiếu.</dd>
        </div>
        <div>
          <dt className="font-medium text-fg">Người dùng</dt>
          <dd>Chọn mẫu có sẵn, hỏi đáp, xuất và sửa Excel.</dd>
        </div>
      </dl>
    </>
  );
}
