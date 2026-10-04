package index

// Prompts are in English; documents and answers are usually Vietnamese, so
// every prompt asks to keep the document's language.

const promptCard = `You write the catalogue card of a document.
Reply with JSON only: {"title": "...", "summary": "..."}
- title: the document's real title (from its content), in its language.
- summary: at most %d words; who/what/when, key identifiers and amounts. Describe the content only, do not classify the file.
  When one file holds several papers, describe them by page range, e.g. "tr. 1–2 CCCD ...; tr. 3–7 biên bản ...".`

const promptSelectCases = `You pick which cases (file sets identified by a code) most likely contain the answer to a question.
You get the question and a list of cases: [c<n>] code (title) — number of files and their titles {case metadata}.
Reply with JSON only: {"select": [{"case": "c<n>"}]} with at most %d cases, best first. Select none if nothing fits.`

const promptTreeSearch = `You find where the answer to a question is in the files of one case by reading tables of contents, the way a reader
uses the contents page of a binder. No page text is shown yet: only file cards and table-of-contents nodes.
- A file line is: [d<n>] file name (pages) {metadata} — summary.
- A node line is: [d<n>.n<k>] title (tr. pages) — summary. Indentation shows nesting; " +" or "(+k mục, expand X)" marks
  children that are not shown yet.
- "Keyword hints" show cheap full-text matches; they are hints, not answers.
Reply with JSON only: {"select": [{"node": "d<n>.n<k>" | "d<n>"}], "expand": ["d<n>" | "d<n>.n<k>", ...]}
- select: the most specific nodes (or a whole small file) whose pages likely hold the answer, at most %d, best first.
  Their pages will be read next.
- expand: files or nodes whose hidden children you must see before choosing. Do not expand what you can already select.
Return {"select": [], "expand": []} when nothing fits.`

const promptLocate = `You find the exact lines that answer a question in document pages.
Pages are given as <page n="..." doc="..."> blocks; every line starts with its id [L<number>].
Reply with JSON only, no explanations: {"hits": [{"doc": "...", "page": <int>, "lines": [<int>, ...], "quote": "...", "relevance": <0..1>}], "not_found": true|false}
- quote must be copied verbatim from the cited lines (no paraphrase).
- lines are the L numbers of the page; cite the fewest lines that contain the answer.
- At most %d hits, most relevant first. If nothing answers the question, return {"hits": [], "not_found": true}.`
