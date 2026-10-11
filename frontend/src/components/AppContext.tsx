import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";

import { settings } from "../api/client";
import { createKB, listCases, listEngines, listKBs } from "../api/endpoints";
import type { Case, ClassLabel, EngineInfo, KnowledgeBase } from "../api/types";
import { SettingsDialog, type SettingsTab } from "./SettingsDialog";
import { useToast } from "./toast";
import { Icon, Modal } from "./ui";

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
  // Settings opens on a tab: account, keys (key & cost), users (admin), server.
  openSettings: (tab?: SettingsTab) => void;
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
  const [showSettings, setShowSettings] = useState<SettingsTab | null>(null);
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
    openSettings: (tab) => setShowSettings(tab ?? "account"),
    openCreateKB: () => setShowCreate(true),
  };

  return (
    <Ctx.Provider value={value}>
      {children}
      {showSettings && (
        <SettingsDialog
          tab={showSettings}
          onClose={() => setShowSettings(null)}
          onSaved={() => {
            setShowSettings(null);
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
  const [docTypes, setDocTypes] = useState<ClassLabel[]>([]);
  const [busy, setBusy] = useState(false);
  const setType = (i: number, patch: Partial<ClassLabel>) => setDocTypes((xs) => xs.map((x, k) => (k === i ? { ...x, ...patch } : x)));
  useEffect(() => {
    listEngines().then(setEngines, () => {});
  }, []);
  const submit = async () => {
    if (!name.trim()) return onError(new Error("Nhập tên knowledge base"));
    const types = docTypes
      .map((t) => ({ name: t.name.trim(), title: t.title.trim(), description: t.description?.trim() || undefined }))
      .filter((t) => t.name || t.title);
    if (types.some((t) => !/^[a-z0-9_][a-z0-9_-]*$/.test(t.name) || !t.title))
      return onError(new Error("Mỗi loại giấy tờ cần mã (chữ thường, số, _ hoặc -) và tên"));
    setBusy(true);
    try {
      onCreated(await createKB({ name: name.trim(), description: desc.trim(), config: { parser_engine: engine, doc_types: types } }));
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
      <div className="field">
        <span>Loại giấy tờ</span>
        <span className="font-normal">Mỗi trang của file được phân loại theo ảnh vào các loại này; trang liền nhau cùng loại gom thành một giấy tờ trên cây hồ sơ.</span>
        {docTypes.map((t, i) => (
          <div key={i} className="flex items-center gap-2">
            <input className="input w-32" value={t.name} onChange={(e) => setType(i, { name: e.target.value })} placeholder="Mã, vd hop_dong" />
            <input className="input w-36" value={t.title} onChange={(e) => setType(i, { title: e.target.value })} placeholder="Tên, vd Hợp đồng" />
            <input className="input min-w-0 flex-1" value={t.description ?? ""} onChange={(e) => setType(i, { description: e.target.value })} placeholder="Mô tả (tuỳ chọn)" />
            <button className="btn-icon" title="Xoá loại" onClick={() => setDocTypes((xs) => xs.filter((_, k) => k !== i))}>
              <Icon name="close" size={18} />
            </button>
          </div>
        ))}
        <button className="btn btn-text self-start" onClick={() => setDocTypes((xs) => [...xs, { name: "", title: "" }])}>
          <Icon name="add" size={18} /> Thêm loại giấy tờ
        </button>
      </div>
    </Modal>
  );
}
