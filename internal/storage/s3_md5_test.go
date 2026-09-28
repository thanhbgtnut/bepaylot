package storage

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/thanhenti/bepaylot/internal/config"
)

func TestS3SendsContentMD5(t *testing.T) {
	var mu sync.Mutex
	var bad []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if len(body) > 0 {
			sum := md5.Sum(body)
			if got, want := r.Header.Get("Content-MD5"), base64.StdEncoding.EncodeToString(sum[:]); got != want {
				mu.Lock()
				bad = append(bad, r.Method+" "+r.URL.String()+": Content-MD5="+got)
				mu.Unlock()
			}
		}
		if r.Method == http.MethodPost {
			w.Write([]byte(`<DeleteResult></DeleteResult>`))
			return
		}
		w.Header().Set("ETag", `"x"`)
	}))
	defer srv.Close()

	ctx := context.Background()
	st, err := NewS3(ctx, config.S3Cfg{Endpoint: srv.URL, Region: "us-east-1", Bucket: "b", AccessKey: "k", SecretKey: "s", UsePathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Put(ctx, "a.txt", strings.NewReader("hello bepaylot"), 14, "text/plain"); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete(ctx, "a.txt", "b.txt"); err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		t.Fatalf("missing/wrong Content-MD5: %v", bad)
	}
}

func TestS3SignsPayloadSHA256OverHTTPS(t *testing.T) {
	var mu sync.Mutex
	var bad []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sum := sha256.Sum256(body)
		if got := r.Header.Get("X-Amz-Content-Sha256"); got != hex.EncodeToString(sum[:]) {
			mu.Lock()
			bad = append(bad, r.Method+" "+r.URL.String()+": X-Amz-Content-Sha256="+got)
			mu.Unlock()
		}
		switch {
		case r.URL.Query().Has("uploads"):
			w.Write([]byte(`<InitiateMultipartUploadResult><UploadId>u</UploadId></InitiateMultipartUploadResult>`))
		case r.Method == http.MethodPost:
			w.Write([]byte(`<CompleteMultipartUploadResult><ETag>"x"</ETag></CompleteMultipartUploadResult>`))
		default:
			w.Header().Set("ETag", `"x"`)
		}
	}))
	defer srv.Close()
	ca := t.TempDir() + "/ca.pem"
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_CA_BUNDLE", ca)

	ctx := context.Background()
	st, err := NewS3(ctx, config.S3Cfg{Endpoint: srv.URL, Region: "us-east-1", Bucket: "b", AccessKey: "k", SecretKey: "s", UsePathStyle: true, PartSizeMB: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Put(ctx, "small.txt", strings.NewReader("hello bepaylot"), 14, "text/plain"); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("x", 20<<20) // above the multipart threshold
	if _, err := st.Put(ctx, "big.bin", strings.NewReader(big), -1, "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		t.Fatalf("payload not signed with SHA-256: %v", bad)
	}
}
