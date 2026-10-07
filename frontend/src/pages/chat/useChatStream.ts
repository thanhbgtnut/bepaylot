import { useCallback, useRef, useState } from "react";

import { readSSE } from "../../api/client";
import { streamMessage } from "../../api/endpoints";
import type { ChatItem, TurnBlock } from "./model";

type Assistant = Extract<ChatItem, { role: "assistant" }>;

let keySeq = 0;

// Streams one agent turn from POST /v1/messages and keeps the chat items
// up to date. The assistant turn is rebuilt immutably on every event, with
// text deltas batched per animation frame.
export function useChatStream(onSession: (id: string, created: boolean) => void) {
  const [items, setItems] = useState<ChatItem[]>([]);
  const [busy, setBusy] = useState(false);
  const abort = useRef<AbortController | null>(null);

  const send = useCallback(
    async (text: string, opt: { sessionId: string | null; caseId: string | null; system?: string }) => {
      const turn: Assistant = { role: "assistant", key: "a" + ++keySeq, blocks: [], pending: true };
      // Mutable working copy; published to React state via flush().
      const blocks: TurnBlock[] = [];
      const byIndex = new Map<number, TurnBlock>();
      const byTool = new Map<string, Extract<TurnBlock, { kind: "tool" }>>();
      const json = new Map<number, string>();
      let stopReason = "";
      let frame = 0;
      const flush = (final = false) => {
        cancelAnimationFrame(frame);
        frame = 0;
        const snapshot: Assistant = {
          ...turn,
          blocks: blocks.map((b) => ({ ...b })),
          pending: !final && blocks.length === 0,
          stopReason: final ? stopReason : undefined,
        };
        setItems((xs) => xs.map((x) => (x.key === turn.key ? snapshot : x)));
      };
      const schedule = () => {
        if (!frame) frame = requestAnimationFrame(() => flush());
      };

      setItems((xs) => [...xs, { role: "user", key: "u" + ++keySeq, text }, turn]);
      setBusy(true);
      abort.current = new AbortController();
      let created = !opt.sessionId;
      let sessionId = opt.sessionId;
      try {
        const res = await streamMessage({ text, sessionId: opt.sessionId, caseId: opt.caseId, system: opt.system, signal: abort.current.signal });
        await readSSE(res, (ev, d) => {
          switch (ev) {
            case "message_start": {
              const sid = d?.message?.session_id;
              if (sid && sid !== sessionId) {
                created = created || !sessionId;
                sessionId = sid;
                onSession(sid, created);
              }
              break;
            }
            case "content_block_start": {
              const b = d.content_block ?? {};
              let blk: TurnBlock | null = null;
              if (b.type === "text") blk = { kind: "text", text: "", streaming: true };
              else if (b.type === "thinking") blk = { kind: "thinking", text: "" };
              else if (b.type === "tool_use") {
                const t: Extract<TurnBlock, { kind: "tool" }> = { kind: "tool", id: b.id, name: b.name, input: "", state: "running" };
                byTool.set(b.id, t);
                blk = t;
              }
              if (blk) {
                blocks.push(blk);
                byIndex.set(d.index, blk);
              }
              break;
            }
            case "content_block_delta": {
              const blk = byIndex.get(d.index);
              const dl = d.delta ?? {};
              if (!blk) break;
              if (dl.type === "text_delta" && blk.kind === "text") blk.text += dl.text;
              else if (dl.type === "thinking_delta" && blk.kind === "thinking") blk.text += dl.thinking;
              else if (dl.type === "input_json_delta" && blk.kind === "tool") {
                const s = (json.get(d.index) ?? "") + dl.partial_json;
                json.set(d.index, s);
                blk.input = s;
              }
              break;
            }
            case "content_block_stop": {
              const blk = byIndex.get(d.index);
              if (blk?.kind === "text") blk.streaming = false;
              break;
            }
            case "tool_execution_start": {
              let t = byTool.get(d.tool_use_id);
              if (!t) {
                t = { kind: "tool", id: d.tool_use_id, name: d.name, input: d.input, state: "running" };
                byTool.set(d.tool_use_id, t);
                blocks.push(t);
              } else if (d.input) t.input = d.input;
              break;
            }
            case "tool_execution_stop": {
              const t = byTool.get(d.tool_use_id);
              if (t) {
                t.result = d.result;
                t.state = d.is_error ? "error" : "done";
              }
              break;
            }
            case "message_delta":
              stopReason = d?.delta?.stop_reason || stopReason;
              break;
            case "steered":
              blocks.push({ kind: "notice", text: "Phiên đang chạy một lượt khác — tin nhắn đã được chuyển vào lượt đó." });
              break;
            case "error":
              blocks.push({ kind: "notice", tone: "err", text: d?.error?.message ?? JSON.stringify(d) });
              break;
          }
          schedule();
        });
      } catch (e) {
        if ((e as Error).name === "AbortError") blocks.push({ kind: "notice", text: "Đã ngắt stream (agent có thể vẫn chạy tiếp ở server)." });
        else blocks.push({ kind: "notice", tone: "err", text: (e as Error).message });
      } finally {
        for (const b of blocks) {
          if (b.kind === "text") b.streaming = false;
          if (b.kind === "tool" && b.state === "running") b.state = "unknown";
        }
        flush(true);
        setBusy(false);
        abort.current = null;
      }
      return sessionId;
    },
    [onSession],
  );

  const stop = useCallback(() => abort.current?.abort(), []);
  return { items, setItems, busy, send, stop };
}
