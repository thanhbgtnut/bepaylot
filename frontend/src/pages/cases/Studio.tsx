import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";

import { createCheck, createSheet, deleteCheck, deleteSheet, downloadSheetForm, listChecks, listExportTemplates, listSheets } from "../../api/endpoints";
import type { Case, CheckRun, ExportTemplate, Sheet, SplitView } from "../../api/types";
import { useApp } from "../../components/AppContext";
import { useToast } from "../../components/toast";
import { Icon, Modal, Spinner } from "../../components/ui";
import { fmtDate } from "../../lib/format";

const XL = "#107c41"; // the Excel accent of the Studio card

// Studio column of the case page (§7.2): the "Xuất Excel" card — one
// sub-table per document type of the KB that has fields (set in Settings),
// filled with values extracted when a sheet is made and reused by the next
// ones — the Excel forms of the KB (§6.9.8: one button each; they are imported
// in Cài đặt → Mẫu xuất Excel) and the sheets made for the case; a sheet opens
// on its own page (§7.6), a sheet made for a form downloads that form.
export function Studio({ kcase, split, className }: { kcase: Case; split: SplitView | null; className: string }) {
  const { kbs, openSettings } = useApp();
  const { toast, fail } = useToast();
  const nav = useNavigate();
  const [sheets, setSheets] = useState<Sheet[] | null>(null);
  const [starting, setStarting] = useState(false);
  const [off, setOff] = useState<Record<string, boolean>>({}); // sub-tables unticked
  const [picking, setPicking] = useState(false); // the dialog to tick the document types
  const [forms, setForms] = useState<ExportTemplate[]>([]);

  useEffect(() => {
    listExportTemplates(kcase.kb_id).then(setForms, () => setForms([]));
  }, [kcase.kb_id]);


  const loadSheets = useCallback(() => listSheets(kcase.id).then(setSheets, () => setSheets([])), [kcase.id]);

  // Cross-checks (§6.9.9): runs of the case, polled while one is running.
  const [checks, setChecks] = useState<CheckRun[]>([]);
  const [checking, setChecking] = useState(false);
  const loadChecks = useCallback(() => listChecks(kcase.id).then(setChecks, () => setChecks([])), [kcase.id]);
  useEffect(() => {
    loadChecks();
  }, [loadChecks]);
  const checkRunning = checks.some((c) => c.status === "pending" || c.status === "running");
  useEffect(() => {
    if (!checkRunning) return;
    const t = setInterval(() => (loadChecks(), loadSheets()), 2000);
    return () => clearInterval(t);
  }, [checkRunning, loadChecks, loadSheets]);
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

  const startForm = async (t: ExportTemplate) => {
    try {
      const s = await createSheet(kcase.id, undefined, t.id);
      setSheets((xs) => [s, ...(xs ?? [])]);
      toast(`Đang tạo “${t.name}”; xong thì bấm tải ở danh sách Đã tạo`);
    } catch (e) {
      fail(e);
    }
  };

  const checkCfg = kbs.find((k) => k.id === kcase.kb_id)?.config.checks;
  const nRules = checkCfg?.rules.filter((r) => !r.off).length ?? 0;
  const startCheck = async () => {
    if (!nRules) return openSettings("checks");
    setChecking(true);
    try {
      const c = await createCheck(kcase.id);
      setChecks((xs) => [c, ...xs]);
      loadSheets();
      nav(`/cases/${kcase.id}/checks/${c.id}`);
    } catch (e) {
      fail(e);
    } finally {
      setChecking(false);
    }
  };
  const removeCheck = async (c: CheckRun) => {
    if (!confirm("Xoá lần đối chiếu này?")) return;
    try {
      await deleteCheck(c.id);
      setChecks((xs) => xs.filter((x) => x.id !== c.id));
    } catch (e) {
      fail(e);
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

      <div className="mt-3 rounded-2xl border border-line p-3">
        <div className="flex items-center gap-2 px-1 pb-1">
          <span className="min-w-0 flex-1 text-sm font-medium text-fg">Xuất theo mẫu</span>
          <button className="btn-icon btn-sm" onClick={() => openSettings("forms")} title="Quản lý mẫu ở Cài đặt">
            <Icon name="settings" size={18} />
          </button>
        </div>
        {!forms.length && (
          <button className="block w-full px-1 py-1 text-left text-xs text-subtle hover:text-accent" onClick={() => openSettings("forms")}>
            Chưa có mẫu. Nhập file Excel mẫu ở Cài đặt → Mẫu xuất Excel.
          </button>
        )}
        {forms.map((t) => (
          <button key={t.id} disabled={!usable} onClick={() => startForm(t)} className="flex w-full min-w-0 items-center gap-3 rounded-xl px-2 py-2 text-left hover:bg-fg/5 disabled:cursor-default" title="Tạo bảng theo mẫu">
            <Icon name="request_page" size={22} fill className="text-[#107c41]" />
            <span className="min-w-0 flex-1">
              <span className="block truncate text-sm text-fg">{t.name}</span>
              <span className="block truncate text-xs text-muted">{t.file_name}</span>
            </span>
          </button>
        ))}
      </div>

      <div className="mt-3 rounded-2xl p-4" style={{ background: "color-mix(in srgb, #5b47c9 12%, var(--surface))" }}>
        <div className="flex items-start gap-3">
          <span className="grid size-10 flex-none place-items-center rounded-xl bg-surface">
            <Icon name="fact_check" size={24} fill className="text-[#5b47c9]" />
          </span>
          <div className="min-w-0 flex-1">
            <div className="text-sm font-medium text-fg">Đối chiếu chứng từ</div>
            <div className="text-xs text-muted">
              {nRules ? `Gom chứng từ theo hợp đồng và kiểm ${nRules} quy tắc` : "Kiểm tra chéo hoá đơn, hợp đồng, uỷ nhiệm chi…"}
            </div>
          </div>
          <button className="btn-icon btn-sm" onClick={() => openSettings("checks")} title="Quy tắc đối chiếu">
            <Icon name="tune" size={18} />
          </button>
        </div>
        <button className="btn mt-3 w-full border-transparent bg-[#5b47c9] text-white hover:shadow-1" disabled={checking || (nRules > 0 && !usable)} onClick={startCheck}>
          {checking ? <Spinner className="size-4 border-2 border-white/40 border-t-white" /> : <Icon name={nRules ? "play_arrow" : "tune"} size={18} />}
          {nRules ? "Chạy đối chiếu" : "Cài đặt quy tắc đối chiếu"}
        </button>
        {checks.slice(0, 5).map((c) => {
          const busy = c.status === "pending" || c.status === "running";
          const n = c.result?.counts;
          return (
            <div key={c.id} className="group mt-1 flex items-center rounded-xl hover:bg-surface/60">
              <button onClick={() => nav(`/cases/${kcase.id}/checks/${c.id}`)} className="flex min-w-0 flex-1 items-center gap-2 px-2 py-2 text-left">
                {busy ? (
                  <Spinner className="size-4" />
                ) : (
                  <Icon name={c.status === "failed" ? "error" : n?.fail ? "cancel" : n?.unknown || n?.unlinked ? "help" : "check_circle"} size={18} fill className={c.status === "failed" || n?.fail ? "text-err" : n?.unknown || n?.unlinked ? "text-warn" : "text-ok"} />
                )}
                <span className="min-w-0 flex-1 truncate text-xs text-fg">
                  {busy
                    ? "Đang đối chiếu…"
                    : c.status === "failed"
                      ? "Lỗi: " + (c.error || "không đối chiếu được")
                      : `${n?.groups ?? 0} hợp đồng · ${n?.fail ?? 0} không đạt${n?.unlinked ? ` · ${n.unlinked} chưa xác định` : ""}`}
                </span>
                <span className="text-[11px] whitespace-nowrap text-muted">{fmtDate(c.created_at)}</span>
              </button>
              <button className="btn-icon btn-sm flex-none focus-visible:opacity-100 lg:opacity-0 lg:group-hover:opacity-100" onClick={() => removeCheck(c)} title="Xoá lần đối chiếu">
                <Icon name="delete" size={16} />
              </button>
            </div>
          );
        })}
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
                {s.export_template_id && s.status === "done" && (
                  <button className="btn-icon btn-sm flex-none" onClick={() => downloadSheetForm(s, kcase.code).catch(fail)} title="Tải theo mẫu">
                    <Icon name="download" size={18} />
                  </button>
                )}
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
