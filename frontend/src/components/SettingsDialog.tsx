import { useCallback, useEffect, useState } from "react";

import { changePassword, createAPIKey, listAPIKeys, revokeAPIKey } from "../api/auth";
import { settings } from "../api/client";
import { getAdminUsage, getLLMKey, getMyUsage, issueAPIKey, listAdminUsers, listKBs, setLLMKey, updateAdminUser } from "../api/endpoints";
import { isAdmin, type AdminUser, type APIKey, type LLMKeyView, type UsageSummary } from "../api/types";
import { fmtMoney, ROLE_LABEL } from "../lib/format";
import { useAuth } from "./AuthContext";
import { useToast } from "./toast";
import { Icon, Modal, Spinner } from "./ui";

export type SettingsTab = "account" | "keys" | "users" | "server";

// Settings (§7.8): account, key & cost (a usage page like ChatGPT/Claude's,
// next to the key setup), users for admins, and the API server. Cost is shown
// only here.
export function SettingsDialog({ tab: initial, onClose, onSaved }: { tab: SettingsTab; onClose: () => void; onSaved: () => void }) {
  const { user } = useAuth();
  const admin = isAdmin(user);
  const [tab, setTab] = useState<SettingsTab>(initial === "users" && !admin ? "keys" : initial);
  const [base, setBase] = useState(settings.base);
  const tabs: [SettingsTab, string][] = [["account", "Tài khoản"], ["keys", "Key & chi phí"], ...(admin ? ([["users", "Người dùng"]] as [SettingsTab, string][]) : []), ["server", "Máy chủ"]];

  return (
    <Modal
      title="Cài đặt"
      icon="settings"
      onClose={onClose}
      width={tab === "users" ? 820 : 640}
      footer={
        <button
          className="btn btn-primary"
          onClick={() => {
            settings.base = base;
            onSaved();
          }}
        >
          Xong
        </button>
      }
    >
      <div className="-mx-6 -mt-2 flex overflow-x-auto border-b border-line px-3">
        {tabs.map(([k, l]) => (
          <button key={k} className={"tab " + (tab === k ? "tab-active" : "")} onClick={() => setTab(k)}>
            {l}
          </button>
        ))}
      </div>
      {tab === "account" && <AccountTab />}
      {tab === "keys" && <KeysTab admin={admin} />}
      {tab === "users" && admin && <UsersTab />}
      {tab === "server" && <ServerTab base={base} setBase={setBase} />}
    </Modal>
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

  const save = async () => {
    if (!users) return;
    try {
      for (const u of users) {
        const want = limits[u.id]?.replace(/[^\d]/g, "") ?? "";
        const cur = u.monthly_limit != null ? String(u.monthly_limit) : "";
        if (want !== cur) await updateAdminUser(u.id, { monthly_limit: want === "" ? -1 : Number(want) });
      }
      toast("Đã lưu giới hạn");
      load();
    } catch (e) {
      fail(e);
    }
  };

  return (
    <>
      <div className="flex flex-wrap items-center gap-2">
        <p className="min-w-60 flex-1 text-sm">Gán quyền đặt prompt, cấp key và đặt giới hạn chi phí theo tháng cho từng người.</p>
        <button className="btn btn-tonal btn-sm" onClick={save}>
          <Icon name="save" size={18} /> Lưu giới hạn
        </button>
      </div>
      {issued && (
        <div className="flex flex-col gap-1">
          <span className="text-xs">Key cho {issued.email}:</span>
          <NewKey value={issued.key} />
        </div>
      )}
      {!users ? (
        <Spinner />
      ) : (
        <div className="overflow-x-auto rounded-xl border border-line">
          <table className="w-full min-w-170 text-[13px]">
            <thead>
              <tr className="bg-surface-2 text-left text-xs">
                <th className="px-3 py-2 font-medium">Người dùng</th>
                <th className="px-3 py-2 font-medium">Quyền</th>
                <th className="px-3 py-2 font-medium">Đã dùng tháng này</th>
                <th className="px-3 py-2 font-medium">Giới hạn / tháng</th>
                <th className="px-3 py-2 text-right font-medium">Key</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => {
                const lim = u.monthly_limit ?? 0;
                const p = lim > 0 ? Math.min(100, Math.round((u.spent / lim) * 100)) : 0;
                return (
                  <tr key={u.id} className="border-t border-line align-top">
                    <td className="px-3 py-2">
                      <div className="text-fg">{u.name || u.email}</div>
                      <div className="text-xs">{u.email}</div>
                    </td>
                    <td className="px-3 py-2">
                      <select
                        className="input h-8 text-[13px]"
                        value={u.role ?? "user"}
                        disabled={u.id === me?.id}
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
                    <td className="px-3 py-2" style={{ minWidth: 150 }}>
                      {lim > 0 && (
                        <div className="h-1.5 overflow-hidden rounded-full bg-surface-3">
                          <i className={"block h-full rounded-full " + (p >= 100 ? "bg-err" : p >= 80 ? "bg-warn" : "bg-accent")} style={{ width: `${p}%` }} />
                        </div>
                      )}
                      <div className="mt-1 text-xs tabular-nums">
                        {fmtMoney(u.spent, currency)}
                        {lim > 0 ? ` · ${p}%` : ""}
                      </div>
                    </td>
                    <td className="px-3 py-2">
                      <input
                        className="input h-8 w-32 text-[13px] tabular-nums"
                        inputMode="numeric"
                        placeholder="không giới hạn"
                        value={limits[u.id] ?? ""}
                        onChange={(e) => setLimits((m) => ({ ...m, [u.id]: e.target.value }))}
                      />
                    </td>
                    <td className="px-3 py-2 text-right">
                      <span className="mr-1 tabular-nums">{u.keys}</span>
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
      <p className="text-xs">
        <b>Được đặt prompt</b>: viết prompt tuỳ chỉnh, tạo và phát hành mẫu dùng chung, sửa trường của bảng Excel, xem tỷ lệ AI sai theo trường. <b>Người dùng</b>: chọn mẫu có sẵn, hỏi đáp, xuất và sửa Excel.
      </p>
    </>
  );
}

function ServerTab({ base, setBase }: { base: string; setBase: (v: string) => void }) {
  const [result, setResult] = useState<{ ok: boolean; msg: string } | null>(null);
  const [testing, setTesting] = useState(false);
  const test = async () => {
    settings.base = base;
    setTesting(true);
    try {
      const list = await listKBs();
      setResult({ ok: true, msg: `Kết nối OK · ${list.length} knowledge base` });
    } catch (e) {
      setResult({ ok: false, msg: (e as Error).message });
    } finally {
      setTesting(false);
    }
  };
  return (
    <section className="flex flex-col gap-3">
      <h3 className="text-sm font-medium text-fg">Máy chủ API</h3>
      <div className="flex gap-2">
        <input className="input flex-1" value={base} onChange={(e) => setBase(e.target.value)} placeholder="để trống = cùng origin" />
        <button className="btn btn-text" onClick={test} disabled={testing}>
          {testing ? "Đang kiểm tra…" : "Kiểm tra"}
        </button>
      </div>
      {result && <span className={"badge self-start " + (result.ok ? "badge-ok" : "badge-err")}>{result.msg}</span>}
    </section>
  );
}
