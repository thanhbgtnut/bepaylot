import { blobURL, download, request, requestRaw, uploadForm } from "./client";
import type {
  Case,
  CaseType,
  Document,
  DocumentDetail,
  DocumentPage,
  EngineInfo,
  KBConfig,
  KnowledgeBase,
  PageSearchHit,
  PageView,
  SearchHit,
  SessionBrief,
  TranscriptMessage,
  TreeNode,
  UploadResult,
  WikiGraph,
  WikiLintIssue,
  WikiLogEntry,
  WikiPage,
  WikiRevision,
  WikiTOC,
} from "./types";

const q = (params: Record<string, string | number | undefined | null>) => {
  const s = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== null && v !== "") s.set(k, String(v));
  const str = s.toString();
  return str ? "?" + str : "";
};
const enc = encodeURIComponent;

// ---- knowledge bases

export const listKBs = () => request<{ data: KnowledgeBase[] }>("/kbs").then((r) => r.data ?? []);
export const createKB = (body: { name: string; description?: string; config?: KBConfig }) =>
  request<KnowledgeBase>("/kbs", { body });
export const updateKB = (id: string, body: { name?: string; description?: string; config?: KBConfig }) =>
  request<KnowledgeBase>(`/kbs/${id}`, { method: "PATCH", body });
export const listEngines = () => request<{ data: EngineInfo[] }>("/parser/engines").then((r) => r.data ?? []);

// ---- cases (one dossier = one case; the wiki and the chat are per case)

export const listCaseTypes = () => request<{ data: CaseType[] }>("/case-types").then((r) => r.data ?? []);
export const listCases = (kb: string, f: { q?: string; limit?: number } = {}) =>
  request<{ data: Case[] }>(`/kbs/${kb}/cases` + q({ q: f.q, limit: f.limit ?? 500 })).then((r) => r.data ?? []);
export const getCase = (id: string) => request<Case>(`/cases/${id}`);
export const createCase = (kb: string, body: { code: string; case_type?: string; title?: string }) =>
  request<Case>(`/kbs/${kb}/cases`, { body });

// ---- documents

export interface DocumentQuery {
  caseId?: string;
  q?: string;
  status?: string;
  metadata?: Record<string, unknown>;
  limit?: number;
  before?: string;
}
export const listDocuments = (kb: string, f: DocumentQuery = {}) =>
  request<{ data: Document[]; next_cursor?: string }>(
    `/kbs/${kb}/documents` +
      q({
        case_id: f.caseId,
        q: f.q,
        status: f.status,
        limit: f.limit ?? 100,
        before: f.before,
        metadata: f.metadata && Object.keys(f.metadata).length ? JSON.stringify(f.metadata) : undefined,
      }),
  );

export function uploadDocuments(
  kb: string,
  files: File[],
  opt: { caseCode: string; caseType?: string; metadata?: Record<string, unknown>; callbackUrl?: string; onProgress?: (p: number) => void },
) {
  const fd = new FormData();
  // The case goes first so the server knows it before the files stream in.
  fd.append("case_code", opt.caseCode);
  if (opt.caseType) fd.append("case_type", opt.caseType);
  for (const f of files) fd.append("file", f, f.name);
  if (opt.metadata && Object.keys(opt.metadata).length) fd.append("metadata", JSON.stringify(opt.metadata));
  if (opt.callbackUrl) fd.append("callback_url", opt.callbackUrl);
  return uploadForm<UploadResult>(`/kbs/${kb}/documents`, fd, opt.onProgress);
}

export const getDocument = (id: string) => request<DocumentDetail>(`/documents/${id}`);
export const deleteDocument = (id: string) => request(`/documents/${id}`, { method: "DELETE" });
export const reparseDocument = (id: string, body: { pages?: number[]; engine?: string } = {}) =>
  request(`/documents/${id}/reparse`, { body });
export const listPages = (id: string) => request<{ data: DocumentPage[] }>(`/documents/${id}/pages`).then((r) => r.data ?? []);
export const getPage = (id: string, n: number) => request<PageView>(`/documents/${id}/pages/${n}`);
export const pageImageURL = (id: string, n: number) => blobURL(`/documents/${id}/pages/${n}/image`);
export const getMarkdown = (id: string) => request<{ markdown: string }>(`/documents/${id}/markdown`).then((r) => r.markdown);
export const getTree = (id: string) => request<{ nodes: TreeNode[] }>(`/documents/${id}/tree`).then((r) => r.nodes ?? []);
export const downloadOriginal = (d: Document) => download(`/documents/${d.id}/file`, d.file_name);
export const searchInDocument = (
  id: string,
  body: { query: string; mode?: "keyword" | "reasoning"; page_from?: number; page_to?: number },
) => request<{ pages: PageSearchHit[] }>(`/documents/${id}/search`, { body }).then((r) => r.pages ?? []);
export const locateCitation = (id: string) =>
  request<{ hits: SearchHit[] }>(`/citations` + q({ id })).then((r) => r.hits ?? []);

// ---- sessions / chat

export const listSessions = () => request<{ data: SessionBrief[] }>("/sessions?limit=100").then((r) => r.data ?? []);
export const deleteSession = (id: string) => request(`/sessions/${id}`, { method: "DELETE" });
export const listMessages = (id: string) =>
  request<{ data: TranscriptMessage[] }>(`/sessions/${id}/messages?limit=500`).then((r) => r.data ?? []);

export interface MessageRequest {
  text: string;
  sessionId?: string | null;
  // The case a new session is bound to; a session never changes its case.
  caseId?: string | null;
  signal?: AbortSignal;
}
export function streamMessage(m: MessageRequest) {
  const metadata: Record<string, unknown> = {};
  if (m.caseId) metadata.case_id = m.caseId;
  if (m.sessionId) metadata.session_id = m.sessionId;
  return requestRaw("/messages", {
    body: { max_tokens: 4096, stream: true, messages: [{ role: "user", content: m.text }], metadata },
    headers: { Accept: "text/event-stream" },
    signal: m.signal,
  });
}

// ---- wiki (one per case)

const page = (c: string, slug: string) => `/cases/${c}/wiki/pages/${slug.split("/").map(enc).join("/")}`;

export const getWiki = (c: string) => request<WikiTOC>(`/cases/${c}/wiki`);
export const getWikiPage = (c: string, slug: string) => request<WikiPage>(page(c, slug));
export const editWikiPage = (c: string, slug: string, body: { title?: string; content?: string }) =>
  request<{ page: WikiPage; invalid_footnotes?: string[] }>(page(c, slug), { method: "PUT", body });
export const wikiRevisions = (c: string, slug: string) =>
  request<{ data: WikiRevision[] }>(page(c, slug) + "/revisions").then((r) => r.data ?? []);
export const restoreWikiRevision = (c: string, slug: string, v: number) =>
  request<WikiPage>(page(c, slug) + `/revisions/${v}/restore`, { body: {} });
export const decideWikiProposal = (c: string, slug: string, action: "accept" | "reject") =>
  request<WikiPage>(page(c, slug) + "/proposal", { body: { action } });
export const deleteWikiNote = (c: string, slug: string) => request(page(c, slug), { method: "DELETE" });
export const searchWiki = (c: string, text: string) =>
  request<{ data: { slug: string; title: string; kind: string; snippet: string }[] }>(`/cases/${c}/wiki/search` + q({ q: text })).then(
    (r) => r.data ?? [],
  );
export const wikiGraph = (c: string) => request<WikiGraph>(`/cases/${c}/wiki/links`);
export const wikiLog = (c: string) => request<{ data: WikiLogEntry[] }>(`/cases/${c}/wiki/log?limit=200`).then((r) => r.data ?? []);
export const wikiLint = (c: string, status = "open") =>
  request<{ data: WikiLintIssue[] }>(`/cases/${c}/wiki/lint` + q({ status })).then((r) => r.data ?? []);
export const runWikiLint = (c: string) => request(`/cases/${c}/wiki/lint`, { body: {} });
export const setLintStatus = (c: string, id: number, status: "fixed" | "dismissed" | "open") =>
  request(`/cases/${c}/wiki/lint/${id}`, { method: "PATCH", body: { status } });
export const rebuildWiki = (c: string) => request(`/cases/${c}/wiki/rebuild`, { body: {} });
export const exportWiki = (c: string, code: string, format: "md" | "html" = "md") =>
  download(`/cases/${c}/wiki/export?format=${format}`, `wiki-${code}.${format === "md" ? "zip" : "html"}`);
