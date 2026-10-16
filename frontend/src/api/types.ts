// Types mirroring the bepaylot /v1 API (see docs/swagger.yaml).

export interface BBox {
  x0: number;
  y0: number;
  x1: number;
  y1: number;
}

export interface KBConfig {
  parser_engine?: string;
  /** Loại giấy tờ của KB: trang của file được phân loại theo ảnh vào các loại này. */
  doc_types?: DocType[];
  /** Quy tắc đối chiếu chứng từ (§6.9.9); chỉ sửa qua PUT /kbs/:id/checks. */
  checks?: CheckConfig;
}

/** Loại giấy tờ của KB kèm các trường bóc tách từ mỗi giấy tờ loại đó (§6.9.3); cũng là cột của bảng Excel. */
export interface DocType extends ClassLabel {
  fields?: SheetField[];
}

export interface MetadataField {
  key: string;
  type: string;
  description?: string;
  required?: boolean;
  values?: string[];
}

export interface KnowledgeBase {
  id: string;
  name: string;
  description?: string;
  config: KBConfig;
  metadata_schema?: { fields: MetadataField[]; strict?: boolean };
  created_at: string;
  updated_at: string;
}

export type DocStatus =
  | "queued"
  | "splitting"
  | "parsing"
  | "assembling"
  | "indexing"
  | "completed"
  | "partial"
  | "failed"
  | "cancelled"
  | "deleting";

export interface Document {
  id: string;
  kb_id: string;
  case_id: string;
  file_name: string;
  mime_type: string;
  size_bytes: number;
  page_count: number;
  gen: number;
  status: DocStatus;
  parse_status: string;
  index_status: string;
  pages_done: number;
  pages_failed: number;
  engine?: string;
  error?: string;
  metadata?: Record<string, unknown>;
  title?: string;
  summary?: string;
  callback_url?: string;
  created_at: string;
  updated_at: string;
}

export interface DocumentDetail extends Document {
  progress: number;
  failed_pages?: { page_no: number; error: string }[];
}

export interface DocumentPage {
  page_no: number;
  status: string;
  /** Loại giấy tờ Decision API đọc từ ảnh trang (mã trong doc_types của KB). */
  doc_type?: string;
  doc_type_confidence?: number;
  width: number;
  height: number;
  dpi: number;
  text_source: string;
  text_quality: number;
  engine?: string;
  markdown: string;
  text_plain: string;
  is_blank: boolean;
  error?: string;
  ocr_ms: number;
}

export interface PageLine {
  page_no: number;
  line_no: number;
  block_no: number;
  text: string;
  text_ocr?: string;
  text_layer?: string;
  text_source: string;
  confidence: number;
  bbox: BBox;
  in_figure: boolean;
  low_confidence: boolean;
}

export interface PageBlock {
  block_no: number;
  type: string;
  raw_class?: string;
  bbox: BBox;
  text: string;
  html?: string;
  text_source: string;
  is_furniture: boolean;
}

export interface PageView extends DocumentPage {
  lines: PageLine[];
  blocks: PageBlock[];
}

export interface TreeNode {
  id: string;
  parent_id?: string;
  short_id: string;
  level: number;
  ord: number;
  title?: string;
  origin: string;
  /** origin = "doctype": mã loại giấy tờ của node (một giấy tờ trong file). */
  doc_type?: string;
  page_start: number;
  page_end: number;
  summary?: string;
  token_count?: number;
  tree_tokens?: number;
}

export interface LineMatch {
  line_no: number;
  snippet: string;
  bbox: BBox;
  score?: number;
}

export interface PageSearchHit {
  page_no: number;
  score: number;
  hits: LineMatch[];
}

export interface SearchHit {
  document_id: string;
  file_name: string;
  page_no: number;
  lines: number[];
  quote: string;
  citation_id: string;
  bboxes: BBox[];
  metadata?: Record<string, unknown>;
}

export interface UploadResult {
  batch_id: string;
  case: { id: string; code: string; case_type: string; created: boolean };
  documents: { document_id: string; file_name: string; status: string; duplicate: boolean }[];
  rejected: { file_name: string; index: number; errors: string[] }[];
}

export interface EngineInfo {
  name: string;
  default: boolean;
  available: boolean;
  error?: string;
}

// ---- sessions / messages

export interface SessionBrief {
  id: string;
  title: string;
  summary?: string;
  metadata?: Record<string, unknown>;
  case_id?: string;
  template_id?: string;
  created_at: string;
  updated_at: string;
}

export interface OutputBlock {
  type: "text" | "thinking" | "tool_use" | "tool_result" | string;
  text?: string;
  thinking?: string;
  id?: string;
  name?: string;
  input?: unknown;
  tool_use_id?: string;
  content?: unknown;
  is_error?: boolean;
}

export interface TranscriptMessage {
  id: string;
  seq: number;
  role: "user" | "assistant";
  content: OutputBlock[] | string;
  stop_reason?: string;
  created_at: string;
}

// ---- cases

export interface Case {
  id: string;
  kb_id: string;
  code: string;
  case_type: string;
  title?: string;
  status: "open" | "closed";
  metadata?: Record<string, unknown>;
  documents?: Record<string, number>;
  created_at: string;
  updated_at: string;
}

export interface CaseType {
  name: string;
  title?: string;
  code: { pattern?: string; normalize?: string };
}

// ---- case table of contents (§6.6): cards + first tree branches, no LLM

export interface TOCBranch {
  node_id: string;
  title: string;
  page_start: number;
  page_end: number;
  summary?: string;
  has_more?: boolean;
}

export interface TOCDoc {
  ref: string;
  document_id: string;
  file_name: string;
  status: DocStatus;
  page_count: number;
  title?: string;
  summary?: string;
  metadata?: Record<string, unknown>;
  branches?: TOCBranch[];
  tree_tokens: number;
}

export interface CaseTOC {
  case: Case;
  documents: TOCDoc[];
  pending?: { document_id: string; file_name: string; status: DocStatus }[];
  text?: string;
  token_count: number;
  truncated?: boolean;
}

// ---------------------------------------------------------------- auth
export interface AuthUser {
  id: string;
  email: string;
  name: string;
  auth_provider: string;
  has_password: boolean;
  is_admin: boolean;
  // admin | prompt_editor | user (§8.4)
  role?: Role;
  monthly_limit?: number;
  last_login_at?: string;
  created_at: string;
}

export interface AuthTokens {
  access_token: string;
  refresh_token: string;
  token_type: string;
  expires_at: string;
  user: AuthUser;
  is_new_user?: boolean;
}

export interface AuthConfig {
  registration_enabled: boolean;
  password_min_length: number;
  oidc: { enabled: boolean; display_name?: string };
  auth_bypass: boolean;
}

export interface APIKey {
  id: string;
  name: string;
  prefix: string;
  last_used_at?: string;
  revoked_at?: string;
  created_at: string;
  issued_by?: string;
}

// ---- U43–U46: roles, templates, sheets, usage

export type Role = "admin" | "prompt_editor" | "user";
export const canEditPrompts = (u?: AuthUser | null) => u?.role === "admin" || u?.role === "prompt_editor" || !!u?.is_admin;
export const isAdmin = (u?: AuthUser | null) => u?.role === "admin" || !!u?.is_admin;

export interface SheetField {
  key: string;
  label: string;
  value_type?: string;
  // guides the agent when it extracts the field
  description?: string;
}

export interface PromptTemplate {
  id: string;
  kind: "chat";
  slug: string;
  name: string;
  description: string;
  case_type?: string;
  status: "draft" | "published" | "archived";
  current_version: number;
  latest_version: number;
  version: number;
  body?: string;
  created_by_name?: string;
  updated_at: string;
}

export interface Evidence {
  document_id: string;
  file_name?: string;
  page_no: number;
  line_from?: number;
  line_to?: number;
  bbox?: number[];
  quote: string;
  citation_id: string;
}

// One sub-table of a sheet (§6.9.6): the fields of one document type of the
// KB; label "" = the single table of an old sheet (one row per file).
export interface SheetTable {
  label: string;
  title: string;
  description?: string;
  fields: SheetField[];
}

export interface SheetCell {
  field_id?: string;
  ai_field_id?: string;
  ai_value_text: string;
  note?: string;
}

// One row = one document (a segment of a file) of a sub-table (§6.9.6).
export interface SheetRow {
  table: string;
  bundle?: string;
  segment_id?: string;
  segment_no?: number;
  document_id: string;
  file_name?: string;
  page_start?: number;
  page_end?: number;
  cells: Record<string, SheetCell>;
}

export interface Sheet {
  id: string;
  case_id: string;
  name: string;
  status: "pending" | "running" | "done" | "failed";
  filled: number;
  total: number;
  tables: string[];
  columns: SheetTable[];
  rows: SheetRow[];
  error?: string;
  // the Excel form the sheet was built for (§6.9.8)
  export_template_id?: string;
  created_at: string;
  updated_at: string;
}

// ---- U85: Excel forms of a KB (§6.9.8)

export interface ExportColumn {
  col: string;
  header: string;
  note?: string;
}

export interface ExportPlaceholder {
  cell: string;
  text: string;
  note?: string;
}

// What the code found in the form.
export interface ExportLayout {
  sheet: string;
  title?: string;
  header_top: number;
  header_bottom: number;
  data_start: number;
  data_rows: number;
  total_row?: number;
  total_label?: string;
  columns: ExportColumn[];
  placeholders: ExportPlaceholder[];
}

export type ExportKind = "seq" | "field" | "formula" | "blank";

// Formulas use {r} for the row, {first}/{last} for the record rows.
export interface ExportColumnMap {
  col: string;
  kind: ExportKind;
  key?: string;
  formula?: string;
  total?: string;
}

export interface ExportFillMap {
  cell: string;
  doc_type?: string;
  key?: string;
  text?: string;
}

export interface ExportMapping {
  doc_type: string;
  columns: ExportColumnMap[];
  placeholders: ExportFillMap[];
  new_fields?: { doc_type: string; field: SheetField }[];
}

export interface ExportTemplate {
  id: string;
  kb_id: string;
  name: string;
  file_name: string;
  layout: ExportLayout;
  mapping: ExportMapping;
  created_at: string;
  updated_at: string;
}

export interface SheetCellView extends SheetCell {
  value: string;
  confidence: number;
  status?: string;
  source?: string;
  value_matched: boolean;
  field_note?: string;
  evidence: Evidence[];
  edited: boolean;
  calc: boolean;
  low: boolean;
}

export interface SheetRowView extends SheetRow {
  cells_view: Record<string, SheetCellView>;
}

export interface SheetTableView extends SheetTable {
  rows: SheetRowView[];
}

export interface SheetView extends Sheet {
  case_code: string;
  tables_view: SheetTableView[];
}

export interface SheetImportEdit {
  table: string;
  segment_id?: string;
  document_id?: string;
  key: string;
  label: string;
  bundle?: string;
  sheet: string;
  cell: string;
  value: string;
  ai_value: string;
  web_value?: string;
}

export interface SheetImport {
  edits: SheetImportEdit[];
  conflicts: SheetImportEdit[];
  ignored: { sheet: string; cell: string; text: string; reason: string }[];
}

// ---- U48, U49: tách & gom trang (§6.9.7)

export interface ClassLabel {
  name: string;
  title: string;
  description?: string;
}

export interface Segment {
  id?: string;
  document_id: string;
  page_start: number;
  page_end: number;
  label: string;
  proposed_label?: string;
  confidence: number;
  source: "pipeline" | "user";
  needs_review?: boolean;
  bundle?: string;
}

export interface SplitFile {
  document_id: string;
  file_name: string;
  page_count: number;
  status: "none" | "proposed" | "reviewed";
  classify_status: string;
  indexed: boolean;
  marks: Record<string, string>;
  segments: Segment[];
}

export interface SplitView {
  labels: ClassLabel[];
  opens_with: string[];
  files: SplitFile[];
  bundles: string[];
  reviewed: boolean;
}

export interface UsagePeriod {
  spent: number;
  limit: number;
  resets_at?: string;
}

export interface UsageSummary {
  currency: string;
  month: UsagePeriod;
  today: UsagePeriod;
  by_kind: Record<string, number>;
  updated_at: string;
}

export interface AdminUser extends AuthUser {
  is_active: boolean;
  spent: number;
  keys: number;
}

export interface LLMKeyView {
  provider: string;
  kind: string;
  base_url: string;
  masked_key: string;
  overridden: boolean;
  models: string[];
}

// ---- U87: cross-checks of the documents of a case (§6.9.9)

export interface CheckStep {
  id?: string;
  text: string;
  formula?: string;
  ai?: boolean;
  explanation?: string;
}

export interface CheckLink {
  doc_type: string;
  steps: CheckStep[];
}

export interface CheckRule {
  id?: string;
  name: string;
  text: string;
  formula?: string;
  ai?: boolean;
  off?: boolean;
  explanation?: string;
}

export interface CheckConfig {
  anchor: string;
  links: CheckLink[];
  rules: CheckRule[];
}

export interface CompileResult {
  name: string;
  formula?: string;
  ai: boolean;
  explanation: string;
  missing?: string;
}

export interface CheckRef {
  doc: string;
  label: string;
  field_id?: string;
}

export interface CheckDoc {
  id: string;
  document_id: string;
  type: string;
  type_title: string;
  label: string;
  file_name?: string;
  page_start?: number;
  page_end?: number;
}

export interface CheckMember extends CheckDoc {
  source: "rule" | "ai" | "user" | "single";
  reason: string;
  candidates?: string[];
}

export interface CheckOutcome {
  rule_id: string;
  name: string;
  text: string;
  ai?: boolean;
  status: "pass" | "fail" | "unknown" | "error";
  detail?: string;
  failing?: { note: string; refs: CheckRef[] }[];
  missing?: string[];
  refs?: CheckRef[];
}

export interface CheckGroup {
  anchor: CheckDoc;
  members: CheckMember[];
  results: CheckOutcome[];
}

export interface CheckResult {
  anchor: string;
  anchor_title: string;
  groups: CheckGroup[];
  unlinked: CheckMember[];
  counts: { groups: number; pass: number; fail: number; unknown: number; unlinked: number };
  ran_at: string;
}

export interface CheckRun {
  id: string;
  case_id: string;
  sheet_id: string;
  status: "pending" | "running" | "done" | "failed";
  error?: string;
  result?: CheckResult;
  created_at: string;
  updated_at: string;
}

