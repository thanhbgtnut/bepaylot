import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";

import type { DocStatus } from "../api/types";
import { metaValue, SOURCE, STATUS, TERMINAL, type Tone } from "../lib/format";

// ---------------------------------------------------------------- icons
// Material Symbols Rounded (self-hosted). Short aliases keep call sites terse;
// any other string is used as the symbol name directly.
const ALIAS: Record<string, string> = {
  docs: "folder_open",
  chat: "forum",
  wiki: "menu_book",
  graph: "hub",
  graphNodes: "hub",
  gear: "settings",
  plus: "add",
  close: "close",
  back: "arrow_back",
  left: "chevron_left",
  right: "chevron_right",
  down: "arrow_drop_down",
  refresh: "refresh",
  upload: "upload_file",
  search: "search",
  folder: "folder",
  file: "description",
  page: "article",
  list: "view_list",
  grid: "grid_view",
  edit: "edit",
  trash: "delete",
  download: "download",
  fit: "fit_screen",
  send: "send",
  stop: "stop_circle",
  tool: "build",
  path: "route",
  history: "history",
  more: "more_vert",
  info: "info",
  kb: "database",
};
export type IconName = string;

export function Icon({ name, className = "", fill = false, size }: { name: IconName; className?: string; fill?: boolean; size?: number }) {
  return (
    <span className={"material-symbols-rounded " + (fill ? "icon-fill " : "") + className} style={size ? { fontSize: size } : undefined} aria-hidden>
      {ALIAS[name] ?? name}
    </span>
  );
}

// File-type icon with Drive-like colours.
export function FileIcon({ mime, name, size = 22 }: { mime?: string; name?: string; size?: number }) {
  const n = (name ?? "").toLowerCase();
  if (mime === "application/pdf" || n.endsWith(".pdf")) return <Icon name="picture_as_pdf" fill size={size} className="text-[#ea4335]" />;
  if (mime?.startsWith("image/") || /\.(png|jpe?g|tiff?|webp|bmp|gif)$/.test(n)) return <Icon name="image" fill size={size} className="text-[#d93025]" />;
  return <Icon name="description" fill size={size} className="text-[#4285f4]" />;
}

// ---------------------------------------------------------------- small pieces
export function Spinner({ className = "" }: { className?: string }) {
  return <span className={"spinner " + className} />;
}

export function Badge({ tone = "", children, title }: { tone?: Tone; children: ReactNode; title?: string }) {
  return (
    <span className={"badge" + (tone ? " badge-" + tone : "")} title={title}>
      {children}
    </span>
  );
}

export function StatusBadge({ status }: { status: DocStatus }) {
  const [label, tone] = STATUS[status] ?? [status, ""];
  const spinning = !TERMINAL.has(status) && status !== "enriching";
  return (
    <Badge tone={tone}>
      {spinning && <span className="spinner size-2.5 border-[1.5px]" />}
      {label}
    </Badge>
  );
}

export function SourceBadge({ source }: { source?: string }) {
  const [label, tone] = SOURCE[source ?? ""] ?? [source || "—", ""];
  return <Badge tone={tone}>{label}</Badge>;
}

export function MetaChips({ meta }: { meta?: Record<string, unknown> }) {
  if (!meta) return null;
  return (
    <>
      {Object.entries(meta).map(([k, v]) => (
        <span key={k} className="tag">
          <span className="text-subtle">{k}</span>
          <span className="font-medium text-fg">{metaValue(v)}</span>
        </span>
      ))}
    </>
  );
}

export function Empty({ icon, title, children }: { icon?: string; title?: ReactNode; children?: ReactNode }) {
  return (
    <div className="empty">
      {icon && (
        <div className="mb-4 grid size-24 place-items-center rounded-full bg-surface-2">
          <Icon name={icon} size={44} className="text-subtle" />
        </div>
      )}
      {title && <div className="mb-1 text-[18px] text-fg">{title}</div>}
      {children && <div className="max-w-115 text-sm">{children}</div>}
    </div>
  );
}

export function Loading() {
  return (
    <div className="empty">
      <Spinner />
    </div>
  );
}

// ---------------------------------------------------------------- menu
export interface MenuItem {
  icon?: string;
  label: ReactNode;
  onClick?: () => void;
  danger?: boolean;
  disabled?: boolean;
  checked?: boolean;
  divider?: boolean;
}

// A dropdown menu anchored to its trigger, rendered in a portal.
export function Menu({
  trigger,
  items,
  align = "left",
}: {
  trigger: (open: () => void, isOpen: boolean) => ReactNode;
  items: MenuItem[];
  align?: "left" | "right";
}) {
  const [anchor, setAnchor] = useState<DOMRect | null>(null);
  const wrap = useRef<HTMLSpanElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null);

  useLayoutEffect(() => {
    if (!anchor || !menu.current) return;
    const m = menu.current.getBoundingClientRect();
    let left = align === "right" ? anchor.right - m.width : anchor.left;
    left = Math.max(8, Math.min(window.innerWidth - m.width - 8, left));
    let top = anchor.bottom + 4;
    if (top + m.height > window.innerHeight - 8) top = Math.max(8, anchor.top - m.height - 4);
    setPos({ left, top });
  }, [anchor, align]);

  useEffect(() => {
    if (!anchor) return;
    const close = (e: Event) => {
      if (menu.current?.contains(e.target as Node) || wrap.current?.contains(e.target as Node)) return;
      setAnchor(null);
    };
    const key = (e: KeyboardEvent) => e.key === "Escape" && setAnchor(null);
    document.addEventListener("mousedown", close);
    document.addEventListener("keydown", key);
    window.addEventListener("resize", () => setAnchor(null), { once: true });
    return () => {
      document.removeEventListener("mousedown", close);
      document.removeEventListener("keydown", key);
    };
  }, [anchor]);

  return (
    <>
      <span ref={wrap} className="contents">
        {trigger(() => {
          const el = wrap.current?.firstElementChild as HTMLElement | null;
          setPos(null);
          setAnchor(anchor ? null : (el?.getBoundingClientRect() ?? null));
        }, !!anchor)}
      </span>
      {anchor &&
        createPortal(
          <div ref={menu} className="menu fixed" style={pos ?? { left: -9999, top: -9999 }} onClick={(e) => e.stopPropagation()}>
            {items.map((it, i) =>
              it.divider ? (
                <div key={i} className="my-2 border-t border-line" />
              ) : (
                <button
                  key={i}
                  className={"menu-item " + (it.danger ? "text-err" : "")}
                  disabled={it.disabled}
                  onClick={() => {
                    setAnchor(null);
                    it.onClick?.();
                  }}
                >
                  {it.checked !== undefined ? (
                    <Icon name={it.checked ? "check" : ""} className="w-5 text-accent" />
                  ) : (
                    it.icon && <Icon name={it.icon} className={it.danger ? "text-err" : "text-muted"} />
                  )}
                  <span className="flex-1">{it.label}</span>
                </button>
              ),
            )}
          </div>,
          document.body,
        )}
    </>
  );
}

// ---------------------------------------------------------------- dialog (M3)
export function Modal({
  title,
  icon,
  onClose,
  children,
  footer,
  width = 560,
}: {
  title: ReactNode;
  icon?: string;
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
  width?: number;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const d = ref.current!;
    if (!d.open) d.showModal();
    d.focus(); // avoid a focus ring on the close button when opening
    return () => d.close();
  }, []);
  return (
    <dialog
      ref={ref}
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
      onClick={(e) => e.target === ref.current && onClose()}
      tabIndex={-1}
      className="m-auto rounded-[28px] outline-none border-0 bg-surface p-0 text-fg shadow-3 backdrop:bg-(--scrim)"
      style={{ width: `min(${width}px, calc(100vw - 32px))` }}
    >
      <div className="flex items-start gap-3 px-6 pt-6 pb-4">
        {icon && <Icon name={icon} className="mt-0.5 text-accent" />}
        <h2 className="flex-1 text-2xl font-normal">{title}</h2>
        <button className="btn-icon btn-sm -mt-1 -mr-2" onClick={onClose} aria-label="Đóng">
          <Icon name="close" />
        </button>
      </div>
      <div className="flex max-h-[68vh] flex-col gap-4 overflow-auto px-6 pb-2 text-muted">{children}</div>
      {footer && <div className="flex justify-end gap-2 px-6 pt-4 pb-6">{footer}</div>}
    </dialog>
  );
}
