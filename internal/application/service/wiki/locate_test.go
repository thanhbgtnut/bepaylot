package wiki

import "testing"

func TestLocateChecksValuesAgainstTheirLines(t *testing.T) {
	lines := lineTexts{1: {
		1: "HỢP ĐỒNG THUÊ MẶT BẰNG",
		2: "Bên B: Công ty TNHH Anh Dương",
		3: "Mã số thuế: 0101-234-567",
		4: "Vốn kinh doanh: 50.000.000 đồng",
		5: "Địa chỉ: 12 Lê Lợi,",
		6: "Hà Nội",
	}}
	cases := []struct {
		at, value string
		want      loc
		ok        bool
	}{
		{"p1:L2", "Công ty TNHH Anh Dương", loc{1, 2, 2}, true},
		{"p1:L2", "cong ty tnhh anh duong", loc{1, 2, 2}, true}, // accents aside
		{"p1:L3", "0101234567", loc{1, 3, 3}, true},             // digits only
		{"p1:L1", "50000000", loc{1, 4, 4}, true},               // wrong line, same page
		{"p1:L5", "12 Lê Lợi, Hà Nội", loc{1, 5, 6}, true},      // broken over two lines
		{"p1:L4", "999.999.999", loc{}, false},                  // not on the page
		{"p2:L1", "Công ty TNHH Anh Dương", loc{}, false},       // no such page
		{"dòng 2", "Công ty TNHH Anh Dương", loc{}, false},      // no usable citation
		{"p1:L2", "Anh Duong Holdings", loc{}, false},           // a guess
	}
	for _, c := range cases {
		got, ok := locate(lines, c.at, c.value)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("locate(%q, %q) = %+v %v, want %+v %v", c.at, c.value, got, ok, c.want, c.ok)
		}
	}
	if valueText(5e+07) != "50000000" {
		t.Errorf("valueText(5e+07) = %q", valueText(5e+07))
	}
}
