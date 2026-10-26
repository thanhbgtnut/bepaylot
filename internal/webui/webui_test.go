package webui

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/route"
)

func serve(t *testing.T, base string) *route.Engine {
	t.Helper()
	ui := fstest.MapFS{
		"index.html":    {Data: []byte(`<!doctype html><html><head><script src="./assets/app.js"></script></head></html>`)},
		"assets/app.js": {Data: []byte("app")},
	}
	e := route.NewEngine(config.NewOptions(nil))
	e.NoRoute(Handler(ui, base))
	return e
}

// Under a context root the app answers only below it, its index.html names
// the root in <base href>, and the domain root redirects into it.
func TestHandlerContextRoot(t *testing.T) {
	e := serve(t, "/bepaylot")
	for _, tc := range []struct {
		path, want string
		code       int
	}{
		{"/bepaylot/cases/x", `<base href="/bepaylot/">`, 200},
		{"/bepaylot/", `<base href="/bepaylot/">`, 200},
		{"/bepaylot/assets/app.js", "app", 200},
		{"/bepaylot/v1/nope", "not_found_error", 404},
		{"/other-web/x", "not_found_error", 404},
	} {
		w := ut.PerformRequest(e, "GET", tc.path, nil).Result()
		if w.StatusCode() != tc.code || !strings.Contains(string(w.Body()), tc.want) {
			t.Errorf("%s: %d %q, want %d containing %q", tc.path, w.StatusCode(), w.Body(), tc.code, tc.want)
		}
	}
	for _, p := range []string{"/", "/bepaylot"} {
		w := ut.PerformRequest(e, "GET", p, nil).Result()
		if w.StatusCode() != 302 || string(w.Header.Peek("Location")) != "/bepaylot/" {
			t.Errorf("%s: %d → %q, want 302 → /bepaylot/", p, w.StatusCode(), w.Header.Peek("Location"))
		}
	}
}

// At the domain root (no context root) the base is "/".
func TestHandlerNoContextRoot(t *testing.T) {
	w := ut.PerformRequest(serve(t, ""), "GET", "/documents", nil).Result()
	if w.StatusCode() != 200 || !strings.Contains(string(w.Body()), `<base href="/">`) {
		t.Fatalf("got %d %q", w.StatusCode(), w.Body())
	}
}
