import { memo, useLayoutEffect, useMemo, useRef } from "react";

import { linkCitations, markdownToHTML, type MarkdownOptions } from "../lib/markdown";
import { useCitation } from "./Citation";

// Renders sanitized markdown. Citation ids become clickable chips that open
// the citation popover; [[slug]] wiki links call onWikiLink.
export const Markdown = memo(function Markdown({
  text,
  className = "",
  streaming = false,
  onWikiLink,
  ...opt
}: {
  text: string;
  className?: string;
  streaming?: boolean;
  onWikiLink?: (slug: string) => void;
} & MarkdownOptions) {
  const ref = useRef<HTMLDivElement>(null);
  const cite = useCitation();
  const html = useMemo(() => markdownToHTML(text, opt), [text, opt.wikiLinks, opt.knownSlugs, opt.slugTitles, opt.footnotes]); // eslint-disable-line react-hooks/exhaustive-deps

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
          return;
        }
        const w = t.closest<HTMLElement>("a[data-slug]");
        if (w && onWikiLink) {
          e.preventDefault();
          onWikiLink(w.dataset.slug!);
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
