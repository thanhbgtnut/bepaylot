package index

// Prompts are in English; documents and answers are usually Vietnamese, so
// every prompt asks to keep the document's language.

const promptSummarize = `You summarize parts of a document so a reader can decide, from the summary alone, whether the part contains what they are looking for.
Rules:
- Write each summary in the document's language (usually Vietnamese), at most %d words.
- Keep concrete identifiers: names, codes, numbers, amounts, dates, addresses.
- No preamble, no markdown.
Reply with JSON only: {"summaries": {"<id>": "<summary>", ...}} with one entry per input id.`

const promptCard = `You write the catalogue card of a document.
Reply with JSON only: {"title": "...", "doc_type": "...", "summary": "..."}
- title: the document's real title (from its content), in its language.
- doc_type: a short type label in the document's language, e.g. "Giấy chứng nhận đăng ký hộ kinh doanh", "Báo cáo tài chính", "Hợp đồng".
- summary: at most %d words; who/what/when, key identifiers and amounts.`

const promptTOC = `You propose a table of contents for a document that has no headings.
You get the first characters of each page. Group consecutive pages that belong together.
Reply with JSON only: {"toc": [{"title": "...", "page_start": <int>}, ...]} ordered by page_start, 3 to 30 entries, titles in the document's language.`

const promptSelectDocs = `You pick which documents most likely contain the answer to a question.
You get the question and a list of document cards (id, file name, metadata, title, type, summary, pages).
Reply with JSON only: {"select": [{"doc": "<id>", "reason": "..."}]} with at most %d documents, best first. Select none if nothing fits.`

const promptSelectNodes = `You navigate a document's table of contents to find where the answer to a question is.
Each line is: [node_id] title (pages) — summary. Indentation shows nesting.
Reply with JSON only: {"select": [{"node_id": "...", "reason": "..."}], "expand": ["node_id", ...], "answerable": true|false}
- select: the most specific nodes that likely contain the answer (at most 5).
- expand: nodes whose children you need to see before deciding (only nodes marked with +).
- answerable: false if this document clearly does not contain the answer.`

const promptLocate = `You find the exact lines that answer a question in document pages.
Pages are given as <page n="..." doc="..."> blocks; every line starts with its id [L<number>].
Reply with JSON only: {"hits": [{"doc": "...", "page": <int>, "lines": [<int>, ...], "quote": "...", "relevance": <0..1>, "reason": "..."}], "not_found": true|false}
- quote must be copied verbatim from the cited lines (no paraphrase).
- lines are the L numbers of the page; cite the fewest lines that contain the answer.
- At most %d hits, most relevant first. If nothing answers the question, return {"hits": [], "not_found": true}.`
