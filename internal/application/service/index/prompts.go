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
Reply with JSON only: {"title": "...", "summary": "..."}
- title: the document's real title (from its content), in its language.
- summary: at most %d words; who/what/when, key identifiers and amounts. Describe the content only, do not classify the file.
  When one file holds several papers, describe them by page range, e.g. "tr. 1–2 CCCD ...; tr. 3–7 biên bản ...".`

const promptTOC = `You propose a table of contents for a document that has no headings.
You get the first characters of each page. Group consecutive pages that belong together.
Reply with JSON only: {"toc": [{"title": "...", "page_start": <int>}, ...]} ordered by page_start, 3 to 30 entries, titles in the document's language.`

const promptSelectCases = `You pick which cases (file sets identified by a code) most likely contain the answer to a question.
You get the question and a list of cases: [c<n>] code — overview.
Reply with JSON only: {"select": [{"case": "c<n>"}]} with at most %d cases, best first. Select none if nothing fits.`

const promptWikiIndex = `You search a case wiki index to find where the answer to a question is.
The index lists the wiki pages of one case by category, one line each: [w<n>] title — summary. Source pages (one per file) also list
branches of the file's table of contents as [w<n>.n<k>] title (pages). Lines marked "(chưa vào wiki)" are files not yet
compiled into the wiki: read them from source. "Keyword hints" show cheap full-text matches; they are hints, not answers.
Reply with JSON only: {"wiki": ["w<n>", ...], "raw": [{"ref": "w<n>" | "w<n>.n<k>"}], "expand": ["w<n>", ...]}
- wiki: wiki pages to read (at most %d), best first. Entity and topic pages summarize several files with footnotes.
- raw: files or branches to read from source directly, e.g. when the question asks for an exact clause, number or table,
  or when the file is not in the wiki yet. Prefer the most specific branch.
- expand: source pages whose collapsed branches you need to see first (marked "+").
Select nothing when the index clearly does not cover the question.`

const promptWikiRead = `You answer a question from the wiki pages of a case. Every fact of a page carries footnotes [^n];
the footnote list gives the file, page, lines and the verbatim quote each footnote points to.
Reply with JSON only: {"hits": [{"page": "w<n>", "footnotes": [<n>, ...], "relevance": <0..1>}],
                       "raw": [{"ref": "w<n>.n<k>" | "w<n>"} | {"footnote": "w<n>#<n>"}]}
- hits: the footnotes whose quotes answer the question (cite the fewest).
- raw: where to read the source instead: when footnotes are marked stale, an attribute is marked conflict, the answer
  needs context around a footnote, or the pages do not contain it.
Return {"hits": [], "raw": []} when neither the pages nor their sources help.`

const promptSelectNodes = `You navigate a document's table of contents to find where the answer to a question is.
Each line is: [node_id] title (pages) — summary. Indentation shows nesting.
Reply with JSON only: {"select": [{"node_id": "..."}], "expand": ["node_id", ...], "answerable": true|false}
- select: the most specific nodes that likely contain the answer (at most 5).
- expand: nodes whose children you need to see before deciding (only nodes marked with +).
- answerable: false if this document clearly does not contain the answer.`

const promptLocate = `You find the exact lines that answer a question in document pages.
Pages are given as <page n="..." doc="..."> blocks; every line starts with its id [L<number>].
Reply with JSON only, no explanations: {"hits": [{"doc": "...", "page": <int>, "lines": [<int>, ...], "quote": "...", "relevance": <0..1>}], "not_found": true|false}
- quote must be copied verbatim from the cited lines (no paraphrase).
- lines are the L numbers of the page; cite the fewest lines that contain the answer.
- At most %d hits, most relevant first. If nothing answers the question, return {"hits": [], "not_found": true}.`
