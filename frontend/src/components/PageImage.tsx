import { useEffect, useRef, useState } from "react";

import { pageImageURL } from "../api/endpoints";
import type { BBox } from "../api/types";
import { Spinner } from "./ui";

export interface PageBox {
  key: string | number;
  bbox: BBox;
  className?: string;
  title?: string;
}

// A rendered page image with boxes drawn over it. Boxes are in page pixel
// units (the page's width × height); the SVG uses the same viewBox, so boxes
// stay aligned whatever size the image is displayed at.
export function PageImage({
  docId,
  pageNo,
  width,
  height,
  boxes = [],
  hideBoxes = false,
  scrollTo,
  onBoxClick,
  onBoxHover,
}: {
  docId: string;
  pageNo: number;
  width?: number;
  height?: number;
  boxes?: PageBox[];
  hideBoxes?: boolean;
  // Scrolls the first box with this class into view once the image loads.
  scrollTo?: string;
  onBoxClick?: (key: string) => void;
  onBoxHover?: (key: string | null) => void;
}) {
  const [src, setSrc] = useState<string | null>(null);
  const [err, setErr] = useState("");
  const [natural, setNatural] = useState<[number, number] | null>(null);
  const svgRef = useRef<SVGSVGElement>(null);

  useEffect(() => {
    let alive = true;
    setSrc(null);
    setErr("");
    setNatural(null);
    pageImageURL(docId, pageNo).then(
      (u) => alive && setSrc(u),
      (e) => alive && setErr((e as Error).message),
    );
    return () => {
      alive = false;
    };
  }, [docId, pageNo]);

  useEffect(() => {
    if (!natural || !scrollTo) return;
    const el = svgRef.current?.querySelector("rect." + scrollTo);
    if (el) setTimeout(() => el.scrollIntoView({ block: "center", behavior: "smooth" }), 60);
  }, [natural, scrollTo, boxes]);

  const W = width || natural?.[0] || 0;
  const H = height || natural?.[1] || 0;

  if (err)
    return (
      <div className="grid aspect-3/4 place-items-center rounded-md bg-surface-2 p-4 text-center text-xs text-muted">
        Không tải được ảnh trang
        <br />
        {err}
      </div>
    );
  return (
    <div className={"relative w-full overflow-hidden rounded-md bg-surface-2 leading-none" + (hideBoxes ? " nobox" : "")}>
      {!src && (
        <div className="grid aspect-3/4 place-items-center">
          <Spinner />
        </div>
      )}
      {src && (
        <img
          src={src}
          alt={`Trang ${pageNo}`}
          className="block h-auto w-full"
          onLoad={(e) => setNatural([e.currentTarget.naturalWidth, e.currentTarget.naturalHeight])}
        />
      )}
      {src && natural && W > 0 && (
        <svg
          ref={svgRef}
          viewBox={`0 0 ${W} ${H}`}
          preserveAspectRatio="none"
          className="absolute inset-0 size-full"
          onClick={(e) => {
            const r = (e.target as Element).closest("rect");
            if (r && onBoxClick) onBoxClick(r.getAttribute("data-key")!);
          }}
          onMouseOver={(e) => {
            const r = (e.target as Element).closest("rect");
            onBoxHover?.(r ? r.getAttribute("data-key") : null);
          }}
          onMouseLeave={() => onBoxHover?.(null)}
        >
          {boxes.map((b) => (
            <rect
              key={b.key}
              data-key={b.key}
              x={b.bbox.x0}
              y={b.bbox.y0}
              width={Math.max(1, b.bbox.x1 - b.bbox.x0)}
              height={Math.max(1, b.bbox.y1 - b.bbox.y0)}
              className={"pagebox " + (b.className ?? "")}
            >
              {b.title && <title>{b.title}</title>}
            </rect>
          ))}
        </svg>
      )}
    </div>
  );
}
