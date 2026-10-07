package types

import "github.com/google/uuid"

// Worker pools. Each pool is an independent asynq.Server, so concurrency is
// isolated between them (§4.1).
const (
	PoolCore        = "core"
	PoolRender      = "render"
	PoolOCR         = "ocr"
	PoolIndex       = "index"
	PoolMaintenance = "maintenance"
)

// Queue names.
const (
	QueueDefault           = "default"
	QueueInteractive       = "interactive"
	QueueRender            = "render"
	QueueRenderInteractive = "render_interactive"
	QueuePage              = "page"
	QueuePageInteractive   = "page_interactive"
	QueueIndex             = "index"
	QueueIndexInteractive  = "index_interactive"
	QueueMaintenance       = "low"
	QueueCallback          = "callback"
)

// Task types.
const (
	TaskDocumentSplit    = "document:split"
	TaskPageRender       = "page:render"
	TaskPageOCR          = "page:ocr"
	TaskDocumentAssemble = "document:assemble"
	TaskIndexBuild       = "index:build"
	TaskIndexTree        = "index:tree"
	TaskDocumentDelete   = "document:delete"
	TaskCaseDelete       = "case:delete"
	TaskGenCleanup       = "document:gen_cleanup"
	TaskHousekeeping     = "housekeeping:sweep"
	TaskDocumentCallback = "document:callback"
	TaskCaseSheet        = "case:sheet"
)

// QueueDefinition is the single source of truth for queue topology: worker
// servers and the admin inspector both read it.
type QueueDefinition struct {
	Name      string
	Pool      string
	Weight    int
	TaskTypes []string
}

var queueDefinitions = []QueueDefinition{
	{QueueDefault, PoolCore, 1, []string{TaskDocumentSplit, TaskDocumentAssemble}},
	{QueueInteractive, PoolCore, 3, []string{TaskDocumentSplit, TaskDocumentAssemble}},
	{QueueRender, PoolRender, 1, []string{TaskPageRender}},
	{QueueRenderInteractive, PoolRender, 3, []string{TaskPageRender}},
	{QueuePage, PoolOCR, 1, []string{TaskPageOCR}},
	{QueuePageInteractive, PoolOCR, 3, []string{TaskPageOCR}},
	{QueueIndex, PoolIndex, 1, []string{TaskIndexBuild, TaskIndexTree}},
	{QueueIndexInteractive, PoolIndex, 3, []string{TaskIndexBuild, TaskIndexTree, TaskCaseSheet}},
	{QueueMaintenance, PoolMaintenance, 1, []string{TaskDocumentDelete, TaskCaseDelete, TaskGenCleanup, TaskHousekeeping}},
	{QueueCallback, PoolCore, 1, []string{TaskDocumentCallback}},
}

// QueueDefinitions returns a copy of the topology.
func QueueDefinitions() []QueueDefinition {
	out := make([]QueueDefinition, len(queueDefinitions))
	for i, d := range queueDefinitions {
		out[i] = d
		out[i].TaskTypes = append([]string(nil), d.TaskTypes...)
	}
	return out
}

// Pools lists every pool name once, in topology order.
func Pools() []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range queueDefinitions {
		if !seen[d.Pool] {
			seen[d.Pool] = true
			out = append(out, d.Pool)
		}
	}
	return out
}

// QueueWeightsForPool returns the asynq queue weights of one pool.
func QueueWeightsForPool(pool string) map[string]int {
	w := map[string]int{}
	for _, d := range queueDefinitions {
		if d.Pool == pool {
			w[d.Name] = d.Weight
		}
	}
	return w
}

// QueueFor returns the queue a task is enqueued on; interactive picks the
// higher-weight lane of the task's pool when there is one.
func QueueFor(taskType string, interactive bool) string {
	var first string
	for _, d := range queueDefinitions {
		for _, t := range d.TaskTypes {
			if t != taskType {
				continue
			}
			if first == "" {
				first = d.Name
			}
			if interactive && d.Weight > 1 {
				return d.Name
			}
			if !interactive && d.Weight == 1 {
				return d.Name
			}
		}
	}
	return first
}

// DocTaskPayload is the payload of every per-document pipeline task.
type DocTaskPayload struct {
	DocumentID  uuid.UUID `json:"document_id"`
	KBID        uuid.UUID `json:"kb_id"`
	Gen         int       `json:"gen"`
	Interactive bool      `json:"interactive,omitempty"`
	// Pages is the page range of a page:render batch or the page of page:ocr.
	Pages []int `json:"pages,omitempty"`
}

// Task scopes used by dead letters and pending ops.
const (
	ScopeDocument      = "document"
	ScopeCase          = "case"
	ScopeKnowledgeBase = "knowledge_base"
	ScopeUnknown       = "unknown"
)

// SheetTaskPayload is the payload of case:sheet (§6.9.6).
type SheetTaskPayload struct {
	SheetID uuid.UUID `json:"sheet_id"`
	UserID  uuid.UUID `json:"user_id"`
}
