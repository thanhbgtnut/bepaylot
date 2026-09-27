import DOMPurify from "dompurify";
import { marked } from "marked";

import { CITE_RE, citationLabel, parseCitation } from "./format";

export interface MarkdownOptions {
  // Turn [[slug]] / [[slug|label]] into wiki links; unknown slugs are marked dead.
  wikiLinks?: boolean;
  knownSlugs?: Set<string>;
  // Page titles shown for [[slug]] links without a label.
  slugTitles?: Map<string, string>;
  // Wiki footnotes: [^n] becomes a superscript that opens its citation.
  footnotes?: Map<number, { citation: string; stale?: boolean; label?: string }>;
}

const esc = (s: string) =>
  s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!);

export function markdownToHTML(md: string, opt: MarkdownOptions = {}): string {
  let src = md ?? "";
  if (opt.wikiLinks) {
    src = src.replace(/\[\[([^\]|]+)(?:\|([^\]]+))?\]\]/g, (_, slug: string, label?: string) => {
      const s = slug.trim();
      const dead = opt.knownSlugs && !opt.knownSlugs.has(s);
      return `<a class="wikilink${dead ? " dead" : ""}" href="#" data-slug="${esc(s)}">${esc(label || opt.slugTitles?.get(s) || s)}</a>`;
    });
  }
  if (opt.footnotes) {
    const fns = opt.footnotes;
    src = src.replace(/\[\^(\d+)\](?!:)/g, (_m, n: string) => {
      const f = fns.get(Number(n));
      if (!f) return `<sup class="fn missing" title="Chú thích ${n} không còn">${n}</sup>`;
      return `<sup><button type="button" class="cite fn${f.stale ? " stale" : ""}" data-cite="${esc(f.citation)}" title="${esc(f.label ?? f.citation)}">${n}</button></sup>`;
    });
  }
  const html = marked.parse(src, { gfm: true, async: false }) as string;
  return DOMPurify.sanitize(html, { ADD_ATTR: ["data-slug", "data-cite", "target"] });
}

// Replaces citation ids in text nodes with <button class="cite"> elements.
// Runs on the rendered DOM so code spans and existing buttons are left alone.
export function linkCitations(root: HTMLElement) {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode: (n) =>
      n.parentElement?.closest("code,pre,.cite") ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT,
  });
  const nodes: Text[] = [];
  while (walker.nextNode()) {
    const t = walker.currentNode as Text;
    if (/doc:[0-9a-f]{8}-/i.test(t.nodeValue ?? "")) nodes.push(t);
  }
  for (const node of nodes) {
    const text = node.nodeValue ?? "";
    const frag = document.createDocumentFragment();
    let last = 0;
    for (const m of text.matchAll(CITE_RE)) {
      const c = parseCitation(m[1]);
      if (!c) continue;
      frag.appendChild(document.createTextNode(text.slice(last, m.index)));
      const b = document.createElement("button");
      b.type = "button";
      b.className = "cite";
      b.dataset.cite = c.id;
      b.title = c.id;
      b.textContent = citationLabel(c);
      frag.appendChild(b);
      last = (m.index ?? 0) + m[0].length;
    }
    frag.appendChild(document.createTextNode(text.slice(last)));
    node.parentNode?.replaceChild(frag, node);
  }
}
