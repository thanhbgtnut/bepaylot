import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";

import { downloadSheet, getSheet, importSheet, saveSheetEdits } from "../../api/endpoints";
import type { SheetImport, SheetRowView, SheetView } from "../../api/types";
import { useCitation } from "../../components/Citation";
import { PageImage } from "../../components/PageImage";
import { useToast } from "../../components/toast";
import { Empty, FileIcon, Icon, Loading } from "../../components/ui";
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
  documentId?: string;
}

const XL = "#107c41";
const draftKey = (id: string) => "bp.sheetDraft." + id;

// The sheet page (§7.6): a spreadsheet of the case's fields, its own route
// with a way back to the phương án. Cells differ from the AI value are
// tracked; saving makes them confirmed fields and records the AI's errors.
export function SheetPage() {
  const { caseId = "", sheetId = "" } = useParams();
  const nav = useNavigate();
  const { toast, fail } = useToast();
  const cite = useCitation();
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
  const [sel, setSel] = useState(0);
  const [side, setSide] = useState<"edits" | "source">("edits");
  const [tab, setTab] = useState<"summary" | "sources">("summary");
  const [saving, setSaving] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);

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

  const rows = useMemo(() => sheet?.rows_view ?? [], [sheet]);
  const valueOf = (r: SheetRowView) => drafts[r.key]?.value ?? r.value;
  const unsaved = rows.filter((r) => drafts[r.key] && valueKey(drafts[r.key].value) !== valueKey(r.value));
  const changes = rows.filter((r) => valueKey(valueOf(r)) !== valueKey(r.ai_value_text));

  const setValue = (r: SheetRowView, value: string, origin: Origin = "page") =>
    setDrafts((d) => {
      const next = { ...d };
      if (valueKey(value) === valueKey(r.value)) delete next[r.key];
      else next[r.key] = { ...next[r.key], value, origin };
      return next;
    });

  const save = async () => {
    if (!sheet) return;
    const missing = unsaved.find((r) => !r.document_id && !drafts[r.key]?.documentId);
    if (missing) {
      setSel(rows.indexOf(missing));
      setSide("source");
      return fail(new Error(`Chọn file nguồn cho "${missing.label}" trước khi lưu`));
    }
    setSaving(true);
    try {
      const n = unsaved.filter((r) => valueKey(drafts[r.key].value) !== valueKey(r.ai_value_text)).length;
      const v = await saveSheetEdits(
        sheet.id,
        unsaved.map((r) => ({ key: r.key, value: drafts[r.key].value, origin: drafts[r.key].origin, document_id: drafts[r.key].documentId })),
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
        const r = rows.find((x) => x.key === e.key);
        if (r) setValue(r, e.value, "xlsx");
      }
      setSide("edits");
      toast(
        `Đã đọc file: ${res.edits.length} ô khác giá trị lúc tải` +
          (res.conflicts.length ? `, ${res.conflicts.length} xung đột` : "") +
          (res.ignored.length ? `, ${res.ignored.length} dòng thêm tay bị bỏ qua` : ""),
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
  const r = rows[sel];

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
            Mẫu {sheet.template_name || sheet.name} v{sheet.template_version} · tạo {fmtDate(sheet.created_at)} · {sheet.filled}/{sheet.total} trường từ {sheet.sources?.length ?? 0} nguồn
          </div>
        </div>
        <input ref={fileInput} type="file" accept=".xlsx" hidden onChange={(e) => e.target.files?.[0] && upload(e.target.files[0])} />
        <button className="btn" onClick={() => fileInput.current?.click()} disabled={sheet.status !== "done"}>
          <Icon name="upload_file" size={18} /> Tải lên bản đã sửa
        </button>
        <button className="btn" onClick={() => downloadSheet(sheet).catch(fail)} disabled={sheet.status !== "done"}>
          <Icon name="download" size={18} /> Tải .xlsx
        </button>
        <button className="btn btn-primary" onClick={save} disabled={!unsaved.length || saving}>
          <Icon name="save" size={18} /> Lưu{unsaved.length ? ` ${unsaved.length} chỉnh sửa` : ""}
        </button>
      </header>

      {sheet.status !== "done" ? (
        <Empty icon={sheet.status === "failed" ? "error" : "hourglass_top"} title={sheet.status === "failed" ? "Không tạo được bảng" : `Đang bóc tách ${sheet.filled}/${sheet.total} trường…`}>
          {sheet.error || "Trường đã duyệt được dùng lại; agent đang đọc các trang cho trường còn thiếu."}
        </Empty>
      ) : (
        <div className="flex min-h-0 flex-1 border-t border-line max-lg:flex-col">
          <div className="flex min-h-0 min-w-0 flex-1 flex-col">
            <div className="min-h-0 flex-1 overflow-auto">
              {tab === "summary" ? (
                <table className="w-full min-w-215 border-collapse text-[13px]">
                  <thead className="sticky top-0 z-1">
                    <tr className="bg-surface-2 text-[11.5px] text-subtle">
                      {["", "A · Trường", "B · Giá trị", "C · Tin cậy", "D · Nguồn", "E · Trạng thái"].map((h, i) => (
                        <th key={i} className="border border-line px-2 py-1 font-normal">
                          {h}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((row, i) => {
                      const v = valueOf(row);
                      const edited = valueKey(v) !== valueKey(row.ai_value_text);
                      const tone = edited ? "bg-accent-container" : row.calc ? "bg-vlm-soft" : row.low ? "bg-warn-soft" : "";
                      return (
                        <tr key={row.key} onClick={() => setSel(i)}>
                          <td className={"w-10 border border-line text-center text-[11.5px] " + (sel === i ? "bg-[#107c41] text-white" : "bg-surface-2 text-subtle")}>{i + 4}</td>
                          <td className="border border-line px-2.5 py-1.5 text-fg">{row.label}</td>
                          <td className={"relative border border-line p-0 focus-within:outline-2 focus-within:-outline-offset-2 focus-within:outline-[#107c41] " + tone}>
                            <input
                              className="h-full min-h-8 w-full bg-transparent px-2.5 py-1.5 text-fg tabular-nums outline-none"
                              value={v}
                              placeholder={row.field_id ? "" : "không tìm thấy"}
                              onFocus={() => setSel(i)}
                              onChange={(e) => setValue(row, e.target.value)}
                              onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
                              aria-label={row.label}
                            />
                            {edited && <span className="pointer-events-none absolute top-0 right-0 border-[5px] border-transparent border-t-accent border-r-accent" />}
                          </td>
                          <td className="border border-line px-2.5 py-1.5 text-right tabular-nums">{row.field_id ? Math.round(row.confidence * 100) + "%" : ""}</td>
                          <td className="border border-line px-2.5 py-1.5">
                            {row.evidence[0] && (
                              <button
                                className="cite"
                                onClick={(e) => {
                                  e.stopPropagation();
                                  cite.open(row.evidence[0].citation_id, (e.currentTarget as HTMLElement).getBoundingClientRect());
                                }}
                                title={row.evidence[0].quote}
                              >
                                {(row.evidence[0].file_name ?? "").slice(0, 14)} · tr.{row.evidence[0].page_no}
                              </button>
                            )}
                          </td>
                          <td className="border border-line px-2.5 py-1.5">
                            <StatusBadge row={row} edited={edited} />
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              ) : (
                <table className="w-full min-w-175 border-collapse text-[13px]">
                  <thead className="sticky top-0">
                    <tr className="bg-surface-2 text-[11.5px] text-subtle">
                      {["key", "file", "trang", "câu gốc"].map((h) => (
                        <th key={h} className="border border-line px-2 py-1 text-left font-normal">
                          {h}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {rows.flatMap((row) =>
                      row.evidence.map((e, j) => (
                        <tr key={row.key + j}>
                          <td className="border border-line px-2.5 py-1.5 font-mono text-xs">{row.key}</td>
                          <td className="border border-line px-2.5 py-1.5">{e.file_name}</td>
                          <td className="border border-line px-2.5 py-1.5 tabular-nums">{e.page_no}</td>
                          <td className="border border-line px-2.5 py-1.5">{e.quote}</td>
                        </tr>
                      )),
                    )}
                  </tbody>
                </table>
              )}
            </div>
            <div className="flex flex-none flex-wrap gap-1 border-t border-line bg-surface-2 px-2 pt-1">
              {(
                [
                  ["summary", "Tổng hợp"],
                  ["sources", "Nguồn"],
                ] as const
              ).map(([k, l]) => (
                <button
                  key={k}
                  onClick={() => setTab(k)}
                  className={"rounded-t-md px-4 py-1.5 text-[13px] " + (tab === k ? "border-b-2 bg-surface font-medium" : "text-muted")}
                  style={tab === k ? { color: XL, borderColor: XL } : undefined}
                >
                  {l}
                </button>
              ))}
              <span className="flex-1" />
              <div className="flex flex-wrap items-center gap-3 px-2 text-[11.5px] text-muted">
                <span className="flex items-center gap-1">
                  <i className="inline-block size-3 rounded-sm bg-accent-container" /> người dùng sửa
                </span>
                <span className="flex items-center gap-1">
                  <i className="inline-block size-3 rounded-sm bg-warn-soft" /> tin cậy thấp
                </span>
                <span className="flex items-center gap-1">
                  <i className="inline-block size-3 rounded-sm bg-vlm-soft" /> agent tính
                </span>
              </div>
            </div>
          </div>

          <aside className="flex w-full flex-none flex-col border-line max-lg:max-h-[45%] max-lg:border-t lg:w-88 lg:border-l">
            <div className="flex flex-none border-b border-line px-2">
              <button className={"tab " + (side === "edits" ? "tab-active" : "")} onClick={() => setSide("edits")}>
                Chỉnh sửa so với AI {changes.length ? `(${changes.length})` : ""}
              </button>
              <button className={"tab " + (side === "source" ? "tab-active" : "")} onClick={() => setSide("source")}>
                Nguồn ô B{sel + 4}
              </button>
            </div>
            <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-auto p-3">
              {side === "edits" ? (
                <>
                  {imported?.conflicts.map((c) => {
                    const row = rows.find((x) => x.key === c.key);
                    return (
                      <div key={"c" + c.key} className="rounded-xl border border-warn p-3">
                        <div className="flex items-center gap-2">
                          <span className="min-w-0 flex-1 truncate text-sm font-medium text-fg">{c.label}</span>
                          <span className="badge badge-warn">xung đột</span>
                        </div>
                        <div className="mt-1 text-xs">Trên web đã đổi thành "{c.web_value}" sau lúc tải file.</div>
                        <div className="mt-2 flex gap-2">
                          <button
                            className="btn btn-sm"
                            onClick={() => {
                              if (row) setValue(row, c.value, "xlsx");
                              setImported((m) => m && { ...m, conflicts: m.conflicts.filter((x) => x.key !== c.key) });
                            }}
                          >
                            Giữ bản Excel
                          </button>
                          <button className="btn btn-text btn-sm" onClick={() => setImported((m) => m && { ...m, conflicts: m.conflicts.filter((x) => x.key !== c.key) })}>
                            Giữ bản trên web
                          </button>
                        </div>
                      </div>
                    );
                  })}
                  {changes.length ? (
                    changes.map((row) => {
                      const d = drafts[row.key];
                      const saved = !d;
                      return (
                        <div key={row.key} className="rounded-xl border border-line p-3" onClick={() => setSel(rows.indexOf(row))}>
                          <div className="flex items-center gap-2">
                            <span className="min-w-0 flex-1 truncate text-sm font-medium text-fg">{row.label}</span>
                            <span className="font-mono text-[11px] text-subtle">B{rows.indexOf(row) + 4}</span>
                          </div>
                          <div className="mt-1 text-[13px] text-err line-through">{row.ai_value_text || "(AI không tìm thấy)"}</div>
                          <div className="text-[13px] font-medium text-ok">{valueOf(row) || "(để trống)"}</div>
                          <div className="mt-2 flex items-center gap-2">
                            <span className={"badge " + (saved ? "badge-ok" : d.origin === "xlsx" ? "badge-vlm" : "badge-info")}>
                              {saved ? "đã lưu" : d.origin === "xlsx" ? "từ file Excel tải lên" : "sửa trên trang này"}
                            </span>
                            <span className="flex-1" />
                            {!saved && (
                              <button
                                className="btn btn-text btn-sm"
                                onClick={() =>
                                  setDrafts((m) => {
                                    const n = { ...m };
                                    delete n[row.key];
                                    return n;
                                  })
                                }
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
                      Sửa trực tiếp ở cột B hoặc tải lên file .xlsx đã sửa.
                    </div>
                  )}
                  {!!imported?.ignored.length && (
                    <div className="rounded-xl bg-surface-2 p-3 text-xs">
                      <div className="mb-1 font-medium text-fg">Bỏ qua khi đọc file</div>
                      {imported.ignored.map((x) => (
                        <div key={x.cell}>
                          {x.cell}: {x.text} — {x.reason}
                        </div>
                      ))}
                    </div>
                  )}
                  <p className="px-1 text-xs text-muted">
                    Khi lưu, mỗi chỉnh sửa thành giá trị đã duyệt của trường; ô khác giá trị AI được ghi là <b>AI sai</b> cho mẫu "{sheet.template_name || sheet.name}" v{sheet.template_version}. Người soạn mẫu xem tỷ lệ sai theo trường để sửa prompt.
                  </p>
                </>
              ) : r ? (
                <SourcePane
                  row={r}
                  sources={sheet.sources ?? []}
                  documentId={drafts[r.key]?.documentId}
                  onDocument={(id) => setDrafts((m) => ({ ...m, [r.key]: { value: valueOf(r), origin: m[r.key]?.origin ?? "page", documentId: id } }))}
                />
              ) : null}
            </div>
          </aside>
        </div>
      )}
    </div>
  );
}

function StatusBadge({ row, edited }: { row: SheetRowView; edited: boolean }) {
  if (edited) return <span className="badge badge-info">người dùng sửa</span>;
  if (!row.field_id) return <span className="badge">không tìm thấy</span>;
  if (row.status === "confirmed") return <span className="badge badge-ok">đã duyệt</span>;
  if (row.calc) return <span className="badge badge-vlm">agent tính</span>;
  if (row.low) return <span className="badge badge-warn">cần xem</span>;
  return <span className="badge">AI đề xuất</span>;
}

// Where the value of a row comes from: the page with the evidence boxed.
function SourcePane({
  row,
  sources,
  documentId,
  onDocument,
}: {
  row: SheetRowView;
  sources: { id: string; file_name: string }[];
  documentId?: string;
  onDocument: (id: string) => void;
}) {
  const e = row.evidence[0];
  if (!e)
    return (
      <>
        <div className="text-sm font-medium text-fg">{row.label}</div>
        <div className="text-xs">{row.note ? "Agent: " + row.note : "Agent không tìm thấy giá trị cho trường này."}</div>
        <label className="field">
          <span>File nguồn khi bạn tự nhập giá trị</span>
          <select className="input" value={documentId ?? ""} onChange={(ev) => onDocument(ev.target.value)}>
            <option value="">Chọn file…</option>
            {sources.map((s) => (
              <option key={s.id} value={s.id}>
                {s.file_name}
              </option>
            ))}
          </select>
        </label>
      </>
    );
  const box = e.bbox && e.bbox.length === 4 ? [{ key: "ev", bbox: { x0: e.bbox[0], y0: e.bbox[1], x1: e.bbox[2], y1: e.bbox[3] }, className: "hl" }] : [];
  return (
    <>
      <div className="text-sm font-medium text-fg">{row.label}</div>
      <div className="flex items-center gap-2 text-xs text-muted">
        <FileIcon name={e.file_name} size={16} /> {e.file_name} · trang {e.page_no}
      </div>
      <div className="overflow-hidden rounded-xl border border-line bg-surface-2">
        <PageImage docId={e.document_id} pageNo={e.page_no} boxes={box} scrollTo={box[0]?.key} />
      </div>
      <div className="rounded-lg bg-surface-2 p-3 text-[13px]">
        <div className="text-xs text-muted">Câu gốc</div>
        {e.quote}
      </div>
      {!row.value_matched && row.source === "agent" && (
        <div className="rounded-lg bg-warn-soft p-2 text-xs text-warn">{row.field_note ? "Giá trị do agent tính: " + row.field_note : "Giá trị AI không xuất hiện nguyên văn trong câu gốc."}</div>
      )}
    </>
  );
}
