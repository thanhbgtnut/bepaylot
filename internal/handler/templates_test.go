package handler

import (
	"testing"

	"github.com/thanhenti/bepaylot/internal/types"
)

// A sheet template does not depend on a KB or a case type's labels: any
// document type code is a valid sub-table label (U60).
func TestCheckTemplateLabelIndependentOfCaseType(t *testing.T) {
	h := &Handlers{}
	fields := []types.SheetField{{Key: "so_kien", Label: "Số kiện"}}
	table := func(label string) []types.SheetTable {
		return []types.SheetTable{{Label: label, Title: "Bảng", Fields: fields}}
	}
	for _, label := range []string{"packing", ""} {
		if msg := h.checkTemplate(types.TemplateSheet, "prompt", "default", table(label)); msg != "" {
			t.Errorf("label %q: %s", label, msg)
		}
	}
	for _, label := range []string{types.LabelOther, types.LabelUnknown, "Packing List"} {
		if msg := h.checkTemplate(types.TemplateSheet, "prompt", "default", table(label)); msg == "" {
			t.Errorf("label %q: want an error", label)
		}
	}
}
