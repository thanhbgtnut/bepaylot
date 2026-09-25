package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/types"
	"github.com/thanhenti/bepaylot/internal/types/interfaces"
)

type fakeSearcher struct {
	interfaces.Searcher
	got types.SearchRequest
}

func (f *fakeSearcher) Search(_ context.Context, req types.SearchRequest) (*types.SearchResponse, error) {
	f.got = req
	return &types.SearchResponse{Hits: []types.SearchHit{{CitationID: "doc:x:p1:l1-1", FileName: "a.pdf", PageNo: 1, Quote: "q"}}}, nil
}

func invoke(t *testing.T, ctx context.Context, s *Session, name, args string) (string, error) {
	t.Helper()
	for _, bt := range s.Executable() {
		info, _ := bt.Info(ctx)
		if info.Name == name {
			return bt.(tool.InvokableTool).InvokableRun(ctx, args)
		}
	}
	t.Fatalf("tool %s not bound", name)
	return "", nil
}

func TestKnowledgeToolsScopeAndFilter(t *testing.T) {
	reg, err := NewRegistry(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	fs := &fakeSearcher{}
	if err := reg.SetKnowledgeTools(fs, nil); err != nil {
		t.Fatal(err)
	}
	plain := reg.NewSession()
	if _, ok := plain.VisibleDescriptions()["kb_search"]; ok {
		t.Fatal("kb_search must not be bound without knowledge bases")
	}
	s := reg.NewSession()
	s.EnableKnowledge()
	if _, ok := s.VisibleDescriptions()["kb_search"]; !ok {
		t.Fatal("kb_search not bound after EnableKnowledge")
	}

	owner, kbA, kbB := uuid.New(), uuid.New(), uuid.New()
	ctx := WithKBScope(context.Background(), KBScope{Owner: owner, KBIDs: []uuid.UUID{kbA}, Filter: types.MetadataFilter{"ma_ho_so": "HS-A"}})
	out, err := invoke(t, ctx, s, "kb_search", `{"query":"chủ hộ","metadata":{"ma_ho_so":"HS-B","loai":"GCN"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if fs.got.OwnerID != owner || len(fs.got.KBIDs) != 1 || fs.got.KBIDs[0] != kbA {
		t.Fatalf("scope not applied: %+v", fs.got)
	}
	if fs.got.Metadata["ma_ho_so"] != "HS-A" || fs.got.Metadata["loai"] != "GCN" {
		t.Fatalf("pinned filter must win and be ANDed: %v", fs.got.Metadata)
	}
	var res struct {
		Hits []kbHit `json:"hits"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || len(res.Hits) != 1 || res.Hits[0].CitationID == "" {
		t.Fatalf("result = %s", out)
	}
	// A KB outside the session scope is refused.
	if _, err := invoke(t, ctx, s, "kb_search", `{"query":"x","kb_ids":["`+kbB.String()+`"]}`); err == nil || !strings.Contains(err.Error(), "not attached") {
		t.Fatalf("foreign KB err = %v", err)
	}
	// Without a scope the tools refuse to run.
	if _, err := invoke(t, context.Background(), s, "kb_search", `{"query":"x"}`); err == nil {
		t.Fatal("kb_search without scope should fail")
	}
}
