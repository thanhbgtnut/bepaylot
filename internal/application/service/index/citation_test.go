package index

import "testing"

func TestParseCitation(t *testing.T) {
	const doc = "doc:e32b31f8-2aad-4e95-a527-03bfea15cca4"
	for _, c := range []struct {
		in           string
		page, lo, hi int
		wantErr      bool
	}{
		{in: doc + ":p3:l2-5", page: 3, lo: 2, hi: 5},
		{in: doc + ":p1:l9", page: 1, lo: 9, hi: 9},
		{in: doc + ":p1:l8-l10", page: 1, lo: 8, hi: 10},
		{in: doc + ":p4", page: 4, lo: -1, hi: -1},
		{in: doc + ":p4:l", wantErr: true},
		{in: "doc:not-a-uuid:p1", wantErr: true},
	} {
		_, page, lo, hi, err := ParseCitation(c.in)
		if (err != nil) != c.wantErr || (err == nil && (page != c.page || lo != c.lo || hi != c.hi)) {
			t.Errorf("%s: page=%d lines=%d-%d err=%v", c.in, page, lo, hi, err)
		}
	}
}
