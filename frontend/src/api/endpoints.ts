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
  CaseTOC,
  AdminUser,
  CorrectionStat,
  LLMKeyView,
  PromptTemplate,
  Sheet,
  SheetField,
  SheetImport,
  SheetView,
  UsageSummary,
  APIKey,
  AuthUser,
} from "./types";

const q = (params: Record<string, string | number | undefined | null>) => {
  const s = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== null && v !== "") s.set(k, String(v));
  const str = s.toString();
  return str ? "?" + str : "";
};

// ---- knowledge bases

export const listKBs = () => request<{ data: KnowledgeBase[] }>("/kbs").then((r) => r.data ?? []);
export const createKB = (body: { name: string; description?: string; config?: KBConfig }) =>
  request<KnowledgeBase>("/kbs", { body });
export const updateKB = (id: string, body: { name?: string; description?: string; config?: KBConfig }) =>
  request<KnowledgeBase>(`/kbs/${id}`, { method: "PATCH", body });
export const listEngines = () => request<{ data: EngineInfo[] }>("/parser/engines").then((r) => r.data ?? []);

// ---- cases (one dossier = one case; the table of contents and the chat are per case)

export const listCaseTypes = () => request<{ data: CaseType[] }>("/case-types").then((r) => r.data ?? []);
export const listCases = (kb: string, f: { q?: string; limit?: number } = {}) =>
  request<{ data: Case[] }>(`/kbs/${kb}/cases` + q({ q: f.q, limit: f.limit ?? 500 })).then((r) => r.data ?? []);
export const getCase = (id: string) => request<Case>(`/cases/${id}`);
export const createCase = (kb: string, body: { code: string; case_type?: string; title?: string }) =>
  request<Case>(`/kbs/${kb}/cases`, { body });
// The case table of contents: file cards and the first branches of their trees.
export const getCaseTOC = (id: string) => request<CaseTOC>(`/cases/${id}/toc`);

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
  // A custom prompt for this turn (prompt editors only, §8.4).
  system?: string;
  signal?: AbortSignal;
}
export function streamMessage(m: MessageRequest) {
  const metadata: Record<string, unknown> = {};
  if (m.caseId) metadata.case_id = m.caseId;
  if (m.sessionId) metadata.session_id = m.sessionId;
  return requestRaw("/messages", {
    body: { max_tokens: 4096, stream: true, messages: [{ role: "user", content: m.text }], metadata, ...(m.system ? { system: m.system } : {}) },
    headers: { Accept: "text/event-stream" },
    signal: m.signal,
  });
}

export const createSession = (body: { case_id: string; template_id?: string; title?: string }) =>
  request<SessionBrief>("/sessions", { body });
export const setSessionTemplate = (id: string, templateId: string) =>
  request<SessionBrief>(`/sessions/${id}`, { method: "PATCH", body: { template_id: templateId } });

// ---- prompt templates (§8.4)

export const listTemplates = (kind: "chat" | "sheet", caseType?: string, drafts = false) =>
  request<{ data: PromptTemplate[] }>("/templates" + q({ kind, case_type: caseType, drafts: drafts ? 1 : undefined })).then((r) => r.data ?? []);
export const getTemplate = (id: string, version?: number) => request<PromptTemplate>(`/templates/${id}` + q({ version }));
export const createTemplate = (body: { kind: "chat" | "sheet"; slug: string; name: string; description?: string; case_type?: string; body: string; fields?: SheetField[] }) =>
  request<PromptTemplate>("/templates", { body });
export const addTemplateVersion = (id: string, body: { name?: string; description?: string; body: string; fields?: SheetField[] }) =>
  request<PromptTemplate>(`/templates/${id}/versions`, { body });
export const publishTemplate = (id: string, version: number) => request<PromptTemplate>(`/templates/${id}/publish`, { body: { version } });
export const templateCorrections = (id: string) =>
  request<{ data: CorrectionStat[] }>(`/templates/${id}/corrections`).then((r) => r.data ?? []);

// ---- case sheets (§6.9.6)

export const createSheet = (caseId: string, templateId: string) => request<Sheet>(`/cases/${caseId}/sheets`, { body: { template_id: templateId } });
export const listSheets = (caseId: string) => request<{ data: Sheet[] }>(`/cases/${caseId}/sheets`).then((r) => r.data ?? []);
export const getSheet = (id: string) => request<SheetView>(`/sheets/${id}`);
export const saveSheetEdits = (id: string, edits: { key: string; value: string; origin: "page" | "xlsx"; document_id?: string }[]) =>
  request<SheetView>(`/sheets/${id}/edits`, { body: { edits } });
export const importSheet = (id: string, file: File) => {
  const fd = new FormData();
  fd.append("file", file, file.name);
  return uploadForm<SheetImport>(`/sheets/${id}/import`, fd);
};
export const downloadSheet = (s: { id: string; name: string; case_code?: string }) =>
  download(`/sheets/${s.id}/xlsx`, `${s.case_code ? s.case_code + "_" : ""}${s.name}.xlsx`);

// ---- usage and admin (§8.5)

export const getMyUsage = () => request<UsageSummary>("/me/usage");
export const getAdminUsage = () => request<UsageSummary>("/admin/usage");
export const listAdminUsers = () => request<{ currency: string; data: AdminUser[] }>("/admin/users");
export const updateAdminUser = (id: string, body: { role?: string; monthly_limit?: number; is_active?: boolean }) =>
  request<AuthUser>(`/admin/users/${id}`, { method: "PATCH", body });
export const issueAPIKey = (userId: string, name: string) =>
  request<{ key: APIKey; api_key: string }>(`/admin/users/${userId}/api-keys`, { body: { name } });
export const getLLMKey = () => request<LLMKeyView>("/admin/llm");
export const setLLMKey = (apiKey: string) => request<LLMKeyView>("/admin/llm", { method: "PUT", body: { api_key: apiKey } });
