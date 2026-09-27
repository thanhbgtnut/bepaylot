// Types mirroring the bepaylot /v1 API (see docs/swagger.yaml).

export interface BBox {
  x0: number;
  y0: number;
  x1: number;
  y1: number;
}

export interface KBConfig {
  parser_engine?: string;
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
  | "enriching"
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
  wiki_status: string;
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
  page_start: number;
  page_end: number;
  summary?: string;
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
  wiki_schema: string;
  wiki_status: "none" | "building" | "ready" | "stale" | "failed";
  wiki_version: number;
  wiki_built_at?: string;
  wiki_docs_covered: number;
  documents?: Record<string, number>;
  created_at: string;
  updated_at: string;
}

export interface CaseType {
  name: string;
  title?: string;
  code: { pattern?: string; normalize?: string };
}

// ---- wiki (one per case)

export type WikiKind = "overview" | "source" | "entity" | "topic" | "note";

export interface WikiAttribute {
  value: unknown;
  footnotes?: number[];
  conflict?: boolean;
  history?: { value: unknown; footnotes?: number[]; citation_id?: string }[];
}

export interface WikiFootnote {
  n: number;
  document_id: string;
  gen: number;
  page_no: number;
  line_from: number;
  line_to: number;
  quote: string;
  citation_id: string;
  status: "valid" | "stale";
  file_name?: string;
}

export interface WikiLink {
  from: string;
  from_title?: string;
  to: string;
  to_title?: string;
  relation?: string;
  attributes?: Record<string, unknown>;
  footnote_n?: number;
}

export interface WikiPage {
  id: string;
  case_id: string;
  slug: string;
  kind: WikiKind;
  entity_type?: string;
  document_id?: string;
  title: string;
  aliases?: string[];
  summary: string;
  content: string;
  attributes?: Record<string, WikiAttribute>;
  version: number;
  last_edit_source: "system" | "user";
  proposed_content?: string;
  proposed_attributes?: Record<string, WikiAttribute>;
  created_at: string;
  updated_at: string;
  footnotes?: WikiFootnote[];
  links_out?: WikiLink[];
  links_in?: WikiLink[];
}

export interface WikiPageRef {
  slug: string;
  title: string;
  kind: WikiKind;
  entity_type?: string;
  summary: string;
  document_id?: string;
  proposed?: boolean;
  updated_at: string;
}

export interface WikiTOC {
  case: Case;
  pages: WikiPageRef[];
  pending?: { document_id: string; file_name: string; status: string; wiki_status: string }[];
  open_lint_issues: number;
  documents_total: number;
}

export interface WikiRevision {
  version: number;
  title: string;
  content: string;
  edit_source: "system" | "user";
  edited_at: string;
}

export interface WikiLogEntry {
  id: number;
  at: string;
  op: string;
  ref: string;
  document_id?: string;
  pages: string[];
  summary: string;
  actor: string;
  llm_calls?: number;
}

export interface WikiLintIssue {
  id: number;
  kind: string;
  page_ids: string[];
  pages?: string[];
  detail: Record<string, unknown>;
  status: "open" | "fixed" | "dismissed";
  found_at: string;
}

export interface WikiGraph {
  nodes: { slug: string; title: string; kind: WikiKind; entity_type?: string }[];
  edges: WikiLink[];
}
