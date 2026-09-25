// Package archtest enforces the module boundaries of spec §3.3 (N9).
package archtest

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mod = "github.com/thanhenti/bepaylot/"

// imports returns the module-internal imports of every non-test Go file under
// dir (recursively), keyed by the importing package directory.
func imports(t *testing.T, dir string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		pkg := filepath.ToSlash(filepath.Dir(path))
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(p, mod) {
				out[pkg] = append(out[pkg], strings.TrimPrefix(p, mod))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Rule 1: a service module never imports another service module; it goes
// through types/interfaces. The pure metadata helper is shared by design.
func TestServicesDoNotImportEachOther(t *testing.T) {
	const svc = "internal/application/service/"
	all := imports(t, "../application/service")
	if len(all) < 5 {
		t.Fatalf("scanned only %d service packages; is the path right?", len(all))
	}
	for pkg, imps := range all {
		own := strings.TrimPrefix(pkg, "../application/service/")
		own = strings.SplitN(own, "/", 2)[0]
		for _, imp := range imps {
			if !strings.HasPrefix(imp, svc) {
				continue
			}
			other := strings.SplitN(strings.TrimPrefix(imp, svc), "/", 2)[0]
			if other != own && other != "metadata" {
				t.Errorf("%s imports service %s; depend on types/interfaces instead", pkg, imp)
			}
		}
	}
}

// Rule 3: parser is a pure library — no database, queue, storage or HTTP.
func TestParserIsPure(t *testing.T) {
	banned := []string{"internal/application/", "internal/queue", "internal/storage", "internal/handler", "internal/router", "internal/container"}
	parsers := imports(t, "../parser")
	if len(parsers) < 4 {
		t.Fatalf("scanned only %d parser packages", len(parsers))
	}
	for pkg, imps := range parsers {
		for _, imp := range imps {
			for _, b := range banned {
				if strings.HasPrefix(imp, b) {
					t.Errorf("%s imports %s; the parser must stay a pure library", pkg, imp)
				}
			}
		}
	}
}

// Modules meet only in the container: services never import the HTTP layer.
func TestServicesDoNotImportHTTP(t *testing.T) {
	for pkg, imps := range imports(t, "../application") {
		for _, imp := range imps {
			if strings.HasPrefix(imp, "internal/handler") || strings.HasPrefix(imp, "internal/router") || strings.HasPrefix(imp, "internal/container") {
				t.Errorf("%s imports %s", pkg, imp)
			}
		}
	}
}
