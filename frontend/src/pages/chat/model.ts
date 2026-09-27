import type { OutputBlock, TranscriptMessage } from "../../api/types";

// UI model of a conversation: user bubbles and assistant turns made of
// text / thinking / tool blocks in the order they were produced.

export type ToolState = "running" | "done" | "error" | "unknown";

export type TurnBlock =
  | { kind: "text"; text: string; streaming?: boolean }
  | { kind: "thinking"; text: string }
  | { kind: "tool"; id: string; name: string; input: unknown; result?: unknown; state: ToolState }
  | { kind: "notice"; text: string; tone?: "err" | "" };

export type ChatItem =
  | { role: "user"; key: string; text: string }
  | { role: "assistant"; key: string; blocks: TurnBlock[]; stopReason?: string; pending?: boolean };

function contentText(c: TranscriptMessage["content"]): string {
  if (typeof c === "string") return c;
  return c
    .filter((b) => b.type === "text")
    .map((b) => b.text ?? "")
    .join("\n");
}

// Rebuilds the UI model from a stored transcript. Tool results arrive in the
// following user message (tool_result blocks) and are attached to their call.
export function fromTranscript(msgs: TranscriptMessage[]): ChatItem[] {
  const items: ChatItem[] = [];
  const tools = new Map<string, Extract<TurnBlock, { kind: "tool" }>>();
  let cur: Extract<ChatItem, { role: "assistant" }> | null = null;
  for (const m of msgs) {
    const blocks: OutputBlock[] = Array.isArray(m.content) ? m.content : [{ type: "text", text: m.content }];
    if (m.role === "user") {
      for (const b of blocks) {
        if (b.type !== "tool_result" || !b.tool_use_id) continue;
        const t = tools.get(b.tool_use_id);
        if (t) {
          t.result = b.content;
          t.state = b.is_error ? "error" : "done";
        }
      }
      const text = contentText(m.content).trim();
      if (text) {
        items.push({ role: "user", key: m.id, text });
        cur = null;
      }
      continue;
    }
    if (!cur) {
      cur = { role: "assistant", key: m.id, blocks: [] };
      items.push(cur);
    }
    for (const b of blocks) {
      if (b.type === "text" && b.text) cur.blocks.push({ kind: "text", text: b.text });
      else if (b.type === "thinking" && b.thinking) cur.blocks.push({ kind: "thinking", text: b.thinking });
      else if (b.type === "tool_use" && b.id) {
        const t: Extract<TurnBlock, { kind: "tool" }> = { kind: "tool", id: b.id, name: b.name ?? "", input: b.input, state: "unknown" };
        tools.set(b.id, t);
        cur.blocks.push(t);
      } else if (b.type === "tool_result" && b.tool_use_id) {
        const t = tools.get(b.tool_use_id);
        if (t) {
          t.result = b.content;
          t.state = b.is_error ? "error" : "done";
        }
      }
    }
    if (m.stop_reason) cur.stopReason = m.stop_reason;
  }
  return items;
}

// Pretty-prints a tool input/result (JSON strings are parsed) for display.
export function pretty(x: unknown, max = 8000): string {
  if (x == null || x === "") return "";
  let v = x;
  if (typeof x === "string") {
    try {
      v = JSON.parse(x);
    } catch {
      return x.length > max ? x.slice(0, max) + "\n…" : x;
    }
  }
  if (Array.isArray(v) && v.every((b) => b && typeof b === "object" && "text" in b)) {
    return pretty(v.map((b) => (b as { text: string }).text).join("\n"), max);
  }
  const s = JSON.stringify(v, null, 2);
  return s.length > max ? s.slice(0, max) + "\n…" : s;
}

export function argsLine(input: unknown): string {
  if (input == null || input === "") return "";
  let o = input;
  if (typeof o === "string") {
    try {
      o = JSON.parse(o);
    } catch {
      return o as string;
    }
  }
  if (typeof o !== "object") return String(o);
  return Object.entries(o as Record<string, unknown>)
    .map(([k, v]) => `${k}=${typeof v === "object" ? JSON.stringify(v) : v}`)
    .join("  ");
}
