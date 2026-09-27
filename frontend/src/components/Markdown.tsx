import { memo, useLayoutEffect, useMemo, useRef } from "react";

import { linkCitations, markdownToHTML } from "../lib/markdown";
import { useCitation } from "./Citation";

// Renders sanitized markdown. Citation ids become clickable chips that open
// the citation popover.
export const Markdown = memo(function Markdown({ text, className = "", streaming = false }: { text: string; className?: string; streaming?: boolean }) {
  const ref = useRef<HTMLDivElement>(null);
  const cite = useCitation();
  const html = useMemo(() => markdownToHTML(text), [text]);

  useLayoutEffect(() => {
    if (ref.current) linkCitations(ref.current);
  }, [html]);

  return (
    <div
      ref={ref}
      className={"md " + (streaming ? "caret " : "") + className}
      dangerouslySetInnerHTML={{ __html: html }}
      onClick={(e) => {
        const t = e.target as HTMLElement;
        const c = t.closest<HTMLElement>(".cite");
        if (c?.dataset.cite) {
          e.preventDefault();
          cite.open(c.dataset.cite, c.getBoundingClientRect());
        }
      }}
    />
  );
});

// Plain text in which citation ids are clickable (tool results, refs lists).
export function CitedText({ text, className = "" }: { text: string; className?: string }) {
  const ref = useRef<HTMLSpanElement>(null);
  const cite = useCitation();
  useLayoutEffect(() => {
    if (!ref.current) return;
    ref.current.textContent = text;
    linkCitations(ref.current);
  }, [text]);
  return (
    <span
      ref={ref}
      className={className}
      onClick={(e) => {
        const c = (e.target as HTMLElement).closest<HTMLElement>(".cite");
        if (c?.dataset.cite) cite.open(c.dataset.cite, c.getBoundingClientRect());
      }}
    />
  );
}
