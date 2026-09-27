# Prototype giao diện BePaylot

Giao diện HTML tĩnh gọi thẳng API `/v1` của bepaylot. Không cần build: HTML, CSS và JS thuần. Chỉ ba thư viện được tải từ cdnjs: `marked` và `DOMPurify` để hiển thị markdown, `cytoscape` để vẽ graph.

| Trang | Chức năng | API chính |
|---|---|---|
| `index.html` | Danh sách file trong KB (lọc theo tên, trạng thái, metadata), upload file kèm metadata. Trang chi tiết: ảnh trang có khung toạ độ từng dòng OCR, danh sách dòng (OCR/VLM, bbox, độ tin cậy, chữ OCR gốc), markdown, blocks, tìm trong file có `page_from`/`page_to`, mục lục (cây vectorless) | `/kbs`, `/kbs/:id/documents`, `/documents/:id`, `/documents/:id/pages[/:n[/image]]`, `/documents/:id/search`, `/documents/:id/tree` |
| `chat.html` | Hỏi đáp qua agent, stream SSE, hiện từng lời gọi tool `kb_*` kèm input và kết quả. Chọn KB và ghim bộ lọc metadata (`kb_filter`, ví dụ `group_code`). Bấm citation `tr.3 · d.9` để xem đúng vùng trên ảnh trang | `POST /messages` (stream), `/sessions`, `/citations` |
| `wiki.html` | Trang wiki sinh từ graph: liên kết `[[slug]]`, nguồn trích dẫn, liên kết đến và đi, sửa tay, lịch sử phiên bản | `/kbs/:id/wiki/pages[/:slug[/revisions]]` |
| `graph.html` | Graph thực thể và quan hệ: tổng quan, tìm thực thể, mở rộng hàng xóm 1–3 bước, tìm đường đi giữa hai nút, chi tiết thực thể (thuộc tính, quan hệ, bằng chứng có citation) | `/kbs/:id/graph/entities`, `/graph/entities/:id[/neighbors]`, `/kbs/:id/graph/path` |

## Chạy

```bash
# 1. Backend (cổng 8080)
make run            # hoặc binary server với -role all

# 2. Phục vụ thư mục prototypes
python3 -m http.server 5173 --directory prototypes
# mở http://localhost:5173
```

Lần đầu mở trang, nhập **API base URL** (mặc định `http://localhost:8080`) và **API key** (tạo bằng `make seed`). Cả hai được lưu trong `localStorage` của trình duyệt. Mở trực tiếp file bằng `file://` cũng chạy được, vì CORS mặc định cho phép mọi origin (`cors_origins: []`).

## Ghi chú

- Ảnh trang được tải bằng `fetch` kèm header `x-api-key`. Server có thể redirect sang presigned URL của MinIO, và MinIO trả CORS nên trình duyệt đọc được.
- Toạ độ `bbox` của dòng tính theo pixel của ảnh trang (`width` × `height` trong `/documents/:id/pages/:n`). Lớp SVG phủ lên ảnh dùng cùng `viewBox`, nên khung luôn khớp bất kể kích thước hiển thị.
- Wiki và graph chỉ có dữ liệu khi KB bật `graph_enabled`. Nút "Bật graph & xây dựng" sẽ `PATCH /kbs/:id` rồi gọi `POST /kbs/:id/graph/rebuild`.
- Nút "Dừng" trong chat chỉ ngắt stream phía trình duyệt; lượt chạy ở server vẫn tiếp tục và được lưu vào lịch sử.
