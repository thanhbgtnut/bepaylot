import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";

import { deleteSession, getCase, listMessages, listSessions } from "../../api/endpoints";
import type { Case, SessionBrief } from "../../api/types";
import { useApp } from "../../components/AppContext";
import { CasePicker } from "../../components/CasePicker";
import { CitedText, Markdown } from "../../components/Markdown";
import { useToast } from "../../components/toast";
import { Icon, Loading, Menu, Spinner } from "../../components/ui";
import { fmtDate } from "../../lib/format";
import { argsLine, fromTranscript, pretty, type ChatItem, type TurnBlock } from "./model";
import { useChatStream } from "./useChatStream";

const SUGGEST: [string, string][] = [
  ["list_alt", "Liệt kê các tài liệu trong phạm vi, mỗi tài liệu gồm những loại giấy tờ nào và ở trang nào."],
  ["gavel", "Xem tổng quan từng trang, tìm các trang là hợp đồng rồi cho biết giá trị hợp đồng và các bên."],
  ["badge", "Số định danh cá nhân của người đứng tên là gì? Trích dẫn đúng trang."],
  ["manage_search", "Tìm trong trang 7–12 các điều khoản về thời hạn thanh toán."],
];

export function ChatPage() {
  const { sessionId = null } = useParams();
  const nav = useNavigate();
  const { kcase, cases } = useApp();
  const { fail } = useToast();
  const [sessions, setSessions] = useState<SessionBrief[]>([]);
  const [showHistory, setShowHistory] = useState(true);
  // A session is bound to one case for good; a new one takes the case
  // picked in the scope bar.
  const [sessCase, setSessCase] = useState<Case | null>(null);
  const [loadingHistory, setLoadingHistory] = useState(false);
  const [input, setInput] = useState("");
  const selfNav = useRef<string | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const textarea = useRef<HTMLTextAreaElement>(null);
  const stick = useRef(true);

  const loadSessions = useCallback(() => listSessions().then(setSessions, fail), [fail]);
  useEffect(() => {
    loadSessions();
  }, [loadSessions]);

  const onSession = useCallback(
    (id: string, created: boolean) => {
      if (created) {
        selfNav.current = id;
        nav(`/chat/${id}`, { replace: true });
      }
    },
    [nav],
  );
  const { items, setItems, busy, send, stop } = useChatStream(onSession);

  useEffect(() => {
    if (!sessionId) {
      setItems([]);
      return;
    }
    if (selfNav.current === sessionId) {
      selfNav.current = null; // already streaming into it
      return;
    }
    let alive = true;
    setLoadingHistory(true);
    listMessages(sessionId)
      .then((m) => alive && setItems(fromTranscript(m)))
      .catch((e) => alive && fail(e))
      .finally(() => alive && setLoadingHistory(false));
    return () => {
      alive = false;
    };
  }, [sessionId]); // eslint-disable-line react-hooks/exhaustive-deps

  // The case of the open session.
  const sessCaseId = sessions.find((x) => x.id === sessionId)?.case_id ?? "";
  useEffect(() => {
    if (!sessCaseId) return setSessCase(null);
    const known = cases?.find((c) => c.id === sessCaseId);
    if (known) return setSessCase(known);
    let alive = true;
    getCase(sessCaseId).then(
      (c) => alive && setSessCase(c),
      () => alive && setSessCase(null),
    );
    return () => {
      alive = false;
    };
  }, [sessCaseId, cases]);
  const scopeCase = sessionId && sessCaseId ? sessCase : kcase;

  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [items]);

  const submit = async (text = input) => {
    const t = text.trim();
    if (!t || busy) return;
    if (!scopeCase) return fail(new Error("Chọn hồ sơ trước khi hỏi: agent chỉ tra cứu trong một hồ sơ"));
    setInput("");
    if (textarea.current) textarea.current.style.height = "auto";
    stick.current = true;
    await send(t, { sessionId, caseId: scopeCase.id });
    loadSessions();
  };

  return (
    <div className="flex min-h-0 flex-1">
      {/* history */}
      {showHistory && (
        <aside className="hidden w-72 flex-none flex-col border-r border-line md:flex">
          <div className="flex items-center gap-1 px-3 pt-4 pb-2">
            <button className="btn btn-tonal flex-1 justify-start" disabled={busy} onClick={() => nav("/chat")}>
              <Icon name="edit_square" size={20} /> Cuộc trò chuyện mới
            </button>
            <button className="btn-icon" title="Ẩn lịch sử" onClick={() => setShowHistory(false)}>
              <Icon name="left_panel_close" />
            </button>
          </div>
          <div className="px-6 pt-3 pb-1 text-xs font-medium text-muted">Gần đây</div>
          <div className="min-h-0 flex-1 overflow-auto px-3 pb-3">
            {!sessions.length && <div className="px-3 py-2 text-sm text-subtle">Chưa có cuộc trò chuyện</div>}
            {sessions.map((s) => {
              const on = s.id === sessionId;
              return (
                <div
                  key={s.id}
                  onClick={() => !busy && nav(`/chat/${s.id}`)}
                  className={"group flex h-10 cursor-pointer items-center gap-3 rounded-full pr-1 pl-4 " + (on ? "bg-accent-soft text-on-accent-soft" : "hover:bg-fg/8")}
                  title={`${s.title || "Cuộc trò chuyện"} · ${fmtDate(s.updated_at)}`}
                >
                  <Icon name="chat_bubble" size={18} className={on ? "" : "text-muted"} />
                  <span className="min-w-0 flex-1 truncate text-sm">{s.title || "Cuộc trò chuyện"}</span>
                  <span onClick={(e) => e.stopPropagation()}>
                    <Menu
                      align="right"
                      trigger={(open, isOpen) => (
                        <button className={"btn-icon btn-sm " + (isOpen || on ? "" : "opacity-0 group-hover:opacity-100")} onClick={open}>
                          <Icon name="more_vert" size={18} />
                        </button>
                      )}
                      items={[
                        {
                          icon: "delete",
                          label: "Xoá",
                          danger: true,
                          onClick: async () => {
                            if (!confirm("Xoá cuộc trò chuyện này?")) return;
                            try {
                              await deleteSession(s.id);
                              if (on) nav("/chat");
                              loadSessions();
                            } catch (err) {
                              fail(err);
                            }
                          },
                        },
                      ]}
                    />
                  </span>
                </div>
              );
            })}
          </div>
        </aside>
      )}

      <section className="flex min-h-0 min-w-0 flex-1 flex-col">
        {/* scope */}
        <div className="flex flex-none flex-wrap items-center gap-2 px-4 pt-3 pb-2">
          {!showHistory && (
            <button className="btn-icon max-md:hidden" title="Hiện lịch sử" onClick={() => setShowHistory(true)}>
              <Icon name="left_panel_open" />
            </button>
          )}
          <CasePicker value={scopeCase} disabled={!!(sessionId && sessCaseId)} />
          <span className="text-xs text-muted">
            <Icon name="lock" size={14} className="align-[-2px]" />{" "}
            {scopeCase ? "Agent chỉ đọc tài liệu của hồ sơ này" : "Chọn hồ sơ để bắt đầu hỏi"}
          </span>
        </div>

        {/* conversation */}
        <div
          ref={scroller}
          className="min-h-0 flex-1 overflow-auto"
          onScroll={(e) => {
            const el = e.currentTarget;
            stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 160;
          }}
        >
          <div className="mx-auto flex max-w-200 flex-col gap-8 px-6 pt-4 pb-8">
            {loadingHistory && <Loading />}
            {!loadingHistory && !items.length && (
              <div className="pt-[8vh]">
                <h2 className="gradient-text text-[44px] leading-13 font-medium tracking-tight">Xin chào</h2>
                <p className="mt-1 text-[32px] leading-10 text-subtle">Bạn cần tìm gì trong tài liệu?</p>
                <div className="mt-10 grid gap-3 sm:grid-cols-2">
                  {SUGGEST.map(([icon, s]) => (
                    <button
                      key={s}
                      className="flex min-h-30 flex-col justify-between rounded-2xl bg-surface-2 p-4 text-left text-sm text-fg transition-colors hover:bg-surface-3"
                      onClick={() => {
                        setInput(s);
                        textarea.current?.focus();
                      }}
                    >
                      {s}
                      <span className="grid size-9 place-items-center self-end rounded-full bg-surface">
                        <Icon name={icon} size={20} className="text-muted" />
                      </span>
                    </button>
                  ))}
                </div>
              </div>
            )}
            {items.map((it) => (it.role === "user" ? <UserBubble key={it.key} text={it.text} /> : <AssistantTurn key={it.key} item={it} />))}
          </div>
        </div>

        {/* composer */}
        <div className="flex-none px-4 pb-4">
          <div className="mx-auto flex max-w-200 items-end gap-2 rounded-[28px] bg-surface-2 py-2 pr-2 pl-6 focus-within:bg-surface-3">
            <textarea
              ref={textarea}
              rows={1}
              value={input}
              onChange={(e) => {
                setInput(e.target.value);
                e.target.style.height = "auto";
                e.target.style.height = Math.min(220, e.target.scrollHeight) + "px";
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                  e.preventDefault();
                  submit();
                }
              }}
              placeholder="Hỏi về tài liệu…"
              className="max-h-55 min-h-10 flex-1 resize-none bg-transparent py-2.5 text-base text-fg outline-none placeholder:text-muted"
            />
            {busy ? (
              <button className="btn-icon size-12 bg-fg/8" onClick={stop} title="Dừng">
                <Icon name="stop" fill />
              </button>
            ) : (
              <button className={"btn-icon size-12 " + (input.trim() ? "bg-accent text-on-accent hover:bg-accent" : "")} onClick={() => submit()} disabled={!input.trim()} title="Gửi">
                <Icon name="send" fill />
              </button>
            )}
          </div>
          <p className="mx-auto mt-2 max-w-200 text-center text-xs text-subtle">
            Agent duyệt mục lục của hồ sơ, chỉ đọc đúng các trang cần thiết rồi trả lời. Bấm vào nhãn trích dẫn để xem vùng trên trang gốc.
          </p>
        </div>
      </section>
    </div>
  );
}

export function UserBubble({ text }: { text: string }) {
  return <div className="max-w-[80%] self-end rounded-3xl rounded-tr-md bg-surface-2 px-5 py-3 text-[15px] leading-6 wrap-break-word whitespace-pre-wrap">{text}</div>;
}

export function AssistantTurn({ item }: { item: Extract<ChatItem, { role: "assistant" }> }) {
  // Consecutive tool calls and thoughts are grouped under one expandable row.
  const groups: (TurnBlock | TurnBlock[])[] = [];
  for (const b of item.blocks) {
    const work = b.kind === "tool" || b.kind === "thinking";
    const last = groups[groups.length - 1];
    if (work && Array.isArray(last)) last.push(b);
    else groups.push(work ? [b] : b);
  }
  const working = item.pending || item.blocks.some((b) => b.kind === "tool" && b.state === "running");
  return (
    <div className="flex gap-4">
      <div className={"grid size-8 flex-none place-items-center rounded-full " + (working ? "animate-pulse" : "")}>
        <svg viewBox="0 0 24 24" className="size-7" aria-hidden>
          <defs>
            <linearGradient id="spark" x1="0" y1="0" x2="1" y2="1">
              <stop offset="0" stopColor="#4285f4" />
              <stop offset=".55" stopColor="#9b72cb" />
              <stop offset="1" stopColor="#d96570" />
            </linearGradient>
          </defs>
          <path d="M12 2c.6 4.8 3.2 8.4 10 10-6.8 1.6-9.4 5.2-10 10-.6-4.8-3.2-8.4-10-10 6.8-1.6 9.4-5.2 10-10z" fill="url(#spark)" />
        </svg>
      </div>
      <div className="flex min-w-0 flex-1 flex-col gap-3 pt-1">
        {item.pending && (
          <div className="flex items-center gap-2 text-sm text-muted">
            <Spinner className="size-4 border-2" /> Đang đọc tài liệu…
          </div>
        )}
        {groups.map((g, i) => (Array.isArray(g) ? <WorkGroup key={i} blocks={g} /> : <Block key={i} b={g} />))}
        {item.stopReason === "max_tokens" && <div className="text-xs text-warn">Câu trả lời bị cắt do giới hạn độ dài.</div>}
        {item.stopReason === "interrupted" && <div className="text-xs text-muted">Đã dừng.</div>}
      </div>
    </div>
  );
}

function WorkGroup({ blocks }: { blocks: TurnBlock[] }) {
  const tools = blocks.filter((b): b is Extract<TurnBlock, { kind: "tool" }> => b.kind === "tool");
  const running = tools.some((t) => t.state === "running");
  const failed = tools.some((t) => t.state === "error");
  const names = [...new Set(tools.map((t) => t.name))];
  return (
    <details className="group/w rounded-2xl border border-line">
      <summary className="flex h-11 cursor-pointer list-none items-center gap-2 rounded-2xl px-4 text-sm text-muted hover:bg-fg/4 [&::-webkit-details-marker]:hidden">
        {running ? <Spinner className="size-4 border-2" /> : <Icon name={failed ? "error" : "construction"} size={18} className={failed ? "text-err" : ""} />}
        <span className="min-w-0 flex-1 truncate">
          {tools.length ? `${running ? "Đang dùng" : "Đã dùng"} ${tools.length} công cụ · ${names.join(", ")}` : "Suy nghĩ"}
        </span>
        <Icon name="expand_more" size={20} className="transition-transform group-open/w:rotate-180" />
      </summary>
      <div className="flex flex-col gap-2 border-t border-line p-3">
        {blocks.map((b, i) => (
          <Block key={i} b={b} />
        ))}
      </div>
    </details>
  );
}

function Block({ b }: { b: TurnBlock }) {
  switch (b.kind) {
    case "text":
      return b.text || b.streaming ? <Markdown text={b.text} streaming={b.streaming} className="text-[15px]" /> : null;
    case "thinking":
      return b.text ? <div className="rounded-xl bg-surface-2 px-4 py-3 text-[13px] whitespace-pre-wrap text-muted italic">{b.text}</div> : null;
    case "tool":
      return <ToolCard b={b} />;
    case "notice":
      return <div className={"self-start rounded-lg px-3 py-1.5 text-[13px] " + (b.tone === "err" ? "bg-err-soft text-err" : "bg-surface-2 text-muted")}>{b.text}</div>;
  }
}

function ToolCard({ b }: { b: Extract<TurnBlock, { kind: "tool" }> }) {
  const result = pretty(b.result);
  return (
    <details className="rounded-xl bg-surface-2 text-[13px]">
      <summary className="flex h-10 cursor-pointer list-none items-center gap-2 px-3 [&::-webkit-details-marker]:hidden">
        <Icon name={b.state === "error" ? "error" : b.state === "running" ? "pending" : "check_circle"} size={18} className={b.state === "error" ? "text-err" : b.state === "done" ? "text-ok" : "text-muted"} />
        <b className="font-mono font-medium">{b.name}</b>
        <span className="min-w-0 flex-1 truncate font-mono text-muted">{argsLine(b.input)}</span>
      </summary>
      <pre className="max-h-75 overflow-auto border-t border-line px-3 py-2.5 font-mono text-xs wrap-break-word whitespace-pre-wrap text-muted">{pretty(b.input)}</pre>
      {result && (
        <pre className={"max-h-75 overflow-auto border-t border-line px-3 py-2.5 font-mono text-xs wrap-break-word whitespace-pre-wrap " + (b.state === "error" ? "text-err" : "")}>
          <CitedText text={result} />
        </pre>
      )}
    </details>
  );
}
