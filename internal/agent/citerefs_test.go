package agent

import (
	"strings"
	"testing"
)

// Citations written with case refs leave the server as document ids however
// the text is chunked; other text is unchanged.
func TestCiteRefsRewritesRefCitations(t *testing.T) {
	const id1, id2 = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	in := "Giá 400 triệu [doc:d1:p2:l4-9], hoá đơn [doc:d2.n1:p1:l6] và [doc:" + id1 + ":p3:l1]; d9 lạ [doc:d9:p1:l1]. Hết đoạn d"
	want := "Giá 400 triệu [doc:" + id1 + ":p2:l4-9], hoá đơn [doc:" + id2 + ":p1:l6] và [doc:" + id1 + ":p3:l1]; d9 lạ [doc:d9:p1:l1]. Hết đoạn d"
	for _, size := range []int{1, 2, 3, 5, 8, 1000} {
		c := &citeRefs{refs: map[string]string{"d1": id1, "d2": id2}}
		var out strings.Builder
		r := []rune(in)
		for i := 0; i < len(r); i += size {
			out.WriteString(c.feed(string(r[i:min(i+size, len(r))])))
		}
		out.WriteString(c.flush())
		if out.String() != want {
			t.Fatalf("chunk %d:\n got %s\nwant %s", size, out.String(), want)
		}
	}
	if got := (&citeRefs{}).feed("doc:d1:p1"); got != "doc:d1:p1" {
		t.Fatalf("no refs must pass through, got %q", got)
	}
}
