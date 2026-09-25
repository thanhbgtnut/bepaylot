//go:build pdfium_cgo

// Command pdfium-worker is the child process go-pdfium's multi_threaded mode
// starts for every PDFium instance (§5.7). It needs cgo and libpdfium; build
// with: CGO_ENABLED=1 go build -tags pdfium_cgo ./cmd/pdfium-worker
package main

import "github.com/klippa-app/go-pdfium/multi_threaded/worker"

func main() {
	worker.StartWorker(nil)
}
