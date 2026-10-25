# Prototype giao diện BePaylot

Bản mock tĩnh của frontend (`frontend/src`): cùng màn hình, cùng bố cục và class Tailwind, nhưng dữ liệu là dữ liệu mẫu trong `assets/data.js`, không gọi backend. Dùng để demo luồng và bàn giao diện mà không cần chạy server.

| Đường dẫn (hash) | Màn hình | Ứng với |
|---|---|---|
| `#/login` | Đăng nhập (email/mật khẩu, SSO) | `pages/auth/LoginPage.tsx` |
| `#/documents` | Danh sách file của thư viện: lọc theo hồ sơ, loại, trạng thái; dạng danh sách/lưới; tải file lên kèm mã hồ sơ | `pages/documents/DocumentsPage.tsx`, `UploadDialog.tsx` |
| `#/documents/:id?page=&tab=&hl=` | Trình xem: thumbnail chia theo giấy tờ, khung toạ độ từng dòng (OCR/VLM/độ tin cậy thấp), panel Chi tiết · Dòng · Markdown · Blocks · Tìm · Mục lục, parse lại | `pages/documents/DocumentDetail.tsx` |
| `#/chat[/:session]` | Hỏi đáp gắn với một hồ sơ: lịch sử, nhóm lời gọi tool `kb_*`, trích dẫn `tr.· d.` mở vùng trên trang gốc | `pages/chat/ChatPage.tsx` |
| `#/cases/:id[?doc=&node=]` | Phương án: Nguồn (cây file → giấy tờ → mục) · trình xem trang của nguồn đang chọn (mặc định file đầu; chọn giấy tờ/mục thì cuộn tới trang đầu và viền các trang của mục, không có tóm tắt chữ) · Trường trích xuất (mỗi giấy tờ trong phạm vi đang xem một nhóm, giá trị dài tự xuống dòng, bấm trường để tô dòng nguồn trên trang; nút bút hoặc nháy đúp để sửa tại chỗ, Enter/rời ô là lưu, Esc huỷ, giá trị đã sửa giữ “AI: …” kèm Hoàn tác; thu gọn thành thanh dọc) · Studio (Xuất Excel, Xuất theo mẫu, Đối chiếu chứng từ, bảng đã tạo). **Khác frontend:** bỏ cột Trò chuyện, nút “Hỏi đáp” mở trang Hỏi đáp với hồ sơ này | `pages/cases/CasePage.tsx`, `Studio.tsx` |
| `#/cases/:id/split` | Tách & gom trang: đề xuất của AI theo bộ, đổi loại, cắt/gộp, chuyển bộ, dấu điểm cắt | `pages/cases/SplitPage.tsx` |
| `#/cases/:id/sheets/:sheet` | Bảng Excel: mỗi loại giấy tờ một sheet, sửa ô (nháy đúp), nguồn của ô, lịch sử chỉnh sửa, tải .xlsx | `pages/sheets/SheetPage.tsx` |
| `#/cases/:id/checks/:check` | Kết quả đối chiếu theo bộ quy tắc: băng kết luận (số vấn đề, số giấy tờ có lỗi); trái là các giấy tờ của bộ với trạng thái (✓, "n lỗi", kiểm tra lại, chưa ghép), bấm để lọc; giữa là **Cần xử lý**: mỗi quy tắc lỗi một thẻ có câu quy tắc, "Giấy tờ sai", bảng so giá trị từng giấy tờ (dòng sai tô đỏ, giá trị AI đọc chưa chắc có nhãn), gợi ý cách xử lý, nút Xem trang gốc / Sửa số liệu; chứng từ chưa ghép chọn hợp đồng tại chỗ; quy tắc đạt thu gọn ở dưới. **Khác frontend:** bố cục theo vấn đề thay cho theo hợp đồng | `pages/checks/CheckPage.tsx` |
| `#/settings/doctypes` | **Mới, chưa có ở frontend.** Cài đặt → Loại giấy tờ: danh mục **dùng chung cho mọi thư viện**, dạng danh sách (số trường, mẫu đã duyệt, độ chính xác, thư viện đang dùng); lọc theo thư viện / chưa dùng, tìm theo tên; nút thư viện ở cuối dòng thêm/bỏ loại khỏi từng thư viện. Thư viện chỉ dùng loại đã được thêm (hộp thoại Sửa thư viện chọn từ danh mục) | — |
| `#/settings/doctypes/:code?tab=samples` | Tài liệu mẫu kiểu Docsumo: tải mẫu lên (chỉ hiện trạng thái “đang xử lý” → “chưa chạy thử”; phía sau là docpipe), xem 3 chế độ: Ảnh gốc · Nội dung (markdown VLM, “nguồn chính”) · Các dòng (TurboOCR từng dòng kèm % chắc chắn, “để đối chiếu”, dòng dưới 70% ghi “bỏ qua”), mỗi chế độ có thẻ giải thích vai trò; chạy thử, kết quả từng trường bên phải với ✓/✗, sửa rồi **lưu làm dữ liệu mẫu** | — |
| `#/settings/doctypes/:code?tab=prompt` | Tab “Hướng dẫn AI” (prompt). Một màn cho cả vòng sửa → chạy thử → tối ưu: thanh công cụ chỉ có 2 nút (Tối ưu bằng AI, Lưu & chạy thử); trái là soạn (trường, hướng dẫn, quy tắc, ví dụ few-shot, “Xem prompt gửi model” thu gọn); phải là điểm từng trường × mẫu và danh sách phiên bản mở sẵn (bấm dòng để dùng lại, nút xoá hiện khi rê chuột). Khi có đề xuất: trái thành so sánh prompt v_cũ → v_mới, phải là “Vì sao thay đổi” (bấm để tô dòng) và điểm trước/sau theo trường; chỉ có Áp dụng hoặc Bỏ | — |
| `#/settings/checks/:id` | Một bộ quy tắc: mỗi quy tắc là một câu “trường của giấy tờ A · cách so · trường của giấy tờ B”, bật/tắt, sửa, xoá; hộp thoại Thêm quy tắc: chọn giấy tờ A và trường, giấy tờ B và trường cùng kiểu, cách so theo kiểu chữ / số tiền / ngày; ngay dưới cách so là ô **viết bằng lời** (AI đánh giá); xem trước câu quy tắc. Cách ghép chứng từ với giấy tờ gốc thu gọn ở dưới. Studio của hồ sơ chọn bộ quy tắc trước khi Chạy đối chiếu | — |
| `#/settings/:tab` | Cài đặt: Loại giấy tờ (trên), Mẫu xuất Excel, Quy tắc đối chiếu (danh sách **bộ quy tắc** dùng chung như loại giấy tờ: số quy tắc, giấy tờ được so, thư viện đang dùng; lọc theo thư viện), Người dùng; menu tài khoản mở Thông tin, Key & chi phí | `components/SettingsDialog.tsx`, `FormsTab.tsx`, `ChecksTab.tsx` |

## Chạy

Mở thẳng `prototypes/index.html` trong trình duyệt (chạy được qua `file://`), hoặc:

```bash
python3 -m http.server 5173 --directory prototypes
# mở http://localhost:5173
```

Cần mạng để tải Tailwind (`@tailwindcss/browser`), font Roboto/Material Symbols, `marked` và `DOMPurify` từ CDN.

## Ghi chú

- `index.html` chép token màu (sáng/tối) và các class `.btn`, `.chip`, `.badge`… từ `frontend/src/index.css`. Khi frontend đổi token hay component class, chép lại phần này.
- Ảnh trang là SVG dựng từ nội dung mẫu (1240×1754, như ảnh A4 150 dpi); khung dòng dùng cùng toạ độ nên vùng trích dẫn và vùng “nguồn của ô” khớp đúng dòng.
- Các thao tác ghi (tải lên, lưu, parse lại, chạy đối chiếu…) chỉ hiện thông báo. Gửi câu hỏi trong chat trả về một câu trả lời mẫu.
- Loại giấy tờ (`#/settings/doctypes`): chỉ Uỷ nhiệm chi có sẵn 5 mẫu và kết quả của prompt v1 và v2 để demo vòng chạy thử → duyệt → tối ưu. Mẫu dùng làm ví dụ few-shot được chấm theo kiểu bỏ-một-ra.
- Không còn trang wiki và graph: các route `/wiki` và `/graph` đã bị bỏ khỏi backend, truy vấn chỉ đi qua cây mục lục.
- Chữ trên giao diện viết cho người nghiệp vụ: không dùng docpipe, OCR, VLM, markdown, JSON, schema, prompt, agent, token, key… (prompt → “hướng dẫn AI”, agent → “trợ lý”, OCR/VLM → “đọc tài liệu”). Thuật ngữ kỹ thuật chỉ để trong comment code và README này.
- Nút quay lại của mọi trang con dùng một trail theo màn (`track`, `backOf` trong `app.js`): về đúng màn trước và đúng trạng thái của nó (file, mục, trang), ghi tên màn đích; mở thẳng bằng link thì về màn cha. Mục ở thanh bên bắt đầu trail mới.
