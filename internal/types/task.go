package types

import "github.com/google/uuid"

// Worker pools. Each pool is an independent asynq.Server, so concurrency is
// isolated between them (§4.1).
const (
	PoolCore        = "core"
	PoolRender      = "render"
	PoolOCR         = "ocr"
	PoolIndex       = "index"
	PoolEnrichment  = "enrichment"
	PoolWiki        = "wiki"
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
	QueueGraph             = "graph"
	QueueWiki              = "wiki"
	QueueMaintenance       = "low"
)

// Task types.
const (
	TaskDocumentSplit    = "document:split"
	TaskPageRender       = "page:render"
	TaskPageOCR          = "page:ocr"
	TaskDocumentAssemble = "document:assemble"
	TaskIndexBuild       = "index:build"
	TaskIndexTree        = "index:tree"
	TaskGraphExtract     = "graph:extract"
	TaskGraphResolve     = "graph:resolve"
	TaskWikiIngest       = "wiki:ingest"
	TaskWikiFinalize     = "wiki:finalize"
	TaskDocumentDelete   = "document:delete"
	TaskGenCleanup       = "document:gen_cleanup"
	TaskHousekeeping     = "housekeeping:sweep"
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
	{QueueIndexInteractive, PoolIndex, 3, []string{TaskIndexBuild, TaskIndexTree}},
	{QueueGraph, PoolEnrichment, 1, []string{TaskGraphExtract}},
	{QueueWiki, PoolWiki, 1, []string{TaskGraphResolve, TaskWikiIngest, TaskWikiFinalize}},
	{QueueMaintenance, PoolMaintenance, 1, []string{TaskDocumentDelete, TaskGenCleanup, TaskHousekeeping}},
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
	// Batch numbers graph:extract batches.
	Batch int `json:"batch,omitempty"`
}

// KBTaskPayload is the payload of KB-scoped tasks (wiki, graph resolve).
type KBTaskPayload struct {
	KBID uuid.UUID `json:"kb_id"`
}

// Task scopes used by dead letters and pending ops.
const (
	ScopeDocument      = "document"
	ScopeKnowledgeBase = "knowledge_base"
	ScopeUnknown       = "unknown"
)
