package tools

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/types"
	"github.com/thanhenti/bepaylot/internal/types/interfaces"
)

// segmentRef is d<n>.s<k>: the k-th document of the file d<n> (§8.2).
var segmentRef = regexp.MustCompile(`^(d\d+)\.s(\d+)$`)

// SetFieldTools adds kb_save_fields and kb_get_fields to the case tools
// (§8.2). Call after SetKnowledgeTools, which resets the case tools.
func (r *Registry) SetFieldTools(searcher interfaces.Searcher, fields interfaces.FieldService) error {
	type fieldArg struct {
		Key        string   `json:"key" jsonschema:"required" jsonschema_description:"Field name in snake_case, e.g. so_tien_vay."`
		Ord        int      `json:"ord,omitempty" jsonschema_description:"Index when a field has several values (one per list row); default 0."`
		Value      any      `json:"value" jsonschema:"required" jsonschema_description:"The value: string, number, bool, date (YYYY-MM-DD) or a small object."`
		ValueType  string   `json:"value_type,omitempty" jsonschema:"enum=string,enum=number,enum=money,enum=date,enum=bool,enum=json"`
		Confidence float64  `json:"confidence" jsonschema:"required" jsonschema_description:"Your confidence in the value, 0-1."`
		Evidence   []string `json:"evidence" jsonschema:"required" jsonschema_description:"citation_id of the lines the value is read from (doc:…:p<n>:l<a>-<b>, refs d<n> allowed)."`
		Note       string   `json:"note,omitempty" jsonschema_description:"Required when the value is derived (a sum, a ratio) and not written in the quoted lines: how it was computed."`
	}
	type saveArgs struct {
		DocumentID string     `json:"document_id,omitempty" jsonschema_description:"The file the fields belong to (document id or ref d<n>). Not needed with segment."`
		Segment    string     `json:"segment,omitempty" jsonschema_description:"One document of a file that holds several (ref d<n>.s<k>, as listed by kb_list_documents): the fields belong to that document."`
		Fields     []fieldArg `json:"fields" jsonschema:"required"`
	}
	save, err := utils.InferTool("kb_save_fields",
		"Store extracted field values of a file of this case, each with the citation_id of the lines it is read from. Values are saved as proposals for a person to review; the result says per field whether it was saved and whether the value appears in the cited lines.",
		func(ctx context.Context, a saveArgs) (map[string]any, error) {
			docRef, seg := strings.TrimSpace(a.DocumentID), (*uuid.UUID)(nil)
			if m := segmentRef.FindStringSubmatch(strings.TrimSpace(a.Segment)); m != nil {
				docRef = m[1]
				_, d, err := docArg(ctx, searcher, docRef)
				if err != nil {
					return nil, err
				}
				k, _ := strconv.Atoi(m[2])
				id, err := fields.SegmentAt(ctx, d, k)
				if err != nil {
					return nil, err
				}
				seg = &id
			} else if strings.TrimSpace(a.Segment) != "" {
				return nil, errors.New("segment must look like d<n>.s<k>")
			}
			if docRef == "" {
				return nil, errors.New("give document_id or segment")
			}
			sc, doc, err := docArg(ctx, searcher, docRef)
			if err != nil {
				return nil, err
			}
			if len(a.Fields) == 0 {
				return nil, errors.New("fields is empty")
			}
			in := make([]types.FieldInput, 0, len(a.Fields))
			for _, f := range a.Fields {
				cites := make([]string, 0, len(f.Evidence))
				for _, c := range f.Evidence {
					c = strings.TrimSpace(c)
					if m := refCitation.FindStringSubmatch(c); m != nil {
						if id, err := sc.resolveDoc(ctx, searcher, m[1]); err == nil {
							c = "doc:" + id.String() + m[2]
						}
					}
					cites = append(cites, c)
				}
				in = append(in, types.FieldInput{DocumentID: doc, SegmentID: seg, Key: f.Key, Ord: f.Ord, Value: f.Value, ValueType: f.ValueType,
					Confidence: f.Confidence, Citations: cites, Note: f.Note})
			}
			var sid *uuid.UUID
			if id, ok := RunSessionIDFrom(ctx); ok {
				sid = &id
			}
			return map[string]any{"fields": fields.SaveAgentFields(ctx, sc.Owner, sc.CaseID, sid, in)}, nil
		})
	if err != nil {
		return err
	}
	type getArgs struct {
		DocumentID string `json:"document_id,omitempty" jsonschema_description:"Only this file (document id or ref d<n>)."`
		Key        string `json:"key,omitempty" jsonschema_description:"Only this field."`
	}
	get, err := utils.InferTool("kb_get_fields",
		"Fields already stored for this case. current: values a person confirmed (the only values to treat as true); unreviewed: proposals nobody has reviewed yet. Each with its evidence.",
		func(ctx context.Context, a getArgs) (map[string]any, error) {
			sc, err := caseScope(ctx)
			if err != nil {
				return nil, err
			}
			var doc *uuid.UUID
			if strings.TrimSpace(a.DocumentID) != "" {
				_, id, err := docArg(ctx, searcher, a.DocumentID)
				if err != nil {
					return nil, err
				}
				doc = &id
			}
			list, err := fields.CaseFields(ctx, sc.CaseID, doc, strings.TrimSpace(a.Key))
			if err != nil {
				return nil, sc.alive(ctx, searcher, err)
			}
			current, unreviewed := []types.Field{}, []types.Field{}
			for _, f := range list {
				if f.Status == types.FieldConfirmed {
					current = append(current, f)
				} else {
					unreviewed = append(unreviewed, f)
				}
			}
			return map[string]any{"current": current, "unreviewed": unreviewed}, nil
		})
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.knowledge == nil {
		return errors.New("SetFieldTools: call SetKnowledgeTools first")
	}
	for _, t := range []tool.InvokableTool{save, get} {
		e, err := newEntry(context.Background(), t)
		if err != nil {
			return err
		}
		r.knowledge[e.Name] = e
	}
	return nil
}
