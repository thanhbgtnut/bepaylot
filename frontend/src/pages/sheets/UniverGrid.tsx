import { useEffect, useRef } from "react";
import { UniverSheetsCorePreset } from "@univerjs/preset-sheets-core";
import viVN from "@univerjs/preset-sheets-core/locales/vi-VN";
import { BorderStyleTypes, createUniver, LocaleType, mergeLocales, type ICellData, type IStyleData, type IWorksheetData } from "@univerjs/presets";
import "@univerjs/preset-sheets-core/lib/index.css";

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
}

// Commands that would move cells away from their field or document: rows,
// columns and ranges inserted, removed or moved, merges, sorts, sheets.
const BLOCKED =
  /^sheet\.command\.(insert-(row|col|multi|range|sheet)|remove-(row|col|sheet)|delete-range-move|move-(rows|cols|range)|add-worksheet-merge|reorder-range|copy-sheet|set-worksheet-(name|order)|paste-besides-border)/;
const XL_HEAD = "rgba(16, 124, 65, 0.12)";
const TEXT = { pattern: "@" }; // text format: extracted values stay as written (0123, 15.000.000, 01/02/2024)
const key = (sheet: string, r: number, c: number) => `${sheet}:${r}:${c}`;
const sheetOf = (ti: number) => "t" + ti;

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
}: {
  tables: SheetTableView[];
  idOf: (t: SheetTableView, row: SheetRowView, key: string) => string;
  valueOf: (id: string) => string;
  toneOf: (id: string) => "edited" | "calc" | "low" | "";
  tab: string;
  sel: string | null;
  onSelect: (id: string) => void;
  onValue: (id: string, value: string) => void;
}) {
  const host = useRef<HTMLDivElement>(null);
  const api = useRef<FUniver | null>(null);
  const layout = useRef<Layout | null>(null);
  const shown = useRef(new Map<string, { v: string; tone: string }>());
  const live = useRef({ tables, idOf, valueOf, toneOf, onSelect, onValue });
  live.current = { tables, idOf, valueOf, toneOf, onSelect, onValue };

  // Build the workbook once; the page remounts the grid (key) when rows or
  // fields change.
  useEffect(() => {
    const { tables, idOf, valueOf, toneOf } = live.current;
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
    };
    shown.current = new Map();
    const sheets: Record<string, Partial<IWorksheetData>> = {};
    tables.forEach((t, ti) => {
      const sheet = sheetOf(ti);
      const bundle = t.rows.some((r) => r.bundle);
      const lead = bundle ? 2 : 1;
      const cellData: Record<number, Record<number, ICellData>> = { 0: {} };
      const head: IStyleData = { bl: 1, bg: { rgb: XL_HEAD } };
      const fix = (r: number, c: number, v: string, s: IStyleData = {}) => {
        (cellData[r] ??= {})[c] = { v, s };
        L.fixed.set(key(sheet, r, c), v);
      };
      fix(0, 0, t.label ? "Loại tài liệu" : "File", head);
      if (bundle) fix(0, 1, "Bộ", head);
      t.fields.forEach((f, fi) => fix(0, lead + fi, f.label, head));
      t.rows.forEach((row, ri) => {
        const r = ri + 1;
        const top = ri > 0 && t.rows[ri - 1].bundle !== row.bundle ? { bd: { t: rule } } : {};
        fix(r, 0, t.label ? t.title : (row.file_name ?? ""), top);
        if (bundle) fix(r, 1, row.bundle ?? "", { ...top, bl: 1 });
        t.fields.forEach((f, fi) => {
          const id = idOf(t, row, f.key);
          const c = lead + fi;
          const v = valueOf(id);
          const tone = toneOf(id);
          const base = { ...top, n: TEXT };
          cellData[r][c] = {
            v,
            t: 1,
            s: { ...base, ...(tone ? { bg: { rgb: tones[tone] } } : {}) },
          };
          L.base.set(id, base);
          L.pos.set(id, { sheet, r, c });
          L.at.set(key(sheet, r, c), id);
          shown.current.set(id, { v, tone });
        });
      });
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
      locales: { [LocaleType.VI_VN]: mergeLocales(viVN) },
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
      ],
    });
    api.current = univerAPI;
    univerAPI.createWorkbook({
      id: "sheet",
      name: "sheet",
      sheetOrder: Object.keys(sheets),
      sheets,
    });

    const subs = [
      univerAPI.addEvent(univerAPI.Event.BeforeCommandExecute, (e) => {
        if (BLOCKED.test(e.id)) e.cancel = true;
      }),
      // Only field cells open the editor.
      univerAPI.addEvent(univerAPI.Event.BeforeSheetEditStart, (e) => {
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

  return <div ref={host} className="absolute inset-0" />;
}
