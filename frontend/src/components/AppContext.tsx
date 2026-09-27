import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";

import { settings } from "../api/client";
import { createKB, listCases, listEngines, listKBs } from "../api/endpoints";
import type { Case, EngineInfo, KnowledgeBase } from "../api/types";
import { useToast } from "./toast";
import { Modal } from "./ui";

interface AppState {
  kbs: KnowledgeBase[];
  kb: KnowledgeBase | null;
  loadingKBs: boolean;
  connected: boolean | null;
  connError: string;
  selectKB: (id: string) => void;
  reloadKBs: () => Promise<void>;
  // Cases (hồ sơ) of the current knowledge base; the wiki, graph and chat
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
  const [showSettings, setShowSettings] = useState(!settings.key);
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

function SettingsDialog({ onClose, onSaved }: { onClose: () => void; onSaved: () => void }) {
  const [base, setBase] = useState(settings.base);
  const [key, setKey] = useState(settings.key);
  const [result, setResult] = useState<{ ok: boolean; msg: string } | null>(null);
  const [testing, setTesting] = useState(false);
  const apply = () => {
    settings.base = base;
    settings.key = key;
  };
  const test = async () => {
    apply();
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
    <Modal
      title="Kết nối backend"
      icon="settings_ethernet"
      onClose={onClose}
      footer={
        <>
          <button className="btn btn-text" onClick={test} disabled={testing}>
            {testing ? "Đang kiểm tra…" : "Kiểm tra"}
          </button>
          <button
            className="btn btn-primary"
            onClick={() => {
              apply();
              onSaved();
            }}
          >
            Lưu
          </button>
        </>
      }
    >
      <label className="field">
        <span>API base URL</span>
        <input className="input" value={base} onChange={(e) => setBase(e.target.value)} placeholder="để trống = cùng origin (proxy /v1 của Vite)" />
      </label>
      <label className="field">
        <span>API key (header x-api-key)</span>
        <input className="input" type="password" autoComplete="off" value={key} onChange={(e) => setKey(e.target.value)} placeholder="bp_…" />
      </label>
      <p className="text-xs text-muted">
        Key lưu trong localStorage của trình duyệt này. Tạo key bằng <code>make seed</code> ở thư mục backend.
      </p>
      {result && <span className={"badge self-start " + (result.ok ? "badge-ok" : "badge-err")}>{result.msg}</span>}
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
