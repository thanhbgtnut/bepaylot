import { useState } from "react";

import { createTemplate, listTemplates, publishTemplate } from "../../api/endpoints";
import { canEditPrompts, type PromptTemplate } from "../../api/types";
import { useAuth } from "../../components/AuthContext";
import { useToast } from "../../components/toast";
import { Icon, Modal } from "../../components/ui";
import { toCode } from "../../lib/format";

export interface ChatConfig {
  templateId: string; // "" = default agent
  custom?: string; // prompt editors only
}

const slug = (s: string) => (toCode(s) || "mau") + "_" + Math.random().toString(36).slice(2, 6);

const optCls = (on: boolean) => "flex cursor-pointer gap-3 rounded-xl border px-4 py-3 " + (on ? "border-accent bg-accent/5" : "border-line hover:bg-fg/4");

// "Cấu hình cuộc trò chuyện", like NotebookLM's Configure chat (§7.7): users
// pick a published template; only prompt editors write a custom prompt and
// may share it as a template.
export function ChatConfigDialog({
  templates,
  caseType,
  value,
  onClose,
  onSave,
}: {
  templates: PromptTemplate[];
  caseType?: string;
  value: ChatConfig;
  onClose: () => void;
  onSave: (c: ChatConfig, templates?: PromptTemplate[]) => void;
}) {
  const { user } = useAuth();
  const { toast, fail } = useToast();
  const editor = canEditPrompts(user);
  const [pick, setPick] = useState(value.custom && editor ? "custom" : value.templateId);
  const [custom, setCustom] = useState(value.custom ?? "");
  const [share, setShare] = useState(false);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);

  const save = async () => {
    if (pick !== "custom") return onSave({ templateId: pick });
    if (!custom.trim()) return fail(new Error("Nhập prompt tuỳ chỉnh"));
    if (!share) return onSave({ templateId: "", custom: custom.trim() });
    if (!name.trim()) return fail(new Error("Đặt tên cho mẫu dùng chung"));
    setBusy(true);
    try {
      const t = await createTemplate({ kind: "chat", slug: slug(name), name: name.trim(), case_type: caseType, body: custom.trim() });
      await publishTemplate(t.id, t.version);
      toast("Đã phát hành mẫu dùng chung");
      onSave({ templateId: t.id }, await listTemplates("chat", caseType));
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      title="Cấu hình cuộc trò chuyện"
      icon="tune"
      onClose={onClose}
      width={600}
      footer={
        <>
          <button className="btn btn-text" onClick={onClose}>
            Huỷ
          </button>
          <button className="btn btn-primary" onClick={save} disabled={busy}>
            Lưu
          </button>
        </>
      }
    >
      <p className="text-sm">Chọn cách agent làm việc với hồ sơ này. Áp dụng cho các câu hỏi tiếp theo trong phương án.</p>
      <div className="flex flex-col gap-2">
        <label className={optCls(pick === "")}>
          <input type="radio" checked={pick === ""} onChange={() => setPick("")} className="mt-1" />
          <span>
            <span className="block text-sm font-medium text-fg">Mặc định</span>
            <span className="block text-xs">Agent tra cây hồ sơ, đọc đúng trang rồi trả lời có trích dẫn.</span>
          </span>
        </label>
        {templates.map((t) => (
          <label key={t.id} className={optCls(pick === t.id)}>
            <input type="radio" checked={pick === t.id} onChange={() => setPick(t.id)} className="mt-1" />
            <span className="min-w-0">
              <span className="block text-sm font-medium text-fg">{t.name}</span>
              <span className="block text-xs">
                {t.description ? t.description + " " : ""}
                {t.created_by_name ? `Do ${t.created_by_name} soạn · ` : ""}v{t.current_version}
              </span>
            </span>
          </label>
        ))}
        {editor ? (
          <label className={optCls(pick === "custom")}>
            <input type="radio" checked={pick === "custom"} onChange={() => setPick("custom")} className="mt-1" />
            <span className="flex min-w-0 flex-1 flex-col gap-2">
              <span className="text-sm font-medium text-fg">Tuỳ chỉnh</span>
              <textarea
                className="input"
                rows={4}
                value={custom}
                onFocus={() => setPick("custom")}
                onChange={(e) => setCustom(e.target.value)}
                placeholder="Ví dụ: Đóng vai chuyên viên thẩm định. Mọi số liệu phải có trích dẫn trang. Nêu rõ rủi ro nếu tỷ lệ vay/TSBĐ trên 70%."
              />
              <label className="flex items-center gap-2 text-xs">
                <input type="checkbox" checked={share} onChange={(e) => setShare(e.target.checked)} /> Lưu thành mẫu dùng chung cho mọi người
              </label>
              {share && <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder="Tên mẫu, ví dụ: Thẩm định phương án vay" />}
            </span>
          </label>
        ) : (
          <div className="flex gap-3 rounded-xl border border-dashed border-outline px-4 py-3 text-xs">
            <Icon name="lock" size={20} className="text-subtle" />
            <div>
              <span className="block text-sm font-medium text-fg">Tuỳ chỉnh</span>
              Tài khoản của bạn dùng mẫu có sẵn. Chỉ người được quản trị cấp quyền "Được đặt prompt" mới viết được prompt riêng. Câu hỏi trong khung chat vẫn gửi tự do.
            </div>
          </div>
        )}
      </div>
    </Modal>
  );
}
