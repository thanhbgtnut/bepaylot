import { useEffect, useRef, useState } from "react";

import { listCaseTypes, listDocuments, reparseDocument, uploadDocuments } from "../../api/endpoints";
import type { CaseType, KnowledgeBase, UploadResult } from "../../api/types";
import { useApp } from "../../components/AppContext";
import { useToast } from "../../components/toast";
import { FileIcon, Icon, Modal, Spinner } from "../../components/ui";
import { fmtSize } from "../../lib/format";
import { PagePicker, type PickFile } from "./PagePicker";

// Bước của hộp thoại tải lên (U111): 1. tải file lên, 2. chọn trang cần xử lý.
function Stepper({ step }: { step: 1 | 2 }) {
  return (
    <div className="flex items-center gap-2 text-xs font-medium">
      {["Tải file lên", "Chọn trang cần xử lý"].map((l, i) => (
        <span key={l} className="contents">
          {i > 0 && <span className="h-px w-6 bg-line" />}
          <span className={"flex items-center gap-1.5 " + (i + 1 === step ? "text-fg" : "text-muted")}>
            <span className={"grid size-5 place-items-center rounded-full text-[11px] " + (i + 1 < step ? "bg-accent text-on-accent" : i + 1 === step ? "bg-accent-soft text-on-accent-soft" : "bg-surface-3")}>
              {i + 1 < step ? <Icon name="check" size={14} /> : i + 1}
            </span>
            {l}
          </span>
        </span>
      ))}
    </div>
  );
}

// Runs fn over items, n at a time.
async function eachLimit<T>(items: T[], n: number, fn: (x: T) => Promise<unknown>) {
  let next = 0;
  await Promise.all(
    Array.from({ length: Math.min(n, items.length) }, async () => {
      while (next < items.length) await fn(items[next++]);
    }),
  );
}

interface MetaRow {
  id: number;
  key: string;
  value: string;
}
let rowSeq = 0;

// Numbers stay numbers ("123" -> 123) except codes with a leading zero.
const typed = (v: string): unknown => (/^-?\d+(\.\d+)?$/.test(v) && !/^-?0\d/.test(v) ? Number(v) : v);

export function UploadDialog({
  kb,
  initialFiles = [],
  onClose,
  onUploaded,
  onOpen,
}: {
  kb: KnowledgeBase;
  initialFiles?: File[];
  onClose: () => void;
  onUploaded: () => void;
  onOpen: (docId: string) => void;
}) {
  const { toast, fail } = useToast();
  const { cases, kcase, reloadCases, selectCase } = useApp();
  const [code, setCode] = useState(kcase && kcase.code !== "_UNASSIGNED" ? kcase.code : "");
  const [caseType, setCaseType] = useState("");
  const [types, setTypes] = useState<CaseType[]>([]);
  useEffect(() => {
    listCaseTypes().then(setTypes, () => {});
  }, []);
  const existing = cases?.find((c) => c.code.toLowerCase() === code.trim().toLowerCase());
  const input = useRef<HTMLInputElement>(null);
  const [files, setFiles] = useState<File[]>(initialFiles);
  const [over, setOver] = useState(false);
  const schemaKeys = kb.metadata_schema?.fields?.map((f) => f.key) ?? [];
  const [rows, setRows] = useState<MetaRow[]>(() =>
    schemaKeys.map((k) => ({ id: ++rowSeq, key: k, value: "" })),
  );
  const [callback, setCallback] = useState("");
  const [progress, setProgress] = useState<number | null>(null);
  const [result, setResult] = useState<UploadResult | null>(null);
  const [busy, setBusy] = useState(false);
  // Bước 2: các file mới của lần tải, khi server đã đếm xong số trang.
  const [counting, setCounting] = useState(false);
  const [pick, setPick] = useState<PickFile[] | null>(null);

  // Files are uploaded on hold: the server only counts their pages. Wait for
  // the count, then let the user pick the pages to process (U111).
  useEffect(() => {
    if (!result?.batch_id || !result.documents?.some((a) => !a.duplicate)) return;
    const ids = new Set(result.documents.filter((a) => !a.duplicate).map((a) => a.document_id));
    let alive = true;
    let timer: ReturnType<typeof setTimeout>;
    setCounting(true);
    const poll = async () => {
      try {
        const { data } = await listDocuments(kb.id, { batchId: result.batch_id, limit: 500 });
        if (!alive) return;
        const mine = data.filter((d) => ids.has(d.id));
        if (mine.length < ids.size || mine.some((d) => d.status === "queued" || d.status === "splitting")) {
          timer = setTimeout(poll, 1000);
          return;
        }
        const failed = mine.filter((d) => d.status === "failed");
        if (failed.length) fail(new Error(`${failed.length} file không đọc được số trang: ${failed.map((d) => d.file_name).join(", ")}`));
        setPick(mine.filter((d) => d.status === "not_started").map((d) => ({ id: d.id, name: d.file_name, mime: d.mime_type, pages: d.page_count })));
        setCounting(false);
      } catch (e) {
        if (alive) (fail(e), setCounting(false));
      }
    };
    poll();
    return () => {
      alive = false;
      clearTimeout(timer);
    };
  }, [result, kb.id, fail]);

  // Copy the FileList now: it is live, and clearing the input (so the same
  // file can be picked again) empties it before a deferred state update runs.
  const add = (list: FileList | null) => {
    const picked = list ? Array.from(list) : [];
    if (picked.length) setFiles((f) => [...f, ...picked]);
  };
  const setRow = (id: number, patch: Partial<MetaRow>) => setRows((rs) => rs.map((r) => (r.id === id ? { ...r, ...patch } : r)));

  const submit = async () => {
    if (!code.trim()) return fail(new Error("Nhập mã hồ sơ: mỗi file thuộc đúng một hồ sơ"));
    const metadata: Record<string, unknown> = {};
    for (const r of rows) if (r.key.trim() && r.value.trim()) metadata[r.key.trim()] = typed(r.value.trim());
    setBusy(true);
    setProgress(0);
    try {
      const res = await uploadDocuments(kb.id, files, {
        caseCode: code.trim(),
        caseType: existing ? undefined : caseType || undefined,
        metadata,
        callbackUrl: callback.trim(),
        hold: true,
        onProgress: setProgress,
      });
      setResult(res);
      setFiles([]);
      toast(`Đã tải ${res.documents?.length ?? 0} file vào hồ sơ ${res.case?.code ?? code}${res.case?.created ? " (hồ sơ mới)" : ""}`);
      await reloadCases();
      if (res.case?.id) selectCase(res.case.id);
      onUploaded();
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  };

  const total = files.reduce((n, f) => n + f.size, 0);
  if (pick?.length)
    return (
      <PagePicker
        title="Tải file lên"
        head={
          <>
            <Stepper step={2} />
            <p className="text-sm">
              Đã tải lên <b className="font-medium text-fg">{pick.length} file</b>. Bỏ tick file không cần xử lý, sửa khoảng trang của từng file nếu chỉ cần một phần.
            </p>
          </>
        }
        files={pick}
        later
        onClose={() => {
          toast(`Đã lưu ${pick.length} file ở trạng thái Chưa xử lý`);
          onClose();
        }}
        onSubmit={async (picked) => {
          await eachLimit(picked, 6, (p) => reparseDocument(p.id, { pages: p.pages }));
          const n = picked.reduce((a, p) => a + p.pages.length, 0);
          toast(`Đang đọc & phân loại ${picked.length} file (${n} trang)${picked.length < pick.length ? ` · ${pick.length - picked.length} file để Chưa xử lý` : ""}`);
          onUploaded();
          onClose();
        }}
      />
    );
  return (
    <Modal
      title="Tải file lên"
      icon="upload_file"
      onClose={onClose}
      width={620}
      footer={
        <>
          <button className="btn btn-text" onClick={onClose}>
            {result ? "Xong" : "Huỷ"}
          </button>
          <button className="btn btn-primary" onClick={submit} disabled={!files.length || !code.trim() || busy}>
            {busy ? "Đang tải…" : `Tải lên${files.length ? ` ${files.length} file` : ""}`}
          </button>
        </>
      }
    >
      <Stepper step={1} />
      <p className="text-sm">
        Vào thư viện <b className="font-medium text-fg">{kb.name}</b>
      </p>
      <div className="grid grid-cols-[1.4fr_1fr] gap-2">
        <label className="field">
          Mã hồ sơ
          <input className="input" list="case-codes" autoFocus placeholder="VD: RT112233" value={code} onChange={(e) => setCode(e.target.value)} />
          <datalist id="case-codes">
            {cases
              ?.filter((c) => c.code !== "_UNASSIGNED")
              .map((c) => (
                <option key={c.id} value={c.code}>
                  {c.title}
                </option>
              ))}
          </datalist>
        </label>
        <label className="field">
          Loại hồ sơ
          <select className="input" value={existing ? existing.case_type : caseType} disabled={!!existing} onChange={(e) => setCaseType(e.target.value)}>
            <option value="">Mặc định</option>
            {types.map((t) => (
              <option key={t.name} value={t.name}>
                {t.title || t.name}
              </option>
            ))}
          </select>
        </label>
      </div>
      <p className="-mt-2 text-xs text-subtle">
        {code.trim() ? (existing ? "Thêm file vào hồ sơ có sẵn." : "Mã mới — hồ sơ sẽ được tạo khi tải lên.") : "Mỗi file thuộc đúng một hồ sơ; mục lục và hỏi đáp làm việc theo hồ sơ."}
      </p>
      <div
        className={
          "flex cursor-pointer flex-col items-center gap-2 rounded-2xl border-2 border-dashed px-6 py-7 text-center transition-colors " +
          (over ? "border-accent bg-accent/8 text-accent" : "border-line text-muted hover:bg-fg/4")
        }
        onClick={() => input.current?.click()}
        onDragOver={(e) => {
          e.preventDefault();
          setOver(true);
        }}
        onDragLeave={() => setOver(false)}
        onDrop={(e) => {
          e.preventDefault();
          setOver(false);
          add(e.dataTransfer.files);
        }}
      >
        <Icon name="cloud_upload" size={36} className="text-accent" />
        <div className="text-sm">
          Kéo thả file vào đây hoặc <span className="font-medium text-accent">chọn từ máy</span>
        </div>
        <div className="text-xs text-subtle">PDF, ảnh scan (JPG, PNG, TIFF) · chọn được nhiều file</div>
      </div>
      {/* Outside the drop zone, so its click does not bubble back into it. */}
      <input
        ref={input}
        type="file"
        multiple
        hidden
        accept=".pdf,image/*"
        onChange={(e) => {
          add(e.target.files);
          e.target.value = "";
        }}
      />

      {files.length > 0 && (
        <div className="overflow-hidden rounded-xl border border-line">
          <div className="flex h-10 items-center bg-surface-2 px-4 text-xs font-medium">
            <span className="flex-1">{files.length} file</span>
            <span>{fmtSize(total)}</span>
          </div>
          <div className="max-h-48 overflow-auto">
            {files.map((f, i) => (
              <div key={i} className="flex h-11 items-center gap-3 border-t border-line pr-1 pl-4 text-sm text-fg">
                <FileIcon mime={f.type} name={f.name} size={20} />
                <span className="flex-1 truncate">{f.name}</span>
                <span className="text-xs text-muted">{fmtSize(f.size)}</span>
                <button className="btn-icon btn-sm" onClick={() => setFiles((fs) => fs.filter((_, j) => j !== i))} aria-label="Bỏ file">
                  <Icon name="close" size={18} />
                </button>
              </div>
            ))}
          </div>
        </div>
      )}

      <div className="flex flex-col gap-2">
        <div className="flex items-center">
          <span className="flex-1 text-sm font-medium text-fg">Metadata</span>
          <button className="btn btn-text btn-sm" onClick={() => setRows((rs) => [...rs, { id: ++rowSeq, key: "", value: "" }])}>
            <Icon name="add" size={18} /> Thêm trường
          </button>
        </div>
        {rows.map((r) => (
          <div key={r.id} className="grid grid-cols-[1fr_1.4fr_40px] items-center gap-2">
            <input className="input" placeholder="Khoá" value={r.key} onChange={(e) => setRow(r.id, { key: e.target.value })} />
            <input className="input" placeholder="Giá trị" value={r.value} onChange={(e) => setRow(r.id, { value: e.target.value })} />
            <button className="btn-icon" aria-label="Xoá trường" onClick={() => setRows((rs) => rs.filter((x) => x.id !== r.id))}>
              <Icon name="remove_circle_outline" size={20} />
            </button>
          </div>
        ))}
        <p className="text-xs text-subtle">Áp dụng cho mọi file trong lần tải này (tuỳ chọn).</p>
      </div>

      <label className="field">
        Callback URL (tuỳ chọn)
        <input className="input" type="url" placeholder="https://… nhận POST khi xử lý xong" value={callback} onChange={(e) => setCallback(e.target.value)} />
      </label>

      {progress !== null && (
        <div>
          <div className="h-1 overflow-hidden rounded-full bg-accent/20">
            <div className="h-full rounded-full bg-accent transition-all" style={{ width: `${Math.round(progress * 100)}%` }} />
          </div>
          <div className="mt-1.5 flex items-center gap-2 text-xs">
            {busy ? (
              `Đang tải ${Math.round(progress * 100)}%…`
            ) : counting ? (
              <>
                <Spinner className="size-3 border-2" /> Đã nhận — đang đếm số trang để chọn trang cần xử lý…
              </>
            ) : (
              "Đã nhận."
            )}
          </div>
        </div>
      )}

      {result && (
        <div className="flex flex-col overflow-hidden rounded-xl border border-line">
          {result.documents?.map((a) => (
            <div key={a.document_id} className="flex h-11 items-center gap-3 border-b border-line px-4 text-sm text-fg last:border-0">
              <Icon name={a.duplicate ? "content_copy" : "check_circle"} fill size={20} className={a.duplicate ? "text-warn" : "text-ok"} />
              <span className="flex-1 truncate">{a.file_name}</span>
              <span className="text-xs text-muted">{a.duplicate ? "Đã có sẵn" : "Đã nhận"}</span>
              <button className="btn btn-text btn-sm" onClick={() => onOpen(a.document_id)}>
                Mở
              </button>
            </div>
          ))}
          {result.rejected?.map((r) => (
            <div key={r.index} className="flex min-h-11 items-center gap-3 border-b border-line px-4 py-2 text-sm text-fg last:border-0">
              <Icon name="error" fill size={20} className="text-err" />
              <span className="flex-1 truncate">{r.file_name}</span>
              <span className="text-xs text-err">{r.errors?.join("; ")}</span>
            </div>
          ))}
        </div>
      )}
    </Modal>
  );
}
