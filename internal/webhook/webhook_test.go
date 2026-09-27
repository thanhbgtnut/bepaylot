package webhook

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestValidateURL(t *testing.T) {
	for _, c := range []struct {
		url          string
		allowPrivate bool
		ok           bool
	}{
		{"https://hooks.example.com/bepaylot?x=1", false, true},
		{"http://example.com", false, true},
		{"ftp://example.com/x", false, false},
		{"/relative", false, false},
		{"https://user:pw@example.com", false, false},
		{"http://localhost:9000/cb", false, false},
		{"http://localhost:9000/cb", true, true},
		{"http://127.0.0.1/cb", false, false},
		{"http://10.1.2.3/cb", false, false},
		{"http://169.254.169.254/latest/meta-data", false, false},
		{"http://[::1]/cb", false, false},
		{"http://100.64.0.1/cb", false, false},
	} {
		err := ValidateURL(c.url, c.allowPrivate)
		if (err == nil) != c.ok {
			t.Errorf("ValidateURL(%q, %v) = %v, want ok=%v", c.url, c.allowPrivate, err, c.ok)
		}
	}
}

func TestSendSignsAndReportsStatus(t *testing.T) {
	var got http.Header
	var body []byte
	code := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, body = r.Header.Clone(), must(io.ReadAll(r.Body))
		w.WriteHeader(code)
		_, _ = w.Write([]byte("thanks"))
	}))
	defer srv.Close()
	c := New(Config{Secret: "s3cret", AllowPrivate: true, Timeout: 2 * time.Second})
	sent := time.Unix(1790000000, 0)
	res := c.Send(context.Background(), Delivery{URL: srv.URL, Event: "document.completed", ID: "d-1", Attempt: 2, Body: []byte(`{"a":1}`), SentAt: sent})
	if !res.OK() || res.Response != "thanks" {
		t.Fatalf("result = %+v", res)
	}
	if string(body) != `{"a":1}` || got.Get("Content-Type") != "application/json" || got.Get(HeaderEvent) != "document.completed" ||
		got.Get(HeaderDelivery) != "d-1" || got.Get(HeaderAttempt) != "2" || got.Get(HeaderTimestamp) != strconv.FormatInt(sent.Unix(), 10) {
		t.Fatalf("headers = %v body = %s", got, body)
	}
	if got.Get(HeaderSignature) != Sign("s3cret", sent.Unix(), body) {
		t.Fatalf("signature = %q", got.Get(HeaderSignature))
	}

	code = http.StatusInternalServerError
	if res := c.Send(context.Background(), Delivery{URL: srv.URL, Body: []byte(`{}`)}); res.OK() || res.StatusCode != 500 || res.Error() != "HTTP 500" {
		t.Fatalf("500 result = %+v", res)
	}
	code = http.StatusFound // redirects are not followed and do not count as success
	if res := c.Send(context.Background(), Delivery{URL: srv.URL, Body: []byte(`{}`)}); res.OK() || res.StatusCode != 302 {
		t.Fatalf("302 result = %+v", res)
	}
}

func TestSendBlocksPrivateAtDial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	res := New(Config{}).Send(context.Background(), Delivery{URL: srv.URL, Body: []byte(`{}`)})
	if !errors.Is(res.Err, ErrPrivateAddress) {
		t.Fatalf("err = %v, want ErrPrivateAddress", res.Err)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
