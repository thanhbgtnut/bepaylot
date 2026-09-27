package handler_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/thanhenti/bepaylot/internal/application/service/cases"
	"github.com/thanhenti/bepaylot/internal/types"
)

// A session is bound to one case for good: another case is 409, the removed
// kb_ids / kb_filter are 422 (N17).
func TestSessionCaseBinding(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	kb, err := e.store.KBs.Create(ctx, types.KnowledgeBase{OwnerID: e.user.ID, Name: "kb"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := e.cases.Create(ctx, e.user.ID, kb.ID, cases.CreateRequest{Code: "rt112233", CaseType: "thanh_toan"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.cases.Create(ctx, e.user.ID, kb.ID, cases.CreateRequest{Code: "RT445566", CaseType: "thanh_toan"})
	if err != nil {
		t.Fatal(err)
	}
	status := func(resp *http.Response) (int, map[string]any) {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return resp.StatusCode, m
	}

	code, body := status(e.do(t, http.MethodPost, "/v1/sessions", map[string]any{"case": map[string]any{"kb_id": kb.ID, "code": " rt112233"}}))
	if code != 201 || body["case_id"] != a.ID.String() {
		t.Fatalf("create bound session: %d %v", code, body)
	}
	sid := body["id"].(string)
	msg := func(meta map[string]any) (int, map[string]any) {
		return status(e.do(t, http.MethodPost, "/v1/messages", map[string]any{"model": "bepaylot-fake-1", "max_tokens": 64,
			"messages": []map[string]any{{"role": "user", "content": "xin chào"}}, "metadata": meta}))
	}
	if code, body := msg(map[string]any{"session_id": sid, "case_id": b.ID.String()}); code != http.StatusConflict {
		t.Fatalf("another case: %d %v", code, body)
	}
	if code, body := msg(map[string]any{"session_id": sid, "kb_ids": []string{kb.ID.String()}}); code != http.StatusUnprocessableEntity {
		t.Fatalf("legacy kb_ids: %d %v", code, body)
	}
	if code, body := msg(map[string]any{"session_id": sid, "case_id": a.ID.String()}); code != 200 {
		t.Fatalf("same case: %d %v", code, body)
	}
	if code, _ := status(e.do(t, http.MethodPatch, "/v1/sessions/"+sid, map[string]any{"case_id": b.ID.String()})); code != http.StatusConflict {
		t.Fatalf("patch case_id: %d", code)
	}

	// An unbound session gets bound by the first message that names a case.
	code, body = msg(map[string]any{"case_id": b.ID.String()})
	if code != 200 {
		t.Fatalf("bind on first message: %d %v", code, body)
	}
	code, list := status(e.do(t, http.MethodGet, "/v1/sessions?case_id="+b.ID.String(), nil))
	if code != 200 || len(list["data"].([]any)) != 1 {
		t.Fatalf("sessions of B: %d %v", code, list)
	}
	// Attachments need a case on the session.
	code, plain := status(e.do(t, http.MethodPost, "/v1/sessions", map[string]any{}))
	if code != 201 {
		t.Fatal(code)
	}
	req, _ := http.NewRequest(http.MethodPost, "http://"+e.addr+"/v1/sessions/"+plain["id"].(string)+"/attachments", nil)
	req.Header.Set("x-api-key", e.apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable && resp.StatusCode != http.StatusConflict {
		t.Fatalf("attachment without case: %d", resp.StatusCode)
	}
}
