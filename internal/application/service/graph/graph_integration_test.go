package graph_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/thanhenti/bepaylot/internal/parser/pdf/pdftest"
	"github.com/thanhenti/bepaylot/internal/testkit"
	"github.com/thanhenti/bepaylot/internal/types"
)

func TestGraphAndWikiEndToEnd(t *testing.T) {
	h := testkit.New(t)
	kb := h.KB(types.KBConfig{GraphEnabled: true, GraphSchema: "ho_kinh_doanh"}, nil)

	// The script extracts the owner and business from whatever unit mentions
	// them, plus one fabricated person that must be dropped.
	h.LLM.Extract = func(user string) any {
		var ents []map[string]any
		var rels []map[string]any
		if strings.Contains(user, "NGUYEN VAN TINH") {
			ents = append(ents,
				map[string]any{"type": "CaNhan", "name": "NGUYEN VAN TINH", "attributes": map[string]any{"ho_ten": "Nguyen Van Tinh", "so_dinh_danh": "070082001498"}, "evidence": "Ho va ten: NGUYEN VAN TINH"},
				map[string]any{"type": "CaNhan", "name": "TRAN THI B", "attributes": map[string]any{"ho_ten": "Tran Thi B"}, "evidence": "Nguoi dai dien: TRAN THI B"},
			)
		}
		if strings.Contains(user, "VAT LIEU XAY DUNG") {
			ents = append(ents, map[string]any{"type": "HoKinhDoanh", "name": "HO KINH DOANH VAT LIEU XAY DUNG",
				"attributes": map[string]any{"ten": "Vat lieu xay dung", "ma_so": "070082001498", "von_kinh_doanh": "50.000.000"},
				"evidence":   "Ten ho kinh doanh: HO KINH DOANH VAT LIEU XAY DUNG"})
		}
		if strings.Contains(user, "NGUYEN VAN TINH") && strings.Contains(user, "VAT LIEU XAY DUNG") {
			rels = append(rels, map[string]any{"type": "CHU_HO", "source": map[string]string{"type": "CaNhan", "name": "NGUYEN VAN TINH"},
				"target": map[string]string{"type": "HoKinhDoanh", "name": "HO KINH DOANH VAT LIEU XAY DUNG"}, "evidence": "Ho va ten: NGUYEN VAN TINH"})
		}
		return map[string]any{"entities": ents, "relations": rels}
	}

	doc := h.Upload(kb.ID, "gcn.pdf", map[string]any{"ma_ho_so": "HS-A"}, []pdftest.Page{
		{"GIAY CHUNG NHAN", "Ten ho kinh doanh: HO KINH DOANH VAT LIEU XAY DUNG", "Ho va ten: NGUYEN VAN TINH"},
	})
	h.Drain()

	d, err := h.Store.Documents.Get(h.Ctx, doc)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != types.DocCompleted || d.GraphStatus != types.StageDone {
		t.Fatalf("doc status %s graph %s err %q", d.Status, d.GraphStatus, d.Error)
	}
	ents, err := h.Graph.SearchEntities(h.Ctx, h.Owner.ID, kb.ID, "", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 {
		t.Fatalf("entities = %+v (fabricated person must be dropped)", ents)
	}
	persons, _ := h.Graph.SearchEntities(h.Ctx, h.Owner.ID, kb.ID, "nguyễn văn tình", "CaNhan", 5)
	if len(persons) != 1 {
		t.Fatalf("accent-insensitive entity search = %+v", persons)
	}
	detail, err := h.Graph.Entity(h.Ctx, h.Owner.ID, persons[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Relations) != 1 || detail.Relations[0].Type != "CHU_HO" || len(detail.Mentions) == 0 {
		t.Fatalf("detail = %+v", detail)
	}
	if sp := detail.Mentions[0].SourceSpans; len(sp) == 0 || sp[0].Page != 1 || sp[0].BBox.IsZero() {
		t.Fatalf("mention spans = %+v", detail.Mentions[0].SourceSpans)
	}
	nb, err := h.Graph.Neighbors(h.Ctx, h.Owner.ID, persons[0].ID, nil, 2)
	if err != nil || len(nb.Entities) != 2 || len(nb.Relations) != 1 {
		t.Fatalf("neighbors = %+v err %v", nb, err)
	}
	var hkd uuid.UUID
	for _, e := range nb.Entities {
		if e.Type == "HoKinhDoanh" {
			hkd = e.ID
			if e.Attributes["von_kinh_doanh"] != 5e7 {
				t.Fatalf("money not coerced: %v", e.Attributes)
			}
		}
	}
	path, err := h.Graph.Path(h.Ctx, h.Owner.ID, persons[0].ID, hkd, 3)
	if err != nil || len(path.Relations) != 1 {
		t.Fatalf("path = %+v err %v", path, err)
	}

	// Wiki pages with citations and cross links; the index page lists them.
	pages, err := h.Wiki.Pages(h.Ctx, h.Owner.ID, kb.ID)
	if err != nil {
		t.Fatal(err)
	}
	var person, index *types.WikiPage
	for i := range pages {
		switch pages[i].Slug {
		case "nguyen-van-tinh":
			person = &pages[i]
		case "index":
			index = &pages[i]
		}
	}
	if person == nil || index == nil {
		t.Fatalf("pages = %+v", pages)
	}
	if !strings.Contains(person.Content, "[[ho-kinh-doanh-vat-lieu-xay-dung]]") || len(person.OutLinks) != 1 ||
		!strings.Contains(person.Content, "doc:"+doc.String()+":p1:") {
		t.Fatalf("person page:\n%s\nout=%v", person.Content, person.OutLinks)
	}
	// A user edit survives re-extraction.
	if _, err := h.Wiki.Edit(h.Ctx, h.Owner.ID, kb.ID, "nguyen-van-tinh", "", "", "# Sửa tay"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Graph.Rebuild(h.Ctx, h.Owner.ID, kb.ID); err != nil {
		t.Fatal(err)
	}
	h.Drain()
	p, _ := h.Wiki.Page(h.Ctx, h.Owner.ID, kb.ID, "nguyen-van-tinh")
	if p.Content != "# Sửa tay" || p.LastEditSource != "user" {
		t.Fatalf("user edit overwritten: %q (%s)", p.Content, p.LastEditSource)
	}
	revs, _ := h.Wiki.Revisions(h.Ctx, h.Owner.ID, kb.ID, "nguyen-van-tinh")
	if len(revs) < 2 {
		t.Fatalf("revisions = %d", len(revs))
	}
}
