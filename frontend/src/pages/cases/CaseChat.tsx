import { useEffect, useLayoutEffect, useRef, useState } from "react";

import { createSession, listMessages, listTemplates, setSessionTemplate } from "../../api/endpoints";
import { canEditPrompts, type Case, type CaseTOC, type PromptTemplate } from "../../api/types";
import { useAuth } from "../../components/AuthContext";
import { useToast } from "../../components/toast";
import { Icon, Loading } from "../../components/ui";
import { AssistantTurn, UserBubble } from "../chat/ChatPage";
import { fromTranscript } from "../chat/model";
import { useChatStream } from "../chat/useChatStream";
import { ChatConfigDialog, type ChatConfig } from "./dialogs";

// Per-browser memory of the case's conversation and of the prompt chosen for
// it (§7.2): reopening the case continues where it was.
const mem = {
  get(k: string) {
    try {
      return localStorage.getItem("bp." + k) ?? "";
    } catch {
      return "";
    }
  },
  set(k: string, v: string) {
    try {
      if (v) localStorage.setItem("bp." + k, v);
      else localStorage.removeItem("bp." + k);
    } catch {
      /* private mode */
    }
  },
};

const SUGGEST = ["Tóm tắt phương án: nhu cầu vốn, thời hạn và tài sản bảo đảm.", "Khả năng trả nợ dựa trên BCTC gần nhất ra sao?", "Số liệu giữa các file có chỗ nào lệch nhau không?"];

// The chat column of the case page: the agent session bound to the case,
// with the prompt template (or, for prompt editors, a custom prompt) chosen
// in the NotebookLM-style "Cấu hình cuộc trò chuyện" dialog (§7.7).
export function CaseChat({ kcase, toc }: { kcase: Case; toc: CaseTOC }) {
  const { user } = useAuth();
  const { fail } = useToast();
  const editor = canEditPrompts(user);
  const [sessionId, setSessionId] = useState(() => mem.get("caseSession." + kcase.id) || null);
  const [config, setConfig] = useState<ChatConfig>(() => {
    try {
      return JSON.parse(mem.get("caseChat." + kcase.id) || "null") ?? { templateId: "" };
    } catch {
      return { templateId: "" };
    }
  });
  const [templates, setTemplates] = useState<PromptTemplate[]>([]);
  const [showConfig, setShowConfig] = useState(false);
  const [loading, setLoading] = useState(false);
  const [input, setInput] = useState("");
  // The template the session has; unknown for a session reopened from an
  // earlier visit, so the first message sets it.
  const sessionTemplate = useRef<string | null>(sessionId ? null : "");
  const scroller = useRef<HTMLDivElement>(null);
  const textarea = useRef<HTMLTextAreaElement>(null);

  const { items, setItems, busy, send, stop } = useChatStream((id) => {
    setSessionId(id);
    mem.set("caseSession." + kcase.id, id);
  });

  useEffect(() => {
    listTemplates("chat", kcase.case_type).then(setTemplates, () => {});
  }, [kcase.case_type]);
  useEffect(() => mem.set("caseChat." + kcase.id, JSON.stringify(config)), [config, kcase.id]);

  // Load the conversation of the case once.
  useEffect(() => {
    if (!sessionId) return;
    let alive = true;
    setLoading(true);
    listMessages(sessionId)
      .then((m) => alive && setItems(fromTranscript(m)))
      .catch(() => {
        // The session is gone: start a new one on the next message.
        mem.set("caseSession." + kcase.id, "");
        if (alive) setSessionId(null);
      })
      .finally(() => alive && setLoading(false));
    return () => {
      alive = false;
    };
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  useLayoutEffect(() => {
    const el = scroller.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [items]);

  const tpl = templates.find((t) => t.id === config.templateId);
  const label = config.custom && editor ? "Prompt tuỳ chỉnh" : tpl ? "Mẫu: " + tpl.name : "Mặc định";

  const submit = async (text = input) => {
    const t = text.trim();
    if (!t || busy) return;
    setInput("");
    if (textarea.current) textarea.current.style.height = "auto";
    try {
      // The template lives on the session (§8.4); create or update it first.
      let sid = sessionId;
      const want = config.custom ? "" : config.templateId;
      if (!sid) {
        const s = await createSession({ case_id: kcase.id, template_id: want || undefined, title: t.slice(0, 60) });
        sid = s.id;
        sessionTemplate.current = want;
        setSessionId(sid);
        mem.set("caseSession." + kcase.id, sid);
      } else if (sessionTemplate.current !== want) {
        await setSessionTemplate(sid, want);
        sessionTemplate.current = want;
      }
      await send(t, { sessionId: sid, caseId: kcase.id, system: editor && config.custom ? config.custom : undefined });
    } catch (e) {
      fail(e);
    }
  };

  const newChat = () => {
    mem.set("caseSession." + kcase.id, "");
    setSessionId(null);
    setItems([]);
    sessionTemplate.current = "";
  };

  const pages = toc.documents.reduce((a, d) => a + (d.page_count || 0), 0);
  const summary = toc.documents
    .map((d) => d.summary)
    .filter(Boolean)
    .slice(0, 3)
    .join(" ");

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div ref={scroller} className="min-h-0 flex-1 overflow-auto">
        <div className="mx-auto flex max-w-200 flex-col gap-6 px-6 pt-5 pb-6">
          <div className="rounded-2xl bg-surface-2 p-5">
            <div className="flex items-center gap-2 text-xs text-muted">
              <Icon name="folder_open" size={16} /> {kcase.code} · {toc.documents.length} nguồn · {pages} trang
              <span className="flex-1" />
              {items.length > 0 && (
                <button className="btn btn-text btn-sm -my-1" onClick={newChat} disabled={busy}>
                  <Icon name="edit_square" size={16} /> Cuộc trò chuyện mới
                </button>
              )}
            </div>
            <h2 className="mt-2 text-[22px] leading-8 text-fg">{kcase.title || kcase.code}</h2>
            {summary && <p className="mt-2 line-clamp-4 text-[15px] leading-6 text-fg">{summary}</p>}
            {!items.length && (
              <div className="mt-3 flex flex-wrap gap-2">
                {SUGGEST.map((q) => (
                  <button key={q} className="chip h-auto py-1.5 text-left whitespace-normal" onClick={() => submit(q)}>
                    {q}
                  </button>
                ))}
              </div>
            )}
          </div>
          {loading && <Loading />}
          {items.map((it) => (it.role === "user" ? <UserBubble key={it.key} text={it.text} /> : <AssistantTurn key={it.key} item={it} />))}
        </div>
      </div>
      <div className="flex-none px-4 pb-4">
        <div className="mx-auto mb-2 flex max-w-200 items-center gap-2">
          <button className="chip max-w-80" onClick={() => setShowConfig(true)} title="Cấu hình cuộc trò chuyện">
            <Icon name="tune" size={18} />
            <span className="truncate">{label}</span>
            <Icon name="arrow_drop_down" size={18} className="-mr-1" />
          </button>
          {!editor && (
            <span className="text-xs text-subtle">
              <Icon name="lock" size={14} className="align-[-2px]" /> dùng mẫu có sẵn
            </span>
          )}
        </div>
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
            placeholder="Hỏi về phương án này…"
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
        <p className="mx-auto mt-2 max-w-200 text-center text-xs text-subtle">Agent chỉ đọc các file của hồ sơ này. Bấm nhãn trích dẫn để xem vùng trên trang gốc.</p>
      </div>
      {showConfig && (
        <ChatConfigDialog
          templates={templates}
          caseType={kcase.case_type}
          value={config}
          onClose={() => setShowConfig(false)}
          onSave={(c, list) => {
            if (list) setTemplates(list);
            setConfig(c);
            setShowConfig(false);
          }}
        />
      )}
    </div>
  );
}
