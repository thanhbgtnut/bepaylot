import { useCallback, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";

import { getCase, getCheck, rerunCheck, setCheckLink } from "../../api/endpoints";
import type { Case, CheckDoc, CheckGroup, CheckMember, CheckOutcome, CheckRow, CheckRun, DocType } from "../../api/types";
import { useApp } from "../../components/AppContext";
import { BackButton } from "../../components/nav";
import { useToast } from "../../components/toast";
import { Icon, Spinner } from "../../components/ui";
import { typeColor } from "../../lib/doctype";
import { fmtDate } from "../../lib/format";

const SOURCE: Record<CheckMember["source"], string> = {
  rule: "Theo quy tắc",
  ai: "AI xác định",
  user: "Bạn chọn",
  single: "Chỉ có một hợp đồng",
};

// The result of a cross-check by one rule set (§6.9.9, §7.14), laid out by
// problem (U108): a verdict on top; the documents of the set on the left with
// their status (which one is wrong); in the middle every failing rule with
// the documents that are wrong and the values compared, then the documents no
// contract took; rules that pass are folded at the bottom. Picking a document
// keeps only the rules about it.
export function CheckPage() {
  const { caseId = "", checkId = "" } = useParams();
  const { kbs } = useApp();
  const { toast, fail } = useToast();
  const [run, setRun] = useState<CheckRun | null>(null);
  const [kcase, setCase] = useState<Case | null>(null);
  const [sel, setSel] = useState<string | null>(null); // a document (segment) to filter by
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => getCheck(checkId).then(setRun, fail), [checkId, fail]);
  useEffect(() => {
    load();
    getCase(caseId).then(setCase, () => {});
  }, [load, caseId]);
  const running = run?.status === "pending" || run?.status === "running";
  useEffect(() => {
    if (!running) return;
    const t = setInterval(load, 2000);
    return () => clearInterval(t);
  }, [running, load]);

  const act = async (p: Promise<CheckRun>, msg: string) => {
    setBusy(true);
    try {
      setRun(await p);
      toast(msg);
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  };
  const move = (m: CheckMember, v: string) =>
    act(
      setCheckLink(checkId, v === "auto" ? { segment_id: m.id, anchor_segment_id: null, auto: true } : { segment_id: m.id, anchor_segment_id: v === "none" ? null : v }),
      "Đã cập nhật và đối chiếu lại",
    );

  const res = run?.result;
  const types = kbs.find((k) => k.id === kcase?.kb_id)?.config.doc_types ?? [];
  const groups = res?.groups ?? [];
  const multi = groups.length > 1;
  const outcomes = groups.flatMap((g) => g.results.map((r) => ({ g, r })));
  const fails = outcomes.filter(({ r }) => r.status === "fail" || r.status === "error");
  const unknowns = outcomes.filter(({ r }) => r.status === "unknown");
  const passes = outcomes.filter(({ r }) => r.status === "pass");
  const wrongOf = (id: string) => outcomes.filter(({ r }) => r.rows?.some((x) => x.segment === id && x.status === "fail")).length;
  const unsureOf = (id: string) => outcomes.some(({ r }) => r.rows?.some((x) => x.segment === id && x.unsure));
  const about = (g: CheckGroup, r: CheckOutcome) => !sel || g.anchor.id === sel || !!r.rows?.some((x) => x.segment === sel);
  const nIssues = fails.length + unknowns.length + (res?.unlinked.length ?? 0);
  const badDocs = new Set(outcomes.flatMap(({ r }) => (r.rows ?? []).filter((x) => x.status === "fail").map((x) => x.segment)));
  const selMember = groups.flatMap((g) => g.members.map((m) => ({ g, m }))).find((x) => x.m.id === sel);
  const shownIssues = [...fails, ...unknowns].filter(({ g, r }) => about(g, r));

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="flex min-h-16 flex-none flex-wrap items-center gap-2 px-4 pt-3 pb-2">
        <BackButton fallback={`/cases/${caseId}`} fallbackLabel={`Hồ sơ ${kcase?.code ?? ""}`} />
        <span className="h-6 border-l border-line max-sm:hidden" />
        <div className="min-w-0 flex-1 px-2">
          <h1 className="flex items-center gap-2 truncate text-[22px] leading-7 font-normal text-fg">
            <Icon name="fact_check" size={24} fill className="text-[#5b47c9]" />
            Kết quả đối chiếu
          </h1>
          <div className="truncate text-xs text-muted">
            {res?.ruleset_name ? `Bộ quy tắc “${res.ruleset_name}” · ` : ""}
            {res ? `chạy lúc ${fmtDate(res.ran_at)}` : run ? `Tạo ${fmtDate(run.created_at)}` : ""}
          </div>
        </div>
        {run && (
          <>
            {res?.ruleset_id && (
              <Link className="btn btn-text" to={`/settings/checks/${res.ruleset_id}`}>
                <Icon name="rule" size={18} /> Bộ quy tắc
              </Link>
            )}
            <button className="btn btn-tonal" disabled={busy || running} onClick={() => act(rerunCheck(checkId), "Đã đối chiếu lại")}>
              {busy ? <Spinner className="size-4 border-2" /> : <Icon name="refresh" size={18} />} Đối chiếu lại
            </button>
          </>
        )}
      </header>

      {!run ? (
        <Spinner className="m-8" />
      ) : running ? (
        <div className="empty">
          <Spinner className="mb-4 size-8" />
          <div className="text-fg">Đang bóc tách thông tin và đối chiếu…</div>
          <div className="mt-1 text-sm">Thông tin đã có được dùng lại; chỉ phần còn thiếu mới cần AI đọc.</div>
        </div>
      ) : run.status === "failed" || !res ? (
        <div className="empty">
          <Icon name="error" size={40} className="mb-3 text-err" />
          <div className="text-fg">Không đối chiếu được</div>
          <div className="mt-1 max-w-xl text-sm">{run.error}</div>
        </div>
      ) : (
        <>
          <div className="px-6 pb-3">
            <div className={"flex flex-wrap items-center gap-4 rounded-2xl px-5 py-3 " + (nIssues ? "bg-err-soft/50" : "bg-ok-soft/60")}>
              <Icon name={nIssues ? "report" : "verified"} size={32} fill className={nIssues ? "text-err" : "text-ok"} />
              <div className="min-w-0 flex-1">
                <div className="text-base font-medium text-fg">{nIssues ? `${nIssues} vấn đề cần xử lý` : "Mọi quy tắc đều đạt"}</div>
                <div className="text-sm text-muted">
                  {badDocs.size ? `${badDocs.size} giấy tờ có lỗi` : "Không giấy tờ nào sai"} · {fails.length} quy tắc không đạt · {unknowns.length} thiếu thông tin · {res.unlinked.length} chứng từ chưa ghép · {passes.length} quy tắc đạt
                </div>
              </div>
            </div>
          </div>
          <div className="flex min-h-0 flex-1 border-t border-line max-md:flex-col">
            <nav className="flex-none overflow-auto border-r border-line p-2 md:w-64 max-md:max-h-56" aria-label="Giấy tờ trong bộ">
              <div className="flex items-center px-2 pt-1 pb-1.5">
                <span className="flex-1 text-xs font-medium text-muted">Giấy tờ trong bộ</span>
                {sel && (
                  <button className="text-xs text-accent hover:underline" onClick={() => setSel(null)}>
                    Xem tất cả
                  </button>
                )}
              </div>
              {groups.map((g) => (
                <div key={g.anchor.id} className="mb-1">
                  <DocRow d={g.anchor} types={types} on={sel === g.anchor.id} status={<span className="text-[11px] text-muted">gốc</span>} onPick={() => setSel(sel === g.anchor.id ? null : g.anchor.id)} />
                  {g.members.map((m) => {
                    const bad = wrongOf(m.id);
                    return (
                      <DocRow
                        key={m.id}
                        d={m}
                        types={types}
                        on={sel === m.id}
                        bad={bad > 0}
                        title={m.reason}
                        status={
                          bad ? (
                            <span className="inline-flex items-center gap-1 text-[11px] font-medium text-err">
                              <Icon name="cancel" size={16} fill />
                              {bad} lỗi
                            </span>
                          ) : unsureOf(m.id) ? (
                            <span title="Có giá trị AI đọc chưa chắc">
                              <Icon name="error" size={18} className="text-warn" />
                            </span>
                          ) : (
                            <Icon name="check_circle" size={18} fill className="text-ok" />
                          )
                        }
                        onPick={() => setSel(sel === m.id ? null : m.id)}
                      />
                    );
                  })}
                </div>
              ))}
              {res.unlinked.length > 0 && (
                <>
                  <div className="px-2 pt-3 pb-1.5 text-xs font-medium text-muted">Chưa ghép được</div>
                  {res.unlinked.map((m) => (
                    <DocRow key={m.id} d={m} types={types} on={sel === m.id} title={m.reason} status={<Icon name="link_off" size={18} className="text-warn" />} onPick={() => setSel(sel === m.id ? null : m.id)} />
                  ))}
                </>
              )}
              {!groups.length && <div className="p-3 text-sm text-subtle">Hồ sơ chưa có {res.anchor_title.toLowerCase()} nào.</div>}
            </nav>
            <main className="min-w-0 flex-1 overflow-auto px-6 py-4">
              <div className="flex max-w-5xl flex-col gap-3">
                {sel && (
                  <div className="flex items-center gap-2 text-sm text-muted">
                    <Icon name="filter_alt" size={18} />
                    Đang xem các quy tắc liên quan tới giấy tờ đã chọn
                  </div>
                )}
                {selMember && (
                  <div className="flex flex-wrap items-center gap-3 rounded-2xl border border-line px-4 py-3 text-sm">
                    <span className="min-w-0 flex-1">
                      <b className="font-medium text-fg">{selMember.m.label}</b> thuộc {selMember.g.anchor.label}
                      <span className="block text-xs text-muted">
                        {SOURCE[selMember.m.source]} · {selMember.m.reason}
                      </span>
                    </span>
                    <AnchorSelect m={selMember.m} groups={groups} current={selMember.g.anchor.id} busy={busy} onMove={move} />
                  </div>
                )}
                <h2 className="text-sm font-medium text-fg">Cần xử lý ({shownIssues.length + (sel ? 0 : res.unlinked.length)})</h2>
                {shownIssues.map(({ g, r }) => (
                  <IssueCard key={g.anchor.id + r.rule_id} g={g} r={r} multi={multi} sel={sel} caseId={caseId} sheetId={run.sheet_id} types={types} />
                ))}
                {!sel &&
                  res.unlinked.map((m) => (
                    <section key={m.id} className="rounded-2xl border border-l-4 border-line border-l-warn bg-surface px-4 py-3">
                      <div className="flex flex-wrap items-start gap-3">
                        <div className="min-w-0 flex-1">
                          <p className="text-sm text-fg">
                            <i className="mr-1.5 inline-block size-2 rounded-full align-middle" style={{ background: typeColor(m.type, types) }} />
                            <Link className="font-medium hover:underline" to={`/documents/${m.document_id}?page=${m.page_start ?? 1}`}>
                              {m.label}
                            </Link>{" "}
                            <span className="text-muted">chưa ghép được với {res.anchor_title.toLowerCase()} nào</span>
                          </p>
                          <p className="mt-0.5 text-xs text-muted">{m.reason}</p>
                        </div>
                        <span className="badge badge-warn h-7 px-2.5">
                          <Icon name="link_off" size={14} /> Chưa ghép
                        </span>
                      </div>
                      <div className="mt-2 flex flex-wrap items-center gap-2">
                        <span className="text-xs text-muted">Thuộc {res.anchor_title.toLowerCase()}</span>
                        <AnchorSelect m={m} groups={groups} busy={busy} onMove={move} />
                        <span className="text-xs text-subtle">Lựa chọn được giữ cho các lần sau.</span>
                      </div>
                    </section>
                  ))}
                {sel && !shownIssues.length && <p className="rounded-2xl bg-ok-soft/50 px-4 py-3 text-sm text-fg">Giấy tờ này không có lỗi nào.</p>}
                {!nIssues && <p className="rounded-2xl bg-ok-soft/50 px-4 py-3 text-sm text-fg">Không có gì cần xử lý.</p>}
                <details className="group mt-2 rounded-2xl border border-line" open={!!sel}>
                  <summary className="flex min-h-12 cursor-pointer list-none items-center gap-2 px-4 text-sm font-medium text-fg [&::-webkit-details-marker]:hidden">
                    <Icon name="check_circle" size={20} fill className="text-ok" />
                    <span className="flex-1">Đạt ({passes.filter(({ g, r }) => about(g, r)).length})</span>
                    <Icon name="expand_more" size={20} className="text-muted transition-transform group-open:rotate-180" />
                  </summary>
                  {passes
                    .filter(({ g, r }) => about(g, r))
                    .map(({ g, r }) => (
                      <div key={g.anchor.id + r.rule_id} className="border-t border-line">
                        <div className="flex flex-wrap items-start gap-3 px-4 pt-3 pb-2">
                          <p className="min-w-0 flex-1 text-sm leading-7 text-fg">
                            {r.name}
                            {multi && <span className="text-muted"> · {g.anchor.label}</span>}
                          </p>
                          {r.rows && (
                            <span className="text-xs text-muted">
                              {r.rows.length}/{r.rows.length} giấy tờ khớp
                            </span>
                          )}
                        </div>
                        <Rows r={r} sel={sel} />
                      </div>
                    ))}
                </details>
              </div>
            </main>
          </div>
        </>
      )}
    </div>
  );
}

function DocRow({ d, types, on, bad, title, status, onPick }: { d: CheckDoc; types: DocType[]; on: boolean; bad?: boolean; title?: string; status: React.ReactNode; onPick: () => void }) {
  return (
    <button
      className={"flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left " + (on ? "bg-accent-soft" : bad ? "bg-err-soft/40 hover:bg-err-soft/70" : "hover:bg-fg/5")}
      title={title}
      aria-pressed={on}
      onClick={onPick}
    >
      <i className="size-2 flex-none rounded-full" style={{ background: typeColor(d.type, types) }} />
      <span className="min-w-0 flex-1">
        <span className={"block truncate text-sm " + (bad ? "font-medium text-err" : "text-fg")}>{d.label}</span>
        <span className="block truncate text-[11px] text-muted">
          {d.type_title} · tr. {d.page_start}
        </span>
      </span>
      {status}
    </button>
  );
}

// One failing (or unknown) rule: the rule, the documents that are wrong, the
// values compared document by document, what to do.
function IssueCard({ g, r, multi, sel, caseId, sheetId, types }: { g: CheckGroup; r: CheckOutcome; multi: boolean; sel: string | null; caseId: string; sheetId: string; types: DocType[] }) {
  const wrong = (r.rows ?? []).filter((x) => x.status === "fail");
  const failing = r.status === "fail" || r.status === "error";
  return (
    <section className={"overflow-hidden rounded-2xl border border-l-4 border-line bg-surface " + (failing ? "border-l-err" : "border-l-warn")}>
      <div className="flex flex-wrap items-start gap-3 px-4 pt-3 pb-2">
        <div className="min-w-0 flex-1">
          <p className="text-sm leading-7 text-fg">
            {r.a && r.b ? (
              <>
                <Chip label={r.a.label} title={r.a.type_title} color={typeColor(r.a.type, types)} /> {r.op === "sum_le" ? "cộng lại không vượt quá" : r.op_text}{" "}
                <Chip label={r.b.label} title={r.b.type_title} color={typeColor(r.b.type, types)} />
              </>
            ) : (
              r.name
            )}
            {multi && <span className="text-muted"> · {g.anchor.label}</span>}
          </p>
          {r.ai && (
            <span className="badge badge-vlm">
              <Icon name="auto_awesome" size={12} /> AI đánh giá
            </span>
          )}
        </div>
        {failing ? (
          <span className="badge badge-err h-7 px-2.5">
            <Icon name="cancel" size={14} /> {r.status === "error" ? "Lỗi quy tắc" : "Không đạt"}
          </span>
        ) : (
          <span className="badge badge-warn h-7 px-2.5">
            <Icon name="help" size={14} /> Thiếu thông tin
          </span>
        )}
      </div>
      {wrong.length > 0 && (
        <div className="mx-4 mb-2 flex flex-wrap items-center gap-2 text-sm">
          <span className="text-muted">Giấy tờ sai:</span>
          {wrong.map((x) => (
            <Link key={x.segment} className="tag !bg-err-soft !text-err hover:underline" to={`/documents/${x.document_id}?page=${x.page}`}>
              <Icon name="description" size={14} />
              {x.label} · tr. {x.page}
            </Link>
          ))}
        </div>
      )}
      <Rows r={r} sel={sel} />
      {!r.rows && failing && (r.failing ?? []).length > 0 && (
        <ul className="mx-4 mb-3 flex flex-col gap-1 text-sm text-err">
          {(r.failing ?? []).map((f, i) => (
            <li key={i}>{f.note}</li>
          ))}
        </ul>
      )}
      {r.status === "unknown" && (
        <ul className="mx-4 mb-3 flex flex-col gap-1 text-sm text-fg">
          {(r.missing ?? []).map((m, i) => (
            <li key={i} className="flex items-start gap-2">
              <Icon name="help" size={18} className="mt-0.5 flex-none text-warn" />
              {m}
            </li>
          ))}
        </ul>
      )}
      {r.status === "error" && r.detail && <p className="mx-4 mb-3 text-sm text-err">{r.detail}</p>}
      <div className="flex flex-wrap items-center gap-2 border-t border-line bg-surface-2/50 px-4 py-2.5">
        <p className="min-w-0 flex-1 text-xs text-muted">{r.hint || (r.status === "unknown" ? "Bổ sung giấy tờ hoặc thông tin còn thiếu rồi đối chiếu lại; nếu không cần thì tắt quy tắc này trong bộ." : "")}</p>
        {wrong[0] && (
          <Link className="btn btn-sm" to={`/documents/${wrong[0].document_id}?page=${wrong[0].page}`}>
            <Icon name="open_in_new" size={16} /> Xem trang gốc
          </Link>
        )}
        <Link className="btn btn-sm" to={`/cases/${caseId}/sheets/${sheetId}`}>
          <Icon name="table_view" size={16} /> Sửa số liệu
        </Link>
      </div>
    </section>
  );
}

function Chip({ label, title, color }: { label: string; title: string; color: string }) {
  return (
    <span className="inline-flex max-w-full items-center gap-1.5 rounded-lg bg-surface-2 px-2 py-0.5 align-middle text-fg">
      <i className="size-2 flex-none rounded-full" style={{ background: color }} />
      <b className="font-medium">{label}</b>
      <span className="text-muted">của {title}</span>
    </span>
  );
}

// The comparisons of a rule, one row per document; a wrong row is red.
function Rows({ r, sel }: { r: CheckOutcome; sel: string | null }) {
  if (!r.rows?.length) return null;
  const sum = r.op === "sum_le";
  const cell = (x: CheckRow) => (
    <tr key={x.segment} className={"border-t border-line " + (x.status === "fail" ? "bg-err-soft/50 " : "") + (sel === x.segment ? "outline-2 -outline-offset-2 outline-accent" : "")}>
      <td className="py-2 pl-4">
        <Link className={"hover:underline " + (x.status === "fail" ? "font-medium text-err" : "text-fg")} to={`/documents/${x.document_id}?page=${x.page}`}>
          {x.label}
        </Link>
        <span className="ml-1.5 text-xs text-muted">tr. {x.page}</span>
      </td>
      <td className={"px-3 tabular-nums " + (x.status === "fail" ? "font-medium text-err" : "text-fg")}>
        {x.value_a || <span className="text-subtle italic">không có</span>}{" "}
        {x.unsure && (
          <span className="badge badge-warn align-middle" title="AI đọc chưa chắc chắn, nên xem trang gốc">
            kiểm tra lại
          </span>
        )}
      </td>
      {!sum && <td className="px-3 tabular-nums text-fg">{x.value_b || <span className="text-subtle italic">không có</span>}</td>}
      <td className="pr-4 text-right whitespace-nowrap">
        {x.status === "fail" ? (
          <span className="inline-flex items-center gap-1 text-xs font-medium text-err">
            <Icon name="cancel" size={16} fill />
            Không khớp
          </span>
        ) : x.status === "unknown" ? (
          <span className="inline-flex items-center gap-1 text-xs text-warn">
            <Icon name="help" size={16} />
            Thiếu
          </span>
        ) : x.status === "pass" ? (
          <span className="inline-flex items-center gap-1 text-xs text-ok">
            <Icon name="check_circle" size={16} fill />
            Khớp
          </span>
        ) : null}
      </td>
    </tr>
  );
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-120 text-sm">
        <thead>
          <tr className="text-left text-xs text-muted">
            <th className="py-2 pl-4 font-medium">Giấy tờ</th>
            <th className="px-3 font-medium">{r.a?.label}</th>
            {!sum && (
              <th className="px-3 font-medium">
                {r.b?.label} <span className="font-normal text-subtle">({r.b?.type_title})</span>
              </th>
            )}
            <th className="w-28 pr-4 text-right font-medium">{sum ? "" : "Kết quả"}</th>
          </tr>
        </thead>
        <tbody>
          {r.rows.map(cell)}
          {r.total && (
            <tr className="border-t-2 border-line bg-surface-2/60 font-medium">
              <td className="py-2 pl-4 text-fg">Tổng</td>
              <td className="px-3 tabular-nums text-fg">{r.total.a}</td>
              <td className={"pr-4 text-right text-xs " + (r.status === "pass" ? "text-ok" : "text-err")}>
                {r.status === "pass" ? "≤" : ">"} {r.total.b}
                {r.total.note && <span className="block font-normal text-muted">{r.total.note}</span>}
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}

function AnchorSelect({ m, groups, current, busy, onMove }: { m: CheckMember; groups: CheckGroup[]; current?: string; busy: boolean; onMove: (m: CheckMember, v: string) => void }) {
  const cands = new Set(m.candidates ?? []);
  return (
    <select className="input h-9 w-64" value={current ?? ""} disabled={busy} onChange={(e) => onMove(m, e.target.value)} aria-label={`Hợp đồng của ${m.label}`}>
      {!current && <option value="">— chọn —</option>}
      {groups.map((g) => (
        <option key={g.anchor.id} value={g.anchor.id}>
          {cands.has(g.anchor.id) ? "★ " : ""}
          {g.anchor.label}
        </option>
      ))}
      <option value="none">Không thuộc hợp đồng nào</option>
      {m.source === "user" && <option value="auto">Để máy tự xác định lại</option>}
    </select>
  );
}
