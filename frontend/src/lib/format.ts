import type { DocStatus } from "../api/types";

export function fmtDate(s?: string) {
  if (!s) return "";
  return new Date(s).toLocaleString("vi-VN", { day: "2-digit", month: "2-digit", year: "numeric", hour: "2-digit", minute: "2-digit" });
}

export function fmtSize(n?: number) {
  if (!n) return "";
  const u = ["B", "KB", "MB", "GB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(i ? 1 : 0)} ${u[i]}`;
}

export type Tone = "" | "ok" | "warn" | "err" | "info" | "vlm";

export const STATUS: Record<DocStatus, [string, Tone]> = {
  queued: ["Đang chờ", "info"],
  splitting: ["Tách trang", "info"],
  parsing: ["Đang OCR", "info"],
  assembling: ["Đang ghép", "info"],
  indexing: ["Lập chỉ mục", "info"],
  completed: ["Hoàn tất", "ok"],
  partial: ["Một phần", "warn"],
  failed: ["Lỗi", "err"],
  cancelled: ["Đã huỷ", ""],
  deleting: ["Đang xoá", "warn"],
};

export const TERMINAL = new Set<string>(["completed", "partial", "failed", "cancelled", "deleting"]);
export const isBusy = (s: string) => !TERMINAL.has(s);

export const SOURCE: Record<string, [string, Tone]> = {
  vlm: ["VLM", "vlm"],
  ocr: ["OCR", "info"],
  merged: ["OCR+layer", "ok"],
  layer: ["Text layer", "ok"],
  layer_only: ["Text layer", "ok"],
};

export function metaValue(v: unknown) {
  return typeof v === "object" && v !== null ? JSON.stringify(v) : String(v);
}

// "group_code=G1, loai=GCN" -> {group_code: "G1", loai: "GCN"}
export function parseKeyValues(s: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const part of s.split(",")) {
    const [k, ...rest] = part.split("=");
    if (k.trim() && rest.length) out[k.trim()] = rest.join("=").trim();
  }
  return out;
}

// Accent-insensitive folding for Vietnamese.
export const fold = (s: string) =>
  s.normalize("NFD").replace(/[̀-ͯ]/g, "").replace(/đ/g, "d").replace(/Đ/g, "D").toLowerCase();

// Splits text around the first accent-insensitive match of q.
export function splitMatch(text: string, q: string): [string, string, string] | null {
  if (!q) return null;
  const i = fold(text).indexOf(fold(q));
  if (i < 0) return null;
  return [text.slice(0, i), text.slice(i, i + q.length), text.slice(i + q.length)];
}

// ---- citations: doc:<uuid>[:p<n>[:l<a>[-<b>]]] (models sometimes write l<a>-l<b>)

export const CITE_RE =
  /\[?\s*(doc:([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})(?::p(\d+))?(?::l(\d+)(?:-l?(\d+))?)?)\s*\]?/gi;

export interface Citation {
  id: string;
  doc: string;
  page?: number;
  from?: number;
  to?: number;
}

export function parseCitation(s: string): Citation | null {
  CITE_RE.lastIndex = 0;
  const m = CITE_RE.exec(s);
  CITE_RE.lastIndex = 0;
  if (!m) return null;
  return {
    id: m[1],
    doc: m[2].toLowerCase(),
    page: m[3] ? +m[3] : undefined,
    from: m[4] ? +m[4] : undefined,
    to: m[5] ? +m[5] : m[4] ? +m[4] : undefined,
  };
}

export function citationLabel(c: Citation) {
  if (!c.page) return "tài liệu";
  let s = "tr." + c.page;
  if (c.from != null) s += " · d." + c.from + (c.to != null && c.to !== c.from ? "–" + c.to : "");
  return s;
}
