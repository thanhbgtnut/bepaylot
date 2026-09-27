package document_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/application/service/document"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/parser/pdf/pdftest"
	"github.com/thanhenti/bepaylot/internal/testkit"
	"github.com/thanhenti/bepaylot/internal/types"
	"github.com/thanhenti/bepaylot/internal/types/interfaces"
	"github.com/thanhenti/bepaylot/internal/webhook"
)

// receiver is a callback endpoint answering with scripted status codes.
type receiver struct {
	mu      sync.Mutex
	codes   []int // per call; the last repeats
	calls   []call
	srv     *httptest.Server
	secret  string
	badSigs int
}

type call struct {
	header http.Header
	body   map[string]any
}

func newReceiver(t *testing.T, secret string, codes ...int) *receiver {
	r := &receiver{codes: codes, secret: secret}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		ts, _ := strconv.ParseInt(req.Header.Get(webhook.HeaderTimestamp), 10, 64)
		r.mu.Lock()
		if r.secret != "" && req.Header.Get(webhook.HeaderSignature) != webhook.Sign(r.secret, ts, raw) {
			r.badSigs++
		}
		r.calls = append(r.calls, call{header: req.Header.Clone(), body: body})
		code := r.codes[min(len(r.calls)-1, len(r.codes)-1)]
		r.mu.Unlock()
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`{"received":true}`))
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *receiver) n() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func harness(t *testing.T, maxAttempts int) *testkit.Harness {
	return testkit.New(t, func(c *config.Config) {
		c.Callback.AllowPrivateNetworks = true // httptest listens on 127.0.0.1
		c.Callback.MaxAttempts = maxAttempts
		c.Callback.Backoff = []time.Duration{5 * time.Millisecond}
		c.Callback.SigningSecret = "test-secret"
	})
}

func upload(h *testkit.Harness, kb uuid.UUID, name, callbackURL string) (*document.UploadResult, error) {
	up, err := h.Docs.BeginUpload(h.Ctx, h.Owner.ID, kb, false)
	if err != nil {
		return nil, err
	}
	up.SetCase(interfaces.CaseRef{Code: "HS-CB-1", Create: true})
	up.SetSharedMetadata(map[string]any{"loai": "GCN"})
	if err := up.AddFile(h.Ctx, name, "", bytes.NewReader(pdftest.Build([]pdftest.Page{{"Giay chung nhan " + name, "Ma so 0101"}}, pdftest.Options{}))); err != nil {
		return nil, err
	}
	up.SetCallbackURL(callbackURL) // after the file, as a multipart form may send it
	return up.Finish(h.Ctx)
}

// settle drains (delayed retries included) until the document's latest
// delivery leaves pending.
func settle(t *testing.T, h *testkit.Harness, doc uuid.UUID) []types.DocumentCallback {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		h.Drain()
		list, err := h.Docs.Callbacks(h.Ctx, h.Owner.ID, doc)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) > 0 && list[0].State != types.CallbackPending {
			return list
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("callback did not settle")
	return nil
}

func TestCallbackRetriesUntilDelivered(t *testing.T) {
	h := harness(t, 5)
	rcv := newReceiver(t, "test-secret", 500, 503, 200)
	kb := h.KB(types.KBConfig{}, nil)
	res, err := upload(h, kb.ID, "a.pdf", rcv.srv.URL+"/hook?src=bepaylot")
	if err != nil {
		t.Fatal(err)
	}
	doc := res.Documents[0].DocumentID
	if res.Documents[0].CallbackURL == "" {
		t.Fatalf("upload result lacks callback_url: %+v", res.Documents[0])
	}
	list := settle(t, h, doc)
	cb := list[0]
	if len(list) != 1 || cb.State != types.CallbackSucceeded || cb.Attempts != 3 || cb.Event != "document.completed" ||
		cb.DeliveredAt == nil || cb.LastStatusCode == nil || *cb.LastStatusCode != 200 || cb.LastError != "" {
		t.Fatalf("delivery = %+v", cb)
	}
	if len(cb.History) != 3 || *cb.History[0].StatusCode != 500 || cb.History[0].Error != "HTTP 500" || *cb.History[1].StatusCode != 503 ||
		*cb.History[2].StatusCode != 200 || cb.History[2].Response != `{"received":true}` {
		t.Fatalf("history = %+v", cb.History)
	}
	if rcv.n() != 3 || rcv.badSigs != 0 {
		t.Fatalf("calls = %d, bad signatures = %d", rcv.n(), rcv.badSigs)
	}
	first, last := rcv.calls[0], rcv.calls[2]
	if first.header.Get(webhook.HeaderDelivery) != cb.ID.String() || last.header.Get(webhook.HeaderDelivery) != cb.ID.String() ||
		last.header.Get(webhook.HeaderAttempt) != "3" || last.header.Get(webhook.HeaderEvent) != "document.completed" {
		t.Fatalf("headers = %v", last.header)
	}
	d := last.body["document"].(map[string]any)
	if last.body["event"] != "document.completed" || d["id"] != doc.String() || d["status"] != "completed" ||
		d["metadata"].(map[string]any)["loai"] != "GCN" || d["case_code"] != "HS-CB-1" || d["case_id"] != res.Case.ID.String() ||
		d["wiki_status"] != "done" || d["page_count"].(float64) != 1 {
		t.Fatalf("body = %v", last.body)
	}
	if got, _ := h.Docs.GetDocument(h.Ctx, doc); got.CallbackURL != rcv.srv.URL+"/hook?src=bepaylot" {
		t.Fatalf("document callback_url = %q", got.CallbackURL)
	}

	// The same status set again (a redelivered task) must not send twice.
	h.Drain()
	if rcv.n() != 3 {
		t.Fatalf("duplicate delivery: %d calls", rcv.n())
	}
}

func TestCallbackFailsAfterMaxAttemptsThenManualRetry(t *testing.T) {
	h := harness(t, 2)
	rcv := newReceiver(t, "test-secret", 500)
	kb := h.KB(types.KBConfig{}, nil)
	res, err := upload(h, kb.ID, "b.pdf", rcv.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	doc := res.Documents[0].DocumentID
	cb := settle(t, h, doc)[0]
	if cb.State != types.CallbackFailed || cb.Attempts != 2 || *cb.LastStatusCode != 500 || cb.LastError != "HTTP 500" || cb.NextAttemptAt != nil {
		t.Fatalf("delivery = %+v", cb)
	}

	rcv.mu.Lock()
	rcv.codes = []int{204}
	rcv.mu.Unlock()
	if _, err := h.Docs.RetryCallback(h.Ctx, h.Owner.ID, doc, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	cb = settle(t, h, doc)[0]
	if cb.State != types.CallbackSucceeded || cb.Attempts != 3 || cb.MaxAttempts != 4 || len(cb.History) != 3 {
		t.Fatalf("after retry = %+v", cb)
	}
}

func TestCallbackIsOptionalAndValidated(t *testing.T) {
	h := harness(t, 3)
	kb := h.KB(types.KBConfig{}, nil)
	res, err := upload(h, kb.ID, "c.pdf", "")
	if err != nil {
		t.Fatal(err)
	}
	h.Drain()
	if list, err := h.Docs.Callbacks(h.Ctx, h.Owner.ID, res.Documents[0].DocumentID); err != nil || len(list) != 0 {
		t.Fatalf("no callback_url must mean no delivery: %v %v", list, err)
	}
	if _, err := h.Docs.RetryCallback(h.Ctx, h.Owner.ID, res.Documents[0].DocumentID, uuid.Nil); !errors.Is(err, document.ErrBadRequest) {
		t.Fatalf("retry without callback: %v", err)
	}

	_, err = upload(h, kb.ID, "d.pdf", "ftp://example.com/hook")
	if !errors.Is(err, document.ErrBadRequest) {
		t.Fatalf("invalid URL err = %v", err)
	}
	docs, _ := h.Docs.ListDocuments(h.Ctx, h.Owner.ID, kb.ID, postgres.DocumentFilter{})
	for _, d := range docs {
		if d.FileName == "d.pdf" {
			t.Fatalf("file with an invalid callback_url was kept: %+v", d)
		}
	}
}

func TestCallbackOnCancelAndPageReparse(t *testing.T) {
	h := harness(t, 3)
	rcv := newReceiver(t, "", 200)
	kb := h.KB(types.KBConfig{}, nil)

	res, err := upload(h, kb.ID, "e.pdf", rcv.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	doc := res.Documents[0].DocumentID
	if err := h.Docs.Cancel(h.Ctx, h.Owner.ID, doc); err != nil {
		t.Fatal(err)
	}
	if cb := settle(t, h, doc)[0]; cb.Event != "document.cancelled" || cb.State != types.CallbackSucceeded {
		t.Fatalf("cancel delivery = %+v", cb)
	}

	res, err = upload(h, kb.ID, "f.pdf", rcv.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	doc = res.Documents[0].DocumentID
	settle(t, h, doc)
	if _, err := h.Docs.Reparse(h.Ctx, h.Owner.ID, doc, document.ReparseRequest{Pages: []int{1}}); err != nil {
		t.Fatal(err)
	}
	h.Drain()
	list := settle(t, h, doc)
	if len(list) != 2 || list[0].Run != 1 || list[1].Run != 0 || list[0].State != types.CallbackSucceeded {
		t.Fatalf("page reparse must send a new delivery: %+v", list)
	}
}
