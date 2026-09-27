import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";

import type { Case } from "../api/types";
import { fold } from "../lib/format";
import { useApp } from "./AppContext";
import { Badge, Icon } from "./ui";

export const caseLabel = (c: Case) => (c.code === "_UNASSIGNED" ? "Chưa gán hồ sơ" : c.code);
export const caseDocs = (c: Case) => Object.values(c.documents ?? {}).reduce((a, b) => a + b, 0);

// Chip that shows the current case (hồ sơ) and opens a searchable list.
export function CasePicker({ value, onChange, disabled }: { value?: Case | null; onChange?: (c: Case) => void; disabled?: boolean }) {
  const { cases, kcase, selectCase } = useApp();
  const cur = value === undefined ? kcase : value;
  const pick = onChange ?? ((c: Case) => selectCase(c.id));
  const btn = useRef<HTMLButtonElement>(null);
  const pop = useRef<HTMLDivElement>(null);
  const [anchor, setAnchor] = useState<DOMRect | null>(null);
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null);
  const [q, setQ] = useState("");

  useLayoutEffect(() => {
    if (!anchor || !pop.current) return;
    const m = pop.current.getBoundingClientRect();
    setPos({ left: Math.max(8, Math.min(window.innerWidth - m.width - 8, anchor.left)), top: anchor.bottom + 4 });
  }, [anchor]);
  useEffect(() => {
    if (!anchor) return;
    const close = (e: Event) => {
      if (pop.current?.contains(e.target as Node) || btn.current?.contains(e.target as Node)) return;
      setAnchor(null);
    };
    const key = (e: KeyboardEvent) => e.key === "Escape" && setAnchor(null);
    document.addEventListener("mousedown", close);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("mousedown", close);
      document.removeEventListener("keydown", key);
    };
  }, [anchor]);

  const f = fold(q.trim());
  const list = (cases ?? []).filter((c) => !f || fold(`${c.code} ${c.title ?? ""}`).includes(f));

  return (
    <>
      <button
        ref={btn}
        disabled={disabled}
        className={"chip max-w-72 " + (cur ? "chip-on" : "")}
        title={disabled ? "Cuộc trò chuyện đã gắn với hồ sơ này" : "Chọn hồ sơ"}
        onClick={() => {
          setQ("");
          setPos(null);
          setAnchor(anchor ? null : btn.current!.getBoundingClientRect());
        }}
      >
        <Icon name="folder_open" size={18} />
        <span className="truncate">{cur ? caseLabel(cur) : cases?.length === 0 ? "Chưa có hồ sơ" : "Chọn hồ sơ"}</span>
        {!disabled && <Icon name="arrow_drop_down" size={18} className="-mr-1" />}
      </button>
      {anchor &&
        createPortal(
          <div ref={pop} className="menu fixed flex max-h-[60vh] w-80 flex-col py-0" style={pos ?? { left: -9999, top: -9999 }}>
            <div className="flex items-center gap-2 border-b border-line px-3 py-2">
              <Icon name="search" size={18} className="text-muted" />
              <input autoFocus className="min-w-0 flex-1 bg-transparent text-sm text-fg outline-none placeholder:text-muted" placeholder="Tìm mã hồ sơ" value={q} onChange={(e) => setQ(e.target.value)} />
            </div>
            <div className="min-h-0 flex-1 overflow-auto py-1">
              {!list.length && <div className="px-4 py-3 text-sm text-subtle">{cases?.length ? "Không có hồ sơ khớp" : "Chưa có hồ sơ — tải file lên kèm mã hồ sơ để tạo."}</div>}
              {list.map((c) => {
                return (
                  <button
                    key={c.id}
                    className="menu-item h-auto py-2"
                    onClick={() => {
                      setAnchor(null);
                      pick(c);
                    }}
                  >
                    <Icon name={c.id === cur?.id ? "check" : ""} className="w-5 text-accent" />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-sm text-fg">{caseLabel(c)}</span>
                      <span className="block truncate text-xs text-muted">
                        {c.title ? c.title + " · " : ""}
                        {caseDocs(c)} file · {c.case_type}
                      </span>
                    </span>
                    {c.status === "closed" && <Badge>đóng</Badge>}
                  </button>
                );
              })}
            </div>
          </div>,
          document.body,
        )}
    </>
  );
}
