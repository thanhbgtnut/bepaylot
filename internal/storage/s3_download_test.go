package storage

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/thanhenti/bepaylot/internal/config"
)

// Some S3-compatible stores answer ranged GETs with a nonstandard
// Content-Range ("bytes=0-9/10"); Download must not depend on it.
func TestS3DownloadNonstandardContentRange(t *testing.T) {
	const data = "%PDF-1.7 hello bepaylot"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"x"`)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes=0-%d/%d", len(data)-1, len(data)))
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		if r.Method == http.MethodGet {
			w.Write([]byte(data))
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	st, err := NewS3(ctx, config.S3Cfg{Endpoint: srv.URL, Region: "us-east-1", Bucket: "b", AccessKey: "k", SecretKey: "s", UsePathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "a.pdf")
	n, err := st.Download(ctx, "a.pdf", path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if n != int64(len(data)) || string(got) != data {
		t.Fatalf("got %d bytes %q", n, got)
	}
}
