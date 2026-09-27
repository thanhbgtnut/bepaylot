import DOMPurify from "dompurify";
import { marked } from "marked";

import { CITE_RE, citationLabel, parseCitation } from "./format";

export function markdownToHTML(md: string): string {
  const html = marked.parse(md ?? "", { gfm: true, async: false }) as string;
  return DOMPurify.sanitize(html, { ADD_ATTR: ["data-cite", "target"] });
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
