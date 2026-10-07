import { useEffect, useState } from "react";

import { addTemplateVersion, createTemplate, listTemplates, publishTemplate, templateCorrections } from "../../api/endpoints";
import { canEditPrompts, type CorrectionStat, type PromptTemplate, type SheetField } from "../../api/types";
import { useAuth } from "../../components/AuthContext";
import { useToast } from "../../components/toast";
import { Icon, Modal } from "../../components/ui";

export interface ChatConfig {
  templateId: string; // "" = default agent
  custom?: string; // prompt editors only
}

const slug = (s: string) =>
  (
    s
      .normalize("NFD")
      .replace(/[̀-ͯ]/g, "")
      .replace(/đ/gi, "d")
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "_")
      .replace(/^_+|_+$/g, "")
      .slice(0, 40) || "mau"
  ) +
  "_" +
  Math.random().toString(36).slice(2, 6);

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

// A starting point for the first sheet template of a "phương án vay vốn".
const DEFAULT_SHEET: { name: string; body: string; fields: SheetField[] } = {
  name: "Bảng tổng hợp phương án vay",
  body: "Bóc tách các trường dưới đây từ các nguồn của phương án. Tìm đúng trang qua cây hồ sơ, đọc trang rồi ghi bằng kb_save_fields kèm citation_id của dòng gốc. Không tìm thấy thì không ghi và nêu lý do. Giá trị tính (tỷ lệ, tổng) phải ghi note cách tính.",
  fields: [
    { key: "ten_doanh_nghiep", label: "Tên doanh nghiệp", value_type: "string" },
    { key: "ma_so_thue", label: "Mã số thuế", value_type: "string" },
    { key: "nguoi_dai_dien", label: "Người đại diện theo pháp luật", value_type: "string" },
    { key: "von_dieu_le", label: "Vốn điều lệ (đồng)", value_type: "money" },
    { key: "doanh_thu_nam_gan_nhat", label: "Doanh thu thuần năm gần nhất (đồng)", value_type: "money" },
    { key: "loi_nhuan_sau_thue", label: "Lợi nhuận sau thuế năm gần nhất (đồng)", value_type: "money" },
    { key: "tong_tai_san", label: "Tổng tài sản (đồng)", value_type: "money" },
    { key: "so_tien_vay", label: "Số tiền đề nghị vay (đồng)", value_type: "money" },
    { key: "thoi_han_vay", label: "Thời hạn vay (tháng)", value_type: "number" },
    { key: "muc_dich_vay", label: "Mục đích vay", value_type: "string" },
    { key: "tai_san_dam_bao", label: "Tài sản bảo đảm", value_type: "string" },
    { key: "gia_tri_tsbd", label: "Giá trị TSBĐ định giá (đồng)", value_type: "money" },
    { key: "ty_le_vay_tsbd", label: "Tỷ lệ vay / TSBĐ", value_type: "number" },
  ],
};
const TYPES = ["string", "number", "money", "date", "bool"];

// Sheet template of the Studio's Excel card (§7.7): users pick a published
// one; prompt editors also edit its fields and prompt (a new version, then
// published) and see how often users corrected each field.
export function SheetTemplateDialog({
  templates,
  caseType,
  value,
  onClose,
  onSave,
}: {
  templates: PromptTemplate[];
  caseType?: string;
  value: string;
  onClose: () => void;
  onSave: (id: string, templates?: PromptTemplate[]) => void;
}) {
  const { user } = useAuth();
  const { toast, fail } = useToast();
  const editor = canEditPrompts(user);
  const [pick, setPick] = useState(value || templates[0]?.id || "");
  const [creating, setCreating] = useState(editor && templates.length === 0);
  const cur = templates.find((t) => t.id === pick);
  const [name, setName] = useState("");
  const [body, setBody] = useState("");
  const [fields, setFields] = useState<SheetField[]>([]);
  const [stats, setStats] = useState<CorrectionStat[] | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    const src = creating ? DEFAULT_SHEET : cur;
    setName(creating ? DEFAULT_SHEET.name : (cur?.name ?? ""));
    setBody(src?.body ?? "");
    setFields((src?.fields ?? []).map((f) => ({ ...f })));
  }, [creating, cur]);
  useEffect(() => {
    setStats(null);
    if (editor && cur && !creating) templateCorrections(cur.id).then(setStats, () => setStats([]));
  }, [editor, cur, creating]);

  const setField = (i: number, f: Partial<SheetField>) => setFields((xs) => xs.map((x, j) => (j === i ? { ...x, ...f } : x)));
  const publish = async () => {
    setBusy(true);
    try {
      const clean = fields.filter((f) => f.key.trim() && f.label.trim()).map((f) => ({ ...f, key: f.key.trim() }));
      let t: PromptTemplate;
      if (creating) t = await createTemplate({ kind: "sheet", slug: slug(name), name: name.trim(), case_type: caseType, body, fields: clean });
      else t = await addTemplateVersion(pick, { name: name.trim(), body, fields: clean });
      t = await publishTemplate(t.id, t.version);
      toast(`Đã phát hành ${t.name} v${t.current_version}`);
      const list = await listTemplates("sheet", caseType);
      setCreating(false);
      setPick(t.id);
      onSave(t.id, list);
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  };

  const latest = stats?.length ? Math.max(...stats.map((s) => s.version)) : 0;
  return (
    <Modal
      title={editor ? "Tuỳ chỉnh bảng Excel" : "Chọn mẫu bảng Excel"}
      icon="table_view"
      onClose={onClose}
      width={editor ? 720 : 560}
      footer={
        <>
          <button className="btn btn-text" onClick={onClose}>
            Huỷ
          </button>
          {editor && (
            <button className="btn" onClick={publish} disabled={busy || !name.trim() || !body.trim() || !fields.length}>
              {creating ? "Tạo & phát hành" : `Lưu & phát hành v${(cur?.latest_version ?? 0) + 1}`}
            </button>
          )}
          {!creating && (
            <button className="btn btn-primary" onClick={() => onSave(pick)} disabled={!pick}>
              Dùng mẫu này
            </button>
          )}
        </>
      }
    >
      {templates.length > 0 && (
        <div className="flex flex-col gap-2">
          {templates.map((t) => (
            <label key={t.id} className={"flex cursor-pointer gap-3 rounded-xl border px-4 py-3 " + (pick === t.id && !creating ? "border-[#107c41] bg-[#107c41]/8" : "border-line hover:bg-fg/4")}>
              <input
                type="radio"
                checked={pick === t.id && !creating}
                onChange={() => {
                  setPick(t.id);
                  setCreating(false);
                }}
                className="mt-1"
              />
              <span>
                <span className="block text-sm font-medium text-fg">{t.name}</span>
                <span className="block text-xs">
                  {t.fields?.length ?? "?"} trường · v{t.current_version}
                  {t.created_by_name ? ` · do ${t.created_by_name} soạn` : ""}
                </span>
              </span>
            </label>
          ))}
        </div>
      )}
      {!editor ? (
        templates.length === 0 ? (
          <div className="rounded-xl bg-surface-2 px-4 py-3 text-sm">Chưa có mẫu bảng nào. Nhờ người có quyền "Được đặt prompt" tạo mẫu.</div>
        ) : (
          <div className="flex gap-3 rounded-xl bg-surface-2 px-4 py-3 text-xs">
            <Icon name="lock" size={20} className="text-subtle" />
            <div>Danh sách trường và prompt của mẫu do người được cấp quyền đặt prompt quản lý. Bạn chọn mẫu có sẵn.</div>
          </div>
        )
      ) : (
        <>
          <div className="flex items-center gap-2">
            <h3 className="flex-1 text-sm font-medium text-fg">{creating ? "Mẫu bảng mới" : "Trường của bảng"}</h3>
            {!creating ? (
              <button className="btn btn-text btn-sm" onClick={() => setCreating(true)}>
                <Icon name="add" size={18} /> Mẫu mới
              </button>
            ) : (
              templates.length > 0 && (
                <button className="btn btn-text btn-sm" onClick={() => setCreating(false)}>
                  Huỷ tạo mới
                </button>
              )
            )}
          </div>
          <label className="field">
            <span>Tên mẫu</span>
            <input className="input" value={name} onChange={(e) => setName(e.target.value)} />
          </label>
          <div className="overflow-x-auto rounded-xl border border-line">
            <table className="w-full min-w-130 text-[13px]">
              <thead>
                <tr className="bg-surface-2 text-left text-xs">
                  <th className="px-3 py-2 font-medium">Key</th>
                  <th className="px-3 py-2 font-medium">Nhãn cột</th>
                  <th className="px-3 py-2 font-medium">Kiểu</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {fields.map((f, i) => (
                  <tr key={i} className="border-t border-line">
                    <td className="px-2 py-1">
                      <input className="w-full bg-transparent px-1 py-1 font-mono text-xs text-fg outline-none focus:bg-surface-2" value={f.key} onChange={(e) => setField(i, { key: e.target.value })} />
                    </td>
                    <td className="px-2 py-1">
                      <input className="w-full bg-transparent px-1 py-1 text-fg outline-none focus:bg-surface-2" value={f.label} onChange={(e) => setField(i, { label: e.target.value })} />
                    </td>
                    <td className="px-2 py-1">
                      <select className="bg-transparent text-fg" value={f.value_type || "string"} onChange={(e) => setField(i, { value_type: e.target.value })}>
                        {TYPES.map((t) => (
                          <option key={t}>{t}</option>
                        ))}
                      </select>
                    </td>
                    <td className="pr-2 text-right">
                      <button className="btn-icon btn-sm" title="Xoá trường" onClick={() => setFields((xs) => xs.filter((_, j) => j !== i))}>
                        <Icon name="close" size={18} />
                      </button>
                    </td>
                  </tr>
                ))}
                <tr className="border-t border-line">
                  <td colSpan={4} className="px-3 py-1.5">
                    <button className="text-xs text-accent" onClick={() => setFields((xs) => [...xs, { key: "", label: "", value_type: "string" }])}>
                      + Thêm trường
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <label className="field">
            <span>Prompt bóc tách</span>
            <textarea className="input font-mono text-xs" rows={5} value={body} onChange={(e) => setBody(e.target.value)} />
          </label>
          {!creating && stats && stats.length > 0 && (
            <section className="flex flex-col gap-2">
              <h3 className="text-sm font-medium text-fg">Tỷ lệ AI sai theo trường · v{latest}</h3>
              <div className="overflow-x-auto rounded-xl border border-line">
                <table className="w-full text-[13px]">
                  <tbody>
                    {stats
                      .filter((s) => s.version === latest)
                      .map((s) => {
                        const p = Math.round(s.rate * 100);
                        return (
                          <tr key={s.key} className="border-t border-line first:border-0">
                            <td className="px-3 py-1.5 font-mono text-xs text-fg">{s.key}</td>
                            <td className="w-1/3 px-3 py-1.5">
                              <div className="h-1.5 overflow-hidden rounded-full bg-surface-3">
                                <i className={"block h-full rounded-full " + (p > 15 ? "bg-err" : p > 8 ? "bg-warn" : "bg-accent")} style={{ width: `${Math.max(p, 1)}%` }} />
                              </div>
                            </td>
                            <td className="px-3 py-1.5 text-right text-xs tabular-nums">
                              {s.edited}/{s.sheets} · {p}%
                            </td>
                            <td className="px-3 py-1.5 text-xs">{s.examples?.[0] ?? ""}</td>
                          </tr>
                        );
                      })}
                  </tbody>
                </table>
              </div>
            </section>
          )}
        </>
      )}
    </Modal>
  );
}
