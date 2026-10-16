import { useState } from "react";

import { saveExportTemplate } from "../api/endpoints";
import type { DocType, ExportColumnMap, ExportFillMap, ExportMapping, ExportTemplate, SheetField } from "../api/types";
import { useToast } from "./toast";
import { Icon, Modal, Spinner } from "./ui";

// The review of an imported Excel form (§6.9.8): the code found the table and
// the agent proposed how each column is filled; the user only changes what is
// wrong and saves. A column the agent could not work out is "Để trống", and
// its formula can be typed here or built later in Excel.
export function FormDialog({
  tpl,
  types,
  onClose,
  onSaved,
}: {
  tpl: ExportTemplate;
  types: DocType[];
  onClose: () => void;
  onSaved: (t: ExportTemplate) => void;
}) {
  const { fail } = useToast();
  const [name, setName] = useState(tpl.name);
  const [m, setM] = useState<ExportMapping>(tpl.mapping);
  const [saving, setSaving] = useState(false);
  // placeholders switched to a fixed text that is still empty
  const [textMode, setTextMode] = useState<Record<string, boolean>>({});
  const l = tpl.layout;

  // Fields of a type: those of the KB and those the agent proposes to add.
  const fieldsOf = (dt: string): (SheetField & { isNew?: boolean })[] => [
    ...(types.find((t) => t.name === dt)?.fields ?? []),
    ...(m.new_fields ?? []).filter((n) => n.doc_type === dt).map((n) => ({ ...n.field, isNew: true })),
  ];
  const rowFields = fieldsOf(m.doc_type);
  const col = (c: string) => m.columns.find((x) => x.col === c) ?? { col: c, kind: "blank" as const };
  const setCol = (c: string, patch: Partial<ExportColumnMap>) =>
    setM((x) => ({ ...x, columns: [...x.columns.filter((y) => y.col !== c), { ...col(c), ...patch }] }));
  const fill = (cell: string) => m.placeholders.find((x) => x.cell === cell) ?? { cell };
  const setFill = (cell: string, f: ExportFillMap) => setM((x) => ({ ...x, placeholders: [...x.placeholders.filter((y) => y.cell !== cell), f] }));

  const setType = (dt: string) => {
    const keys = new Set(fieldsOf(dt).map((f) => f.key));
    setM((x) => ({ ...x, doc_type: dt, columns: x.columns.map((c) => (c.kind === "field" && !keys.has(c.key ?? "") ? { col: c.col, kind: "blank", total: c.total } : c)) }));
  };

  const save = async () => {
    setSaving(true);
    try {
      const cols = l.columns.map((c) => col(c.col));
      const fills = l.placeholders.map((p) => fill(p.cell));
      onSaved(await saveExportTemplate(tpl.id, name, { ...m, columns: cols, placeholders: fills }));
    } catch (e) {
      fail(e);
    } finally {
      setSaving(false);
    }
  };

  const news = m.new_fields ?? [];
  return (
    <Modal
      title="Mẫu xuất Excel"
      icon="table_view"
      width={880}
      onClose={onClose}
      footer={
        <>
          <span className="mr-auto text-xs text-muted">
            Công thức: <code>D{"{r}"}</code> là ô cùng dòng, <code>D{"{first}"}:D{"{last}"}</code> là cả cột dữ liệu. Để trống thì tự dựng trong Excel sau khi tải.
          </span>
          <button className="btn btn-primary" onClick={save} disabled={saving || !m.doc_type}>
            {saving ? <Spinner className="size-4 border-2" /> : <Icon name="check" size={18} />}
            Lưu mẫu
          </button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="field">
            Tên nút xuất
            <input className="input" value={name} onChange={(e) => setName(e.target.value)} />
          </label>
          <label className="field">
            Mỗi dòng của bảng là một
            <select className="input" value={m.doc_type} onChange={(e) => setType(e.target.value)}>
              {types.map((t) => (
                <option key={t.name} value={t.name}>
                  {t.title}
                </option>
              ))}
            </select>
          </label>
        </div>
        <div className="text-xs text-muted">
          {tpl.file_name} · sheet “{l.sheet}” · tiêu đề dòng {l.header_top}
          {l.header_bottom > l.header_top ? `–${l.header_bottom}` : ""} · {l.data_rows} dòng trống từ dòng {l.data_start} (nhiều giấy tờ hơn thì tự chèn thêm dòng)
          {l.total_row ? ` · dòng tổng ${l.total_row}` : ""}
        </div>

        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-xs text-muted">
              <th className="w-10 py-1 font-medium">Cột</th>
              <th className="py-1 font-medium">Tiêu đề trong mẫu</th>
              <th className="w-56 py-1 font-medium">Lấy từ</th>
              {!!l.total_row && <th className="w-44 py-1 font-medium">Dòng tổng</th>}
            </tr>
          </thead>
          <tbody>
            {l.columns.map((c) => {
              const cm = col(c.col);
              const v = cm.kind === "field" ? "f:" + cm.key : cm.kind;
              const isNew = cm.kind === "field" && rowFields.find((f) => f.key === cm.key)?.isNew;
              return (
                <tr key={c.col} className="border-t border-line align-top">
                  <td className="py-2 font-medium text-fg">{c.col}</td>
                  <td className="py-2 pr-3">
                    <div className="text-fg">{c.header}</div>
                    {c.note && <div className="text-xs text-muted">{c.note}</div>}
                  </td>
                  <td className="py-1.5 pr-2">
                    <select
                      className="input h-9 w-full"
                      aria-label={`Cột ${c.col} lấy từ`}
                      value={v}
                      onChange={(e) => {
                        const x = e.target.value;
                        if (x.startsWith("f:")) setCol(c.col, { kind: "field", key: x.slice(2), formula: undefined });
                        else setCol(c.col, { kind: x as ExportColumnMap["kind"], key: undefined });
                      }}
                    >
                      <option value="seq">Số thứ tự</option>
                      {rowFields.map((f) => (
                        <option key={f.key} value={"f:" + f.key}>
                          {f.label}
                          {f.isNew ? " (trường mới)" : ""}
                        </option>
                      ))}
                      <option value="formula">Công thức…</option>
                      <option value="blank">Để trống</option>
                    </select>
                    {cm.kind === "formula" && (
                      <input
                        className="input mt-1 h-9 w-full font-mono text-xs"
                        aria-label={`Công thức cột ${c.col}`}
                        placeholder="SUM(D{r},E{r})"
                        value={cm.formula ?? ""}
                        onChange={(e) => setCol(c.col, { formula: e.target.value })}
                      />
                    )}
                    {isNew && <div className="mt-1 text-xs text-accent">Sẽ thêm vào loại giấy tờ và bóc tách ở lần tạo bảng tới</div>}
                  </td>
                  {!!l.total_row && (
                    <td className="py-1.5">
                      <input
                        className="input h-9 w-full font-mono text-xs"
                        aria-label={`Công thức dòng tổng cột ${c.col}`}
                        placeholder="—"
                        value={cm.total ?? ""}
                        onChange={(e) => setCol(c.col, { total: e.target.value })}
                      />
                    </td>
                  )}
                </tr>
              );
            })}
          </tbody>
        </table>

        {l.placeholders.length > 0 && (
          <div>
            <div className="pb-1 text-xs font-medium text-muted">Ô chờ điền ngoài bảng</div>
            {l.placeholders.map((p) => {
              const f = fill(p.cell);
              const v = f.key ? `${f.doc_type}|${f.key}` : f.text || textMode[p.cell] ? "text" : "";
              return (
                <div key={p.cell} className="grid grid-cols-[3rem_1fr_14rem] items-start gap-2 border-t border-line py-1.5 text-sm">
                  <span className="pt-2 font-medium text-fg">{p.cell}</span>
                  <span className="pt-2">
                    <span className="text-fg">{p.text}</span>
                    {p.note && <span className="block text-xs text-muted">{p.note}</span>}
                  </span>
                  <span>
                    <select
                      className="input h-9 w-full"
                      aria-label={`Ô ${p.cell} điền bằng`}
                      value={v}
                      onChange={(e) => {
                        const x = e.target.value;
                        setTextMode((t) => ({ ...t, [p.cell]: x === "text" }));
                        if (x === "text" || x === "") setFill(p.cell, { cell: p.cell });
                        else {
                          const [dt, key] = x.split("|");
                          setFill(p.cell, { cell: p.cell, doc_type: dt, key });
                        }
                      }}
                    >
                      <option value="">Để nguyên (người lập tự ghi)</option>
                      <option value="text">Văn bản cố định…</option>
                      {types.map((t) =>
                        fieldsOf(t.name).map((fd) => (
                          <option key={t.name + fd.key} value={`${t.name}|${fd.key}`}>
                            {t.title} · {fd.label}
                          </option>
                        )),
                      )}
                    </select>
                    {v === "text" && (
                      <input
                        className="input mt-1 h-9 w-full"
                        aria-label={`Văn bản cho ô ${p.cell}`}
                        placeholder="Chữ thay cho dấu …"
                        value={f.text ?? ""}
                        onChange={(e) => setFill(p.cell, { cell: p.cell, text: e.target.value })}
                      />
                    )}
                  </span>
                </div>
              );
            })}
          </div>
        )}
        {news.length > 0 && (
          <div className="rounded-lg bg-accent-soft px-3 py-2 text-xs text-on-accent-soft">
            Khi lưu, {news.length} trường mới được thêm vào loại giấy tờ:{" "}
            {news.map((n) => `${types.find((t) => t.name === n.doc_type)?.title ?? n.doc_type} · ${n.field.label}`).join(", ")}.
          </div>
        )}
      </div>
    </Modal>
  );
}
