import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";

import { downloadSheet, getSheet, importSheet, saveSheetEdits } from "../../api/endpoints";
import type { SheetCellView, SheetImport, SheetRowView, SheetTableView, SheetView } from "../../api/types";
import { PageImage } from "../../components/PageImage";
import { useToast } from "../../components/toast";
import { Empty, FileIcon, Icon, Loading, Modal } from "../../components/ui";
import { fmtDate } from "../../lib/format";

// The comparison form of a value, like the server's (§6.9.2): accents and
// case ignored; a value of digits and separators compares by its digits, so
// 15.000.000.000 equals 15000000000.
function valueKey(s: string) {
  const t = (s ?? "")
    .trim()
    .normalize("NFD")
    .replace(/[̀-ͯ]/g, "")
    .replace(/đ/gi, "d")
    .toLowerCase();
  if (/^[\d\s.,\-/_']+$/.test(t) && /\d/.test(t)) return t.replace(/\D/g, "");
  return t.replace(/[^\p{L}\p{N}]/gu, "");
}

type Origin = "page" | "xlsx";
interface Draft {
  value: string;
  origin: Origin;
}
type Panel = "source" | "edits" | null;

const XL = "#107c41";
const draftKey = (id: string) => "bp.sheetDraft." + id;
const PANEL_KEY = "bp.sheetPanel";
const rowIdOf = (r: { segment_id?: string; document_id?: string }) => r.segment_id || r.document_id || "";
const cellId = (table: string, rowId: string, key: string) => `${table}|${rowId}|${key}`;
const col = (i: number) => String.fromCharCode(65 + i);
const pages = (a?: number, b?: number) => (!a ? "" : a === b ? `tr. ${a}` : `tr. ${a}–${b}`);

interface Cell {
  id: string;
  table: SheetTableView;
  row: SheetRowView;
  key: string;
  label: string;
  cv: SheetCellView;
  ref: string; // A1-style reference in its sheet
}

// The sheet page (§7.6, U47): one spreadsheet tab per document type, fields
// as columns and one document per row, column A the bundle. Sources,
// confidence and the edit history stay in the right panel, collapsed until
// the user opens it. Cells that differ from the AI value are tracked; saving
// makes them confirmed fields and records the AI's errors.
export function SheetPage() {
  const { caseId = "", sheetId = "" } = useParams();
  const nav = useNavigate();
  const { toast, fail } = useToast();
  const [sheet, setSheet] = useState<SheetView | null>(null);
  const [err, setErr] = useState("");
  const [drafts, setDrafts] = useState<Record<string, Draft>>(() => {
    try {
      return JSON.parse(localStorage.getItem(draftKey(sheetId)) || "{}");
    } catch {
      return {};
    }
  });
  const [imported, setImported] = useState<SheetImport | null>(null);
  const [tab, setTab] = useState("");
  const [sel, setSel] = useState<string | null>(null);
  const [panel, setPanelState] = useState<Panel>(() => {
    try {
      return (localStorage.getItem(PANEL_KEY) as Panel) || null;
    } catch {
      return null;
    }
  });
  const [dl, setDl] = useState(false);
  const [saving, setSaving] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);

  const setPanel = (p: Panel) => {
    setPanelState(p);
    try {
      if (p) localStorage.setItem(PANEL_KEY, p);
      else localStorage.removeItem(PANEL_KEY);
    } catch {
      /* private mode */
    }
  };

  const load = useCallback(() => getSheet(sheetId).then(setSheet, (e) => setErr((e as Error).message)), [sheetId]);
  useEffect(() => {
    load();
  }, [load]);
  // Still building: poll.
  useEffect(() => {
    if (!sheet || sheet.status === "done" || sheet.status === "failed") return;
    const t = setInterval(load, 2000);
    return () => clearInterval(t);
  }, [sheet, load]);
  // Unsaved edits survive leaving the page (§7.6).
  useEffect(() => {
    try {
      if (Object.keys(drafts).length) localStorage.setItem(draftKey(sheetId), JSON.stringify(drafts));
      else localStorage.removeItem(draftKey(sheetId));
    } catch {
      /* private mode */
    }
  }, [drafts, sheetId]);

  const tables = useMemo(() => sheet?.tables_view ?? [], [sheet]);
  const table = tables.find((t) => t.label === tab) ?? tables[0];

  // Every cell of the sheet, for the edit list and lookups.
  const cells = useMemo(() => {
    const m = new Map<string, Cell>();
    for (const t of tables)
      t.rows.forEach((row, ri) =>
        t.fields.forEach((f, fi) => {
          const id = cellId(t.label, rowIdOf(row), f.key);
          m.set(id, { id, table: t, row, key: f.key, label: f.label, cv: row.cells_view[f.key], ref: col(fi + 1) + (ri + 2) });
        }),
      );
    return m;
  }, [tables]);
  const valueOf = (c: Cell) => drafts[c.id]?.value ?? c.cv?.value ?? "";
  const all = [...cells.values()];
  const unsaved = all.filter((c) => drafts[c.id] && valueKey(drafts[c.id].value) !== valueKey(c.cv?.value ?? ""));
  const changes = all.filter((c) => valueKey(valueOf(c)) !== valueKey(c.cv?.ai_value_text ?? ""));

  const setValue = (c: Cell, value: string, origin: Origin = "page") =>
    setDrafts((d) => {
      const next = { ...d };
      if (valueKey(value) === valueKey(c.cv?.value ?? "")) delete next[c.id];
      else next[c.id] = { value, origin };
      return next;
    });

  const save = async () => {
    if (!sheet) return;
    setSaving(true);
    try {
      const n = unsaved.filter((c) => valueKey(drafts[c.id].value) !== valueKey(c.cv?.ai_value_text ?? "")).length;
      const v = await saveSheetEdits(
        sheet.id,
        unsaved.map((c) => ({
          table: c.table.label,
          segment_id: c.row.segment_id,
          document_id: c.row.segment_id ? undefined : c.row.document_id,
          key: c.key,
          value: drafts[c.id].value,
          origin: drafts[c.id].origin,
        })),
      );
      setSheet(v);
      setDrafts({});
      setImported(null);
      toast(n ? `Đã lưu ${unsaved.length} chỉnh sửa và ghi nhận ${n} lỗi AI cho mẫu v${v.template_version}` : `Đã lưu ${unsaved.length} chỉnh sửa`);
    } catch (e) {
      fail(e);
    } finally {
      setSaving(false);
    }
  };

  const upload = async (f: File) => {
    try {
      const res = await importSheet(sheetId, f);
      setImported(res);
      for (const e of res.edits) {
        const c = cells.get(cellId(e.table, e.segment_id || e.document_id || "", e.key));
        if (c) setValue(c, e.value, "xlsx");
      }
      setPanel("edits");
      toast(
        `Đã đọc file: ${res.edits.length} ô khác giá trị lúc tải` +
          (res.conflicts.length ? `, ${res.conflicts.length} xung đột` : "") +
          (res.ignored.length ? `, ${res.ignored.length} ô thêm tay bị bỏ qua` : ""),
      );
    } catch (e) {
      fail(e);
    }
  };

  const back = () => {
    if (unsaved.length) toast("Còn chỉnh sửa chưa lưu, chúng vẫn được giữ trong bản nháp");
    nav(`/cases/${caseId}`);
  };

  if (err)
    return (
      <Empty icon="error" title="Không mở được bảng">
        {err}
      </Empty>
    );
  if (!sheet) return <Loading />;
  const selCell = sel ? cells.get(sel) : undefined;
  const nDocs = tables.reduce((a, t) => a + t.rows.length, 0);
  const nBundles = new Set(tables.flatMap((t) => t.rows.map((r) => r.bundle)).filter(Boolean)).size;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="flex min-h-16 flex-none flex-wrap items-center gap-2 px-4 pt-3 pb-2">
        <button className="btn btn-text" onClick={back} title="Quay lại phương án">
          <Icon name="arrow_back" size={20} /> Phương án {sheet.case_code}
        </button>
        <span className="h-6 border-l border-line max-sm:hidden" />
        <div className="min-w-0 flex-1 px-2">
          <h1 className="flex items-center gap-2 truncate text-[22px] leading-7 font-normal text-fg">
            <Icon name="table_view" size={24} fill className="text-[#107c41]" />
            {sheet.name}
          </h1>
          <div className="truncate text-xs text-muted">
            Mẫu {sheet.template_name || sheet.name} v{sheet.template_version} · tạo {fmtDate(sheet.created_at)} · {tables.length} bảng · {nDocs} giấy tờ
            {nBundles ? ` · ${nBundles} bộ` : ""}
          </div>
        </div>
        <input ref={fileInput} type="file" accept=".xlsx" hidden onChange={(e) => e.target.files?.[0] && upload(e.target.files[0])} />
        <button className="btn" onClick={() => fileInput.current?.click()} disabled={sheet.status !== "done"}>
          <Icon name="upload_file" size={18} /> Tải lên bản đã sửa
        </button>
        <button className="btn" onClick={() => setDl(true)} disabled={sheet.status !== "done"}>
          <Icon name="download" size={18} /> Tải .xlsx
        </button>
        <button className="btn btn-primary" onClick={save} disabled={!unsaved.length || saving}>
          <Icon name="save" size={18} /> Lưu{unsaved.length ? ` ${unsaved.length} chỉnh sửa` : ""}
        </button>
        <button
          className={"btn-icon " + (panel ? "bg-accent-soft text-on-accent-soft" : "")}
          onClick={() => setPanel(panel ? null : "source")}
          title={(panel ? "Ẩn" : "Hiện") + " nguồn & lịch sử"}
        >
          <Icon name={panel ? "right_panel_close" : "right_panel_open"} size={22} />
        </button>
      </header>

      {sheet.status !== "done" ? (
        <Empty icon={sheet.status === "failed" ? "error" : "hourglass_top"} title={sheet.status === "failed" ? "Không tạo được bảng" : `Đang bóc tách ${sheet.filled}/${sheet.total} ô…`}>
          {sheet.error || "Ô đã duyệt được dùng lại; agent đọc các trang của từng bộ cho ô còn thiếu."}
        </Empty>
      ) : (
        <div className="flex min-h-0 flex-1 border-t border-line max-lg:flex-col">
          <div className="flex min-h-0 min-w-0 flex-1 flex-col">
            <div className="min-h-0 flex-1 overflow-auto">{table && <Grid table={table} drafts={drafts} sel={sel} onSelect={setSel} onValue={setValue} cells={cells} />}</div>
            <div className="flex flex-none flex-wrap gap-1 border-t border-line bg-surface-2 px-2 pt-1">
              {tables.map((t) => (
                <button
                  key={t.label || "_"}
                  onClick={() => (setTab(t.label), setSel(null))}
                  className={"rounded-t-md px-4 py-1.5 text-[13px] " + (table === t ? "border-b-2 bg-surface font-medium" : "text-muted")}
                  style={table === t ? { color: XL, borderColor: XL } : undefined}
                >
                  {t.title || "Cả file"} <span className="text-xs text-muted">{t.rows.length}</span>
                </button>
              ))}
              <span className="flex-1" />
              <div className="flex flex-wrap items-center gap-3 px-2 text-[11.5px] text-muted">
                <span className="flex items-center gap-1">
                  <i className="inline-block size-3 rounded-sm bg-accent-container" /> người dùng sửa
                </span>
                <span className="flex items-center gap-1">
                  <i className="inline-block size-3 rounded-sm bg-warn-soft" /> cần xem
                </span>
                <span className="flex items-center gap-1">
                  <i className="inline-block size-3 rounded-sm bg-vlm-soft" /> agent tính
                </span>
              </div>
            </div>
          </div>

          {!panel ? (
            <aside className="flex flex-none items-center gap-1 border-line p-1.5 max-lg:border-t lg:w-12 lg:flex-col lg:border-l">
              <button className="btn-icon" onClick={() => setPanel("source")} title="Nguồn của ô">
                <Icon name="find_in_page" size={22} />
              </button>
              <button className="btn-icon relative" onClick={() => setPanel("edits")} title="Lịch sử chỉnh sửa">
                <Icon name="history" size={22} />
                {changes.length > 0 && (
                  <span className="absolute top-1 right-1 grid h-4 min-w-4 place-items-center rounded-full bg-accent px-1 text-[10px] text-on-accent">{changes.length}</span>
                )}
              </button>
            </aside>
          ) : (
            <aside className="flex w-full flex-none flex-col border-line max-lg:max-h-[50%] max-lg:border-t lg:w-88 lg:border-l">
              <div className="flex flex-none items-center border-b border-line px-2">
                <button className={"tab " + (panel === "source" ? "tab-active" : "")} onClick={() => setPanel("source")}>
                  Nguồn{selCell ? ` ô ${selCell.ref}` : ""}
                </button>
                <button className={"tab " + (panel === "edits" ? "tab-active" : "")} onClick={() => setPanel("edits")}>
                  Lịch sử chỉnh sửa {changes.length ? `(${changes.length})` : ""}
                </button>
                <span className="flex-1" />
                <button className="btn-icon btn-sm" onClick={() => setPanel(null)} title="Thu gọn">
                  <Icon name="right_panel_close" size={20} />
                </button>
              </div>
              <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-auto p-3">
                {panel === "edits" ? (
                  <Edits
                    sheet={sheet}
                    changes={changes}
                    drafts={drafts}
                    imported={imported}
                    valueOf={valueOf}
                    onPick={(c) => (setTab(c.table.label), setSel(c.id))}
                    onUndo={(c) =>
                      setDrafts((m) => {
                        const n = { ...m };
                        delete n[c.id];
                        return n;
                      })
                    }
                    onConflict={(e, keep) => {
                      const c = cells.get(cellId(e.table, e.segment_id || e.document_id || "", e.key));
                      if (keep && c) setValue(c, e.value, "xlsx");
                      setImported((m) => m && { ...m, conflicts: m.conflicts.filter((x) => x !== e) });
                    }}
                  />
                ) : selCell ? (
                  <Source c={selCell} value={valueOf(selCell)} />
                ) : (
                  <div className="px-2 py-6 text-center text-sm text-subtle">Chọn một ô trên lưới để xem nguồn, độ tin cậy và trạng thái.</div>
                )}
              </div>
            </aside>
          )}
        </div>
      )}
      {dl && <DownloadDialog sheet={sheet} onClose={() => setDl(false)} onDownload={(labels) => downloadSheet(sheet, labels).then(() => setDl(false), fail)} />}
    </div>
  );
}

// One sub-table as a spreadsheet: column A the bundle (or file), then one
// column per field; rows of one bundle are grouped with a rule between
// bundles. Only values: no confidence or source columns (U47).
function Grid({
  table,
  cells,
  drafts,
  sel,
  onSelect,
  onValue,
}: {
  table: SheetTableView;
  cells: Map<string, Cell>;
  drafts: Record<string, Draft>;
  sel: string | null;
  onSelect: (id: string) => void;
  onValue: (c: Cell, value: string) => void;
}) {
  const head = "sticky top-0 z-2 h-6 border-r border-b border-line bg-surface-2 px-2 text-center text-[11.5px] font-normal text-subtle";
  const td = "border-r border-b border-line bg-surface";
  const widths = table.fields.map((f) =>
    Math.min(34, Math.max(12, f.label.length + 2, ...table.rows.map((r) => (r.cells_view[f.key]?.value ?? "").length + 3))),
  );
  return (
    <table className="border-separate border-spacing-0 text-[13px]">
      <thead>
        <tr>
          <th className={head + " sticky left-0 z-3 w-10 min-w-10"} />
          <th className={head + " sticky left-10 z-3 min-w-16"}>A</th>
          {table.fields.map((f, i) => (
            <th key={f.key} className={head}>
              {col(i + 1)}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        <tr>
          <td className="sticky left-0 z-1 border-r border-b border-line bg-surface-2 text-center text-[11.5px] text-subtle">1</td>
          <td className="sticky left-10 z-1 border-r border-b border-line bg-[#107c41]/12 px-2.5 py-1.5 font-medium text-fg">{table.label ? "Bộ" : "File"}</td>
          {table.fields.map((f, i) => (
            <td key={f.key} className="border-r border-b border-line bg-[#107c41]/12 px-2.5 py-1.5 font-medium whitespace-nowrap text-fg" style={{ minWidth: widths[i] + "ch" }}>
              {f.label}
            </td>
          ))}
        </tr>
        {table.rows.map((row, ri) => {
          const rowId = rowIdOf(row);
          const bstart = ri > 0 && table.rows[ri - 1].bundle !== row.bundle;
          const selected = sel?.startsWith(table.label + "|" + rowId + "|");
          const rule = bstart ? " border-t-2 border-t-outline" : "";
          return (
            <tr key={rowId}>
              <td className={"sticky left-0 z-1 border-r border-b border-line text-center text-[11.5px]" + rule + (selected ? " bg-[#107c41] text-white" : " bg-surface-2 text-subtle")}>{ri + 2}</td>
              <td className={"sticky left-10 z-1 px-2.5 py-1.5 font-medium whitespace-nowrap text-fg " + td + rule} title={`${row.file_name ?? ""} · ${pages(row.page_start, row.page_end)}`}>
                {table.label ? row.bundle : <span className="font-normal">{row.file_name}</span>}
              </td>
              {table.fields.map((f) => {
                const c = cells.get(cellId(table.label, rowId, f.key))!;
                const cv = c.cv;
                const v = drafts[c.id]?.value ?? cv?.value ?? "";
                const edited = valueKey(v) !== valueKey(cv?.ai_value_text ?? "");
                const tone = edited ? "bg-accent-container" : cv?.calc ? "bg-vlm-soft" : cv?.low ? "bg-warn-soft" : "bg-surface";
                return (
                  <td
                    key={f.key}
                    className={"relative border-r border-b border-line p-0 focus-within:outline-2 focus-within:-outline-offset-2 focus-within:outline-[#107c41] " + tone + rule}
                  >
                    <input
                      className="h-full min-h-8 w-full bg-transparent px-2.5 py-1.5 text-fg tabular-nums outline-none"
                      value={v}
                      placeholder={cv?.field_id ? "" : "—"}
                      onFocus={() => onSelect(c.id)}
                      onChange={(e) => onValue(c, e.target.value)}
                      onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
                      aria-label={`${f.label} · ${row.bundle || row.file_name}`}
                    />
                    {edited && <span className="pointer-events-none absolute top-0 right-0 border-[5px] border-transparent border-t-accent border-r-accent" />}
                  </td>
                );
              })}
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

function StatusBadge({ cv, edited }: { cv?: SheetCellView; edited: boolean }) {
  if (edited) return <span className="badge badge-info">người dùng sửa</span>;
  if (!cv?.field_id) return <span className="badge">không tìm thấy</span>;
  if (cv.status === "confirmed") return <span className="badge badge-ok">đã duyệt</span>;
  if (cv.calc) return <span className="badge badge-vlm">agent tính</span>;
  if (cv.low) return <span className="badge badge-warn">cần xem</span>;
  return <span className="badge">AI đề xuất</span>;
}

// Where a cell's value comes from: its document, the page with the evidence
// boxed, the quote, the confidence and the AI status.
function Source({ c, value }: { c: Cell; value: string }) {
  const cv = c.cv;
  const e = cv?.evidence?.[0];
  const edited = valueKey(value) !== valueKey(cv?.ai_value_text ?? "");
  const box = e?.bbox && e.bbox.length === 4 ? [{ key: "ev", bbox: { x0: e.bbox[0], y0: e.bbox[1], x1: e.bbox[2], y1: e.bbox[3] }, className: "hl" }] : [];
  return (
    <>
      <div className="flex items-center gap-2">
        <span className="flex-1 text-sm font-medium text-fg">{c.label}</span>
        <span className="font-mono text-[11px] text-subtle">{c.ref}</span>
      </div>
      <div className="flex flex-wrap gap-1">
        {c.row.bundle && <span className="badge">Bộ {c.row.bundle}</span>}
        <span className="badge">{c.table.title || "Cả file"}</span>
        <StatusBadge cv={cv} edited={edited} />
      </div>
      <div className="flex items-center gap-2 text-xs text-muted">
        <FileIcon name={c.row.file_name} size={16} /> {c.row.file_name} · {e ? `trang ${e.page_no}` : pages(c.row.page_start, c.row.page_end)}
      </div>
      {!e ? (
        <div className="rounded-lg bg-surface-2 p-3 text-[13px] text-muted">
          {cv?.note ? "Agent: " + cv.note : cv?.field_id ? "Giá trị do người dùng nhập, không có nguồn." : "Agent không tìm thấy trường này trong giấy tờ. Ô vẫn lưu được dù không có nguồn."}
        </div>
      ) : (
        <>
          <div className="flex flex-col gap-1">
            <div className="flex text-xs">
              <span className="flex-1 text-muted">Độ tin cậy</span>
              <span className="text-fg tabular-nums">{Math.round((cv?.confidence ?? 0) * 100)}%</span>
            </div>
            <div className="h-1.5 overflow-hidden rounded-full bg-surface-3">
              <i className={"block h-full rounded-full " + (cv?.low ? "bg-warn" : "bg-ok")} style={{ width: `${Math.round((cv?.confidence ?? 0) * 100)}%` }} />
            </div>
          </div>
          <div className="overflow-hidden rounded-xl border border-line bg-surface-2">
            <PageImage docId={e.document_id} pageNo={e.page_no} boxes={box} scrollTo={box[0]?.key} />
          </div>
          <div className="rounded-lg bg-surface-2 p-3 text-[13px]">
            <div className="text-xs text-muted">Câu gốc</div>
            {e.quote}
          </div>
          {cv && !cv.value_matched && cv.source === "agent" && (
            <div className="rounded-lg bg-warn-soft p-2 text-xs text-warn">{cv.field_note ? "Giá trị do agent tính: " + cv.field_note : "Giá trị AI không xuất hiện nguyên văn trong câu gốc."}</div>
          )}
        </>
      )}
      {edited && (
        <div className="text-xs text-muted">
          Giá trị AI: <s>{cv?.ai_value_text || "(trống)"}</s>
        </div>
      )}
    </>
  );
}

// The edit history: every cell that differs from the AI value, the
// conflicts and ignored cells of an uploaded file.
function Edits({
  sheet,
  changes,
  drafts,
  imported,
  valueOf,
  onPick,
  onUndo,
  onConflict,
}: {
  sheet: SheetView;
  changes: Cell[];
  drafts: Record<string, Draft>;
  imported: SheetImport | null;
  valueOf: (c: Cell) => string;
  onPick: (c: Cell) => void;
  onUndo: (c: Cell) => void;
  onConflict: (e: SheetImport["conflicts"][number], keepExcel: boolean) => void;
}) {
  return (
    <>
      {imported?.conflicts.map((c) => (
        <div key={"c" + c.sheet + c.cell} className="rounded-xl border border-warn p-3">
          <div className="flex items-center gap-2">
            <span className="min-w-0 flex-1 truncate text-sm font-medium text-fg">{c.label}</span>
            <span className="badge badge-warn">xung đột</span>
          </div>
          <div className="mt-1 text-xs">
            {c.sheet} · {c.cell}: trên web đã đổi thành "{c.web_value}" sau lúc tải file.
          </div>
          <div className="mt-2 flex gap-2">
            <button className="btn btn-sm" onClick={() => onConflict(c, true)}>
              Giữ bản Excel
            </button>
            <button className="btn btn-text btn-sm" onClick={() => onConflict(c, false)}>
              Giữ bản trên web
            </button>
          </div>
        </div>
      ))}
      {changes.length ? (
        changes.map((c) => {
          const d = drafts[c.id];
          return (
            <div key={c.id} className="cursor-pointer rounded-xl border border-line p-3 hover:bg-fg/4" onClick={() => onPick(c)}>
              <div className="flex items-center gap-2">
                <span className="min-w-0 flex-1 truncate text-sm font-medium text-fg">{c.label}</span>
                <span className="font-mono text-[11px] text-subtle">
                  {c.table.title || "Cả file"} · {c.ref}
                </span>
              </div>
              <div className="text-xs text-muted">
                {c.row.bundle ? `Bộ ${c.row.bundle} · ` : ""}
                {c.row.file_name} · {pages(c.row.page_start, c.row.page_end)}
              </div>
              <div className="mt-1 text-[13px] text-err line-through">{c.cv?.ai_value_text || "(AI không tìm thấy)"}</div>
              <div className="text-[13px] font-medium text-ok">{valueOf(c) || "(để trống)"}</div>
              <div className="mt-2 flex items-center gap-2">
                <span className={"badge " + (!d ? "badge-ok" : d.origin === "xlsx" ? "badge-vlm" : "badge-info")}>
                  {!d ? "đã lưu" : d.origin === "xlsx" ? "từ file Excel tải lên" : "sửa trên trang này"}
                </span>
                <span className="flex-1" />
                {d && (
                  <button
                    className="btn btn-text btn-sm"
                    onClick={(e) => {
                      e.stopPropagation();
                      onUndo(c);
                    }}
                  >
                    Hoàn tác
                  </button>
                )}
              </div>
            </div>
          );
        })
      ) : (
        <div className="px-2 py-6 text-center text-sm text-subtle">
          Chưa có ô nào khác giá trị AI.
          <br />
          Sửa trực tiếp trên lưới hoặc tải lên file .xlsx đã sửa.
        </div>
      )}
      {!!imported?.ignored.length && (
        <div className="rounded-xl bg-surface-2 p-3 text-xs">
          <div className="mb-1 font-medium text-fg">Bỏ qua khi đọc file</div>
          {imported.ignored.map((x) => (
            <div key={x.sheet + x.cell}>
              {x.sheet} · {x.cell}: {x.text} — {x.reason}
            </div>
          ))}
        </div>
      )}
      <p className="px-1 text-xs text-muted">
        Khi lưu, mỗi chỉnh sửa thành giá trị đã duyệt của trường; ô khác giá trị AI được ghi là <b>AI sai</b> cho mẫu "{sheet.template_name || sheet.name}" v{sheet.template_version}, theo bảng và
        trường.
      </p>
    </>
  );
}

// Which sub-tables go into the .xlsx: one sheet each, data only.
function DownloadDialog({ sheet, onClose, onDownload }: { sheet: SheetView; onClose: () => void; onDownload: (labels: string[]) => void }) {
  const [pick, setPick] = useState<Record<string, boolean>>(() => Object.fromEntries(sheet.tables_view.map((t) => [t.label, true])));
  const chosen = sheet.tables_view.filter((t) => pick[t.label]).map((t) => t.label);
  return (
    <Modal
      title="Tải .xlsx"
      icon="download"
      onClose={onClose}
      width={520}
      footer={
        <>
          <button className="btn btn-text" onClick={onClose}>
            Huỷ
          </button>
          <button className="btn btn-primary" disabled={!chosen.length} onClick={() => onDownload(chosen.length === sheet.tables_view.length ? [] : chosen)}>
            <Icon name="download" size={18} /> Tải {chosen.length} sheet
          </button>
        </>
      }
    >
      <p className="text-sm">Mỗi bảng được chọn là một sheet: dòng 1 là tên trường, cột A là Bộ, mỗi giấy tờ một dòng. File chỉ có dữ liệu, không có cột nguồn, độ tin cậy hay trạng thái.</p>
      <div className="flex flex-col gap-1 rounded-xl border border-line p-1.5">
        {sheet.tables_view.map((t) => (
          <label key={t.label || "_"} className="flex h-10 cursor-pointer items-center gap-3 rounded-lg px-2 text-sm text-fg hover:bg-fg/5">
            <input type="checkbox" checked={!!pick[t.label]} onChange={(e) => setPick((p) => ({ ...p, [t.label]: e.target.checked }))} />
            <span className="flex-1">{t.title || "Cả file"}</span>
            <span className="text-xs text-muted">
              {t.rows.length} dòng · {t.fields.length + 1} cột
            </span>
          </label>
        ))}
      </div>
      <p className="text-xs">Kèm một sheet ẩn chứa mã ô để nhập lại bản đã sửa.</p>
    </Modal>
  );
}
