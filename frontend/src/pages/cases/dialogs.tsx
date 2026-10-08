import { useEffect, useState } from "react";

import { addTemplateVersion, createTemplate, listTemplates, publishTemplate, templateCorrections } from "../../api/endpoints";
import { canEditPrompts, type ClassLabel, type CorrectionStat, type PromptTemplate, type SheetField, type SheetTable } from "../../api/types";
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

// Starting points for a first sheet template: one sub-table per document
// type the case type knows (a payment bundle), else one table of a whole file
// (a loan plan).
const DEFAULT_BODY =
  "Với mỗi giấy tờ được liệt kê (ref, loại, trang), bóc tách các trường còn thiếu. Đọc đúng các trang của giấy tờ, ghi bằng kb_save_fields với segment và citation_id của dòng gốc. Không tìm thấy thì không ghi và nêu lý do. Giá trị tính (tỷ lệ, tổng) phải ghi note cách tính.";
const DEFAULT_FIELDS: Record<string, SheetField[]> = {
  hop_dong: [
    { key: "so_hop_dong", label: "Số hợp đồng", value_type: "string" },
    { key: "ngay_ky", label: "Ngày ký", value_type: "date" },
    { key: "ben_ban", label: "Bên bán", value_type: "string" },
    { key: "ben_mua", label: "Bên mua", value_type: "string" },
    { key: "gia_tri", label: "Giá trị hợp đồng (đồng)", value_type: "money" },
    { key: "thoi_han_tt", label: "Thời hạn thanh toán", value_type: "string" },
  ],
  hoa_don: [
    { key: "ky_hieu", label: "Ký hiệu", value_type: "string" },
    { key: "so_hoa_don", label: "Số hoá đơn", value_type: "string" },
    { key: "ngay_lap", label: "Ngày lập", value_type: "date" },
    { key: "mst_ban", label: "MST bên bán", value_type: "string" },
    { key: "tien_truoc_thue", label: "Tiền trước thuế", value_type: "money" },
    { key: "thue_gtgt", label: "Thuế GTGT", value_type: "money" },
    { key: "tong_tien", label: "Tổng thanh toán", value_type: "money" },
  ],
  bb_ban_giao: [
    { key: "so_bb", label: "Số biên bản", value_type: "string" },
    { key: "ngay_bg", label: "Ngày bàn giao", value_type: "date" },
    { key: "can_cu_hd", label: "Căn cứ HĐ số", value_type: "string" },
    { key: "hang_hoa", label: "Hàng hoá", value_type: "string" },
    { key: "so_luong", label: "Số lượng", value_type: "string" },
  ],
  "": [
    { key: "ten_doanh_nghiep", label: "Tên doanh nghiệp", value_type: "string" },
    { key: "ma_so_thue", label: "Mã số thuế", value_type: "string" },
    { key: "von_dieu_le", label: "Vốn điều lệ (đồng)", value_type: "money" },
    { key: "loi_nhuan_sau_thue", label: "Lợi nhuận sau thuế năm gần nhất (đồng)", value_type: "money" },
    { key: "so_tien_vay", label: "Số tiền đề nghị vay (đồng)", value_type: "money" },
    { key: "muc_dich_vay", label: "Mục đích vay", value_type: "string" },
  ],
};
function defaultSheet(labels: ClassLabel[]): { name: string; tables: SheetTable[] } {
  const typed = labels.filter((l) => DEFAULT_FIELDS[l.name]);
  if (typed.length) return { name: "Bộ chứng từ", tables: typed.map((l) => ({ label: l.name, title: l.title, fields: DEFAULT_FIELDS[l.name] })) };
  return { name: "Bảng tổng hợp phương án vay", tables: [{ label: "", title: "Tổng hợp", fields: DEFAULT_FIELDS[""] }] };
}
const TYPES = ["string", "number", "money", "date", "bool"];
const copyTables = (ts?: SheetTable[]) => (ts ?? []).map((t) => ({ ...t, fields: t.fields.map((f) => ({ ...f })) }));

// Sheet template of the Studio's Excel card (§7.7): users pick a published
// one; prompt editors also edit its sub-tables (one per document type, fields
// as columns) and prompt — a new version, then published — and see how often
// users corrected each field.
export function SheetTemplateDialog({
  templates,
  caseType,
  labels,
  value,
  onClose,
  onSave,
}: {
  templates: PromptTemplate[];
  caseType?: string;
  labels: ClassLabel[];
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
  const [tables, setTables] = useState<SheetTable[]>([]);
  const [active, setActive] = useState(0);
  const [stats, setStats] = useState<CorrectionStat[] | null>(null);
  const [busy, setBusy] = useState(false);
  const labelKey = labels.map((l) => l.name).join(",");

  useEffect(() => {
    const def = defaultSheet(labels);
    setName(creating ? def.name : (cur?.name ?? ""));
    setBody(creating ? DEFAULT_BODY : (cur?.body ?? ""));
    setTables(creating ? copyTables(def.tables) : copyTables(cur?.tables));
    setActive(0);
  }, [creating, cur, labelKey]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    setStats(null);
    if (editor && cur && !creating) templateCorrections(cur.id).then(setStats, () => setStats([]));
  }, [editor, cur, creating]);

  const table = tables[active];
  const titleOf = (l: string) => labels.find((x) => x.name === l)?.title ?? (l ? l : "Cả file");
  const setFields = (fn: (fs: SheetField[]) => SheetField[]) => setTables((ts) => ts.map((t, i) => (i === active ? { ...t, fields: fn(t.fields) } : t)));
  const setField = (i: number, f: Partial<SheetField>) => setFields((xs) => xs.map((x, j) => (j === i ? { ...x, ...f } : x)));
  const unused = [...labels.map((l) => l.name), ""].filter((l) => !tables.some((t) => t.label === l));
  const addTable = (l: string) => {
    setTables((ts) => [...ts, { label: l, title: titleOf(l), fields: [{ key: "", label: "", value_type: "string" }] }]);
    setActive(tables.length);
  };
  const publish = async () => {
    setBusy(true);
    try {
      const clean = tables
        .map((t) => ({ ...t, fields: t.fields.filter((f) => f.key.trim() && f.label.trim()).map((f) => ({ ...f, key: f.key.trim() })) }))
        .filter((t) => t.fields.length);
      let t: PromptTemplate;
      if (creating) t = await createTemplate({ kind: "sheet", slug: slug(name), name: name.trim(), case_type: caseType, body, tables: clean });
      else t = await addTemplateVersion(pick, { name: name.trim(), body, tables: clean });
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
      width={editor ? 760 : 560}
      footer={
        <>
          <button className="btn btn-text" onClick={onClose}>
            Huỷ
          </button>
          {editor && (
            <button className="btn" onClick={publish} disabled={busy || !name.trim() || !body.trim() || !tables.some((t) => t.fields.some((f) => f.key.trim()))}>
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
                  {t.tables?.length ? t.tables.map((x) => x.title || titleOf(x.label)).join(" · ") : "?"} · v{t.current_version}
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
            <div>Bảng con, trường và prompt của mẫu do người được cấp quyền đặt prompt quản lý. Bạn chọn mẫu có sẵn và chọn bảng cần tạo ở thẻ Xuất Excel.</div>
          </div>
        )
      ) : (
        <>
          <div className="flex items-center gap-2">
            <h3 className="flex-1 text-sm font-medium text-fg">{creating ? "Mẫu bảng mới" : "Bảng con theo loại giấy tờ"}</h3>
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
          <div className="flex flex-wrap items-center gap-1">
            {tables.map((t, i) => (
              <button key={t.label || "_"} className={"chip " + (i === active ? "chip-on" : "")} onClick={() => setActive(i)}>
                {t.title || titleOf(t.label)}
              </button>
            ))}
            {unused.length > 0 && (
              <select className="chip" value="" onChange={(e) => e.target.value !== "-" && addTable(e.target.value === "_" ? "" : e.target.value)} aria-label="Thêm bảng con">
                <option value="-">+ Thêm bảng con…</option>
                {unused.map((l) => (
                  <option key={l || "_"} value={l || "_"}>
                    {l ? titleOf(l) : "Cả file (mỗi file một dòng)"}
                  </option>
                ))}
              </select>
            )}
          </div>
          {table && (
            <>
              <div className="flex items-end gap-2">
                <label className="field flex-1">
                  <span>Tên sheet · loại giấy tờ: {table.label ? titleOf(table.label) : "cả file"}</span>
                  <input className="input" value={table.title} onChange={(e) => setTables((ts) => ts.map((t, i) => (i === active ? { ...t, title: e.target.value } : t)))} />
                </label>
                <button
                  className="btn btn-text btn-danger btn-sm mb-1"
                  onClick={() => {
                    setTables((ts) => ts.filter((_, i) => i !== active));
                    setActive(0);
                  }}
                >
                  Xoá bảng con
                </button>
              </div>
              <div className="overflow-x-auto rounded-xl border border-line">
                <table className="w-full min-w-130 text-[13px]">
                  <thead>
                    <tr className="bg-surface-2 text-left text-xs">
                      <th className="px-3 py-2 font-medium">Cột</th>
                      <th className="px-3 py-2 font-medium">Key</th>
                      <th className="px-3 py-2 font-medium">Nhãn cột</th>
                      <th className="px-3 py-2 font-medium">Kiểu</th>
                      <th />
                    </tr>
                  </thead>
                  <tbody>
                    {table.fields.map((f, i) => (
                      <tr key={i} className="border-t border-line">
                        <td className="px-3 py-1 font-mono text-xs">{String.fromCharCode(66 + i)}</td>
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
                      <td colSpan={5} className="px-3 py-1.5 text-xs">
                        Cột A luôn là <b>{table.label ? "Bộ" : "File"}</b> ·{" "}
                        <button className="text-accent" onClick={() => setFields((xs) => [...xs, { key: "", label: "", value_type: "string" }])}>
                          + Thêm trường
                        </button>
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </>
          )}
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
                          <tr key={s.label + "." + s.key} className="border-t border-line first:border-0">
                            <td className="px-3 py-1.5 text-xs text-muted">{titleOf(s.label)}</td>
                            <td className="px-3 py-1.5 font-mono text-xs text-fg">{s.key}</td>
                            <td className="w-1/4 px-3 py-1.5">
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
