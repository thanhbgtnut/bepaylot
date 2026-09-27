# BePaylot frontend

Giao diện web cho API `/v1` của bepaylot. Dựng bằng React 19, Vite, TypeScript và Tailwind CSS 4, theo phong cách **Google Drive (Material 3)**: font Google Sans, icon Material Symbols Rounded, cả hai đều tự host qua npm.
## Chạy

```bash
cd frontend
npm install
npm run dev          # http://localhost:5174
```

Khi dev, Vite proxy `/v1` sang backend `http://localhost:8080`, nên trình duyệt coi như cùng origin và không cần CORS. Đổi backend bằng `BEPAYLOT_API=http://host:port npm run dev`.

Lần đầu mở sẽ vào trang **/login** (bố cục như WeKnora): đăng nhập, **Tạo tài khoản**, hoặc "Đăng nhập bằng …" khi server bật OIDC. Access token và refresh token lưu trong `localStorage`; gặp `401` thì client tự gọi `/v1/auth/refresh` một lần rồi gửi lại request, hết hạn hẳn thì quay về `/login`. Hộp thoại **Tài khoản** (avatar góc phải) để đổi mật khẩu, tạo/thu hồi API key cho script và đổi địa chỉ máy chủ. `VITE_API_BASE` dùng khi frontend không chạy sau proxy.

OIDC khi dev: đăng ký callback `http://localhost:5174/v1/auth/oidc/callback` ở nhà cung cấp; Vite gửi `X-Forwarded-Host` nên backend tự suy ra URL này.

```bash
npm run build        # typecheck + build vào dist/
npm run typecheck
```

Bản build là trang tĩnh (SPA). Khi deploy, cần phục vụ `dist/` với fallback về `index.html`, và proxy `/v1` sang API; nếu không proxy thì đặt `VITE_API_BASE` lúc build.

## Màn hình

Khung chung theo Google Drive:
- **Thanh trên:** ô tìm dạng pill, tìm file trong knowledge base đang chọn.
- **Nút "+ Mới":** tải file lên, tạo knowledge base, mở cuộc trò chuyện mới.
- **Điều hướng:** các mục dạng pill và danh sách knowledge base (giống "Bộ nhớ dùng chung").
- **Nội dung:** nằm trong panel trắng bo góc.
- **Snackbar:** hiện ở góc dưới trái; có hỗ trợ dark mode.

| Đường dẫn | Chức năng |
|---|---|
| `/documents` | File của knowledge base dạng danh sách hoặc lưới (lưới hiện thumbnail trang 1). Filter chip Loại, Trạng thái, Metadata; sắp xếp theo cột; menu kebab (mở, tải xuống, parse lại, xoá). Kéo thả file vào bất kỳ đâu để tải lên (kèm metadata, callback URL). |
| `/documents/:id?page=&hl=&tab=` | Trình xem kiểu Drive preview: nền tối, cột thumbnail trang, trang có khung bbox từng dòng OCR, thanh nổi (chuyển trang ←/→, zoom, bật/tắt khung). Panel "Chi tiết" với các tab: Chi tiết, Dòng OCR, Markdown, Blocks, Tìm (`page_from`/`page_to` tuỳ chọn), Mục lục. |
| `/chat`, `/chat/:sessionId` | Hỏi đáp kiểu Gemini: lời chào gradient, thẻ gợi ý, ô nhập dạng pill. Các lời gọi tool gộp thành một dòng có thể mở ra. Chọn hồ sơ; agent chỉ đọc hồ sơ đó. Citation mở popover có ảnh trang và vùng được khoanh. |
| `/cases/:caseId?doc=&node=` | Hồ sơ đọc theo mục lục (không gọi LLM): cột trái là mục lục hồ sơ, mỗi file mở ra cây mục lục của nó (tiêu đề, khoảng trang); cột giữa là thẻ file hoặc node đang chọn (tóm tắt, mục con) và ảnh các trang của node, kèm liên kết mở trình xem tài liệu. Nút "Hỏi về hồ sơ" mở hỏi đáp gắn với hồ sơ. |

## Cấu trúc

```
src/
  api/          client.ts (cấu hình, lỗi, ảnh blob, upload, SSE) · endpoints.ts · types.ts
  components/   Layout (khung Drive), AppContext (KB, kết nối), Citation (popover), Markdown, PageImage, Thumb, toast, ui (Icon, Menu, Modal…)
  lib/          format.ts (trạng thái, citation, fold tiếng Việt) · markdown.ts
  pages/
    documents/  DocumentsPage, DocumentDetail, UploadDialog, panels
    chat/       ChatPage, useChatStream (SSE → block), model (transcript → UI)
    cases/      CasePage (hồ sơ → file → cây mục lục → trang)
```
