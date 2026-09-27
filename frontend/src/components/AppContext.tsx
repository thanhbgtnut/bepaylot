import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";

import { settings } from "../api/client";
import { changePassword, createAPIKey, listAPIKeys, revokeAPIKey } from "../api/auth";
import { createKB, listCases, listEngines, listKBs } from "../api/endpoints";
import type { APIKey, Case, EngineInfo, KnowledgeBase } from "../api/types";
import { useAuth } from "./AuthContext";
import { useToast } from "./toast";
import { Icon, Modal, Spinner } from "./ui";

interface AppState {
  kbs: KnowledgeBase[];
  kb: KnowledgeBase | null;
  loadingKBs: boolean;
  connected: boolean | null;
  connError: string;
  selectKB: (id: string) => void;
  reloadKBs: () => Promise<void>;
  // Cases (hồ sơ) of the current knowledge base; the case view and the chat
  // work on one case at a time.
  cases: Case[] | null;
  kcase: Case | null;
  selectCase: (id: string) => void;
  reloadCases: () => Promise<Case[]>;
  updateKBLocal: (kb: KnowledgeBase) => void;
  openSettings: () => void;
  openCreateKB: () => void;
}

const Ctx = createContext<AppState>(null!);
export const useApp = () => useContext(Ctx);

export function AppProvider({ children }: { children: ReactNode }) {
  const { fail } = useToast();
  const [kbs, setKBs] = useState<KnowledgeBase[]>([]);
  const [kbId, setKbId] = useState(settings.kb);
  const [loadingKBs, setLoading] = useState(true);
  const [connected, setConnected] = useState<boolean | null>(null);
  const [connError, setConnError] = useState("");
  const [showSettings, setShowSettings] = useState(false);
  const [showCreate, setShowCreate] = useState(false);

  const reloadKBs = useCallback(async () => {
    setLoading(true);
    try {
      const list = await listKBs();
      setKBs(list);
      setConnected(true);
      setConnError("");
      setKbId((cur) => (list.some((k) => k.id === cur) ? cur : (list[0]?.id ?? "")));
    } catch (e) {
      setConnected(false);
      setConnError((e as Error).message);
      setKBs([]);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    reloadKBs();
  }, [reloadKBs]);
  useEffect(() => {
    settings.kb = kbId;
  }, [kbId]);

  const [cases, setCases] = useState<Case[] | null>(null);
  const [caseId, setCaseId] = useState("");
  const reloadCases = useCallback(async () => {
    if (!kbId) {
      setCases([]);
      return [];
    }
    try {
      const list = (await listCases(kbId)).sort((a, b) => a.code.localeCompare(b.code, "vi"));
      setCases(list);
      setCaseId((cur) => {
        const want = cur && list.some((c) => c.id === cur) ? cur : settings.caseOf(kbId);
        return list.some((c) => c.id === want) ? want : (list.find((c) => c.code !== "_UNASSIGNED")?.id ?? list[0]?.id ?? "");
      });
      return list;
    } catch {
      setCases([]);
      return [];
    }
  }, [kbId]);
  useEffect(() => {
    setCases(null);
    setCaseId("");
    reloadCases();
  }, [reloadCases]);
  useEffect(() => {
    if (kbId && caseId) settings.setCaseOf(kbId, caseId);
  }, [kbId, caseId]);

  const value: AppState = {
    kbs,
    kb: kbs.find((k) => k.id === kbId) ?? null,
    loadingKBs,
    connected,
    connError,
    selectKB: setKbId,
    reloadKBs,
    cases,
    kcase: cases?.find((c) => c.id === caseId) ?? null,
    selectCase: setCaseId,
    reloadCases,
    updateKBLocal: (kb) => setKBs((xs) => xs.map((x) => (x.id === kb.id ? kb : x))),
    openSettings: () => setShowSettings(true),
    openCreateKB: () => setShowCreate(true),
  };

  return (
    <Ctx.Provider value={value}>
      {children}
      {showSettings && (
        <SettingsDialog
          onClose={() => setShowSettings(false)}
          onSaved={() => {
            setShowSettings(false);
            reloadKBs();
          }}
        />
      )}
      {showCreate && (
        <CreateKBDialog
          onClose={() => setShowCreate(false)}
          onCreated={async (kb) => {
            setShowCreate(false);
            await reloadKBs();
            setKbId(kb.id);
          }}
          onError={fail}
        />
      )}
    </Ctx.Provider>
  );
}

// Account dialog: the signed-in user, password, API keys for scripts (instead
// of make seed) and the API server address.
function SettingsDialog({ onClose, onSaved }: { onClose: () => void; onSaved: () => void }) {
  const { user, signOut, signIn, config } = useAuth();
  const { toast, fail } = useToast();
  const [base, setBase] = useState(settings.base);
  const [result, setResult] = useState<{ ok: boolean; msg: string } | null>(null);
  const [testing, setTesting] = useState(false);

  const [pwOpen, setPwOpen] = useState(false);
  const [cur, setCur] = useState("");
  const [next, setNext] = useState("");
  const [pwBusy, setPwBusy] = useState(false);

  const [keys, setKeys] = useState<APIKey[] | null>(null);
  const [keyName, setKeyName] = useState("");
  const [created, setCreated] = useState("");
  const loadKeys = useCallback(() => listAPIKeys().then(setKeys, fail), [fail]);
  useEffect(() => {
    loadKeys();
  }, [loadKeys]);

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
  const savePassword = async () => {
    setPwBusy(true);
    try {
      signIn(await changePassword(cur, next));
      toast(user?.has_password ? "Đã đổi mật khẩu, các phiên khác đã bị đăng xuất" : "Đã đặt mật khẩu");
      setPwOpen(false);
      setCur("");
      setNext("");
    } catch (e) {
      fail(e);
    } finally {
      setPwBusy(false);
    }
  };
  const newKey = async () => {
    try {
      const r = await createAPIKey(keyName.trim() || "curl");
      setCreated(r.api_key);
      setKeyName("");
      loadKeys();
    } catch (e) {
      fail(e);
    }
  };
  const revoke = async (k: APIKey) => {
    if (!confirm(`Thu hồi API key "${k.name}"? Script đang dùng key này sẽ bị từ chối.`)) return;
    try {
      await revokeAPIKey(k.id);
      loadKeys();
    } catch (e) {
      fail(e);
    }
  };

  return (
    <Modal
      title="Tài khoản"
      icon="manage_accounts"
      onClose={onClose}
      width={600}
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
      {user && (
        <div className="flex items-center gap-4 rounded-2xl bg-surface-2 p-4">
          <div className="grid size-12 flex-none place-items-center rounded-full bg-accent text-xl text-on-accent">{(user.name || user.email)[0]?.toUpperCase()}</div>
          <div className="min-w-0 flex-1">
            <div className="truncate text-base text-fg">{user.name || user.email}</div>
            <div className="truncate text-xs">{user.email}</div>
            <div className="mt-1 flex gap-1">
              <span className="badge">{user.auth_provider === "local" ? "Email + mật khẩu" : "OIDC · " + user.auth_provider}</span>
              {user.is_admin && <span className="badge badge-info">Admin</span>}
              {config?.auth_bypass && <span className="badge badge-warn">auth_bypass</span>}
            </div>
          </div>
          {!config?.auth_bypass && (
            <button className="btn btn-sm" onClick={signOut}>
              <Icon name="logout" size={18} /> Đăng xuất
            </button>
          )}
        </div>
      )}

      {!config?.auth_bypass && (
        <section className="flex flex-col gap-3">
          <div className="flex items-center">
            <h3 className="flex-1 text-sm font-medium text-fg">Mật khẩu</h3>
            {!pwOpen && (
              <button className="btn btn-text btn-sm" onClick={() => setPwOpen(true)}>
                {user?.has_password ? "Đổi mật khẩu" : "Đặt mật khẩu"}
              </button>
            )}
          </div>
          {!pwOpen && !user?.has_password && <p className="text-xs">Tài khoản chưa có mật khẩu (tạo qua OIDC hoặc make seed). Đặt mật khẩu để đăng nhập bằng email.</p>}
          {pwOpen && (
            <div className="flex flex-col gap-3">
              {user?.has_password && (
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
                <button className="btn btn-tonal btn-sm" onClick={savePassword} disabled={pwBusy || !next}>
                  Lưu mật khẩu
                </button>
              </div>
            </div>
          )}
        </section>
      )}

      <section className="flex flex-col gap-3 border-t border-line pt-4">
        <h3 className="text-sm font-medium text-fg">API key cho script / curl</h3>
        <p className="text-xs">
          Gửi qua header <code>x-api-key</code>. Web đăng nhập bằng phiên riêng, không cần key.
        </p>
        {created && (
          <div className="flex flex-col gap-2 rounded-xl bg-ok-soft p-3 text-ok">
            <span className="text-xs font-medium">Key mới — chỉ hiện một lần, hãy sao chép ngay:</span>
            <div className="flex items-center gap-2">
              <code className="min-w-0 flex-1 truncate rounded-md bg-surface px-2 py-1 font-mono text-xs text-fg">{created}</code>
              <button
                className="btn-icon btn-sm"
                title="Sao chép"
                onClick={() => navigator.clipboard.writeText(created).then(() => toast("Đã sao chép API key"), fail)}
              >
                <Icon name="content_copy" size={18} />
              </button>
            </div>
          </div>
        )}
        <div className="flex gap-2">
          <input className="input flex-1" value={keyName} onChange={(e) => setKeyName(e.target.value)} placeholder="Tên key, ví dụ: curl, n8n" />
          <button className="btn btn-tonal" onClick={newKey}>
            <Icon name="key" size={18} /> Tạo key
          </button>
        </div>
        {keys === null ? (
          <Spinner />
        ) : keys.length === 0 ? (
          <span className="text-xs text-subtle">Chưa có API key</span>
        ) : (
          <ul className="flex flex-col divide-y divide-line rounded-xl border border-line">
            {keys.map((k) => (
              <li key={k.id} className="flex items-center gap-3 px-3 py-2">
                <Icon name="key" size={18} className={k.revoked_at ? "text-subtle" : "text-accent"} />
                <div className="min-w-0 flex-1">
                  <div className={"truncate text-sm " + (k.revoked_at ? "text-subtle line-through" : "text-fg")}>{k.name}</div>
                  <div className="truncate font-mono text-[11px] text-subtle">
                    {k.prefix}… · tạo {new Date(k.created_at).toLocaleDateString("vi-VN")}
                    {k.last_used_at ? " · dùng " + new Date(k.last_used_at).toLocaleString("vi-VN") : ""}
                  </div>
                </div>
                {k.revoked_at ? (
                  <span className="badge">Đã thu hồi</span>
                ) : (
                  <button className="btn btn-text btn-sm btn-danger" onClick={() => revoke(k)}>
                    Thu hồi
                  </button>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="flex flex-col gap-3 border-t border-line pt-4">
        <h3 className="text-sm font-medium text-fg">Máy chủ API</h3>
        <div className="flex gap-2">
          <input className="input flex-1" value={base} onChange={(e) => setBase(e.target.value)} placeholder="để trống = cùng origin (proxy /v1 của Vite)" />
          <button className="btn btn-text" onClick={test} disabled={testing}>
            {testing ? "Đang kiểm tra…" : "Kiểm tra"}
          </button>
        </div>
        {result && <span className={"badge self-start " + (result.ok ? "badge-ok" : "badge-err")}>{result.msg}</span>}
      </section>
    </Modal>
  );
}

function CreateKBDialog({
  onClose,
  onCreated,
  onError,
}: {
  onClose: () => void;
  onCreated: (kb: KnowledgeBase) => void;
  onError: (e: unknown) => void;
}) {
  const [engines, setEngines] = useState<EngineInfo[]>([]);
  const [name, setName] = useState("");
  const [desc, setDesc] = useState("");
  const [engine, setEngine] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    listEngines().then(setEngines, () => {});
  }, []);
  const submit = async () => {
    if (!name.trim()) return onError(new Error("Nhập tên knowledge base"));
    setBusy(true);
    try {
      onCreated(await createKB({ name: name.trim(), description: desc.trim(), config: { parser_engine: engine } }));
    } catch (e) {
      onError(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      title="Knowledge base mới"
      icon="create_new_folder"
      onClose={onClose}
      footer={
        <>
          <button className="btn btn-text" onClick={onClose}>
            Huỷ
          </button>
          <button className="btn btn-primary" onClick={submit} disabled={busy}>
            Tạo
          </button>
        </>
      }
    >
      <label className="field">
        <span>Tên</span>
        <input className="input" autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="VD: Hồ sơ đất đai 2026" />
      </label>
      <label className="field">
        <span>Mô tả</span>
        <input className="input" value={desc} onChange={(e) => setDesc(e.target.value)} />
      </label>
      <label className="field">
        <span>OCR engine</span>
        <select className="input" value={engine} onChange={(e) => setEngine(e.target.value)}>
          <option value="">Mặc định hệ thống</option>
          {engines.map((e) => (
            <option key={e.name} value={e.name}>
              {e.name}
              {e.default ? " (mặc định)" : ""}
              {e.available ? "" : " — không khả dụng"}
            </option>
          ))}
        </select>
      </label>
    </Modal>
  );
}
