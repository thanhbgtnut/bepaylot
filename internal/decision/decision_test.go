package decision

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thanhenti/bepaylot/internal/types"
)

func jpegOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestClassifyPage(t *testing.T) {
	var got request
	answer := `{"answers":{"document_type":{"type":"choice","choice":"uy_nhiem_chi","probabilities":{"uy_nhiem_chi":0.99,"hop_dong":0.01},"confidence":0.98}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" || r.Method != http.MethodPost {
			t.Errorf("request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		w.Write([]byte(answer))
	}))
	defer srv.Close()

	labels := []types.ClassLabel{{Name: "uy_nhiem_chi", Title: "Ủy nhiệm chi", Description: "Payment order"}, {Name: "hop_dong", Title: "Hợp đồng"}}
	c := New(Config{BaseURL: srv.URL + "/", Model: "m1", Instructions: "Đây là loại chứng từ nào?", MaxSide: 100})
	label, conf, err := c.ClassifyPage(context.Background(), labels, jpegOf(t, 400, 200))
	if err != nil || label != "uy_nhiem_chi" || conf != 0.98 {
		t.Fatalf("got %q %v %v", label, conf, err)
	}
	q := got.Questions["document_type"]
	if got.Model != "m1" || q.Type != "choice" || q.Criteria["uy_nhiem_chi"] != "Ủy nhiệm chi — Payment order" || q.Criteria["hop_dong"] != "Hợp đồng" {
		t.Errorf("payload: %+v", got)
	}
	// The image goes inline as a data URL, downscaled to max_side.
	b64, ok := strings.CutPrefix(got.Images[0], "data:image/jpeg;base64,")
	raw, err := base64.StdEncoding.DecodeString(b64)
	if !ok || err != nil {
		t.Fatalf("image is not a jpeg data URL: %v", err)
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(raw)); err != nil || cfg.Width != 100 || cfg.Height != 50 {
		t.Errorf("image size: %+v %v", cfg, err)
	}

	// A choice outside the labels, or an HTTP error, is an error.
	answer = `{"answers":{"document_type":{"choice":"la","confidence":0.9}}}`
	if _, _, err := c.ClassifyPage(context.Background(), labels, jpegOf(t, 10, 10)); err == nil {
		t.Error("an unknown choice must fail")
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", 500) }))
	defer bad.Close()
	if _, _, err := New(Config{BaseURL: bad.URL}).ClassifyPage(context.Background(), labels, jpegOf(t, 10, 10)); err == nil {
		t.Error("HTTP 500 must fail")
	}
}
