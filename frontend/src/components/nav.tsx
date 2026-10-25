import { useLocation, useNavigate } from "react-router-dom";

import { useApp } from "./AppContext";
import { Icon } from "./ui";

// Back navigation (§7.15). Each screen (path without query) is one entry of the
// trail: changing the query on the same screen only updates that entry, coming
// back to a screen already in the trail cuts what follows it, and opening a
// root screen (an item of the sidebar) starts a new trail. The back button
// returns to the previous screen in the state it was left (file, item, page,
// tab); opened straight from a link, it goes to the screen's parent.
const trail: { key: string; href: string }[] = [];
const isRoot = (key: string) => /^\/(documents|chat(\/[^/]+)?|cases(\/[^/]+)?|settings(\/[^/]+)?)?$/.test(key);

function track(key: string, href: string) {
  if (isRoot(key)) return void trail.splice(0, trail.length, { key, href });
  const i = trail.findIndex((x) => x.key === key);
  if (i >= 0) {
    trail.length = i + 1;
    trail[i].href = href;
  } else trail.push({ key, href });
}

// Called on every render of the layout, before the page renders, so the page
// reads a trail that already includes itself.
export function useTrackNav() {
  const loc = useLocation();
  track(loc.pathname, loc.pathname + loc.search);
}

// Where "back" goes from the current screen and the name to show on it.
export function useBack(fallback: string, fallbackLabel: string, labelOf: (href: string) => string = () => "") {
  const prev = trail.at(-2);
  return prev ? { href: prev.href, label: labelOf(prev.href) || screenLabel(prev.href) } : { href: fallback, label: fallbackLabel };
}

function screenLabel(href: string) {
  const p = href.split("?")[0].split("/").filter(Boolean);
  if (p[0] === "documents") return "Tài liệu";
  if (p[0] === "cases" && p[2] === "checks") return "Kết quả đối chiếu";
  if (p[0] === "cases" && p[2] === "sheets") return "Bảng";
  if (p[0] === "cases" && p[2] === "split") return "Tách & gom trang";
  if (p[0] === "cases") return "Hồ sơ";
  if (p[0] === "settings") return "Cài đặt";
  if (p[0] === "chat") return "Hỏi đáp";
  return "Quay lại";
}

// The back button at the start of a sub page's header.
export function BackButton({ fallback, fallbackLabel, labelOf, onLeave, className = "btn btn-text max-w-64" }: { fallback: string; fallbackLabel: string; labelOf?: (href: string) => string; onLeave?: () => void; className?: string }) {
  const nav = useNavigate();
  const { cases } = useApp();
  // A case screen is named by its code ("Hồ sơ RT112233").
  const caseLabel = (h: string) => {
    const m = h.match(/^\/cases\/([^/?]+)(\?|$)/);
    const c = m && cases?.find((x) => x.id === m[1]);
    return c ? "Hồ sơ " + c.code : "";
  };
  const { href, label } = useBack(fallback, fallbackLabel, (h) => labelOf?.(h) || caseLabel(h));
  return (
    <button className={className} onClick={() => (onLeave?.(), nav(href))} title={"Quay lại " + label}>
      <Icon name="arrow_back" size={20} />
      <span className="truncate">{label}</span>
    </button>
  );
}
