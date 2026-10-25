import type { DocType } from "../api/types";

// One color per document type, by its position in the knowledge base's list,
// so a type looks the same on the case tree, the thumbnail strip and the
// check pages.
const TYPE_COLORS = ["#1a73e8", "#e37400", "#188038", "#9334e6", "#d93025", "#007b83", "#b06000", "#c5221f"];

export function typeColor(code: string | undefined, types: DocType[] = []) {
  const i = types.findIndex((t) => t.name === code);
  return i < 0 ? "var(--outline)" : TYPE_COLORS[i % TYPE_COLORS.length];
}

export function typeTitle(code: string | undefined, types: DocType[] = []) {
  if (!code) return "";
  if (code === "other") return "Khác";
  if (code === "unknown") return "Chưa phân loại";
  return types.find((t) => t.name === code)?.title || code;
}
