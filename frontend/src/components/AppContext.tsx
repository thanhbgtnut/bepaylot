import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { Link, useNavigate } from "react-router-dom";

import { settings } from "../api/client";
import { createKB, listCases, listDocTypes, listEngines, listKBs, updateKB } from "../api/endpoints";
import type { Case, DocTypeView, EngineInfo, KnowledgeBase } from "../api/types";
import { SettingsDialog, type SettingsTab } from "./SettingsDialog";
import { useToast } from "./toast";
import { Modal, Spinner } from "./ui";

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
  // account and keys (key & cost) open the "Thông tin" dialog; doctypes and
  // users (admin) open that tab of the "Cài đặt" page.
  openSettings: (tab?: SettingsTab) => void;
  openCreateKB: () => void;
  openEditKB: (kb: KnowledgeBase) => void;
}

const Ctx = createContext<AppState>(null!);
export const useApp = () => useContext(Ctx);

export function AppProvider({ children }: { children: ReactNode }) {
  const { fail } = useToast();
  const nav = useNavigate();
  const [kbs, setKBs] = useState<KnowledgeBase[]>([]);
  const [kbId, setKbId] = useState(settings.kb);
  const [loadingKBs, setLoading] = useState(true);
  const [connected, setConnected] = useState<boolean | null>(null);
  const [connError, setConnError] = useState("");
  const [showSettings, setShowSettings] = useState<"account" | "keys" | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  const [editKB, setEditKB] = useState<KnowledgeBase | null>(null);

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
    openSettings: (tab = "account") => (tab === "account" || tab === "keys" ? setShowSettings(tab) : nav("/settings/" + tab)),
    openCreateKB: () => setShowCreate(true),
    openEditKB: setEditKB,
  };

  return (
    <Ctx.Provider value={value}>
      {children}
      {showSettings && (
        <SettingsDialog tab={showSettings} onClose={() => setShowSettings(null)} />
      )}
      {showCreate && (
        <KBDialog
          onClose={() => setShowCreate(false)}
          onSaved={async (kb) => {
            setShowCreate(false);
            await reloadKBs();
            setKbId(kb.id);
          }}
          onError={fail}
        />
      )}
      {editKB && (
        <KBDialog
          kb={editKB}
          onClose={() => setEditKB(null)}
          onSaved={(kb) => {
            setEditKB(null);
            setKBs((xs) => xs.map((x) => (x.id === kb.id ? kb : x)));
          }}
          onError={fail}
        />
      )}
    </Ctx.Provider>
  );
}

// KBDialog creates a knowledge base, or edits `kb` (name, description, OCR
// engine and document types).
function KBDialog({
  kb,
  onClose,
  onSaved,
  onError,
}: {
  kb?: KnowledgeBase;
  onClose: () => void;
  onSaved: (kb: KnowledgeBase) => void;
  onError: (e: unknown) => void;
}) {
  const [engines, setEngines] = useState<EngineInfo[]>([]);
  const [name, setName] = useState(kb?.name ?? "");
  const [desc, setDesc] = useState(kb?.description ?? "");
  const [engine, setEngine] = useState(kb?.config.parser_engine ?? "");
  // The types of the catalogue this library uses (U97), in its order.
  const [docTypes, setDocTypes] = useState<string[]>((kb?.config.doc_types ?? []).map((t) => t.name));
  const [catalog, setCatalog] = useState<DocTypeView[] | null>(null);
  const [busy, setBusy] = useState(false);
  // A second click lands before `busy` disables the button; the ref stops it
  // from sending the request twice.
  const sending = useRef(false);
  useEffect(() => {
    listEngines().then(setEngines, () => {});
    listDocTypes().then(setCatalog, () => setCatalog([]));
  }, []);
  const submit = async () => {
    if (!name.trim()) return onError(new Error("Nhập tên thư viện"));
    // Only the codes: title, description and fields stay in the catalogue.
    const types = docTypes.map((name) => ({ name, title: "" }));
    if (sending.current) return;
    sending.current = true;
    setBusy(true);
    try {
      // PATCH replaces the whole config, so an edit sends what it has.
      const body = { name: name.trim(), description: desc.trim(), config: { ...kb?.config, parser_engine: engine, doc_types: types } };
      onSaved(kb ? await updateKB(kb.id, body) : await createKB(body));
    } catch (e) {
      onError(e);
    } finally {
      sending.current = false;
      setBusy(false);
    }
  };
  return (
    <Modal
      title={kb ? "Sửa thư viện" : "Thư viện mới"}
      icon={kb ? "edit" : "create_new_folder"}
      onClose={onClose}
      footer={
        <>
          <button className="btn btn-text" onClick={onClose}>
            Huỷ
          </button>
          <button className="btn btn-primary" onClick={submit} disabled={busy}>
            {kb ? "Lưu" : "Tạo"}
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
      <fieldset className="field">
        <legend className="mb-1">Loại giấy tờ</legend>
        <span className="font-normal">
          Chọn từ danh mục chung. Trang được phân loại theo các loại đã chọn.
          {kb && " Thay đổi áp dụng cho file tải lên hoặc xử lý lại sau khi lưu."} Tạo loại mới hoặc sửa trường, hướng dẫn AI ở{" "}
          <Link to="/settings/doctypes" className="text-accent hover:underline" onClick={onClose}>
            Cài đặt → Loại giấy tờ
          </Link>
          .
        </span>
        {!catalog ? (
          <Spinner className="size-4 border-2" />
        ) : !catalog.length ? (
          <span className="text-xs font-normal text-subtle">Danh mục chưa có loại giấy tờ nào.</span>
        ) : (
          <div className="flex max-h-56 flex-col overflow-auto">
            {catalog.map((t) => (
              <label key={t.name} className="flex min-h-9 items-center gap-3 text-sm font-normal text-fg">
                <input
                  type="checkbox"
                  className="size-4 accent-accent"
                  checked={docTypes.includes(t.name)}
                  onChange={(e) => setDocTypes((xs) => (e.target.checked ? [...xs, t.name] : xs.filter((x) => x !== t.name)))}
                />
                <span className="min-w-0 flex-1 truncate">{t.title}</span>
                <span className="text-xs text-subtle">{t.fields.length ? `${t.fields.length} trường` : "chưa có trường"}</span>
              </label>
            ))}
          </div>
        )}
      </fieldset>
    </Modal>
  );
}
