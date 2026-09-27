import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Link } from "react-router-dom";

import { getPage, locateCitation } from "../api/endpoints";
import type { PageView, SearchHit } from "../api/types";
import { PageImage } from "./PageImage";
import { Badge, Spinner } from "./ui";

interface CitationApi {
  open: (id: string, anchor: DOMRect) => void;
}
const Ctx = createContext<CitationApi>({ open: () => {} });
export const useCitation = () => useContext(Ctx);

// Global popover resolving "doc:<id>:p<n>:l<a>-<b>" to the quoted text and the
// highlighted region on the page image.
export function CitationProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<{ id: string; anchor: DOMRect } | null>(null);
  const open = useCallback((id: string, anchor: DOMRect) => setState({ id, anchor }), []);
  return (
    <Ctx.Provider value={{ open }}>
      {children}
      {state && createPortal(<Popover key={state.id} {...state} onClose={() => setState(null)} />, document.body)}
    </Ctx.Provider>
  );
}

function Popover({ id, anchor, onClose }: { id: string; anchor: DOMRect; onClose: () => void }) {
  const ref = useRef<HTMLDivElement>(null);
  const [hit, setHit] = useState<SearchHit | null>(null);
  const [page, setPage] = useState<PageView | null>(null);
  const [err, setErr] = useState("");
  const [pos, setPos] = useState({ left: anchor.left, top: anchor.bottom + 8 });

  useEffect(() => {
    let alive = true;
    locateCitation(id)
      .then(async (hits) => {
        const h = hits[0];
        if (!h) throw new Error("Không tìm thấy vị trí trích dẫn");
        const p = await getPage(h.document_id, h.page_no);
        if (alive) {
          setHit(h);
          setPage(p);
        }
      })
      .catch((e) => alive && setErr((e as Error).message));
    return () => {
      alive = false;
    };
  }, [id]);

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const w = el.offsetWidth;
    const h = Math.max(el.offsetHeight, 120);
    const left = Math.min(window.innerWidth - w - 12, Math.max(12, anchor.left));
    const below = anchor.bottom + 8;
    const top = below + h > window.innerHeight - 12 ? Math.max(12, anchor.top - h - 8) : below;
    setPos({ left, top });
  }, [anchor, hit, page, err]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    const onDown = (e: MouseEvent) => {
      const t = e.target as Element;
      if (!ref.current?.contains(t) && !t.closest?.(".cite")) onClose();
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("mousedown", onDown);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("mousedown", onDown);
    };
  }, [onClose]);

  const lines = hit?.lines?.length ? "&hl=" + hit.lines.join(",") : "";
  return (
    <div
      ref={ref}
      className="fixed z-900 flex w-110 max-w-[calc(100vw-24px)] flex-col gap-3 rounded-2xl bg-surface p-4 shadow-3"
      style={pos}
    >
      {!hit && !err && (
        <div className="flex items-center gap-2 text-xs text-muted">
          <Spinner /> Đang định vị {id}…
        </div>
      )}
      {err && <div className="text-xs text-err">{err}</div>}
      {hit && page && (
        <>
          <div className="flex items-center gap-2">
            <b className="flex-1 truncate font-medium">{hit.file_name}</b>
            <Badge tone="info">Trang {hit.page_no}</Badge>
          </div>
          {hit.quote && <blockquote className="rounded-lg bg-surface-2 px-3 py-2 font-body text-[13px] leading-5 whitespace-pre-wrap text-fg">{hit.quote}</blockquote>}
          <div className="max-h-80 overflow-auto rounded-md">
            <PageImage
              docId={hit.document_id}
              pageNo={hit.page_no}
              width={page.width}
              height={page.height}
              boxes={(hit.bboxes ?? []).map((b, i) => ({ key: i, bbox: b, className: "hl" }))}
              scrollTo="hl"
            />
          </div>
          <div className="flex items-center gap-2">
            <span className="flex-1 truncate font-mono text-[11px] text-muted">{id}</span>
            <Link className="btn btn-tonal btn-sm" to={`/documents/${hit.document_id}?page=${hit.page_no}${lines}`} onClick={onClose}>
              Mở tài liệu →
            </Link>
          </div>
        </>
      )}
    </div>
  );
}
