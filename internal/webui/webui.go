// Package webui embeds the built web app (frontend/, `make ui`) so one
// server binary serves the API and the UI: every path that is not an API
// route gets a file of the build, or index.html for the app's own routes.
package webui

import (
	"bytes"
	"context"
	"embed"
	"io/fs"
	"mime"
	"path"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

//go:embed all:dist
var dist embed.FS

// FS returns the build, and false when the binary was built without it.
func FS() (fs.FS, bool) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, false
	}
	return sub, true
}

// apiPrefixes are never answered with the app: an unknown API path stays a 404.
var apiPrefixes = []string{"/v1/", "/swagger/", "/healthz", "/readyz", "/openapi.yaml"}

// Handler serves files of ui under the context root base ("" or e.g.
// "/bepaylot"); unknown paths get index.html so client-side routes
// (/cases/…, /chat/…) load the app. index.html gets <base href="{base}/">:
// the build uses relative asset URLs, and the app reads its context root
// from that tag, so one build runs under any context root. Hashed assets are
// cached for a year, index.html never.
func Handler(ui fs.FS, base string) app.HandlerFunc {
	index, _ := fs.ReadFile(ui, "index.html")
	index = bytes.Replace(index, []byte("<head>"), []byte(`<head><base href="`+base+`/">`), 1)
	return func(_ context.Context, c *app.RequestContext) {
		p := string(c.Path())
		if base != "" {
			// The domain root and the bare context root go to the app.
			if p == "/" || p == base {
				c.Redirect(consts.StatusFound, []byte(base+"/"))
				return
			}
			if !strings.HasPrefix(p, base+"/") {
				c.JSON(consts.StatusNotFound, map[string]any{"type": "error", "error": map[string]string{"type": "not_found_error", "message": "not found"}})
				return
			}
			p = strings.TrimPrefix(p, base)
		}
		for _, pre := range apiPrefixes {
			if strings.HasPrefix(p, pre) {
				c.JSON(consts.StatusNotFound, map[string]any{"type": "error", "error": map[string]string{"type": "not_found_error", "message": "not found"}})
				return
			}
		}
		if m := string(c.Method()); m != consts.MethodGet && m != consts.MethodHead {
			c.JSON(consts.StatusNotFound, map[string]any{"type": "error", "error": map[string]string{"type": "not_found_error", "message": "not found"}})
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+p), "/")
		if name == "" {
			name = "index.html"
		}
		data, err := fs.ReadFile(ui, name)
		if err != nil || name == "index.html" {
			name, data = "index.html", index
			if len(data) == 0 {
				c.String(consts.StatusNotFound, "web app not built")
				return
			}
		}
		if strings.HasPrefix(name, "assets/") {
			c.Response.Header.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			c.Response.Header.Set("Cache-Control", "no-cache")
		}
		ct := mime.TypeByExtension(path.Ext(name))
		if ct == "" {
			ct = "application/octet-stream"
		}
		c.Data(consts.StatusOK, ct, data)
	}
}
