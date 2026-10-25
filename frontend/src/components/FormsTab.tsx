import { useEffect, useRef, useState } from "react";

import { deleteExportTemplate, listExportTemplates, uploadExportTemplate } from "../api/endpoints";
import { canEditPrompts, type ExportTemplate } from "../api/types";
import { fmtDate } from "../lib/format";
import { useApp } from "./AppContext";
import { useAuth } from "./AuthContext";
import { FormDialog } from "./FormDialog";
import { useToast } from "./toast";
import { Empty, Icon, Spinner } from "./ui";

// Cài đặt → Mẫu xuất Excel (§6.9.8, §7.12): the Excel forms of a knowledge
// base. Prompt editors import a form (the code finds its table, the agent
// proposes how it is filled, the review dialog opens right away), edit its
// mapping and delete it; everyone else sees the list. Each form is a button
// in the Studio of the cases of that library.
export function FormsTab() {
  const { kbs, kb: current, reloadKBs } = useApp();
  const { user } = useAuth();
  const editor = canEditPrompts(user);
  const { toast, fail } = useToast();
  const [picked, setPick] = useState(current?.id ?? kbs[0]?.id ?? "");
  const pick = kbs.some((k) => k.id === picked) ? picked : (current?.id ?? kbs[0]?.id ?? "");
  const kb = kbs.find((k) => k.id === pick);
  const [forms, setForms] = useState<ExportTemplate[] | null>(null);
  const [editing, setEditing] = useState<ExportTemplate | null>(null);
  const [importing, setImporting] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!pick) return;
    setForms(null);
    listExportTemplates(pick).then(setForms, (e) => (setForms([]), fail(e)));
  }, [pick, fail]);

  const importForm = async (file: File) => {
    if (!kb) return;
    setImporting(true);
    try {
      const t = await uploadExportTemplate(kb.id, file);
      setForms((xs) => [...(xs ?? []), t]);
      setEditing(t);
    } catch (e) {
      fail(e);
    } finally {
      setImporting(false);
      if (fileRef.current) fileRef.current.value = "";
    }
  };

  const remove = async (t: ExportTemplate) => {
    if (!confirm(`Xoá mẫu “${t.name}”? Các bảng đã tạo vẫn giữ.`)) return;
    try {
      await deleteExportTemplate(t.id);
      setForms((xs) => (xs ?? []).filter((x) => x.id !== t.id));
    } catch (e) {
      fail(e);
    }
  };

  if (!kbs.length)
    return (
      <Empty icon="create_new_folder" title="Chưa có thư viện">
        Tạo thư viện và khai báo loại giấy tờ trước khi nhập mẫu Excel.
      </Empty>
    );
  const types = kb?.config.doc_types ?? [];
  const typeTitle = (name: string) => types.find((t) => t.name === name)?.title ?? name;
  return (
    <>
      <header className="flex flex-wrap items-end gap-x-6 gap-y-3">
        <div className="min-w-60 flex-1">
          <h2 className="text-base font-medium text-fg">Mẫu xuất Excel</h2>
          <p className="mt-1 max-w-2xl text-sm text-muted">
            Nhập file Excel mẫu (bảng kê, biểu mẫu…): hệ thống tự nhận diện bảng và đề xuất cột nào lấy trường nào, công thức nào. Mỗi mẫu thành một nút xuất ở Studio của các hồ sơ trong thư viện.
          </p>
        </div>
        <label className="field w-full sm:w-72">
          <span>Thư viện</span>
          <select className="input" value={pick} onChange={(e) => setPick(e.target.value)}>
            {kbs.map((k) => (
              <option key={k.id} value={k.id}>
                {k.name}
              </option>
            ))}
          </select>
        </label>
      </header>

      <div className="rounded-2xl border border-line">
        <div className="flex flex-wrap items-center gap-3 border-b border-line px-4 py-3">
          <span className="min-w-0 flex-1 text-sm text-muted">
            {forms === null ? "Đang tải…" : `${forms.length} mẫu`}
            {importing && " · Đang nhận diện bảng và đề xuất cách điền…"}
          </span>
          {editor ? (
            <button className="btn btn-primary" disabled={importing || !types.length} onClick={() => fileRef.current?.click()} title={types.length ? undefined : "Thư viện chưa có loại giấy tờ"}>
              {importing ? <Spinner className="size-4 border-2 border-white/40 border-t-white" /> : <Icon name="upload_file" size={18} />}
              Nhập mẫu Excel
            </button>
          ) : (
            <span className="text-xs text-subtle">Chỉ người được sửa hướng dẫn AI hoặc quản trị mới nhập và sửa mẫu.</span>
          )}
          <input ref={fileRef} type="file" accept=".xlsx" hidden onChange={(e) => e.target.files?.[0] && importForm(e.target.files[0])} />
        </div>
        {forms === null ? (
          <Spinner className="m-6" />
        ) : !forms.length ? (
          <Empty icon="request_page" title="Thư viện này chưa có mẫu">
            {editor ? "Bấm “Nhập mẫu Excel” và chọn file .xlsx." : "Nhờ người được sửa hướng dẫn AI nhập mẫu cho thư viện này."}
          </Empty>
        ) : (
          forms.map((t) => (
            <div key={t.id} className="flex items-center gap-3 border-b border-line px-4 py-3 last:border-b-0">
              <Icon name="request_page" size={24} fill className="text-[#107c41]" />
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm text-fg">{t.name}</div>
                <div className="truncate text-xs text-muted">
                  {t.file_name} · mỗi dòng một {typeTitle(t.mapping.doc_type)} · {t.layout.columns.length} cột · cập nhật {fmtDate(t.updated_at)}
                </div>
              </div>
              {editor && (
                <>
                  <button className="btn btn-text btn-sm" onClick={() => setEditing(t)}>
                    <Icon name="tune" size={18} /> Sửa cách điền
                  </button>
                  <button className="btn-icon btn-sm" onClick={() => remove(t)} title="Xoá mẫu" aria-label={`Xoá mẫu ${t.name}`}>
                    <Icon name="delete" size={18} />
                  </button>
                </>
              )}
            </div>
          ))
        )}
      </div>

      {editing && (
        <FormDialog
          tpl={editing}
          types={types}
          onClose={() => setEditing(null)}
          onSaved={(t) => {
            setForms((xs) => (xs ?? []).map((x) => (x.id === t.id ? t : x)));
            setEditing(null);
            reloadKBs(); // the form's new fields are now fields of the KB
            toast("Đã lưu mẫu");
          }}
        />
      )}
    </>
  );
}
