import { blobURL, download, request, requestRaw, uploadForm } from "./client";
import type {
  Case,
  CaseType,
  Document,
  DocumentDetail,
  DocumentPage,
  CheckRuleset,
  DocTypeJob,
  DocTypePrompt,
  DocTypeSample,
  DocTypeView,
  PromptShot,
  SheetField,
  CheckRun,
  CompileResult,
  EngineInfo,
  ExportMapping,
  ExportTemplate,
  KBConfig,
  KnowledgeBase,
  PageSearchHit,
  PageView,
  SearchHit,
  SessionBrief,
  TranscriptMessage,
  TreeNode,
  UploadResult,
  CaseDocFields,
  CaseFieldView,
  CaseTOC,
  AdminUser,
  LLMKeyView,
  PromptTemplate,
  Sheet,
  SheetImport,
  SheetView,
  SplitView,
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
  /** Các file của một lần tải lên. */
  batchId?: string;
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
        batch_id: f.batchId,
        metadata: f.metadata && Object.keys(f.metadata).length ? JSON.stringify(f.metadata) : undefined,
      }),
  );

export function uploadDocuments(
  kb: string,
  files: File[],
  opt: {
    caseCode: string;
    caseType?: string;
    metadata?: Record<string, unknown>;
    callbackUrl?: string;
    /** Chỉ lưu và đếm trang; trang cần xử lý được chọn sau bằng reparseDocument (U111). */
    hold?: boolean;
    onProgress?: (p: number) => void;
  },
) {
  const fd = new FormData();
  // The case goes first so the server knows it before the files stream in.
  fd.append("case_code", opt.caseCode);
  if (opt.caseType) fd.append("case_type", opt.caseType);
  if (opt.hold) fd.append("hold", "true");
  for (const f of files) fd.append("file", f, f.name);
  if (opt.metadata && Object.keys(opt.metadata).length) fd.append("metadata", JSON.stringify(opt.metadata));
  if (opt.callbackUrl) fd.append("callback_url", opt.callbackUrl);
  return uploadForm<UploadResult>(`/kbs/${kb}/documents`, fd, opt.onProgress);
}

export const getDocument = (id: string) => request<DocumentDetail>(`/documents/${id}`);
export const deleteDocument = (id: string) => request(`/documents/${id}`, { method: "DELETE" });
// With pages, only those pages are processed; pages not picked yet are read for the first time (U111).
export const reparseDocument = (id: string, body: { pages?: number[]; engine?: string } = {}) =>
  request(`/documents/${id}/reparse`, { body });
export const listPages = (id: string) => request<{ data: DocumentPage[] }>(`/documents/${id}/pages`).then((r) => r.data ?? []);
export const getPage = (id: string, n: number) => request<PageView>(`/documents/${id}/pages/${n}`);
export const pageImageURL = (id: string, n: number) => blobURL(`/documents/${id}/pages/${n}/image`);
export const imageURL = (path: string) => blobURL(path);
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

export const listTemplates = (kind: "chat", caseType?: string, drafts = false) =>
  request<{ data: PromptTemplate[] }>("/templates" + q({ kind, case_type: caseType, drafts: drafts ? 1 : undefined })).then((r) => r.data ?? []);
export const getTemplate = (id: string, version?: number) => request<PromptTemplate>(`/templates/${id}` + q({ version }));
export const createTemplate = (body: { kind: "chat"; slug: string; name: string; description?: string; case_type?: string; body: string }) =>
  request<PromptTemplate>("/templates", { body });
export const addTemplateVersion = (id: string, body: { name?: string; description?: string; body: string }) =>
  request<PromptTemplate>(`/templates/${id}/versions`, { body });
export const publishTemplate = (id: string, version: number) => request<PromptTemplate>(`/templates/${id}/publish`, { body: { version } });

// ---- case sheets (§6.9.6)

export const getCaseFields = (caseId: string, documentId: string) =>
  request<{ data: CaseDocFields[] }>(`/cases/${caseId}/fields` + q({ document_id: documentId })).then((r) => r.data ?? []);
export const saveCaseField = (caseId: string, body: { segment_id: string; key: string; value: string }) =>
  request<CaseFieldView>(`/cases/${caseId}/fields`, { method: "PUT", body });

export const createSheet = (caseId: string, tables?: string[], exportTemplateId?: string) =>
  request<Sheet>(`/cases/${caseId}/sheets`, { body: { tables, export_template_id: exportTemplateId } });
export const listSheets = (caseId: string) => request<{ data: Sheet[] }>(`/cases/${caseId}/sheets`).then((r) => r.data ?? []);
export const getSheet = (id: string) => request<SheetView>(`/sheets/${id}`);
export const deleteSheet = (id: string) => request(`/sheets/${id}`, { method: "DELETE" });
export interface SheetEditInput {
  table: string;
  segment_id?: string;
  document_id?: string;
  key: string;
  value: string;
  origin: "page" | "xlsx";
}
export const saveSheetEdits = (id: string, edits: SheetEditInput[]) =>
  request<SheetView>(`/sheets/${id}/edits`, { body: { edits } });
export const importSheet = (id: string, file: File) => {
  const fd = new FormData();
  fd.append("file", file, file.name);
  return uploadForm<SheetImport>(`/sheets/${id}/import`, fd);
};
export const downloadSheet = (s: { id: string; name: string; case_code?: string }, tables?: string[]) =>
  download(`/sheets/${s.id}/xlsx` + q({ tables: tables?.join(",") }), `${s.case_code ? s.case_code + "_" : ""}${s.name}.xlsx`);

// ---- tách & gom trang (§6.9.7)

export const getSplit = (caseId: string) => request<SplitView>(`/cases/${caseId}/split`);
export const classifyCase = (caseId: string, body: { mode?: "titles" | "pages"; document_ids?: string[] } = {}) =>
  request<{ queued: number }>(`/cases/${caseId}/classify`, { body });
export const saveSplit = (
  caseId: string,
  bundles: { segments: { document_id: string; page_start: number; page_end: number; label: string }[] }[],
) => request<SplitView>(`/cases/${caseId}/split`, { method: "PUT", body: { bundles } });

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

// ---- Excel forms of a KB (§6.9.8)
export const listExportTemplates = (kb: string) =>
  request<{ data: ExportTemplate[] }>(`/kbs/${kb}/export-templates`).then((r) => r.data ?? []);
export const uploadExportTemplate = (kb: string, file: File) => {
  const fd = new FormData();
  fd.append("file", file);
  return uploadForm<ExportTemplate>(`/kbs/${kb}/export-templates`, fd);
};
export const saveExportTemplate = (id: string, name: string, mapping: ExportMapping) =>
  request<ExportTemplate>(`/export-templates/${id}`, { method: "PUT", body: { name, mapping } });
export const deleteExportTemplate = (id: string) => request(`/export-templates/${id}`, { method: "DELETE" });
export const downloadSheetForm = (s: { id: string; name: string }, caseCode?: string) =>
  download(`/sheets/${s.id}/form.xlsx`, `${caseCode ? caseCode + "_" : ""}${s.name}.xlsx`);

// ---- cross-checks (§6.9.9)
export const listRulesets = (kbId?: string) => request<{ data: CheckRuleset[] }>("/check-rulesets" + q({ kb_id: kbId })).then((r) => r.data ?? []);
export const getRuleset = (id: string) => request<CheckRuleset>(`/check-rulesets/${id}`);
export const createRuleset = (body: Partial<CheckRuleset>) => request<CheckRuleset>("/check-rulesets", { body });
export const updateRuleset = (rs: CheckRuleset) => request<CheckRuleset>(`/check-rulesets/${rs.id}`, { method: "PUT", body: rs });
export const deleteRuleset = (id: string) => request(`/check-rulesets/${id}`, { method: "DELETE" });
export const copyRuleset = (id: string) => request<CheckRuleset>(`/check-rulesets/${id}/copy`, { method: "POST" });
export const compileRuleset = (id: string, body: { kind: "rule" | "link"; doc_type?: string; anchor: string; text: string }) =>
  request<CompileResult>(`/check-rulesets/${id}/compile`, { body });
export const createCheck = (caseId: string, rulesetId: string) => request<CheckRun>(`/cases/${caseId}/checks`, { body: { ruleset_id: rulesetId } });
export const listChecks = (caseId: string) => request<{ data: CheckRun[] }>(`/cases/${caseId}/checks`).then((r) => r.data ?? []);
export const getCheck = (id: string) => request<CheckRun>(`/checks/${id}`);
export const rerunCheck = (id: string) => request<CheckRun>(`/checks/${id}/rerun`, { method: "POST" });
export const setCheckLink = (id: string, body: { segment_id: string; anchor_segment_id: string | null; auto?: boolean }) =>
  request<CheckRun>(`/checks/${id}/links`, { method: "PUT", body });
export const deleteCheck = (id: string) => request(`/checks/${id}`, { method: "DELETE" });


// ---- catalogue of document types (§10.9)
const dt = (code: string) => `/doc-types/${encodeURIComponent(code)}`;
// A type without fields comes without the key.
const withFields = (t: DocTypeView): DocTypeView => ({ ...t, fields: t.fields ?? [], kb_ids: t.kb_ids ?? [] });
export const listDocTypes = (f: { kb_id?: string; unused?: boolean; q?: string } = {}) =>
  request<{ data: DocTypeView[] }>("/doc-types" + q({ kb_id: f.kb_id, unused: f.unused ? "true" : undefined, q: f.q })).then((r) => (r.data ?? []).map(withFields));
export const getDocType = (code: string) => request<DocTypeView>(dt(code)).then(withFields);
export const createDocType = (body: { title: string; description?: string; kb_ids?: string[] }) => request<DocTypeView>("/doc-types", { body });
export const updateDocType = (code: string, body: { title?: string; description?: string; fields?: SheetField[] }) =>
  request<DocTypeView>(dt(code), { method: "PATCH", body });
export const deleteDocType = (code: string, detach = false) => request(dt(code) + (detach ? "?detach=true" : ""), { method: "DELETE" });
export const copyDocType = (code: string) => request<DocTypeView>(dt(code) + "/copy", { method: "POST" });
export const setDocTypeKBs = (code: string, kbIds: string[]) => request(dt(code) + "/kbs", { method: "PUT", body: { kb_ids: kbIds } });
export const listSamples = (code: string) => request<{ data: DocTypeSample[] }>(dt(code) + "/samples").then((r) => r.data ?? []);
export function uploadSamples(code: string, files: File[], onProgress?: (p: number) => void) {
  const fd = new FormData();
  for (const f of files) fd.append("file", f, f.name);
  return uploadForm<{ data: DocTypeSample[] }>(dt(code) + "/samples", fd, onProgress).then((r) => r.data ?? []);
}
export const getSample = (code: string, id: string) => request<DocTypeSample>(dt(code) + `/samples/${id}`);
export const deleteSample = (code: string, id: string) => request(dt(code) + `/samples/${id}`, { method: "DELETE" });
export const samplePagePath = (code: string, id: string, n: number) => dt(code) + `/samples/${id}/pages/${n}/image`;
export const saveTruth = (code: string, id: string, values: Record<string, string>) =>
  request<DocTypeSample>(dt(code) + `/samples/${id}/truth`, { method: "PUT", body: { values } });
export const runSamples = (code: string, sampleIds?: string[], version?: number) =>
  request<DocTypeJob>(dt(code) + "/samples/run", { body: { sample_ids: sampleIds, version } });
export const listPrompts = (code: string) =>
  request<{ data: DocTypePrompt[] }>(dt(code) + "/prompts").then((r) => (r.data ?? []).map((p) => ({ ...p, fields: p.fields ?? [], rules: p.rules ?? [], shots: p.shots ?? [] })));
export const createPrompt = (code: string, body: { instruction: string; rules: string[]; shots: PromptShot[]; fields?: SheetField[]; note?: string; use?: boolean }) =>
  request<DocTypePrompt>(dt(code) + "/prompts", { body });
export const setCurrentPrompt = (code: string, version: number) => request(dt(code) + "/prompts/current", { method: "PUT", body: { version } });
export const deletePrompt = (code: string, version: number) => request(dt(code) + `/prompts/${version}`, { method: "DELETE" });
export const getPromptText = (code: string, version: number) => request<{ text: string }>(dt(code) + `/prompts/${version}/text`).then((r) => r.text);
export const optimizeDocType = (code: string, version = 0) => request<DocTypeJob>(dt(code) + "/optimize", { body: { version } });
export const listDocTypeJobs = (code: string) => request<{ data: DocTypeJob[] }>(dt(code) + "/jobs").then((r) => r.data ?? []);
export const getDocTypeJob = (code: string, id: string) => request<DocTypeJob>(dt(code) + `/jobs/${id}`);
export const dropDocTypeJob = (code: string, id: string) => request(dt(code) + `/jobs/${id}`, { method: "DELETE" });
