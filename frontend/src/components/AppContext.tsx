import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { useNavigate } from "react-router-dom";

import { settings } from "../api/client";
import { createKB, listCases, listEngines, listKBs, updateKB } from "../api/endpoints";
import type { Case, ClassLabel, DocType, EngineInfo, KnowledgeBase } from "../api/types";
import { SettingsDialog, type SettingsTab } from "./SettingsDialog";
import { useToast } from "./toast";
import { toCode, uniqueCode } from "../lib/format";
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
  // auto: a type added here, whose code follows its name (U65); a saved type
  // keeps its code, the pages already classified carry it.
  const [docTypes, setDocTypes] = useState<(DocType & { auto?: boolean })[]>(kb?.config.doc_types ?? []);
  const [busy, setBusy] = useState(false);
  // A second click lands before `busy` disables the button; the ref stops it
  // from sending the request twice.
  const sending = useRef(false);
  // The list always ends with one empty row (i === docTypes.length): typing
  // in it adds the type and the next empty row appears, so there is no "add"
  // button.
  const withRow = (xs: (DocType & { auto?: boolean })[], i: number) => (i >= xs.length ? [...xs, { name: "", title: "", auto: true }] : xs);
  const setType = (i: number, patch: Partial<ClassLabel>) => setDocTypes((xs) => withRow(xs, i).map((x, k) => (k === i ? { ...x, ...patch } : x)));
  const setTitle = (i: number, title: string) =>
    setDocTypes((xs) =>
      withRow(xs, i).map((x, k, all) => {
        if (k !== i) return x;
        if (!x.auto) return { ...x, title };
        const c = toCode(title);
        const taken = ["other", "unknown", ...all.filter((_, j) => j !== i).map((y) => y.name)];
        return { ...x, title, name: c ? uniqueCode(c, taken) : "" };
      }),
    );
  useEffect(() => {
    listEngines().then(setEngines, () => {});
  }, []);
  const submit = async () => {
    if (!name.trim()) return onError(new Error("Nhập tên thư viện"));
    const types = docTypes
      .map((t) => ({ name: t.name.trim(), title: t.title.trim(), description: t.description?.trim() || undefined, fields: t.fields }))
      .filter((t) => t.name || t.title);
    if (types.some((t) => !/^[a-z0-9_][a-z0-9_-]*$/.test(t.name) || !t.title))
      return onError(new Error("Mỗi loại giấy tờ cần tên có chữ hoặc số (mã tự sinh từ tên)"));
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
      <div className="field">
        <span>Loại giấy tờ</span>
        <span className="font-normal">
          Mỗi trang của file được phân loại theo ảnh vào các loại này; trang liền nhau cùng loại gom thành một giấy tờ trên cây hồ sơ.
          {kb && " Thay đổi áp dụng cho file tải lên hoặc xử lý lại sau khi lưu."}
        </span>
        {[...docTypes, null].map((t, i) => (
          <div key={i} className="flex items-center gap-2">
            <input
              className="input w-36"
              value={t?.title ?? ""}
              onChange={(e) => setTitle(i, e.target.value)}
              placeholder={t ? "Tên loại" : docTypes.length ? "Loại tiếp theo…" : "Tên, vd Hợp đồng"}
              aria-label={t ? `Tên loại giấy tờ ${i + 1}` : "Tên loại giấy tờ mới"}
            />
            <span className="w-32 flex-none truncate font-mono text-xs text-muted" title="Mã tự sinh từ tên">
              {t?.name || "mã tự sinh"}
            </span>
            <input className="input min-w-0 flex-1" value={t?.description ?? ""} onChange={(e) => setType(i, { description: e.target.value })} placeholder="Mô tả (tuỳ chọn)" aria-label="Mô tả loại giấy tờ" />
            <button className={"btn-icon " + (t ? "" : "invisible")} tabIndex={-1} title="Xoá loại" onClick={() => setDocTypes((xs) => xs.filter((_, k) => k !== i))}>
              <Icon name="close" size={18} />
            </button>
          </div>
        ))}
      </div>
    </Modal>
  );
}
