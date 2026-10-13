import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";

import { createSheet, deleteSheet, listSheets } from "../../api/endpoints";
import type { Case, Sheet, SplitView } from "../../api/types";
import { useApp } from "../../components/AppContext";
import { useToast } from "../../components/toast";
import { Icon, Modal, Spinner } from "../../components/ui";
import { fmtDate } from "../../lib/format";

const XL = "#107c41"; // the Excel accent of the Studio card

// Studio column of the case page (§7.2): the "Xuất Excel" card — one
// sub-table per document type of the KB that has fields (set in Settings),
// filled with values extracted when a sheet is made and reused by the next
// ones — and the sheets made for the case; a sheet opens on its own page (§7.6).
export function Studio({ kcase, split, className }: { kcase: Case; split: SplitView | null; className: string }) {
  const { kbs, openSettings } = useApp();
  const { toast, fail } = useToast();
  const nav = useNavigate();
  const [sheets, setSheets] = useState<Sheet[] | null>(null);
  const [starting, setStarting] = useState(false);
  const [off, setOff] = useState<Record<string, boolean>>({}); // sub-tables unticked
  const [picking, setPicking] = useState(false); // the dialog to tick the document types

  const loadSheets = useCallback(() => listSheets(kcase.id).then(setSheets, () => setSheets([])), [kcase.id]);
  useEffect(() => {
    loadSheets();
  }, [loadSheets]);

  // Poll while a sheet is being built, for the "9/13 ô" progress.
  const running = sheets?.some((s) => s.status === "pending" || s.status === "running");
  useEffect(() => {
    if (!running) return;
    const t = setInterval(loadSheets, 2000);
    return () => clearInterval(t);
  }, [running, loadSheets]);

  const types = kbs.find((k) => k.id === kcase.kb_id)?.config.doc_types ?? [];
  const tables = types.filter((t) => t.fields?.length);
  const nFiles = split?.files.filter((f) => f.indexed).length ?? 0;
  const usable = !split || nFiles > 0;
  const picked = tables.filter((t) => usable && !off[t.name]);

  const start = async () => {
    if (!tables.length) return openSettings("doctypes");
    setStarting(true);
    try {
      const s = await createSheet(
        kcase.id,
        picked.map((t) => t.name),
      );
      setSheets((xs) => [s, ...(xs ?? [])]);
      toast("Đang tạo bảng; thông tin chưa có sẽ được bóc tách và lưu lại");
    } catch (e) {
      fail(e);
    } finally {
      setStarting(false);
    }
  };

  const remove = async (s: Sheet) => {
    if (!confirm(`Xoá “${s.name}”? Không thể hoàn tác.`)) return;
    try {
      await deleteSheet(s.id);
      setSheets((xs) => (xs ?? []).filter((x) => x.id !== s.id));
    } catch (e) {
      fail(e);
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
            <div className="truncate text-xs text-muted">Mỗi loại giấy tờ một sheet, mỗi giấy tờ một dòng</div>
          </div>
          {tables.length > 0 && (
            <button className="btn-icon btn-sm" disabled={!usable} onClick={() => setPicking(true)} title="Chọn loại giấy tờ cần xuất">
              <Icon name="tune" size={18} />
            </button>
          )}
        </div>
        <button
          className="btn mt-3 w-full border-transparent text-white hover:shadow-1"
          style={{ background: XL }}
          onClick={start}
          disabled={starting || (tables.length > 0 && !picked.length)}
        >
          {starting ? <Spinner className="size-4 border-2 border-white/40 border-t-white" /> : <Icon name={tables.length ? "auto_awesome" : "tune"} size={18} />}
          {tables.length ? `Tạo ${picked.length} bảng${split && nFiles ? ` từ ${nFiles} file` : ""}` : "Cài đặt trường cần lấy"}
        </button>
        {tables.length > 0 && split && !nFiles && <div className="mt-2 text-xs text-warn">Chưa có file nào index xong để tạo bảng.</div>}
        <div className="mt-2 text-xs text-muted">
          {tables.length
            ? "Thông tin được bóc tách khi tạo bảng và lưu lại; lần sau chỉ bóc tách trường mới và hỏi lại một lần các trường chưa tìm thấy."
            : "Thư viện chưa có trường nào cho loại giấy tờ. Thêm ở Cài đặt → Loại giấy tờ."}
        </div>
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
              <div key={s.id} className="group flex items-center rounded-xl hover:bg-fg/5">
                <button disabled={busy} onClick={() => nav(`/cases/${kcase.id}/sheets/${s.id}`)} className="flex min-w-0 flex-1 items-center gap-3 px-2 py-2.5 text-left disabled:cursor-default">
                  {busy ? (
                    <Spinner className="size-5" />
                  ) : (
                    <Icon name={s.status === "failed" ? "error" : "table_view"} size={22} fill className={s.status === "failed" ? "text-err" : "text-[#107c41]"} />
                  )}
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm text-fg">{s.name}</span>
                    <span className="block truncate text-xs text-muted">
                      {busy
                        ? `Đang bóc tách ${s.filled}/${s.total} ô…`
                        : s.status === "failed"
                          ? "Lỗi: " + (s.error || "không tạo được bảng")
                          : `${(s.columns ?? []).map((t) => t.title || "Cả file").join(" · ")} · ${s.rows?.length ?? 0} dòng · ${fmtDate(s.created_at)}`}
                    </span>
                  </span>
                  {!busy && <Icon name="chevron_right" size={20} className="text-muted" />}
                </button>
                <button className="btn-icon btn-sm mr-1 flex-none focus-visible:opacity-100 lg:opacity-0 lg:group-hover:opacity-100" onClick={() => remove(s)} title="Xoá bảng">
                  <Icon name="delete" size={18} />
                </button>
              </div>
            );
          })
        )}
      </div>
      {picking && (
        <Modal
          title="Chọn loại giấy tờ cần xuất"
          icon="checklist"
          width={480}
          onClose={() => setPicking(false)}
          footer={
            <>
              <button className="btn btn-text mr-auto" onClick={() => setOff(picked.length ? Object.fromEntries(tables.map((t) => [t.name, true])) : {})}>
                {picked.length ? "Bỏ chọn tất cả" : "Chọn tất cả"}
              </button>
              <button className="btn btn-primary" onClick={() => setPicking(false)}>
                Xong ({picked.length})
              </button>
            </>
          }
        >
          <div className="flex flex-col gap-0.5">
            {tables.map((t) => (
              <label key={t.name} className="flex h-9 cursor-pointer items-center gap-2 rounded-lg px-2 text-sm text-fg hover:bg-fg/5">
                <input type="checkbox" checked={!off[t.name]} onChange={(e) => setOff((o) => ({ ...o, [t.name]: !e.target.checked }))} />
                <span className="min-w-0 flex-1 truncate">{t.title}</span>
                <span className="text-xs whitespace-nowrap text-muted">{t.fields?.length} cột</span>
              </label>
            ))}
          </div>
        </Modal>
      )}
    </aside>
  );
}
