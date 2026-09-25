//go:build !pdfium_cgo

// Command pdfium-worker is built only with the pdfium_cgo tag (it links
// libpdfium through cgo). Without the tag this stub explains how to build it;
// use parser.render.mode=webassembly for development without libpdfium.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "pdfium-worker: rebuild with CGO_ENABLED=1 go build -tags pdfium_cgo ./cmd/pdfium-worker (requires libpdfium)")
	os.Exit(2)
}
