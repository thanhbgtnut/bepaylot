package checkexpr

import (
	"strings"
	"testing"
)

func doc(id, typ, label string, kv ...string) Doc {
	d := Doc{ID: id, Type: typ, Label: label, Fields: map[string]FieldValue{}}
	for i := 0; i+1 < len(kv); i += 2 {
		d.Fields[kv[i]] = FieldValue{Text: kv[i+1]}
	}
	return d
}

func types() map[string]TypeInfo {
	f := func(kv ...string) map[string]FieldInfo {
		m := map[string]FieldInfo{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = FieldInfo{Label: kv[i], ValueType: kv[i+1]}
		}
		return m
	}
	return map[string]TypeInfo{
		"hop_dong":   {Title: "Hợp đồng", Fields: f("so_hd", "string", "ben_ban", "string", "ngay_ky", "date", "ngay_het_han", "date", "gia_tri", "money", "stk", "string", "dieu_khoan_tt", "string")},
		"phu_luc":    {Title: "Phụ lục", Fields: f("gia_tri", "money", "stk", "string")},
		"hoa_don":    {Title: "Hoá đơn", Fields: f("so", "string", "ben_ban", "string", "ngay_lap", "date", "tong_tien", "money", "stk", "string", "noi_dung", "string")},
		"de_nghi_tt": {Title: "Đề nghị thanh toán", Fields: f("so_tien", "money", "stk", "string")},
		"bien_ban":   {Title: "Biên bản", Fields: f("ngay", "date")},
		"unc":        {Title: "Uỷ nhiệm chi", Fields: f("so_tien", "money", "stk_nhan", "string")},
	}
}

func scope() Scope {
	hd := doc("hd1", "hop_dong", "HĐ 012/2026", "so_hd", "012/2026/HDMB", "ben_ban", "Công ty TNHH ABC", "ngay_ky", "2026-01-01",
		"ngay_het_han", "2026-12-31", "gia_tri", "600.000.000", "stk", "0123 456 789", "dieu_khoan_tt", "Sau giao hàng")
	return Scope{Types: types(), SingleType: map[string]string{"hop_dong": "hop_dong"}, Single: map[string]Doc{"hop_dong": hd},
		Groups: map[string][]Doc{
			"phu_luc": {doc("pl1", "phu_luc", "PL 01", "gia_tri", "100.000.000", "stk", "0123456789")},
			"hoa_don": {
				doc("h1", "hoa_don", "HĐơn 0001", "ngay_lap", "2026-03-01", "tong_tien", "500.000.000", "stk", "0123456789"),
				doc("h2", "hoa_don", "HĐơn 0002", "ngay_lap", "2027-02-01", "tong_tien", "250.000.000", "stk", "0123456789"),
			},
			"de_nghi_tt": {doc("d1", "de_nghi_tt", "ĐNTT 01", "so_tien", "740.000.000", "stk", "0123456789")},
			"bien_ban":   {},
			"unc":        {doc("u1", "unc", "UNC 01", "so_tien", "740.000.000", "stk_nhan", "999888777")},
		}}
}

func TestUserRules(t *testing.T) {
	sc := scope()
	cases := []struct {
		name, formula, status, contains string
	}{
		{"Q1 ngày trong thời hạn", "BETWEEN(hoa_don.ngay_lap, hop_dong.ngay_ky, hop_dong.ngay_het_han)", Fail, "HĐơn 0002: 01/02/2027 ngoài khoảng"},
		{"Q2 tổng không vượt", "SUM(hoa_don.tong_tien) <= hop_dong.gia_tri + SUM(phu_luc.gia_tri)", Fail, "750.000.000 không ≤ 700.000.000"},
		{"Q3 tài khoản", "SAME(hop_dong.stk, phu_luc.stk, hoa_don.stk, de_nghi_tt.stk, unc.stk_nhan)", Fail, "999888777"},
		{"Q4 sau giao hàng", `IF(hop_dong.dieu_khoan_tt = "Sau giao hàng", COUNT(hoa_don) + COUNT(bien_ban) > 0)`, Pass, ""},
		{"Q5 chuỗi số tiền", "SUM(unc.so_tien) <= SUM(de_nghi_tt.so_tien) AND SUM(de_nghi_tt.so_tien) <= SUM(hoa_don.tong_tien)", Pass, ""},
		{"thiếu dữ liệu", "BETWEEN(bien_ban.ngay, hop_dong.ngay_ky, hop_dong.ngay_het_han)", Unknown, ""},
	}
	for _, c := range cases {
		if _, err := Check(c.formula, sc); err != nil {
			t.Fatalf("%s: check: %v", c.name, err)
		}
		r := Eval(c.formula, sc)
		got := r.Detail
		for _, f := range r.Failing {
			got += " | " + f.Note
		}
		if r.Status != c.status || !strings.Contains(got, c.contains) {
			t.Errorf("%s: %s %q %v", c.name, r.Status, got, r.Missing)
		}
	}
}

func TestMissingFieldIsNotAPass(t *testing.T) {
	sc := scope()
	sc.Groups["hoa_don"][1].Fields["tong_tien"] = FieldValue{}
	r := Eval("SUM(hoa_don.tong_tien) <= hop_dong.gia_tri", sc)
	if r.Status != Unknown || len(r.Missing) == 0 || !strings.Contains(r.Missing[0], "HĐơn 0002") {
		t.Fatalf("%+v", r)
	}
	// false AND unknown is false; true OR unknown is true.
	if r := Eval("1 > 2 AND SUM(hoa_don.tong_tien) > 0", sc); r.Status != Fail {
		t.Fatalf("false AND unknown = %s", r.Status)
	}
	if r := Eval("1 < 2 OR SUM(hoa_don.tong_tien) > 0", sc); r.Status != Pass {
		t.Fatalf("true OR unknown = %s", r.Status)
	}
}

func TestMatchModes(t *testing.T) {
	sc := scope()
	x := doc("h9", "hoa_don", "HĐơn 9", "ben_ban", "CÔNG TY TNHH ABC", "noi_dung", "Thanh toán theo HĐ số 012/2026/HDMB đợt 1")
	sc.Single["x"], sc.SingleType["x"] = x, "hoa_don"
	for f, want := range map[string]string{
		`MATCH(x.ben_ban, hop_dong.ben_ban, "tên")`:                       Pass,
		`CONTAINS(x.noi_dung, hop_dong.so_hd)`:                            Pass,
		`MATCH(x.ben_ban, "Công ty CP XYZ", "tên")`:                       Fail,
		`MATCH(hop_dong.stk, "0123-456-789", "số")`:                       Pass,
		`hop_dong.ngay_het_han = DATE_ADD(hop_dong.ngay_ky, 364, "ngày")`: Pass,
	} {
		if r := Eval(f, sc); r.Status != want {
			t.Errorf("%s = %s %+v", f, r.Status, r)
		}
	}
}

func TestCheckErrors(t *testing.T) {
	sc := scope()
	for f, want := range map[string]string{
		"SUM(hoa_don.khong_co)":   "không có trường",
		"SUM(xyz.a)":              "không có loại giấy tờ",
		"VLOOKUP(hoa_don.so)":     "không có hàm",
		"BETWEEN(hoa_don.so)":     "cần giá trị, cận dưới, cận trên",
		"SUM(hoa_don.tong_tien":   "thiếu dấu đóng ngoặc",
		"hoa_don.tong_tien > 1 1": "thừa",
	} {
		if _, err := Check(f, sc); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", f, err)
		}
	}
}
