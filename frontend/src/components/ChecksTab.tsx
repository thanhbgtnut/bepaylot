import { useCallback, useEffect, useRef, useState } from "react";

import { compileCheck, saveChecks } from "../api/endpoints";
import { canEditPrompts, type CheckConfig, type CheckRule, type CheckStep } from "../api/types";
import { useApp } from "./AppContext";
import { useAuth } from "./AuthContext";
import { useToast } from "./toast";
import { Empty, Icon, Spinner } from "./ui";

// Cài đặt → Quy tắc đối chiếu (§6.9.9, §7.13): how the documents of a case
// are cross-checked, written as plain sentences. Each sentence is turned into
// a formula by the agent when the user leaves it (once); the formula stays
// hidden behind "Công thức" for those who want it, and a sentence that cannot
// be a formula is judged by the AI at each run.

const LINK_HINTS = ["Cùng tên bên bán với hợp đồng", "Nội dung có nhắc số hoặc tên hợp đồng", "Mặt hàng và số lượng phù hợp với hợp đồng"];
const RULE_HINTS = [
  "Ngày hoá đơn nằm trong thời hạn hợp đồng",
  "Tổng giá trị hoá đơn không lớn hơn giá trị hợp đồng cộng phụ lục",
  "Tài khoản bên bán trên các chứng từ trùng với tài khoản nhận tiền trên uỷ nhiệm chi",
  "Nếu hợp đồng thanh toán sau khi giao hàng thì phải có hoá đơn hoặc biên bản bàn giao",
  "Số tiền uỷ nhiệm chi không lớn hơn đề nghị thanh toán, đề nghị thanh toán không lớn hơn tổng hoá đơn",
];

const newId = () => Math.random().toString(36).slice(2, 10);

type Item = CheckStep & Partial<Pick<CheckRule, "name" | "off">>;

export function ChecksTab() {
  const { kbs, kb: current, updateKBLocal } = useApp();
  const { user } = useAuth();
  const editor = canEditPrompts(user);
  const { toast, fail } = useToast();
  const [picked, setPick] = useState(current?.id ?? kbs[0]?.id ?? "");
  const pick = kbs.some((k) => k.id === picked) ? picked : (current?.id ?? kbs[0]?.id ?? "");
  const kb = kbs.find((k) => k.id === pick);
  const types = kb?.config.doc_types ?? [];
  const [cfg, setCfg] = useState<CheckConfig>({ anchor: "", links: [], rules: [] });
  const [dirty, setDirty] = useState(false);
  const [busy, setBusy] = useState(false);
  const [working, setWorking] = useState<Record<string, boolean>>({}); // ids being compiled

  const reset = useCallback(() => {
    const c = kb?.config.checks;
    const anchor = c?.anchor || types.find((t) => /hop_dong|hopdong|contract/.test(t.name))?.name || "";
    setCfg({ anchor, links: c?.links ?? [], rules: c?.rules ?? [] });
    setDirty(false);
  }, [kb]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => reset(), [reset]);

  const change = (fn: (c: CheckConfig) => CheckConfig) => {
    setCfg(fn);
    setDirty(true);
  };

  const stepsOf = (dt: string) => cfg.links.find((l) => l.doc_type === dt)?.steps ?? [];
  const setSteps = (dt: string, fn: (xs: CheckStep[]) => CheckStep[]) =>
    change((c) => {
      const has = c.links.some((l) => l.doc_type === dt);
      const links = has ? c.links.map((l) => (l.doc_type === dt ? { ...l, steps: fn(l.steps) } : l)) : [...c.links, { doc_type: dt, steps: fn([]) }];
      return { ...c, links };
    });
  const setRules = (fn: (xs: CheckRule[]) => CheckRule[]) => change((c) => ({ ...c, rules: fn(c.rules) }));

  // compile turns one sentence into a formula and stores it on the item.
  const compile = async (kind: "rule" | "link", id: string, text: string, docType?: string) => {
    if (!kb || !cfg.anchor || !text.trim()) return;
    setWorking((w) => ({ ...w, [id]: true }));
    try {
      const r = await compileCheck(kb.id, { kind, doc_type: docType, anchor: cfg.anchor, text });
      const patch = { formula: r.formula ?? "", ai: r.ai, explanation: r.explanation + (r.missing ? ` Còn thiếu: ${r.missing}.` : "") };
      if (kind === "link" && docType) setSteps(docType, (xs) => xs.map((x) => (x.id === id ? { ...x, ...patch } : x)));
      else setRules((xs) => xs.map((x) => (x.id === id ? { ...x, ...patch, name: r.name || x.name } : x)));
    } catch (e) {
      fail(e);
    } finally {
      setWorking((w) => ({ ...w, [id]: false }));
    }
  };

  const save = async () => {
    if (!kb) return;
    setBusy(true);
    try {
      const k = await saveChecks(kb.id, cfg);
      updateKBLocal(k);
      setDirty(false);
      toast("Đã lưu quy tắc đối chiếu");
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  };
  const saveRef = useRef(save);
  saveRef.current = save;
  useEffect(() => {
    if (!dirty) return;
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "s") (e.preventDefault(), saveRef.current());
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [dirty]);

  if (!kbs.length)
    return (
      <Empty icon="create_new_folder" title="Chưa có thư viện">
        Tạo thư viện và khai báo loại giấy tờ trước khi cài đặt quy tắc đối chiếu.
      </Empty>
    );
  const anchorTitle = types.find((t) => t.name === cfg.anchor)?.title ?? "giấy tờ gốc";
  const others = types.filter((t) => t.name !== cfg.anchor);
  const off = !editor;
  const pending = Object.values(working).some(Boolean);

  return (
    <>
      <header className="flex flex-wrap items-end gap-x-6 gap-y-3">
        <div className="min-w-60 flex-1">
          <h2 className="text-base font-medium text-fg">Quy tắc đối chiếu chứng từ</h2>
          <p className="mt-1 max-w-2xl text-sm text-muted">
            Viết bằng lời thường. Hệ thống tự hiểu từng câu và chuyển thành phép kiểm tra trên các trường đã khai báo ở Loại giấy tờ. Câu nào máy không tự kiểm được thì AI sẽ đọc thông tin và đánh giá.
          </p>
        </div>
        <label className="field w-full sm:w-72">
          <span>Thư viện</span>
          <select className="input" value={pick} onChange={(e) => setPick(e.target.value)} disabled={dirty} title={dirty ? "Lưu hoặc hoàn tác thay đổi trước khi đổi thư viện" : undefined}>
            {kbs.map((k) => (
              <option key={k.id} value={k.id}>
                {k.name}
              </option>
            ))}
          </select>
        </label>
      </header>
      {!editor && (
        <div className="rounded-xl bg-surface-2 px-4 py-3 text-sm text-muted">
          <Icon name="lock" size={16} className="mr-1 align-[-3px]" />
          Chỉ người được đặt prompt hoặc quản trị mới sửa được quy tắc. Bạn vẫn chạy đối chiếu ở Studio của hồ sơ.
        </div>
      )}
      {!types.length ? (
        <div className="rounded-2xl border border-line">
          <Empty icon="category" title="Thư viện này chưa có loại giấy tờ">
            Khai báo loại giấy tờ và các trường cần lấy ở tab Loại giấy tờ trước.
          </Empty>
        </div>
      ) : (
        <div className="flex flex-col gap-4">
          <Card n={1} title="Giấy tờ gốc của mỗi bộ chứng từ">
            <div className="flex flex-wrap items-center gap-2 text-sm text-fg">
              Mỗi bộ chứng từ xoay quanh một
              <select className="input h-9 w-56" value={cfg.anchor} disabled={off} onChange={(e) => change((c) => ({ ...c, anchor: e.target.value }))} aria-label="Loại giấy tờ gốc">
                <option value="">— chọn loại giấy tờ —</option>
                {types.map((t) => (
                  <option key={t.name} value={t.name}>
                    {t.title}
                  </option>
                ))}
              </select>
              ; các chứng từ khác được gắn vào {anchorTitle.toLowerCase()} mà nó thuộc về.
            </div>
          </Card>

          <Card n={2} title={`Nhận biết chứng từ thuộc ${anchorTitle.toLowerCase()} nào`} hint="Máy xét lần lượt từng bước, dừng khi chỉ còn một hợp đồng phù hợp. Không xác định được thì báo để người dùng tự chọn. Loại không có bước nào: hồ sơ chỉ có một hợp đồng thì gắn luôn.">
            {!cfg.anchor ? (
              <div className="text-sm text-subtle">Chọn giấy tờ gốc ở bước 1 trước.</div>
            ) : (
              <div className="flex flex-col gap-3">
                {others.map((t) => (
                  <details key={t.name} className="group rounded-xl border border-line" open={stepsOf(t.name).length > 0}>
                    <summary className="flex cursor-pointer list-none items-center gap-2 px-3 py-2.5 text-sm">
                      <Icon name="chevron_right" size={18} className="text-muted transition-transform group-open:rotate-90" />
                      <span className="flex-1 font-medium text-fg">{t.title}</span>
                      <span className="text-xs text-muted">{stepsOf(t.name).length ? `${stepsOf(t.name).length} bước` : "chưa có bước"}</span>
                    </summary>
                    <div className="border-t border-line px-3 py-2">
                      <Sentences
                        items={stepsOf(t.name)}
                        disabled={off}
                        working={working}
                        label={(i) => `Bước ${i + 1}`}
                        placeholder={`Gõ một bước, ví dụ: ${LINK_HINTS[0]}`}
                        hints={LINK_HINTS}
                        onChange={(fn) => setSteps(t.name, fn)}
                        onCompile={(id, text) => compile("link", id, text, t.name)}
                      />
                    </div>
                  </details>
                ))}
              </div>
            )}
          </Card>

          <Card n={3} title="Quy tắc kiểm tra" hint={`Chạy trên mỗi ${anchorTitle.toLowerCase()} cùng các chứng từ đã gắn vào nó. Kết quả: Đạt, Không đạt, hoặc Thiếu thông tin (không bao giờ tự coi là đạt).`}>
            {!cfg.anchor ? (
              <div className="text-sm text-subtle">Chọn giấy tờ gốc ở bước 1 trước.</div>
            ) : (
              <Sentences
                items={cfg.rules}
                disabled={off}
                working={working}
                toggle
                label={(i) => `${i + 1}`}
                placeholder={`Gõ một quy tắc, ví dụ: ${RULE_HINTS[1]}`}
                hints={RULE_HINTS}
                onChange={(fn) => setRules((xs) => fn(xs as Item[]) as CheckRule[])}
                onCompile={(id, text) => compile("rule", id, text)}
              />
            )}
          </Card>

          {editor && (
            <div className="sticky bottom-0 flex flex-wrap items-center gap-3 rounded-2xl border border-line bg-surface px-4 py-3 shadow-1">
              <span className="min-w-0 flex-1 text-xs text-muted">Gõ xong một câu và rời ô (hoặc Enter) là hệ thống tự hiểu câu đó. ⌘/Ctrl + S để lưu.</span>
              <span className="text-xs text-muted">{pending ? "Đang hiểu câu…" : dirty ? "Có thay đổi chưa lưu" : "Đã lưu"}</span>
              <button className="btn btn-text" disabled={!dirty || busy} onClick={reset}>
                Hoàn tác
              </button>
              <button className="btn btn-primary" disabled={!dirty || busy || pending || !cfg.anchor} onClick={save}>
                {busy ? <Spinner className="size-4 border-2 border-white/40 border-t-white" /> : <Icon name="check" size={18} />}
                Lưu
              </button>
            </div>
          )}
        </div>
      )}
    </>
  );
}

function Card({ n, title, hint, children }: { n: number; title: string; hint?: string; children: React.ReactNode }) {
  return (
    <section className="rounded-2xl border border-line p-4">
      <div className="mb-3 flex items-start gap-3">
        <span className="grid size-7 flex-none place-items-center rounded-full bg-accent-soft text-sm font-medium text-on-accent-soft">{n}</span>
        <div className="min-w-0">
          <h3 className="text-sm font-medium text-fg">{title}</h3>
          {hint && <p className="mt-0.5 text-xs text-muted">{hint}</p>}
        </div>
      </div>
      {children}
    </section>
  );
}

// Sentences is a list that always ends with an empty row (U76): typing in it
// adds an item; leaving a changed sentence turns it into a formula.
function Sentences({
  items,
  disabled,
  working,
  toggle,
  label,
  placeholder,
  hints,
  onChange,
  onCompile,
}: {
  items: Item[];
  disabled: boolean;
  working: Record<string, boolean>;
  toggle?: boolean;
  label: (i: number) => string;
  placeholder: string;
  hints: string[];
  onChange: (fn: (xs: Item[]) => Item[]) => void;
  onCompile: (id: string, text: string) => void;
}) {
  const [draft, setDraft] = useState("");
  const compiled = useRef<Record<string, string>>({}); // id → text last compiled
  useEffect(() => {
    for (const x of items) if (x.id && (x.formula || x.ai) && compiled.current[x.id] === undefined) compiled.current[x.id] = x.text;
  }, [items]);

  const add = (text: string) => {
    if (!text.trim()) return;
    const id = newId();
    onChange((xs) => [...xs, { id, text, name: "" }]);
    compiled.current[id] = text;
    onCompile(id, text);
  };
  const leave = (x: Item) => {
    if (!x.id || !x.text.trim() || compiled.current[x.id] === x.text) return;
    compiled.current[x.id] = x.text;
    onCompile(x.id, x.text);
  };
  const unused = hints.filter((h) => !items.some((x) => x.text === h));

  return (
    <div className="flex flex-col">
      {items.map((x, i) => (
        <Row
          key={x.id ?? i}
          item={x}
          label={label(i)}
          toggle={toggle}
          disabled={disabled}
          busy={!!(x.id && working[x.id])}
          onText={(text) => onChange((xs) => xs.map((y) => (y === x ? { ...y, text } : y)))}
          onLeave={() => leave(x)}
          onFormula={(formula) => onChange((xs) => xs.map((y) => (y === x ? { ...y, formula, ai: !formula, explanation: formula ? "Công thức do bạn sửa" : y.explanation } : y)))}
          onOff={(o) => onChange((xs) => xs.map((y) => (y === x ? { ...y, off: o } : y)))}
          onRemove={() => onChange((xs) => xs.filter((y) => y !== x))}
          onRetry={() => x.id && onCompile(x.id, x.text)}
        />
      ))}
      {!disabled && (
        <div className="flex items-center gap-2 py-1.5">
          <span className="w-14 flex-none text-xs text-subtle">{label(items.length)}</span>
          <input
            className="input h-9 flex-1"
            placeholder={placeholder}
            value={draft}
            aria-label="Thêm câu mới"
            onChange={(e) => setDraft(e.target.value)}
            onBlur={() => (add(draft), setDraft(""))}
            onKeyDown={(e) => e.key === "Enter" && (add(draft), setDraft(""))}
          />
        </div>
      )}
      {!disabled && unused.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5 pt-1 pl-16">
          <span className="text-xs text-subtle">Gợi ý:</span>
          {unused.map((h) => (
            <button key={h} className="chip h-7 text-xs" onClick={() => add(h)}>
              <Icon name="add" size={14} /> {h}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function Row({
  item: x,
  label,
  toggle,
  disabled,
  busy,
  onText,
  onLeave,
  onFormula,
  onOff,
  onRemove,
  onRetry,
}: {
  item: Item;
  label: string;
  toggle?: boolean;
  disabled: boolean;
  busy: boolean;
  onText: (t: string) => void;
  onLeave: () => void;
  onFormula: (f: string) => void;
  onOff: (off: boolean) => void;
  onRemove: () => void;
  onRetry: () => void;
}) {
  const [showFormula, setShowFormula] = useState(false);
  return (
    <div className={"border-b border-line py-2 last:border-b-0 " + (x.off ? "opacity-60" : "")}>
      <div className="flex items-center gap-2">
        <span className="w-14 flex-none text-xs text-muted">
          {toggle ? (
            <label className="inline-flex cursor-pointer items-center gap-1.5" title={x.off ? "Đang tắt" : "Đang bật"}>
              <input type="checkbox" checked={!x.off} disabled={disabled} onChange={(e) => onOff(!e.target.checked)} />
              {label}
            </label>
          ) : (
            label
          )}
        </span>
        <input
          className="input h-9 min-w-0 flex-1"
          value={x.text}
          disabled={disabled}
          aria-label={label}
          onChange={(e) => onText(e.target.value)}
          onBlur={onLeave}
          onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
        />
        <Status busy={busy} item={x} onRetry={disabled ? undefined : onRetry} />
        {!disabled && (
          <button className="btn-icon btn-sm" tabIndex={-1} onClick={onRemove} aria-label={`Xoá ${label}`} title="Xoá">
            <Icon name="close" size={18} />
          </button>
        )}
      </div>
      {(x.explanation || x.formula) && (
        <div className="mt-1 flex flex-wrap items-baseline gap-x-2 pl-16 text-xs text-muted">
          {x.explanation && <span>{x.explanation}</span>}
          {x.formula !== undefined && (
            <button className="text-accent hover:underline" onClick={() => setShowFormula((v) => !v)}>
              {showFormula ? "Ẩn công thức" : "Công thức"}
            </button>
          )}
        </div>
      )}
      {showFormula && (
        <div className="mt-1 pl-16">
          <input
            className="input h-9 w-full font-mono text-xs"
            value={x.formula ?? ""}
            disabled={disabled}
            placeholder="Để trống = AI đánh giá"
            aria-label={`Công thức của ${label}`}
            onChange={(e) => onFormula(e.target.value)}
          />
        </div>
      )}
    </div>
  );
}

function Status({ busy, item: x, onRetry }: { busy: boolean; item: Item; onRetry?: () => void }) {
  if (busy)
    return (
      <span className="badge h-7 gap-1.5 px-2">
        <Spinner className="size-3.5 border-2" /> Đang hiểu…
      </span>
    );
  if (x.formula)
    return (
      <span className="badge badge-ok h-7 px-2" title="Máy tự kiểm bằng công thức, không tốn AI khi chạy">
        <Icon name="check" size={14} /> Máy tự kiểm
      </span>
    );
  if (x.ai)
    return (
      <span className="badge badge-vlm h-7 px-2" title="AI đọc thông tin các chứng từ và đánh giá mỗi lần chạy">
        <Icon name="auto_awesome" size={14} /> AI đánh giá
      </span>
    );
  return (
    <button className="badge h-7 px-2 hover:bg-fg/8" disabled={!onRetry} onClick={onRetry} title="Bấm để hệ thống hiểu câu này">
      <Icon name="translate" size={14} /> Chưa hiểu
    </button>
  );
}
