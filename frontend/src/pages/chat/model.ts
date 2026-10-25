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

// The name of an assistant step in plain words (U101): what it did and on
// which pages, never the tool name or its JSON arguments.
const STEP: Record<string, string> = {
  kb_case_toc: "Xem danh sách tài liệu của hồ sơ",
  kb_list_documents: "Xem danh sách tài liệu",
  kb_document_tree: "Xem mục lục tài liệu",
  kb_page_overview: "Xem tổng quan các trang",
  kb_read_pages: "Đọc trang",
  kb_search: "Tìm trong hồ sơ",
  kb_find_in_document: "Tìm trong tài liệu",
  kb_locate: "Tìm vị trí trên trang",
  kb_get_fields: "Xem thông tin đã lấy",
  kb_save_fields: "Lưu thông tin đã lấy",
  kb_metadata_values: "Xem thông tin thêm",
  calculate: "Tính toán",
  current_time: "Xem ngày giờ",
  web_search: "Tìm trên web",
  http_fetch: "Đọc trang web",
  load_skill: "Dùng kỹ năng",
};

export function stepLabel(name: string, input: unknown): string {
  let o: Record<string, unknown> = {};
  if (typeof input === "string") {
    try {
      o = JSON.parse(input) ?? {};
    } catch {
      /* not JSON */
    }
  } else if (input && typeof input === "object") o = input as Record<string, unknown>;
  const parts = [STEP[name] ?? (name.startsWith("mcp__") ? "Dùng công cụ ngoài" : "Tra cứu")];
  if (typeof o.query === "string" && o.query) parts.push(`“${o.query}”`);
  const from = Number(o.page_from) || 0, to = Number(o.page_to) || 0;
  if (from) parts.push(to && to !== from ? `trang ${from}–${to}` : `trang ${from}`);
  else if (Array.isArray(o.pages) && o.pages.length) parts.push("trang " + o.pages.join(", "));
  return parts.join(" · ");
}
