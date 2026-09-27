import { useEffect, useRef, useState } from "react";

import { pageImageURL } from "../api/endpoints";

// Lazily loaded page image (only fetched once it scrolls into view).
export function Thumb({ docId, pageNo, className = "", alt }: { docId: string; pageNo: number; className?: string; alt?: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const [visible, setVisible] = useState(false);
  const [src, setSrc] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const io = new IntersectionObserver((es) => es.some((e) => e.isIntersecting) && setVisible(true), { rootMargin: "200px" });
    io.observe(el);
    return () => io.disconnect();
  }, []);

  useEffect(() => {
    if (!visible) return;
    let alive = true;
    pageImageURL(docId, pageNo).then(
      (u) => alive && setSrc(u),
      () => alive && setFailed(true),
    );
    return () => {
      alive = false;
    };
  }, [visible, docId, pageNo]);

  return (
    <div ref={ref} className={"overflow-hidden bg-surface-2 " + className}>
      {src && !failed && <img src={src} alt={alt ?? `Trang ${pageNo}`} className="block size-full object-cover object-top" draggable={false} />}
    </div>
  );
}
