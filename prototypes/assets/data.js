// Dữ liệu mẫu cho prototype tĩnh. Hình dạng theo frontend/src/api/types.ts,
// rút gọn những trường màn hình không dùng.
(function () {
  const DOC_TYPES = [
    { name: "hop_dong", title: "Hợp đồng mua bán", fields: [
      ["so_hop_dong", "Số hợp đồng", "string", "Ở đầu trang 1, sau chữ “Số:”"],
      ["ngay_ky", "Ngày ký", "date", "Dạng dd/mm/yyyy"],
      ["ben_ban", "Bên bán", "string", "Tên pháp nhân bên A"],
      ["ben_mua", "Bên mua", "string", "Tên pháp nhân bên B"],
      ["gia_tri", "Giá trị hợp đồng", "money", "Tổng giá trị đã gồm VAT"],
      ["tai_khoan", "Tài khoản bên bán", "string", "Số tài khoản nhận thanh toán của bên bán"],
      ["thoi_han", "Thời hạn", "date", "Ngày hết hạn hợp đồng"],
    ] },
    { name: "hoa_don", title: "Hoá đơn GTGT", fields: [
      ["so_hoa_don", "Số hoá đơn", "string", ""],
      ["ngay_hoa_don", "Ngày hoá đơn", "date", ""],
      ["ben_ban", "Bên bán", "string", ""],
      ["tong_tien", "Tổng tiền thanh toán", "money", "Dòng “Tổng cộng tiền thanh toán”"],
    ] },
    { name: "unc", title: "Uỷ nhiệm chi", fields: [
      ["so_unc", "Số UNC", "string", "Số chứng từ ở đầu trang"],
      ["ngay", "Ngày", "date", "Ngày lập uỷ nhiệm chi"],
      ["ben_thu_huong", "Bên thụ hưởng", "string", "Tên đơn vị nhận tiền"],
      ["tk_nhan", "Tài khoản nhận", "string", "Số tài khoản bên thụ hưởng"],
      ["so_tien", "Số tiền", "money", ""],
      ["noi_dung", "Nội dung", "string", "Nội dung thanh toán"],
    ] },
    { name: "bien_ban", title: "Biên bản bàn giao", fields: [
      ["ngay_ban_giao", "Ngày bàn giao", "date", ""],
      ["hang_hoa", "Hàng hoá", "string", ""],
    ] },
    { name: "bctc", title: "Báo cáo tài chính", fields: [
      ["nam", "Năm tài chính", "number", ""],
      ["doanh_thu", "Doanh thu thuần", "money", "Mã số 10, Báo cáo KQKD"],
      ["loi_nhuan", "Lợi nhuận sau thuế", "money", "Mã số 60"],
      ["tong_tai_san", "Tổng tài sản", "money", "Mã số 270"],
    ] },
    { name: "gcn_dkdn", title: "Giấy CN đăng ký doanh nghiệp", fields: [] },
  ].map((t) => ({ ...t, fields: t.fields.map(([key, label, value_type, description]) => ({ key, label, value_type, description })) }));

  const KBS = [
    { id: "kb1", name: "Tín dụng doanh nghiệp", description: "Hồ sơ vay vốn lưu động và trung dài hạn của khách hàng doanh nghiệp", config: { doc_types: DOC_TYPES.slice() } },
    { id: "kb2", name: "Hồ sơ cá nhân", description: "Vay tiêu dùng, thế chấp nhà", config: { doc_types: DOC_TYPES.slice(4) } },
  ];

  const CASES = [
    { id: "c1", kb_id: "kb1", code: "RT112233", title: "Công ty TNHH Minh An — vay vốn lưu động 12 tỷ", case_type: "vay_von_luu_dong", status: "open" },
    { id: "c2", kb_id: "kb1", code: "RT112240", title: "CTCP Thép Bắc Việt — bổ sung hạn mức", case_type: "vay_von_luu_dong", status: "open" },
    { id: "c3", kb_id: "kb1", code: "RT112198", title: "Hợp tác xã Đồng Tâm", case_type: "vay_trung_han", status: "closed" },
    { id: "c0", kb_id: "kb1", code: "_UNASSIGNED", title: "", case_type: "", status: "open" },
  ];

  // Nội dung trang: một tiêu đề và các dòng thân. Toạ độ được dựng ở app.js.
  const P = (doc_type, title, body, extra = {}) => ({ doc_type, title, body, ...extra });
  const HD = [
    P("hop_dong", "HỢP ĐỒNG MUA BÁN HÀNG HOÁ", ["Số: 25/2026/HĐMB-MA", "Hôm nay, ngày 15 tháng 03 năm 2026, tại Hà Nội, chúng tôi gồm:", "BÊN BÁN (Bên A): CÔNG TY CP THÉP HOÀ PHÁT NAM", "Mã số thuế: 0101234567 · Tài khoản: 1903 4567 8901 23 tại Techcombank", "BÊN MUA (Bên B): CÔNG TY TNHH MINH AN", "Mã số thuế: 0109876543 · Đại diện: Ông Trần Văn Minh — Giám đốc"]),
    P("hop_dong", "ĐIỀU 1. HÀNG HOÁ, SỐ LƯỢNG, GIÁ", ["Thép cuộn cán nóng SS400, dày 3,0mm — 420 tấn", "Đơn giá: 17.500.000 đồng/tấn (chưa VAT)", "Thành tiền: 7.350.000.000 đồng", "Thuế GTGT 10%: 735.000.000 đồng", "Tổng giá trị hợp đồng: 8.085.000.000 đồng", "Bằng chữ: Tám tỷ không trăm tám mươi lăm triệu đồng chẵn"]),
    P("hop_dong", "ĐIỀU 2. GIAO HÀNG VÀ THANH TOÁN", ["Giao hàng làm 3 đợt từ 01/04/2026 đến 30/06/2026", "Thanh toán bằng chuyển khoản trong 30 ngày kể từ ngày giao hàng", "Bên mua thanh toán theo hoá đơn GTGT và biên bản bàn giao từng đợt", "Thời hạn hợp đồng: đến hết ngày 31/12/2026"], { low: [4] }),
    P("hop_dong", "ĐIỀU 3. TRÁCH NHIỆM CÁC BÊN", ["Bên A giao hàng đúng chủng loại, chất lượng theo tiêu chuẩn JIS G3101", "Bên B nhận hàng, kiểm tra và ký biên bản bàn giao", "Phạt chậm thanh toán: 0,05%/ngày trên số tiền chậm trả"]),
    P("hop_dong", "PHỤ LỤC 01", ["Điều chỉnh khối lượng đợt 3: tăng thêm 30 tấn", "Giá trị điều chỉnh: 577.500.000 đồng (đã gồm VAT)", "Các điều khoản khác giữ nguyên"], { vlm: [1] }),
    P("hop_dong", "ĐẠI DIỆN CÁC BÊN", ["ĐẠI DIỆN BÊN A", "(Ký, đóng dấu)", "ĐẠI DIỆN BÊN B", "Trần Văn Minh"], { vlm: [1, 2, 3, 4] }),
  ];
  const HDUNC = [
    P("hoa_don", "HOÁ ĐƠN GIÁ TRỊ GIA TĂNG", ["Ký hiệu: 1C26THP · Số: 0001523", "Ngày 05 tháng 04 năm 2026", "Đơn vị bán: CÔNG TY CP THÉP HOÀ PHÁT NAM", "Người mua: CÔNG TY TNHH MINH AN", "Cộng tiền hàng: 2.450.000.000", "Tổng cộng tiền thanh toán: 2.695.000.000"]),
    P("unc", "UỶ NHIỆM CHI", ["Số: UNC-0412 · Ngày: 06/04/2026", "Đơn vị trả tiền: CÔNG TY TNHH MINH AN", "Đơn vị thụ hưởng: CÔNG TY CP THÉP HOÀ PHÁT NAM", "Số tài khoản: 1903 4567 8901 23 — Techcombank", "Số tiền: 2.695.000.000 đồng", "Nội dung: TT HĐ 0001523 theo HĐMB 25/2026"]),
    P("bien_ban", "BIÊN BẢN BÀN GIAO HÀNG HOÁ", ["Đợt 1 — ngày 04/04/2026", "Theo hợp đồng số 25/2026/HĐMB-MA", "Hàng hoá: Thép cuộn SS400 — 140 tấn", "Hai bên thống nhất số lượng và chất lượng"]),
    P("hoa_don", "HOÁ ĐƠN GIÁ TRỊ GIA TĂNG", ["Ký hiệu: 1C26THP · Số: 0001687", "Ngày 12 tháng 05 năm 2026", "Đơn vị bán: CÔNG TY CP THÉP HOÀ PHÁT NAM", "Người mua: CÔNG TY TNHH MINH AN", "Cộng tiền hàng: 2.450.000.000", "Tổng cộng tiền thanh toán: 2.695.000.000"]),
    P("unc", "UỶ NHIỆM CHI", ["Số: UNC-0533 · Ngày: 14/05/2026", "Đơn vị trả tiền: CÔNG TY TNHH MINH AN", "Đơn vị thụ hưởng: CÔNG TY CP THÉP HOÀ PHÁT NAM", "Số tài khoản: 1903 4567 8901 99 — Techcombank", "Số tiền: 2.695.000.000 đồng", "Nội dung: TT HĐ 0001687"], { low: [4] }),
    P("hoa_don", "HOÁ ĐƠN GIÁ TRỊ GIA TĂNG", ["Ký hiệu: 1C26THP · Số: 0001802", "Ngày 28 tháng 06 năm 2026", "Đơn vị bán: CÔNG TY CP THÉP HOÀ PHÁT NAM", "Người mua: CÔNG TY TNHH MINH AN", "Cộng tiền hàng: 2.975.000.000", "Tổng cộng tiền thanh toán: 3.272.500.000"]),
    P("unc", "UỶ NHIỆM CHI", ["Số: UNC-0611 · Ngày: 30/06/2026", "Đơn vị trả tiền: CÔNG TY TNHH MINH AN", "Đơn vị thụ hưởng: CÔNG TY TNHH LOGISTICS SAO VIỆT", "Số tài khoản: 0021 0003 4455 — Vietcombank", "Số tiền: 186.000.000 đồng", "Nội dung: Cước vận chuyển tháng 6"]),
    P("other", "", [], { blank: true }),
    P("bien_ban", "BIÊN BẢN BÀN GIAO HÀNG HOÁ", ["Đợt 3 — ngày 27/06/2026", "Theo hợp đồng số 25/2026/HĐMB-MA và phụ lục 01", "Hàng hoá: Thép cuộn SS400 — 170 tấn"]),
  ];
  const BCTC = Array.from({ length: 14 }, (_, i) =>
    i === 0 ? P("bctc", "BÁO CÁO TÀI CHÍNH NĂM 2025", ["CÔNG TY TNHH MINH AN", "Đã được kiểm toán bởi Công ty TNHH Kiểm toán AVN", "Hà Nội, tháng 03 năm 2026"])
    : i === 2 ? P("bctc", "BẢNG CÂN ĐỐI KẾ TOÁN", ["Tại ngày 31 tháng 12 năm 2025", "Tài sản ngắn hạn (100): 38.412.006.215", "Tài sản dài hạn (200): 9.104.556.000", "TỔNG CỘNG TÀI SẢN (270): 47.516.562.215", "Nợ phải trả (300): 29.880.114.902", "Vốn chủ sở hữu (400): 17.636.447.313"])
    : i === 4 ? P("bctc", "BÁO CÁO KẾT QUẢ HOẠT ĐỘNG KINH DOANH", ["Năm 2025", "Doanh thu thuần (10): 112.640.880.400", "Giá vốn hàng bán (11): 101.220.415.000", "Lợi nhuận gộp (20): 11.420.465.400", "Lợi nhuận sau thuế (60): 3.051.207.880"])
    : P("bctc", i < 9 ? "THUYẾT MINH BÁO CÁO TÀI CHÍNH" : "PHỤ LỤC SỐ LIỆU", ["Mục " + (i + 1) + ". Chính sách kế toán áp dụng", "Kỳ kế toán năm bắt đầu từ 01/01 và kết thúc 31/12", "Đơn vị tiền tệ sử dụng: Đồng Việt Nam"]),
  );
  BCTC[11].failed = true;
  const GCN = [P("gcn_dkdn", "GIẤY CHỨNG NHẬN ĐĂNG KÝ DOANH NGHIỆP", ["CÔNG TY TRÁCH NHIỆM HỮU HẠN HAI THÀNH VIÊN TRỞ LÊN", "Mã số doanh nghiệp: 0109876543", "Đăng ký lần đầu: 12/08/2015 · Thay đổi lần 4: 03/01/2025", "Tên công ty: CÔNG TY TNHH MINH AN", "Vốn điều lệ: 15.000.000.000 đồng", "Người đại diện theo pháp luật: TRẦN VĂN MINH"])];

  const D = (id, case_id, file_name, mime, size, pages, extra) => ({
    id, kb_id: "kb1", case_id, file_name, mime_type: mime, size_bytes: size, page_count: pages.length, pages_done: pages.length, pages_failed: 0,
    status: "completed", parse_status: "done", index_status: "done", engine: "turbo-ocr + vlm", created_at: "2026-10-20T09:12:00", updated_at: "2026-10-20T09:15:00",
    metadata: {}, content: pages, ...extra,
  });
  const DOCS = [
    D("d1", "c1", "HD_MuaBan_MinhAn_2026.pdf", "application/pdf", 2_410_000, HD, { title: "Hợp đồng mua bán thép 25/2026/HĐMB-MA", summary: "Hợp đồng mua 420 tấn thép cuộn SS400 giữa CP Thép Hoà Phát Nam (bán) và TNHH Minh An (mua), tổng 8,085 tỷ đồng, giao 3 đợt từ 04 đến 06/2026; phụ lục 01 tăng 30 tấn đợt 3.", metadata: { group_code: "G1", loai: "HD" }, created_at: "2026-10-20T09:12:00" }),
    D("d2", "c1", "HoaDon_UNC_Quy2.pdf", "application/pdf", 5_830_000, HDUNC, { title: "Chứng từ thanh toán quý 2/2026", summary: "Ba hoá đơn GTGT của Thép Hoà Phát Nam, ba uỷ nhiệm chi và hai biên bản bàn giao đợt 1 và 3; một UNC trả cước vận chuyển cho Logistics Sao Việt.", metadata: { group_code: "G1" }, created_at: "2026-10-20T09:14:00" }),
    D("d3", "c1", "BCTC_2025_MinhAn.pdf", "application/pdf", 9_120_000, BCTC, { title: "Báo cáo tài chính kiểm toán 2025", summary: "Doanh thu thuần 112,6 tỷ, lợi nhuận sau thuế 3,05 tỷ, tổng tài sản 47,5 tỷ; nợ phải trả chiếm 63% tổng nguồn vốn.", status: "partial", pages_done: 13, pages_failed: 1, error: "1 trang không đọc được", failed_pages: [{ page_no: 12, error: "Quá thời gian đọc trang, đã thử lại 3 lần" }], created_at: "2026-10-21T14:02:00" }),
    D("d4", "c1", "GCN_DKDN_MinhAn.jpg", "image/jpeg", 820_000, GCN, { title: "Giấy CN đăng ký doanh nghiệp — thay đổi lần 4", summary: "TNHH Minh An, MST 0109876543, vốn điều lệ 15 tỷ, đại diện pháp luật Trần Văn Minh.", created_at: "2026-10-21T14:03:00" }),
    D("d5", "c1", "ScanBoSung_T10.pdf", "application/pdf", 3_300_000, HD.slice(0, 5), { status: "parsing", parse_status: "running", index_status: "pending", pages_done: 2, progress: 0.4, created_at: "2026-10-23T08:40:00" }),
    D("d6", "c2", "HĐTD_ThepBacViet.pdf", "application/pdf", 1_900_000, HD.slice(0, 4), { title: "Hợp đồng tín dụng hạn mức", created_at: "2026-10-18T10:20:00" }),
    D("d7", "c2", "BCTC_2025_BacViet.pdf", "application/pdf", 7_700_000, BCTC.slice(0, 9), { status: "queued", parse_status: "pending", index_status: "pending", pages_done: 0, page_count: 0, created_at: "2026-10-23T08:52:00" }),
    D("d8", "c0", "anh_chup_CCCD.png", "image/png", 410_000, GCN, { title: "", status: "failed", error: "Ảnh quá mờ, không đọc được dòng nào", pages_done: 0, pages_failed: 1, created_at: "2026-10-22T16:31:00" }),
  ];

  // Cây của mỗi file: gốc là file, con cấp 1 là các giấy tờ theo loại (segment),
  // dưới đó là mục theo tiêu đề trang (dựng bằng code từ layout TurboOCR).
  const TREES = {
    d1: [
      ["n1", null, "HD_MuaBan_MinhAn_2026.pdf", 1, 6, ""],
      ["n2", "n1", "Hợp đồng mua bán 25/2026/HĐMB-MA", 1, 4, "hop_dong", "Hợp đồng chính: các bên, hàng hoá, giá, giao hàng, thanh toán."],
      ["n3", "n2", "Các bên ký kết", 1, 1, "", "Bên bán Thép Hoà Phát Nam, bên mua TNHH Minh An."],
      ["n4", "n2", "Điều 1. Hàng hoá, số lượng, giá", 2, 2, "", "420 tấn SS400, tổng 8,085 tỷ đã gồm VAT."],
      ["n5", "n2", "Điều 2. Giao hàng và thanh toán", 3, 3, "", "Giao 3 đợt 04–06/2026, thanh toán trong 30 ngày."],
      ["n6", "n2", "Điều 3. Trách nhiệm các bên", 4, 4, "", ""],
      ["n7", "n1", "Phụ lục 01", 5, 6, "hop_dong", "Tăng 30 tấn đợt 3, thêm 577,5 triệu."],
    ],
    d2: [
      ["n1", null, "HoaDon_UNC_Quy2.pdf", 1, 9, ""],
      ["n2", "n1", "Hoá đơn 0001523", 1, 1, "hoa_don", "2,695 tỷ, ngày 05/04/2026."],
      ["n3", "n1", "UNC-0412", 2, 2, "unc", "Trả HĐ 0001523."],
      ["n4", "n1", "Biên bản bàn giao đợt 1", 3, 3, "bien_ban", "140 tấn."],
      ["n5", "n1", "Hoá đơn 0001687", 4, 4, "hoa_don", ""],
      ["n6", "n1", "UNC-0533", 5, 5, "unc", "Tài khoản nhận khác hợp đồng."],
      ["n7", "n1", "Hoá đơn 0001802", 6, 6, "hoa_don", "3,2725 tỷ, gồm phần phụ lục 01."],
      ["n8", "n1", "UNC-0611", 7, 7, "unc", "Cước vận chuyển, không thuộc HĐMB."],
      ["n9", "n1", "Biên bản bàn giao đợt 3", 9, 9, "bien_ban", "170 tấn."],
    ],
    d3: [
      ["n1", null, "BCTC_2025_MinhAn.pdf", 1, 14, ""],
      ["n2", "n1", "Báo cáo tài chính 2025", 1, 14, "bctc", "Đã kiểm toán bởi AVN."],
      ["n3", "n2", "Bảng cân đối kế toán", 3, 4, "", "Tổng tài sản 47,5 tỷ."],
      ["n4", "n2", "Báo cáo kết quả kinh doanh", 5, 5, "", "Doanh thu 112,6 tỷ, LNST 3,05 tỷ."],
      ["n5", "n2", "Thuyết minh", 6, 9, "", ""],
      ["n6", "n2", "Phụ lục số liệu", 10, 14, "", ""],
    ],
    d4: [["n1", null, "GCN_DKDN_MinhAn.jpg", 1, 1, ""], ["n2", "n1", "Giấy CN đăng ký doanh nghiệp", 1, 1, "gcn_dkdn", "Thay đổi lần 4."]],
    d6: [["n1", null, "HĐTD_ThepBacViet.pdf", 1, 4, ""], ["n2", "n1", "Hợp đồng tín dụng", 1, 4, "hop_dong", ""]],
  };
  for (const k in TREES)
    TREES[k] = TREES[k].map(([short_id, parent, title, page_start, page_end, doc_type, summary], ord) => ({
      id: k + short_id, short_id, parent_id: parent && k + parent, title, page_start, page_end, doc_type, summary: summary || "", ord,
    }));

  // Tách & gom (§7.9): đề xuất của AI cho từng file của hồ sơ c1.
  const SPLIT = {
    labels: DOC_TYPES.map((t) => ({ name: t.name, title: t.title })),
    opens_with: ["hop_dong"],
    reviewed: false,
    files: [
      { document_id: "d1", status: "reviewed", marks: { 5: "“Trang 1/2” — mở phụ lục" }, segments: [
        { page_start: 1, page_end: 4, label: "hop_dong", confidence: 0.98, source: "pipeline", bundle: "B01" },
        { page_start: 5, page_end: 6, label: "hop_dong", confidence: 0.91, source: "pipeline", bundle: "B01" },
      ] },
      { document_id: "d2", status: "proposed", marks: { 2: "đổi tiêu đề", 3: "đổi khổ giấy A5", 4: "“Trang 1/1”", 8: "trang trắng", 9: "đổi khổ giấy A5" }, segments: [
        { page_start: 1, page_end: 1, label: "hoa_don", confidence: 0.96, source: "pipeline", bundle: "B01" },
        { page_start: 2, page_end: 2, label: "unc", confidence: 0.93, source: "pipeline", bundle: "B01" },
        { page_start: 3, page_end: 3, label: "bien_ban", confidence: 0.88, source: "pipeline", bundle: "B01" },
        { page_start: 4, page_end: 5, label: "hoa_don", confidence: 0.62, source: "pipeline", bundle: "B01" },
        { page_start: 6, page_end: 6, label: "hoa_don", confidence: 0.94, source: "pipeline", bundle: "B01" },
        { page_start: 7, page_end: 8, label: "unknown", confidence: 0.31, source: "pipeline", bundle: "B01" },
        { page_start: 9, page_end: 9, label: "bien_ban", confidence: 0.9, source: "pipeline", bundle: "B01" },
      ] },
      { document_id: "d3", status: "proposed", marks: {}, segments: [{ page_start: 1, page_end: 14, label: "bctc", confidence: 0.99, source: "pipeline", bundle: "B01" }] },
      { document_id: "d4", status: "reviewed", marks: {}, segments: [{ page_start: 1, page_end: 1, label: "gcn_dkdn", confidence: 0.97, source: "pipeline", bundle: "B01" }] },
    ],
  };

  const SHEETS = [
    { id: "s1", case_id: "c1", name: "Bảng số liệu RT112233", status: "done", created_at: "2026-10-22T10:05:00", tables: ["hop_dong", "hoa_don", "unc", "bctc"] },
    { id: "s2", case_id: "c1", name: "Mẫu tờ trình thẩm định", status: "done", created_at: "2026-10-22T11:40:00", tables: ["hop_dong", "bctc"], form: "Mau_ToTrinh_v3.xlsx" },
    { id: "s3", case_id: "c1", name: "Bảng số liệu RT112233 (2)", status: "running", filled: 9, total: 13, created_at: "2026-10-23T08:58:00", tables: ["hoa_don", "unc"] },
  ];
  // Hàng của bảng: [loại, file, trang, bộ, {key: [giá trị, độ tin cậy, trang, dòng, cờ]}]
  const ROWS = {
    hop_dong: [
      ["d1", 1, 4, "B01", { so_hop_dong: ["25/2026/HĐMB-MA", 0.99, 1, 1], ngay_ky: ["15/03/2026", 0.97, 1, 2], ben_ban: ["CÔNG TY CP THÉP HOÀ PHÁT NAM", 0.98, 1, 3], ben_mua: ["CÔNG TY TNHH MINH AN", 0.98, 1, 5], gia_tri: ["8.085.000.000", 0.95, 2, 5], tai_khoan: ["1903 4567 8901 23", 0.97, 1, 4], thoi_han: ["31/12/2026", 0.71, 3, 4, "low"] }],
      ["d1", 5, 6, "B01", { so_hop_dong: ["25/2026/HĐMB-MA", 0.8, 5, 1, "calc"], ngay_ky: ["", 0, 0, 0], ben_ban: ["", 0, 0, 0], ben_mua: ["", 0, 0, 0], gia_tri: ["577.500.000", 0.93, 5, 2], tai_khoan: ["", 0, 0, 0], thoi_han: ["", 0, 0, 0] }],
    ],
    hoa_don: [
      ["d2", 1, 1, "B01", { so_hoa_don: ["0001523", 0.99, 1, 1], ngay_hoa_don: ["05/04/2026", 0.98, 1, 2], ben_ban: ["CÔNG TY CP THÉP HOÀ PHÁT NAM", 0.97, 1, 3], tong_tien: ["2.695.000.000", 0.96, 1, 6] }],
      ["d2", 4, 4, "B01", { so_hoa_don: ["0001687", 0.99, 4, 1], ngay_hoa_don: ["12/05/2026", 0.98, 4, 2], ben_ban: ["CÔNG TY CP THÉP HOÀ PHÁT NAM", 0.97, 4, 3], tong_tien: ["2.659.000.000", 0.66, 4, 6, "low"] }],
      ["d2", 6, 6, "B01", { so_hoa_don: ["0001802", 0.99, 6, 1], ngay_hoa_don: ["28/06/2026", 0.98, 6, 2], ben_ban: ["CÔNG TY CP THÉP HOÀ PHÁT NAM", 0.97, 6, 3], tong_tien: ["3.272.500.000", 0.95, 6, 6] }],
    ],
    unc: [
      ["d2", 2, 2, "B01", { so_unc: ["UNC-0412", 0.98, 2, 1], ngay: ["06/04/2026", 0.97, 2, 1], tk_nhan: ["1903 4567 8901 23", 0.93, 2, 4], so_tien: ["2.695.000.000", 0.96, 2, 5], ben_thu_huong: ["CÔNG TY CP THÉP HOÀ PHÁT NAM", 0.97, 2, 3], noi_dung: ["TT HĐ 0001523 theo HĐMB 25/2026", 0.95, 2, 6] }],
      ["d2", 5, 5, "B01", { so_unc: ["UNC-0533", 0.98, 5, 1], ngay: ["14/05/2026", 0.97, 5, 1], tk_nhan: ["1903 4567 8901 99", 0.58, 5, 4, "low"], so_tien: ["2.695.000.000", 0.96, 5, 5], ben_thu_huong: ["CÔNG TY CP THÉP HOÀ PHÁT NAM", 0.97, 5, 3], noi_dung: ["TT HĐ 0001687", 0.95, 5, 6] }],
      ["d2", 7, 7, "B01", { so_unc: ["UNC-0611", 0.98, 7, 1], ngay: ["30/06/2026", 0.97, 7, 1], tk_nhan: ["0021 0003 4455", 0.94, 7, 4], so_tien: ["186.000.000", 0.97, 7, 5], ben_thu_huong: ["CÔNG TY TNHH LOGISTICS SAO VIỆT", 0.97, 7, 3], noi_dung: ["Cước vận chuyển tháng 6", 0.96, 7, 6] }],
    ],
    bctc: [
      ["d3", 1, 14, "B01", { nam: ["2025", 0.99, 1, 0], doanh_thu: ["112.640.880.400", 0.96, 5, 2], loi_nhuan: ["3.051.207.880", 0.95, 5, 5], tong_tai_san: ["47.516.562.215", 0.97, 3, 4] }],
    ],
  };

  // Kết quả một lần đối chiếu theo bộ rs1: mỗi quy tắc (rule = vị trí trong
  // bộ) có các dòng so [giấy tờ, file, trang, giá trị A, giá trị B, đạt?, chưa chắc?].
  const CHECK = {
    id: "k1", case_id: "c1", sheet_id: "s1", ruleset: "rs1", status: "done", created_at: "2026-10-22T10:20:00", ran_at: "2026-10-22T10:21:00",
    counts: { groups: 1, pass: 3, fail: 1, unknown: 1, unlinked: 1 },
    anchor: ["HĐMB 25/2026/HĐMB-MA", "d1", 1, "hop_dong", "tr. 1–4"],
    // [tên, file, trang, loại, cách ghép, lý do]
    members: [
      ["Phụ lục 01", "d1", 5, "hop_dong", "rule", "Cùng số hợp đồng 25/2026"],
      ["Hoá đơn 0001523", "d2", 1, "hoa_don", "rule", "Cùng tên bên bán với hợp đồng"],
      ["UNC-0412", "d2", 2, "unc", "rule", "Nội dung có nhắc số hợp đồng 25/2026"],
      ["Biên bản bàn giao đợt 1", "d2", 3, "bien_ban", "rule", "Nội dung có nhắc số hợp đồng"],
      ["Hoá đơn 0001687", "d2", 4, "hoa_don", "single", "Hồ sơ chỉ có một hợp đồng"],
      ["UNC-0533", "d2", 5, "unc", "ai", "Số tiền khớp hoá đơn 0001687"],
      ["Hoá đơn 0001802", "d2", 6, "hoa_don", "user", "Bạn chọn ngày 22/10"],
      ["Biên bản bàn giao đợt 3", "d2", 9, "bien_ban", "rule", "Nội dung có nhắc số hợp đồng"],
    ],
    results: [
      { rule: 0, status: "pass", rows: [["Hoá đơn 0001523", "d2", 1, "05/04/2026", "31/12/2026", true], ["Hoá đơn 0001687", "d2", 4, "12/05/2026", "31/12/2026", true], ["Hoá đơn 0001802", "d2", 6, "28/06/2026", "31/12/2026", true]] },
      { rule: 1, status: "pass", total: ["8.626.500.000", "8.662.500.000", "Hợp đồng 8.085.000.000 + Phụ lục 01 577.500.000"],
        rows: [["Hoá đơn 0001523", "d2", 1, "2.695.000.000", "", true], ["Hoá đơn 0001687", "d2", 4, "2.659.000.000", "", true, true], ["Hoá đơn 0001802", "d2", 6, "3.272.500.000", "", true]] },
      { rule: 2, status: "fail", rows: [["UNC-0412", "d2", 2, "1903 4567 8901 23", "1903 4567 8901 23", true], ["UNC-0533", "d2", 5, "1903 4567 8901 99", "1903 4567 8901 23", false, true]],
        hint: "UNC-0533 chuyển tiền vào tài khoản lệch 2 số cuối so với hợp đồng. Mở trang gốc để xem: nếu AI đọc sai thì sửa ở bảng số liệu rồi đối chiếu lại; nếu đúng là chuyển nhầm tài khoản thì cần làm rõ với khách hàng." },
      { rule: 3, status: "pass", total: ["5.390.000.000", "8.626.500.000", "Tổng 3 hoá đơn"], rows: [["UNC-0412", "d2", 2, "2.695.000.000", "", true], ["UNC-0533", "d2", 5, "2.695.000.000", "", true]] },
      { rule: 4, status: "unknown", missing: ["Hợp đồng ghi giao hàng 3 đợt (04–06/2026) nhưng không thấy biên bản bàn giao đợt 2 (tháng 05/2026)"] },
    ],
    unlinked: [["UNC-0611", "d2", 7, "unc", "", "Bên thụ hưởng Logistics Sao Việt không khớp bên nào của hợp đồng"]],
  };

  // Bộ quy tắc đối chiếu: dùng chung như loại giấy tờ, thư viện chọn bộ nào
  // được dùng; Studio chọn một bộ để chạy. Mỗi quy tắc so một trường của giấy
  // tờ A với một trường của giấy tờ B; quy tắc viết bằng lời (text) do AI đánh giá.
  const RULESETS = [
    { id: "rs1", name: "Giải ngân theo hợp đồng mua bán", desc: "Gom hoá đơn, uỷ nhiệm chi, biên bản theo hợp đồng; kiểm thời hạn, số tiền và bên nhận tiền.", anchor: "hop_dong", kbs: ["kb1"],
      links: { hoa_don: ["Cùng tên bên bán với hợp đồng"], unc: ["Nội dung có nhắc số hoặc tên hợp đồng", "Số tiền khớp một hoá đơn đã gắn"], bien_ban: ["Nội dung có nhắc số hoặc tên hợp đồng"] },
      checks: [
        { a: ["hoa_don", "ngay_hoa_don"], op: "before", b: ["hop_dong", "thoi_han"], on: true },
        { a: ["hoa_don", "tong_tien"], op: "sum_le", b: ["hop_dong", "gia_tri"], on: true },
        { a: ["unc", "tk_nhan"], op: "same", b: ["hop_dong", "tai_khoan"], on: true },
        { a: ["unc", "so_tien"], op: "sum_le", b: ["hoa_don", "tong_tien"], on: true },
        { text: "Nếu hợp đồng thanh toán sau khi giao hàng thì phải có hoá đơn hoặc biên bản bàn giao", on: true },
      ] },
    { id: "rs2", name: "Kiểm tra uỷ nhiệm chi", desc: "Đối chiếu từng uỷ nhiệm chi với hoá đơn cùng bộ.", anchor: "hoa_don", kbs: ["kb1"], links: { unc: ["Số tiền khớp hoá đơn"] },
      checks: [
        { a: ["unc", "so_tien"], op: "le", b: ["hoa_don", "tong_tien"], on: true },
        { a: ["unc", "ngay"], op: "after", b: ["hoa_don", "ngay_hoa_don"], on: true },
        { a: ["unc", "ben_thu_huong"], op: "same", b: ["hoa_don", "ben_ban"], on: true },
      ] },
    { id: "rs3", name: "Báo cáo tài chính", desc: "Các điều kiện tối thiểu về kết quả kinh doanh.", anchor: "bctc", kbs: ["kb2"], links: {},
      checks: [{ text: "Lợi nhuận sau thuế của năm gần nhất phải lớn hơn 0", on: true }] },
  ];

  const FORMS = [
    { id: "f1", name: "Tờ trình thẩm định", file_name: "Mau_ToTrinh_v3.xlsx", doc_type: "hop_dong", columns: 9, updated_at: "2026-10-12T09:00:00" },
    { id: "f2", name: "Bảng kê chứng từ giải ngân", file_name: "BangKe_GiaiNgan.xlsx", doc_type: "hoa_don", columns: 7, updated_at: "2026-10-15T16:20:00" },
  ];

  const SESSIONS = [
    { id: "x1", title: "UNC có chuyển đúng tài khoản bên bán?", case_id: "c1", updated_at: "2026-10-23T08:30:00" },
    { id: "x2", title: "Tóm tắt BCTC 2025 Minh An", case_id: "c1", updated_at: "2026-10-22T15:12:00" },
    { id: "x3", title: "Hạn mức hiện tại của Thép Bắc Việt", case_id: "c2", updated_at: "2026-10-21T10:02:00" },
  ];

  // Một lượt hỏi đáp mẫu: các lời gọi tool rồi câu trả lời có trích dẫn.
  const TRANSCRIPT = [
    { role: "user", text: "Các uỷ nhiệm chi có chuyển đúng vào tài khoản bên bán trong hợp đồng không?" },
    { role: "assistant", tools: [
      ["kb_case_toc", { case_id: "c1" }, "4 file · HD_MuaBan_MinhAn_2026.pdf (6 tr.) · HoaDon_UNC_Quy2.pdf (9 tr.) · BCTC_2025_MinhAn.pdf (14 tr.) · GCN_DKDN_MinhAn.jpg (1 tr.)"],
      ["kb_document_tree", { document_id: "d2" }, "UNC-0412 tr.2 · UNC-0533 tr.5 · UNC-0611 tr.7 …"],
      ["kb_read_pages", { document_id: "d1", pages: [1] }, "Dòng 5: Mã số thuế: 0101234567 · Tài khoản: 1903 4567 8901 23 tại Techcombank [doc:d1:p1:l5]"],
      ["kb_read_pages", { document_id: "d2", pages: [2, 5, 7] }, "Tr.2 dòng 5: Số tài khoản: 1903 4567 8901 23 … tr.5 dòng 5: Số tài khoản: 1903 4567 8901 99 …"],
    ], text: "Hợp đồng ghi tài khoản bên bán là **1903 4567 8901 23** tại Techcombank [doc:d1:p1:l5].\n\n| UNC | Tài khoản nhận | Khớp hợp đồng |\n|---|---|---|\n| UNC-0412 | 1903 4567 8901 23 [doc:d2:p2:l5] | Có |\n| UNC-0533 | 1903 4567 8901 **99** [doc:d2:p5:l5] | **Không** |\n| UNC-0611 | 0021 0003 4455 [doc:d2:p7:l5] | Không thuộc HĐMB — trả cước vận chuyển |\n\nUNC-0533 lệch 2 số cuối so với hợp đồng. Dòng này hệ thống đọc chưa chắc chắn, nên mở trang gốc để kiểm tra trước khi kết luận." },
  ];

  const USERS = [
    { id: "u1", name: "Nguyễn Thanh", email: "thanh@bepaylot.vn", role: "admin", limit: 0, used: 412000 },
    { id: "u2", name: "Lê Thu Hà", email: "ha.le@bepaylot.vn", role: "prompt_editor", limit: 2000000, used: 1285000 },
    { id: "u3", name: "Phạm Quốc Bảo", email: "bao.pham@bepaylot.vn", role: "user", limit: 500000, used: 96000 },
  ];


  // Phòng thử loại giấy tờ (kiểu Docsumo): tài liệu mẫu đã qua docpipe
  // (markdown VLM là nguồn chính, text TurboOCR tham khảo), giá trị đúng do
  // người dùng duyệt, kết quả của từng phiên bản prompt.
  // src: trường -> số dòng trên trang để tô vùng nguồn.
  const UNC_PROMPT_V1 = "Bạn đọc một Uỷ nhiệm chi của ngân hàng Việt Nam và lấy ra các thông tin bên dưới.\nDựa vào phần Nội dung; chữ số nào không rõ thì kiểm lại ở phần Các dòng.\nChỉ ghi đúng giá trị có trên tài liệu. Thông tin nào không thấy thì để trống, không đoán.";
  const LAB = {
    unc: {
      current: 1,
      versions: [{ v: 1, created_at: "2026-10-20T15:00:00", by: "Lê Thu Hà", note: "Bản đầu tiên", prompt: UNC_PROMPT_V1, rules: [], shots: [], ok: 1 }],
      samples: [
        { id: "s1", file_name: "UNC_Techcombank_0412.pdf", status: "reviewed", ran_at: "2026-10-22T10:12:00",
          content: [P("unc", "UỶ NHIỆM CHI", ["Số: UNC-0412 · Ngày: 06/04/2026", "Tên TK trích nợ: CÔNG TY TNHH MINH AN", "Số TK trích nợ: 1903 2222 0101 88 — Techcombank", "Đơn vị thụ hưởng: CÔNG TY CP THÉP HOÀ PHÁT NAM", "Số TK thụ hưởng: 1903 4567 8901 23 — Techcombank", "Số tiền: 2.695.000.000 đồng", "Nội dung: TT HĐ 0001523 theo HĐMB 25/2026"])],
          src: { so_unc: 2, ngay: 2, ben_thu_huong: 5, tk_nhan: 6, so_tien: 7, noi_dung: 8 },
          truth: { so_unc: "UNC-0412", ngay: "06/04/2026", ben_thu_huong: "CÔNG TY CP THÉP HOÀ PHÁT NAM", tk_nhan: "1903 4567 8901 23", so_tien: "2.695.000.000", noi_dung: "TT HĐ 0001523 theo HĐMB 25/2026" },
          out: {
            1: { so_unc: "UNC-0412", ngay: "06/04/2026", ben_thu_huong: "CÔNG TY CP THÉP HOÀ PHÁT NAM", tk_nhan: "1903 4567 8901 23", so_tien: "2.695.000.000 đồng", noi_dung: "TT HĐ 0001523 theo HĐMB 25/2026" },
            2: { so_unc: "UNC-0412", ngay: "06/04/2026", ben_thu_huong: "CÔNG TY CP THÉP HOÀ PHÁT NAM", tk_nhan: "1903 4567 8901 23", so_tien: "2.695.000.000", noi_dung: "TT HĐ 0001523 theo HĐMB 25/2026" },
          } },
        { id: "s2", file_name: "UNC_VCB_0611.jpg", status: "reviewed", ran_at: "2026-10-22T10:12:00",
          content: [P("unc", "LỆNH CHI / UỶ NHIỆM CHI", ["Số chứng từ: 0611 · Ngày lập: 30/06/2026", "Người trả: CÔNG TY TNHH MINH AN — TK 0021 0001 2345", "Người hưởng: CÔNG TY TNHH LOGISTICS SAO VIỆT", "TK người hưởng: 0021 0003 4455 tại Vietcombank", "Số tiền bằng số: 186.000.000", "Bằng chữ: Một trăm tám mươi sáu triệu đồng", "Nội dung: Cước vận chuyển tháng 6"])],
          src: { so_unc: 2, ngay: 2, ben_thu_huong: 4, tk_nhan: 5, so_tien: 6, noi_dung: 8 },
          truth: { so_unc: "0611", ngay: "30/06/2026", ben_thu_huong: "CÔNG TY TNHH LOGISTICS SAO VIỆT", tk_nhan: "0021 0003 4455", so_tien: "186.000.000", noi_dung: "Cước vận chuyển tháng 6" },
          out: {
            1: { so_unc: "UNC-0611", ngay: "30/06/2026", ben_thu_huong: "CÔNG TY TNHH LOGISTICS SAO VIỆT", tk_nhan: "0021 0001 2345", so_tien: "186.000.000", noi_dung: "Cước vận chuyển tháng 6" },
            2: { so_unc: "0611", ngay: "30/06/2026", ben_thu_huong: "CÔNG TY TNHH LOGISTICS SAO VIỆT", tk_nhan: "0021 0003 4455", so_tien: "186.000.000", noi_dung: "Cước vận chuyển tháng 6" },
          } },
        { id: "s3", file_name: "UNC_BIDV_scan.pdf", status: "reviewed", ran_at: "2026-10-22T10:13:00",
          content: [P("unc", "UỶ NHIỆM CHI", ["Số: 77/UNC", "Ngày 14 tháng 05 năm 2026", "Đơn vị trả tiền: CÔNG TY TNHH MINH AN", "Tài khoản: 2141 0000 5566 77 tại BIDV Hà Nội", "Đơn vị nhận tiền: CÔNG TY TNHH THIẾT BỊ ĐÔNG Á", "Tài khoản: 1240 2001 9988 tại BIDV Đống Đa", "Số tiền: 412.500.000 VND", "Nội dung: Thanh toán đợt 2 HĐ 18/2026"], { low: [6], vlm: [2] })],
          src: { so_unc: 2, ngay: 3, ben_thu_huong: 6, tk_nhan: 7, so_tien: 8, noi_dung: 9 },
          truth: { so_unc: "77/UNC", ngay: "14/05/2026", ben_thu_huong: "CÔNG TY TNHH THIẾT BỊ ĐÔNG Á", tk_nhan: "1240 2001 9988", so_tien: "412.500.000", noi_dung: "Thanh toán đợt 2 HĐ 18/2026" },
          out: {
            1: { so_unc: "77/UNC", ngay: "14 tháng 05 năm 2026", ben_thu_huong: "CÔNG TY TNHH THIẾT BỊ ĐÔNG Á", tk_nhan: "2141 0000 5566 77", so_tien: "412.500.000 VND", noi_dung: "Thanh toán đợt 2 HĐ 18/2026" },
            2: { so_unc: "77", ngay: "14/05/2026", ben_thu_huong: "CÔNG TY TNHH THIẾT BỊ ĐÔNG Á", tk_nhan: "1240 2001 9988", so_tien: "412.500.000", noi_dung: "Thanh toán đợt 2 HĐ 18/2026" },
          } },
        { id: "s4", file_name: "UNC_ACB_0705.pdf", status: "output", ran_at: "2026-10-22T10:13:00",
          content: [P("unc", "UỶ NHIỆM CHI", ["Số: ACB-0705-118 · Ngày: 05/07/2026", "Bên chuyển: CÔNG TY TNHH MINH AN · TK 8811 2233 4", "Bên nhận: CÔNG TY CP BAO BÌ PHƯƠNG NAM", "TK nhận: 9900 1188 2277 — ACB", "Số tiền: 58.300.000 đồng", "Nội dung: TT HĐ 0000932"])],
          src: { so_unc: 2, ngay: 2, ben_thu_huong: 4, tk_nhan: 5, so_tien: 6, noi_dung: 7 },
          truth: null,
          out: {
            1: { so_unc: "ACB-0705-118", ngay: "05/07/2026", ben_thu_huong: "CÔNG TY CP BAO BÌ PHƯƠNG NAM", tk_nhan: "8811 2233 4", so_tien: "58.300.000 đồng", noi_dung: "TT HĐ 0000932" },
            2: { so_unc: "ACB-0705-118", ngay: "05/07/2026", ben_thu_huong: "CÔNG TY CP BAO BÌ PHƯƠNG NAM", tk_nhan: "9900 1188 2277", so_tien: "58.300.000", noi_dung: "TT HĐ 0000932" },
          } },
        { id: "s5", file_name: "UNC_MB_0802.png", status: "converted",
          content: [P("unc", "UỶ NHIỆM CHI", ["Số: 0802/UNC · Ngày: 02/08/2026", "Tài khoản trích nợ: 0680 1234 5678 — MB", "Đơn vị hưởng: CÔNG TY TNHH VẬN TẢI HẢI ĐĂNG", "Số tài khoản hưởng: 0680 9988 7766 — MB", "Số tiền: 74.000.000", "Nội dung: Cước tháng 7"])],
          src: { so_unc: 2, ngay: 2, ben_thu_huong: 4, tk_nhan: 5, so_tien: 6, noi_dung: 7 },
          truth: null,
          out: {
            1: { so_unc: "0802/UNC", ngay: "02/08/2026", ben_thu_huong: "CÔNG TY TNHH VẬN TẢI HẢI ĐĂNG", tk_nhan: "0680 1234 5678", so_tien: "74.000.000", noi_dung: "Cước tháng 7" },
            2: { so_unc: "0802/UNC", ngay: "02/08/2026", ben_thu_huong: "CÔNG TY TNHH VẬN TẢI HẢI ĐĂNG", tk_nhan: "0680 9988 7766", so_tien: "74.000.000", noi_dung: "Cước tháng 7" },
          } },
      ],
      // Kết quả "Tối ưu prompt" khi chạy trên các mẫu đã duyệt (s1–s3).
      proposal: {
        v: 2,
        patterns: [
          { field: "tk_nhan", kind: "rule", hits: ["s2", "s3"], finding: "AI lấy tài khoản của bên trả tiền. Ở cả 3 mẫu, tài khoản đúng nằm ngay sau dòng tên bên nhận, nhưng nhãn khác nhau theo ngân hàng: “Số TK thụ hưởng”, “TK người hưởng”, dòng “Tài khoản” thứ hai.", rule: "Tài khoản nhận là số tài khoản đi cùng bên thụ hưởng (dòng sau “Đơn vị thụ hưởng”, “Người hưởng”, “Đơn vị nhận tiền”, “Bên nhận”). Không lấy tài khoản trích nợ của bên trả tiền." },
          { field: "so_tien", kind: "rule", hits: ["s1", "s3"], finding: "Giá trị kèm đơn vị “đồng”, “VND”; giá trị đã duyệt chỉ có chữ số.", rule: "Số tiền chỉ gồm chữ số và dấu chấm phân cách hàng nghìn; bỏ “đồng”, “VND”, không lấy số tiền bằng chữ." },
          { field: "ngay", kind: "rule", hits: ["s3"], finding: "Ngày viết bằng chữ “Ngày 14 tháng 05 năm 2026” được chép nguyên văn.", rule: "Ngày luôn ghi dạng ngày/tháng/năm (14/05/2026); ngày viết bằng chữ cũng đổi về dạng này." },
          { field: "so_unc", kind: "rule", hits: ["s2"], finding: "AI thêm tiền tố “UNC-” không có trên chứng từ (“Số chứng từ: 0611”).", rule: "Số UNC chép nguyên văn số chứng từ, không thêm hay bớt tiền tố, hậu tố." },
        ],
        shots: [
          { sample: "s2", why: "Bố cục Vietcombank: nhãn “Người hưởng”, “TK người hưởng”, số chứng từ không có tiền tố" },
          { sample: "s3", why: "Bản scan BIDV: hai dòng “Tài khoản” giống nhau, ngày viết bằng chữ, số tiền kèm VND" },
        ],
      },
    },
  };

  window.MOCK = { LAB, DOC_TYPES, KBS, CASES, DOCS, TREES, SPLIT, SHEETS, ROWS, CHECK, RULESETS, FORMS, SESSIONS, TRANSCRIPT, USERS };
})();
