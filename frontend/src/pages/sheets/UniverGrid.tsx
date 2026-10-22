import { useEffect, useRef, useState } from "react";
import { UniverSheetsCorePreset } from "@univerjs/preset-sheets-core";
import viVN from "@univerjs/preset-sheets-core/locales/vi-VN";
import { UniverSheetsFilterPreset } from "@univerjs/preset-sheets-filter";
import filterViVN from "@univerjs/preset-sheets-filter/locales/vi-VN";
import { UniverSheetsFindReplacePreset } from "@univerjs/preset-sheets-find-replace";
import findViVN from "@univerjs/preset-sheets-find-replace/locales/vi-VN";
import { UniverSheetsSortPreset } from "@univerjs/preset-sheets-sort";
import sortViVN from "@univerjs/preset-sheets-sort/locales/vi-VN";
import {
  BorderStyleTypes,
  createUniver,
  IUndoRedoService,
  LocaleType,
  mergeLocales,
  type ICellData,
  type IStyleData,
  type IWorksheetData,
} from "@univerjs/presets";
import "@univerjs/preset-sheets-core/lib/index.css";
import "@univerjs/preset-sheets-filter/lib/index.css";
import "@univerjs/preset-sheets-find-replace/lib/index.css";
import "@univerjs/preset-sheets-sort/lib/index.css";

import type { SheetRowView, SheetTableView } from "../../api/types";

type FUniver = ReturnType<typeof createUniver>["univerAPI"];

// Where every cell of the sheets sits in the workbook: one worksheet per
// sub-table, row 0 the field labels, column A the type (or file), B the
// bundle, then the fields. `fixed` holds the cells the user may not change.
interface Layout {
  pos: Map<string, { sheet: string; r: number; c: number }>;
  at: Map<string, string>; // "sheet:r:c" → cell id
  fixed: Map<string, string>; // "sheet:r:c" → its value
  base: Map<string, IStyleData>; // cell id → its style without the status colour
  order: Map<string, number[]>; // sheet → indexes into its table's rows, top to bottom
}

// Commands that would move cells away from their field or document: rows,
// columns and ranges inserted, removed or moved, merges, sheets. Sorting is
// done here instead (sortRows), moving whole documents.
const BLOCKED =
  /^sheet\.command\.(insert-(row|col|multi|range|sheet)|remove-(row|col|sheet)|delete-range-move|move-(rows|cols|range)|add-worksheet-merge|reorder-range|copy-sheet|set-worksheet-(name|order)|paste-besides-border)/;
const XL_HEAD = "rgba(16, 124, 65, 0.12)";
const TEXT = { pattern: "@" }; // text format: extracted values stay as written (0123, 15.000.000, 01/02/2024)
const key = (sheet: string, r: number, c: number) => `${sheet}:${r}:${c}`;
const sheetOf = (ti: number) => "t" + ti;
const leadOf = (t: SheetTableView) => (t.rows.some((r) => r.bundle) ? 2 : 1);

// Sort order like Excel with Vietnamese values: dates (dd/mm/yyyy) and
// numbers (15.000.000, 2,5) by value and before text, text by Vietnamese
// collation, blanks last.
const collator = new Intl.Collator("vi", { numeric: true, sensitivity: "base" });
function sortKey(s: string): number | null {
  const d = s.trim().match(/^(\d{1,2})\/(\d{1,2})\/(\d{4})$/);
  if (d) return +d[3] * 10000 + +d[2] * 100 + +d[1];
  if (/^-?[\d. ]+(,\d+)?$/.test(s.trim()) && /\d/.test(s)) return Number(s.replace(/[. ]/g, "").replace(",", "."));
  return null;
}
function compare(x: string, y: string) {
  if (!x.trim() || !y.trim()) return !x.trim() === !y.trim() ? 0 : x.trim() ? -1 : 1;
  const a = sortKey(x);
  const b = sortKey(y);
  if (a != null && b != null) return a - b;
  if (a != null || b != null) return a != null ? -1 : 1;
  return collator.compare(x, y);
}

// The sheet grid on Univer (Excel-like: resize, select, copy/paste, undo).
// The page keeps the source of truth: values come from the AI value or the
// draft, every change in the grid goes back through onValue, so saving,
// drafts and the edit history work as before.
export default function UniverGrid({
  tables,
  idOf,
  valueOf,
  toneOf,
  tab,
  sel,
  onSelect,
  onValue,
  onOrder,
}: {
  tables: SheetTableView[];
  idOf: (t: SheetTableView, row: SheetRowView, key: string) => string;
  valueOf: (id: string) => string;
  toneOf: (id: string) => "edited" | "calc" | "low" | "";
  tab: string;
  sel: string | null;
  onSelect: (id: string) => void;
  onValue: (id: string, value: string) => void;
  onOrder: (table: string, order: number[]) => void; // rows of a table after a sort, as indexes into its rows
}) {
  const host = useRef<HTMLDivElement>(null);
  const api = useRef<FUniver | null>(null);
  const layout = useRef<Layout | null>(null);
  const shown = useRef(new Map<string, { v: string; tone: string }>());
  const live = useRef({ tables, idOf, valueOf, toneOf, onSelect, onValue, onOrder });
  live.current = { tables, idOf, valueOf, toneOf, onSelect, onValue, onOrder };
  // The whole text of the hovered cell when the column cuts it off.
  const [tip, setTip] = useState<{ text: string; x: number; y: number } | null>(null);
  const pointer = useRef({ x: 0, y: 0 });

  // Build the workbook once; the page remounts the grid (key) when rows or
  // fields change.
  useEffect(() => {
    const { tables, idOf, valueOf } = live.current;
    const css = getComputedStyle(document.documentElement);
    const color = (v: string) => css.getPropertyValue(v).trim();
    const tones: Record<string, string> = {
      edited: color("--accent-container"),
      calc: color("--vlm-soft"),
      low: color("--warn-soft"),
    };
    const rule = {
      s: BorderStyleTypes.MEDIUM,
      cl: { rgb: color("--outline") },
    };

    const L: Layout = {
      pos: new Map(),
      at: new Map(),
      fixed: new Map(),
      base: new Map(),
      order: new Map(),
    };
    shown.current = new Map();
    // The data rows of a sub-table in the given order, with the maps from
    // cells to documents and fields. Used to build and after a sort.
    const place = (sheet: string, t: SheetTableView, order: number[]) => {
      const { idOf, valueOf, toneOf } = live.current;
      const lead = leadOf(t);
      const rows: ICellData[][] = [];
      order.forEach((ri, i) => {
        const row = t.rows[ri];
        const r = i + 1;
        const bd = i > 0 && t.rows[order[i - 1]].bundle !== row.bundle ? { t: rule } : null;
        const cells: ICellData[] = [];
        const fix = (c: number, v: string, s: IStyleData) => {
          cells[c] = { v, s };
          L.fixed.set(key(sheet, r, c), v);
        };
        fix(0, t.label ? t.title : (row.file_name ?? ""), { bd });
        if (lead === 2) fix(1, row.bundle ?? "", { bd, bl: 1 });
        t.fields.forEach((f, fi) => {
          const id = idOf(t, row, f.key);
          const c = lead + fi;
          const v = valueOf(id);
          const tone = toneOf(id);
          const base: IStyleData = { bd, n: TEXT };
          cells[c] = { v, t: 1, s: { ...base, bg: tone ? { rgb: tones[tone] } : null } };
          L.base.set(id, base);
          L.pos.set(id, { sheet, r, c });
          L.at.set(key(sheet, r, c), id);
          shown.current.set(id, { v, tone });
        });
        rows.push(cells);
      });
      L.order.set(sheet, order);
      return rows;
    };

    const tableOf = new Map<string, SheetTableView>();
    const sheets: Record<string, Partial<IWorksheetData>> = {};
    tables.forEach((t, ti) => {
      const sheet = sheetOf(ti);
      tableOf.set(sheet, t);
      const lead = leadOf(t);
      const head: IStyleData = { bl: 1, bg: { rgb: XL_HEAD } };
      const header: ICellData[] = [];
      const fixHead = (c: number, v: string) => {
        header[c] = { v, s: head };
        L.fixed.set(key(sheet, 0, c), v);
      };
      fixHead(0, t.label ? "Loại tài liệu" : "File");
      if (lead === 2) fixHead(1, "Bộ");
      t.fields.forEach((f, fi) => fixHead(lead + fi, f.label));
      const cellData = Object.fromEntries([header, ...place(sheet, t, t.rows.map((_, i) => i))].map((cells, r) => [r, { ...cells }]));
      // Width from the longest of label and values, like the old grid.
      const chars = (i: number) =>
        i < lead
          ? Math.max(8, ...t.rows.map((r) => (i === 0 ? (t.label ? t.title : r.file_name) : r.bundle)?.length ?? 0))
          : Math.max(t.fields[i - lead].label.length, ...t.rows.map((r) => valueOf(idOf(t, r, t.fields[i - lead].key)).length));
      const columnData: Record<number, { w: number }> = {};
      for (let i = 0; i < lead + t.fields.length; i++) columnData[i] = { w: Math.min(420, Math.max(90, chars(i) * 7.5 + 24)) };
      sheets[sheet] = {
        id: sheet,
        name: (t.title || "Cả file").slice(0, 31),
        rowCount: t.rows.length + 1,
        columnCount: lead + t.fields.length,
        freeze: { xSplit: lead, ySplit: 1, startRow: 1, startColumn: lead },
        cellData,
        columnData,
      };
    });
    layout.current = L;

    // A box of its own: a previous instance (StrictMode remount) is disposed
    // later and must not clear this one.
    const box = host.current!.appendChild(document.createElement("div"));
    box.style.cssText = "position:absolute;inset:0";
    const { univer, univerAPI } = createUniver({
      locale: LocaleType.VI_VN,
      locales: { [LocaleType.VI_VN]: mergeLocales(viVN, findViVN, filterViVN, sortViVN) },
      darkMode: css.colorScheme === "dark",
      presets: [
        UniverSheetsCorePreset({
          container: box,
          header: false,
          toolbar: false,
          footer: false,
          // Values are text on purpose: no "number stored as text" marks.
          disableTextFormatAlert: true,
          disableTextFormatMark: true,
        }),
        // Ctrl+F / Ctrl+H: find, replace one, replace all.
        UniverSheetsFindReplacePreset(),
        // Filter and sort in each column's header, like Excel Online.
        UniverSheetsFilterPreset(),
        UniverSheetsSortPreset(),
      ],
    });
    api.current = univerAPI;
    univerAPI.createWorkbook({
      id: "sheet",
      name: "sheet",
      sheetOrder: Object.keys(sheets),
      sheets,
    });
    const wb = univerAPI.getActiveWorkbook()!;
    tables.forEach((t, ti) => wb.getSheetBySheetId(sheetOf(ti))?.getRange(0, 0, t.rows.length + 1, leadOf(t) + t.fields.length).createFilter());
    const undo = univer.__getInjector().get(IUndoRedoService);
    undo.clearUndoRedo("sheet");

    // Sort whole documents: a row keeps its type, bundle and every field
    // together, and each cell stays tied to its document. The order is a view
    // only, not saved. Undo history is cleared: undoing a sort would put
    // values back into rows that belong to other documents now.
    const sortRows = (sheet: string, rules: { colIndex: number; type: string }[]) => {
      const t = tableOf.get(sheet);
      const ws = wb.getSheetBySheetId(sheet);
      if (!t || !ws) return;
      const lead = leadOf(t);
      const { idOf, valueOf } = live.current;
      const text = (ri: number, c: number) => {
        const row = t.rows[ri];
        if (c === 0) return t.label ? t.title : (row.file_name ?? "");
        if (c < lead) return row.bundle ?? "";
        const f = t.fields[c - lead];
        return f ? valueOf(idOf(t, row, f.key)) : "";
      };
      const order = [...L.order.get(sheet)!].sort((a, b) => {
        for (const { colIndex, type } of rules) {
          const x = text(a, colIndex);
          const y = text(b, colIndex);
          // Blanks stay last either way, like Excel.
          const d = !x.trim() || !y.trim() ? compare(x, y) : compare(x, y) * (type === "desc" ? -1 : 1);
          if (d) return d;
        }
        return 0;
      });
      ws.getRange(1, 0, order.length, lead + t.fields.length).setValues(place(sheet, t, order));
      undo.clearUndoRedo("sheet");
      live.current.onOrder(t.label, order);
      // The selected cell now shows another document: follow it.
      const cur = ws.getSelection()?.getActiveRange()?.getRange();
      const id = cur && L.at.get(key(sheet, cur.startRow, cur.startColumn));
      if (id) live.current.onSelect(id);
    };

    const subs = [
      univerAPI.addEvent(univerAPI.Event.BeforeCommandExecute, (e) => {
        if (BLOCKED.test(e.id)) e.cancel = true;
        if (e.id === "sheet.command.sort-range") {
          e.cancel = true;
          const { subUnitId, orderRules } = e.params;
          setTimeout(() => sortRows(subUnitId, orderRules));
        }
        // The menu's ascending / descending (current or extended range):
        // always the whole table, by the column of the active cell.
        const m = e.id.match(/^sheet\.command\.sort-range-(asc|desc)/);
        if (m) {
          e.cancel = true;
          const ws = wb.getActiveSheet();
          const colIndex = ws.getSelection()?.getActiveRange()?.getRange().startColumn;
          if (colIndex != null) setTimeout(() => sortRows(ws.getSheetId(), [{ colIndex, type: m[1] }]));
        }
      }),
      univerAPI.addEvent(univerAPI.Event.CellHover, (e) => {
        const text = e.worksheet.getRange(e.row, e.column).getDisplayValue();
        const cut = text.includes("\n") || text.length * 7 > e.worksheet.getColumnWidth(e.column) - 12;
        setTip(text && cut ? { text, ...pointer.current } : null);
      }),
      // Only field cells open the editor.
      univerAPI.addEvent(univerAPI.Event.BeforeSheetEditStart, (e) => {
        setTip(null);
        if (!L.at.has(key(e.worksheet.getSheetId(), e.row, e.column))) e.cancel = true;
      }),
      // Typing, paste, delete, fill and undo all land here.
      univerAPI.addEvent(univerAPI.Event.SheetValueChanged, (e) => {
        const restore: [string, number, number, string][] = [];
        for (const range of e.effectedRanges) {
          const sheet = range.getSheetId();
          const { startRow, startColumn } = range.getRange();
          range.getValues().forEach((vals, dr) =>
            vals.forEach((raw, dc) => {
              const k = key(sheet, startRow + dr, startColumn + dc);
              const v = raw == null ? "" : String(raw);
              const id = L.at.get(k);
              if (id) {
                const s = shown.current.get(id);
                if (s && s.v !== v) {
                  s.v = v;
                  live.current.onValue(id, v);
                }
              } else if (L.fixed.has(k) && L.fixed.get(k) !== v) restore.push([sheet, startRow + dr, startColumn + dc, L.fixed.get(k)!]);
            }),
          );
        }
        // Labels, types and bundles are not editable: put them back.
        if (restore.length)
          setTimeout(() => {
            const wb = univerAPI.getActiveWorkbook();
            for (const [sheet, r, c, v] of restore) wb?.getSheetBySheetId(sheet)?.getRange(r, c).setValue(v);
          });
      }),
      // The cell the user moved to, by mouse or keyboard.
      univerAPI.addEvent(univerAPI.Event.CommandExecuted, (e) => {
        if (e.id !== "sheet.operation.set-selections") return;
        const s = e.params?.selections?.at(-1);
        const p = s?.primary ?? s?.range;
        const id = p && L.at.get(key(e.params.subUnitId, p.actualRow ?? p.startRow, p.actualColumn ?? p.startColumn));
        if (id) live.current.onSelect(id);
      }),
    ];
    return () => {
      subs.forEach((s) => s.dispose());
      api.current = null;
      layout.current = null;
      // Univer must not be disposed in the middle of React's commit.
      setTimeout(() => {
        univer.dispose();
        box.remove();
      });
    };
  }, []);

  // Drafts set outside the grid (uploaded .xlsx, undo in the history panel,
  // a save) and the status colours follow into the grid.
  useEffect(() => {
    const wb = api.current?.getActiveWorkbook();
    const L = layout.current;
    if (!wb || !L) return;
    const css = getComputedStyle(document.documentElement);
    const tones: Record<string, string> = {
      edited: css.getPropertyValue("--accent-container").trim(),
      calc: css.getPropertyValue("--vlm-soft").trim(),
      low: css.getPropertyValue("--warn-soft").trim(),
    };
    for (const [id, p] of L.pos) {
      const v = valueOf(id);
      const tone = toneOf(id);
      const s = shown.current.get(id)!;
      if (s.v === v && s.tone === tone) continue;
      const range = wb.getSheetBySheetId(p.sheet)?.getRange(p.r, p.c);
      if (!range) continue;
      s.v = v;
      s.tone = tone;
      range.setValue({
        v,
        t: 1,
        s: { ...L.base.get(id), bg: tone ? { rgb: tones[tone] } : null },
      });
    }
  });

  // The page's tab and selected cell (picked from the edit history).
  useEffect(() => {
    const wb = api.current?.getActiveWorkbook();
    const ti = tables.findIndex((t) => t.label === tab);
    if (!wb) return;
    const sheet = sheetOf(Math.max(0, ti));
    if (wb.getActiveSheet().getSheetId() !== sheet) wb.setActiveSheet(sheet);
    const p = sel ? layout.current?.pos.get(sel) : undefined;
    if (p && p.sheet === sheet) {
      const cur = wb.getActiveSheet().getSelection()?.getActiveRange()?.getRange();
      if (!cur || cur.startRow !== p.r || cur.startColumn !== p.c) wb.getActiveSheet().getRange(p.r, p.c).activate();
    }
  }, [tab, sel, tables]);

  return (
    <>
      <div
        ref={host}
        className="absolute inset-0"
        onPointerMove={(e) => {
          pointer.current = { x: e.clientX, y: e.clientY };
        }}
        onPointerLeave={() => setTip(null)}
        onPointerDown={() => setTip(null)}
        onWheel={() => setTip(null)}
      />
      {tip && (
        <div
          className="pointer-events-none fixed z-50 max-h-[50vh] max-w-[420px] overflow-hidden rounded-lg border border-line bg-surface px-3 py-2 text-sm break-words whitespace-pre-wrap text-fg"
          style={{
            left: Math.max(8, Math.min(tip.x + 12, window.innerWidth - 436)),
            // Below the pointer, or above it in the lower half of the window.
            ...(tip.y < window.innerHeight / 2 ? { top: tip.y + 16 } : { bottom: window.innerHeight - tip.y + 8 }),
            boxShadow: "var(--shadow-2)",
          }}
        >
          {tip.text}
        </div>
      )}
    </>
  );
}
