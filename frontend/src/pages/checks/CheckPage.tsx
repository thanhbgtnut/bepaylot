import { useCallback, useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";

import { getCase, getCheck, rerunCheck, setCheckLink } from "../../api/endpoints";
import type { Case, CheckDoc, CheckGroup, CheckMember, CheckOutcome, CheckRef, CheckRun } from "../../api/types";
import { useToast } from "../../components/toast";
import { Icon, Spinner } from "../../components/ui";
import { fmtDate } from "../../lib/format";

const SOURCE: Record<CheckMember["source"], [string, string]> = {
  rule: ["Theo quy tắc", "badge-ok"],
  ai: ["AI xác định", "badge-vlm"],
  user: ["Bạn chọn", "badge-info"],
  single: ["Chỉ có một hợp đồng", ""],
};

const STATUS: Record<CheckOutcome["status"], [string, string, string]> = {
  pass: ["check_circle", "text-ok", "Đạt"],
  fail: ["cancel", "text-err", "Không đạt"],
  unknown: ["help", "text-warn", "Thiếu thông tin"],
  error: ["error", "text-err", "Lỗi quy tắc"],
};

// The result of a cross-check of a case (§6.9.9, §7.14): contracts on the
// left (with the documents no contract took), the selected contract's rules
// and documents on the right. The user moves a document to another contract
// with one select; the run is evaluated again at once.
export function CheckPage() {
  const { caseId = "", checkId = "" } = useParams();
  const nav = useNavigate();
  const { toast, fail } = useToast();
  const [run, setRun] = useState<CheckRun | null>(null);
  const [kcase, setCase] = useState<Case | null>(null);
  const [sel, setSel] = useState<string>(""); // anchor id, or "unlinked"
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

  const res = run?.result;
  useEffect(() => {
    if (!res || sel) return;
    const firstBad = res.groups.find((g) => g.results.some((r) => r.status !== "pass"));
    setSel(res.unlinked.length ? "unlinked" : (firstBad ?? res.groups[0])?.anchor.id ?? "");
  }, [res, sel]);

  const docs = new Map<string, CheckDoc>();
  for (const g of res?.groups ?? []) {
    docs.set(g.anchor.id, g.anchor);
    for (const m of g.members) docs.set(m.id, m);
  }
  for (const m of res?.unlinked ?? []) docs.set(m.id, m);

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

  const back = () => nav(`/cases/${caseId}`);
  const group = res?.groups.find((g) => g.anchor.id === sel);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="flex min-h-16 flex-none flex-wrap items-center gap-2 px-4 pt-3 pb-2">
        <button className="btn btn-text" onClick={back} title="Quay lại phương án">
          <Icon name="arrow_back" size={20} /> Phương án {kcase?.code ?? ""}
        </button>
        <span className="h-6 border-l border-line max-sm:hidden" />
        <div className="min-w-0 flex-1 px-2">
          <h1 className="flex items-center gap-2 truncate text-[22px] leading-7 font-normal text-fg">
            <Icon name="fact_check" size={24} fill className="text-[#5b47c9]" />
            Đối chiếu chứng từ
          </h1>
          <div className="truncate text-xs text-muted">{run ? `Tạo ${fmtDate(run.created_at)}${res ? ` · đối chiếu lúc ${fmtDate(res.ran_at)}` : ""}` : ""}</div>
        </div>
        {run && (
          <>
            <Link className="btn btn-text" to={`/cases/${caseId}/sheets/${run.sheet_id}`} title="Mở bảng số liệu để sửa giá trị AI đọc sai, rồi bấm Đối chiếu lại">
              <Icon name="table_view" size={18} /> Sửa số liệu
            </Link>
            <Link className="btn btn-text" to="/settings/checks">
              <Icon name="rule" size={18} /> Quy tắc
            </Link>
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
          <div className="flex flex-wrap gap-2 px-6 pb-3">
            <span className="badge h-7 px-2.5">{res.counts.groups} {res.anchor_title.toLowerCase()}</span>
            <span className="badge badge-ok h-7 px-2.5">
              <Icon name="check_circle" size={14} /> {res.counts.pass} đạt
            </span>
            <span className="badge badge-err h-7 px-2.5">
              <Icon name="cancel" size={14} /> {res.counts.fail} không đạt
            </span>
            <span className="badge badge-warn h-7 px-2.5">
              <Icon name="help" size={14} /> {res.counts.unknown} thiếu thông tin
            </span>
            {res.counts.unlinked > 0 && (
              <span className="badge badge-warn h-7 px-2.5">
                <Icon name="warning" size={14} /> {res.counts.unlinked} chứng từ chưa xác định {res.anchor_title.toLowerCase()}
              </span>
            )}
          </div>
          <div className="flex min-h-0 flex-1 border-t border-line max-md:flex-col">
            <nav className="flex-none overflow-auto border-r border-line p-2 md:w-72 max-md:max-h-56" aria-label={res.anchor_title}>
              {res.unlinked.length > 0 && (
                <button onClick={() => setSel("unlinked")} className={"mb-1 flex w-full items-center gap-2 rounded-xl px-3 py-2.5 text-left text-sm " + (sel === "unlinked" ? "bg-warn-soft" : "hover:bg-fg/5")}>
                  <Icon name="warning" size={20} className="text-warn" />
                  <span className="min-w-0 flex-1">
                    <span className="block font-medium text-fg">Chưa xác định</span>
                    <span className="block text-xs text-muted">{res.unlinked.length} chứng từ cần bạn chọn</span>
                  </span>
                </button>
              )}
              {res.groups.map((g) => {
                const bad = g.results.filter((r) => r.status === "fail" || r.status === "error").length;
                const unk = g.results.filter((r) => r.status === "unknown").length;
                return (
                  <button key={g.anchor.id} onClick={() => setSel(g.anchor.id)} className={"flex w-full items-center gap-2 rounded-xl px-3 py-2.5 text-left text-sm " + (sel === g.anchor.id ? "bg-accent-soft" : "hover:bg-fg/5")}>
                    <Icon name={bad ? "cancel" : unk ? "help" : "check_circle"} size={20} fill className={bad ? "text-err" : unk ? "text-warn" : "text-ok"} />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-fg">{g.anchor.label}</span>
                      <span className="block truncate text-xs text-muted">
                        {g.members.length} chứng từ · {bad ? `${bad} không đạt` : unk ? `${unk} thiếu thông tin` : "đạt hết"}
                      </span>
                    </span>
                  </button>
                );
              })}
              {!res.groups.length && <div className="p-3 text-sm text-subtle">Hồ sơ chưa có {res.anchor_title.toLowerCase()} nào.</div>}
            </nav>
            <main className="min-w-0 flex-1 overflow-auto px-6 py-4">
              {sel === "unlinked" ? (
                <Unlinked members={res.unlinked} groups={res.groups} anchorTitle={res.anchor_title} busy={busy} onMove={move} />
              ) : group ? (
                <Group g={group} groups={res.groups} docs={docs} busy={busy} onMove={move} />
              ) : null}
            </main>
          </div>
        </>
      )}
    </div>
  );
}

function docLink(d?: CheckDoc) {
  return d ? `/documents/${d.document_id}?page=${d.page_start ?? 1}` : undefined;
}

function Refs({ refs, docs }: { refs?: CheckRef[]; docs: Map<string, CheckDoc> }) {
  const seen = new Set<string>();
  const list = (refs ?? []).filter((r) => r.doc && !seen.has(r.doc) && seen.add(r.doc));
  if (!list.length) return null;
  return (
    <span className="inline-flex flex-wrap gap-1">
      {list.map((r) => {
        const to = docLink(docs.get(r.doc));
        return to ? (
          <Link key={r.doc} to={to} className="tag hover:bg-fg/8" title="Mở trang gốc">
            <Icon name="description" size={14} /> {r.label}
          </Link>
        ) : null;
      })}
    </span>
  );
}

function Group({ g, groups, docs, busy, onMove }: { g: CheckGroup; groups: CheckGroup[]; docs: Map<string, CheckDoc>; busy: boolean; onMove: (m: CheckMember, v: string) => void }) {
  return (
    <div className="flex max-w-5xl flex-col gap-5">
      <div>
        <h2 className="text-base font-medium text-fg">{g.anchor.label}</h2>
        <div className="text-xs text-muted">
          {g.anchor.file_name} · tr.{g.anchor.page_start}–{g.anchor.page_end} ·{" "}
          <Link className="text-accent hover:underline" to={docLink(g.anchor)!}>
            mở giấy tờ
          </Link>
        </div>
      </div>

      <section className="rounded-2xl border border-line">
        <div className="border-b border-line px-4 py-2.5 text-sm font-medium text-fg">Kết quả kiểm tra</div>
        {!g.results.length && <div className="px-4 py-3 text-sm text-subtle">Chưa có quy tắc nào đang bật.</div>}
        {g.results.map((r) => {
          const [icon, color, label] = STATUS[r.status];
          return (
            <div key={r.rule_id} className="flex gap-3 border-b border-line px-4 py-3 last:border-b-0">
              <Icon name={icon} size={22} fill className={color + " mt-0.5 flex-none"} />
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2 text-sm text-fg">
                  {r.name}
                  <span className={"text-xs " + color}>{label}</span>
                  {r.ai && (
                    <span className="badge badge-vlm">
                      <Icon name="auto_awesome" size={12} /> AI đánh giá
                    </span>
                  )}
                </div>
                {r.name !== r.text && <div className="text-xs text-muted">{r.text}</div>}
                {r.status === "fail" &&
                  (r.failing ?? []).map((f, i) => (
                    <div key={i} className="mt-1 flex flex-wrap items-center gap-2 text-sm text-err">
                      {f.note} <Refs refs={f.refs} docs={docs} />
                    </div>
                  ))}
                {r.status === "pass" && r.detail && <div className="mt-1 text-xs text-muted">{r.detail}</div>}
                {r.status === "unknown" && (
                  <ul className="mt-1 list-disc pl-5 text-xs text-warn">
                    {(r.missing ?? []).map((m, i) => (
                      <li key={i}>{m}</li>
                    ))}
                  </ul>
                )}
                {r.status === "error" && <div className="mt-1 text-xs text-err">{r.detail}</div>}
              </div>
            </div>
          );
        })}
      </section>

      <section className="rounded-2xl border border-line">
        <div className="border-b border-line px-4 py-2.5 text-sm font-medium text-fg">Chứng từ thuộc {g.anchor.type_title.toLowerCase()} này ({g.members.length})</div>
        {!g.members.length && <div className="px-4 py-3 text-sm text-subtle">Chưa có chứng từ nào được gắn.</div>}
        {g.members.map((m) => (
          <MemberRow key={m.id} m={m} groups={groups} current={g.anchor.id} busy={busy} onMove={onMove} />
        ))}
      </section>
    </div>
  );
}

function MemberRow({ m, groups, current, busy, onMove }: { m: CheckMember; groups: CheckGroup[]; current?: string; busy: boolean; onMove: (m: CheckMember, v: string) => void }) {
  const [src, cls] = SOURCE[m.source] ?? ["", ""];
  const cands = new Set(m.candidates ?? []);
  return (
    <div className="flex flex-wrap items-start gap-3 border-b border-line px-4 py-3 last:border-b-0">
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <Link className="text-fg hover:underline" to={docLink(m)!}>
            {m.label}
          </Link>
          <span className="text-xs text-muted">
            {m.file_name} · tr.{m.page_start}–{m.page_end}
          </span>
          {src && <span className={"badge " + cls}>{src}</span>}
        </div>
        <div className="mt-0.5 text-xs text-muted">{m.reason}</div>
      </div>
      <select className="input h-9 w-60" value={current ?? ""} disabled={busy} onChange={(e) => onMove(m, e.target.value)} aria-label={`Hợp đồng của ${m.label}`}>
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
    </div>
  );
}

function Unlinked({ members, groups, anchorTitle, busy, onMove }: { members: CheckMember[]; groups: CheckGroup[]; anchorTitle: string; busy: boolean; onMove: (m: CheckMember, v: string) => void }) {
  return (
    <div className="flex max-w-5xl flex-col gap-3">
      <div>
        <h2 className="text-base font-medium text-fg">Chứng từ chưa xác định {anchorTitle.toLowerCase()}</h2>
        <p className="text-sm text-muted">Máy không chắc các chứng từ này thuộc {anchorTitle.toLowerCase()} nào. Chọn ở ô bên phải; lựa chọn được giữ cho các lần đối chiếu sau. Dấu ★ là các ứng viên máy thấy phù hợp.</p>
      </div>
      <section className="rounded-2xl border border-line">
        {members.map((m) => (
          <MemberRow key={m.id} m={m} groups={groups} busy={busy} onMove={onMove} />
        ))}
      </section>
    </div>
  );
}
