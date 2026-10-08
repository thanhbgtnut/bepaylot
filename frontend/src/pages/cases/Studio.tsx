import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";

import { createSheet, listSheets, listTemplates } from "../../api/endpoints";
import { canEditPrompts, type Case, type PromptTemplate, type Sheet, type SplitView } from "../../api/types";
import { useAuth } from "../../components/AuthContext";
import { useToast } from "../../components/toast";
import { Icon, Spinner } from "../../components/ui";
import { fmtDate } from "../../lib/format";
import { SheetTemplateDialog } from "./dialogs";

const XL = "#107c41"; // the Excel accent of the Studio card

// Studio column of the case page (§7.2): the "Xuất Excel" card — the
// template's sub-tables, one per document type, to pick — and the sheets made
// for the case; a sheet opens on its own page (§7.6).
export function Studio({ kcase, split, className }: { kcase: Case; split: SplitView | null; className: string }) {
  const { user } = useAuth();
  const { toast, fail } = useToast();
  const nav = useNavigate();
  const editor = canEditPrompts(user);
  const [templates, setTemplates] = useState<PromptTemplate[] | null>(null);
  const [tplId, setTplId] = useState(() => {
    try {
      return localStorage.getItem("bp.sheetTpl." + kcase.case_type) ?? "";
    } catch {
      return "";
    }
  });
  const [sheets, setSheets] = useState<Sheet[] | null>(null);
  const [dialog, setDialog] = useState(false);
  const [starting, setStarting] = useState(false);
  const [off, setOff] = useState<Record<string, boolean>>({}); // sub-tables unticked

  const loadSheets = useCallback(() => listSheets(kcase.id).then(setSheets, () => setSheets([])), [kcase.id]);
  useEffect(() => {
    listTemplates("sheet", kcase.case_type).then(setTemplates, () => setTemplates([]));
    loadSheets();
  }, [kcase.case_type, loadSheets]);

  // Poll while a sheet is being built, for the "9/13 trường" progress.
  const running = sheets?.some((s) => s.status === "pending" || s.status === "running");
  useEffect(() => {
    if (!running) return;
    const t = setInterval(loadSheets, 2000);
    return () => clearInterval(t);
  }, [running, loadSheets]);

  const tpl = templates?.find((t) => t.id === tplId) ?? templates?.[0];
  const choose = (id: string) => {
    setTplId(id);
    try {
      localStorage.setItem("bp.sheetTpl." + kcase.case_type, id);
    } catch {
      /* private mode */
    }
  };

  // Rows per sub-table: the reviewed documents of its type; a sub-table
  // without type has one row per indexed file.
  const reviewed = (split?.files ?? []).filter((f) => f.status === "reviewed");
  const docsOf = (label: string) =>
    label ? reviewed.flatMap((f) => f.segments).filter((g) => g.label === label).length : (split?.files.filter((f) => f.indexed).length ?? 0);
  const tables = tpl?.tables ?? [];
  const picked = tables.filter((t) => !off[t.label] && (!split || !t.label || docsOf(t.label) > 0));
  const needSplit = !!split && !split.reviewed && picked.some((t) => t.label);
  const unreviewed = (split?.files ?? []).filter((f) => f.indexed && f.status !== "reviewed");
  const nBundles = new Set(reviewed.flatMap((f) => f.segments.map((g) => g.bundle))).size;
  const titles = (labels: string[]) => labels.map((l) => tables.find((t) => t.label === l)?.title || "Cả file").join(" · ");

  const start = async () => {
    if (!tpl) return setDialog(true);
    setStarting(true);
    try {
      const s = await createSheet(
        kcase.id,
        tpl.id,
        picked.map((t) => t.label),
      );
      setSheets((xs) => [s, ...(xs ?? [])]);
      toast("Đang tạo bảng: ô đã duyệt được dùng lại, ô còn thiếu gửi agent mỗi bộ một lượt");
    } catch (e) {
      fail(e);
    } finally {
      setStarting(false);
    }
  };

  return (
    <aside className={className + " w-full flex-none flex-col overflow-auto border-l border-line px-3 py-3 lg:w-80"}>
      <div className="px-1 pb-3 text-sm font-medium text-fg">Studio</div>
      <div className="rounded-2xl p-4" style={{ background: "color-mix(in srgb, " + XL + " 14%, var(--surface))" }}>
        <div className="flex items-start gap-3">
          <span className="grid size-10 flex-none place-items-center rounded-xl bg-surface">
            <Icon name="table_view" size={24} fill className="text-[#107c41]" />
          </span>
          <div className="min-w-0 flex-1">
            <div className="text-sm font-medium text-fg">Xuất Excel</div>
            <div className="truncate text-xs text-muted">{templates === null ? "…" : (tpl?.name ?? "Chưa có mẫu bảng")}</div>
          </div>
          <button className="btn-icon btn-sm" onClick={() => setDialog(true)} title={editor ? "Tuỳ chỉnh bảng con, trường và prompt" : "Chọn mẫu bảng"}>
            <Icon name={editor ? "edit" : "tune"} size={18} />
          </button>
        </div>
        {tables.length > 0 && (
          <div className="mt-3 flex flex-col gap-0.5 rounded-xl bg-surface p-1.5">
            <div className="px-2 pt-1 pb-0.5 text-xs text-muted">Chọn bảng cần tạo (mỗi loại giấy tờ một bảng)</div>
            {tables.map((t) => {
              const n = docsOf(t.label);
              const usable = !split || !t.label || n > 0;
              return (
                <label key={t.label} className={"flex h-9 items-center gap-2 rounded-lg px-2 text-sm text-fg " + (usable ? "cursor-pointer hover:bg-fg/5" : "opacity-50")}>
                  <input type="checkbox" checked={usable && !off[t.label]} disabled={!usable} onChange={(e) => setOff((o) => ({ ...o, [t.label]: !e.target.checked }))} />
                  <span className="min-w-0 flex-1 truncate">{t.title || "Cả file"}</span>
                  <span className="text-xs whitespace-nowrap text-muted">
                    {split ? `${n} ${t.label ? "giấy tờ" : "file"} · ` : ""}
                    {t.fields.length} cột
                  </span>
                </label>
              );
            })}
          </div>
        )}
        {needSplit ? (
          <>
            <div className="mt-3 flex gap-2 rounded-xl bg-warn-soft p-3 text-xs text-warn">
              <Icon name="content_cut" size={18} />
              <div className="flex-1">
                {unreviewed.length} file chưa duyệt tách & gom ({unreviewed.map((f) => f.file_name).join(", ")}). Bảng chỉ dùng giấy tờ đã duyệt.
              </div>
            </div>
            <button className="btn mt-2 w-full border-transparent text-white hover:shadow-1" style={{ background: XL }} onClick={() => nav(`/cases/${kcase.id}/split`)}>
              <Icon name="content_cut" size={18} /> Tách & gom trang
            </button>
          </>
        ) : (
          <button
            className="btn mt-3 w-full border-transparent text-white hover:shadow-1"
            style={{ background: XL }}
            onClick={start}
            disabled={starting || templates === null || (!tpl && !editor) || (!!tpl && !picked.length)}
          >
            {starting ? <Spinner className="size-4 border-2 border-white/40 border-t-white" /> : <Icon name="auto_awesome" size={18} />}
            {tpl ? `Tạo ${picked.length} bảng${split && nBundles ? ` từ ${nBundles} bộ` : ""}` : editor ? "Tạo mẫu bảng" : "Chưa có mẫu bảng"}
          </button>
        )}
        <div className="mt-2 text-xs text-muted">Trường đã duyệt được dùng lại, không bóc tách lại.</div>
      </div>

      <div className="mt-4 border-t border-line pt-3">
        <div className="px-1 pb-1 text-xs font-medium text-muted">Đã tạo</div>
        {sheets === null ? (
          <Spinner className="m-2" />
        ) : !sheets.length ? (
          <div className="px-1 py-2 text-xs text-subtle">Chưa có bảng nào.</div>
        ) : (
          sheets.map((s) => {
            const busy = s.status === "pending" || s.status === "running";
            return (
              <button
                key={s.id}
                disabled={busy}
                onClick={() => nav(`/cases/${kcase.id}/sheets/${s.id}`)}
                className="flex w-full items-center gap-3 rounded-xl px-2 py-2.5 text-left hover:bg-fg/5 disabled:cursor-default"
              >
                {busy ? <Spinner className="size-5" /> : <Icon name={s.status === "failed" ? "error" : "table_view"} size={22} fill className={s.status === "failed" ? "text-err" : "text-[#107c41]"} />}
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm text-fg">{s.name}</span>
                  <span className="block truncate text-xs text-muted">
                    {busy
                      ? `Đang bóc tách ${s.filled}/${s.total} ô…`
                      : s.status === "failed"
                        ? "Lỗi: " + (s.error || "không tạo được bảng")
                        : `${titles(s.tables ?? [])} · ${s.rows?.length ?? 0} dòng · v${s.template_version} · ${fmtDate(s.created_at)}`}
                  </span>
                </span>
                {!busy && <Icon name="chevron_right" size={20} className="text-muted" />}
              </button>
            );
          })
        )}
      </div>
      {dialog && templates && (
        <SheetTemplateDialog
          templates={templates}
          caseType={kcase.case_type}
          labels={split?.labels ?? []}
          value={tpl?.id ?? ""}
          onClose={() => setDialog(false)}
          onSave={(id, list) => {
            if (list) setTemplates(list);
            choose(id);
            setDialog(false);
          }}
        />
      )}
    </aside>
  );
}
