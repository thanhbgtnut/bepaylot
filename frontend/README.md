# BePaylot frontend

Giao diện web cho API `/v1` của bepaylot. Dựng bằng React 19, Vite, TypeScript và Tailwind CSS 4, theo phong cách **Google Drive (Material 3)**: font Google Sans, icon Material Symbols Rounded, cả hai đều tự host qua npm. Graph 3D dùng cùng stack với [codebase-memory-mcp](https://github.com/DeusData/codebase-memory-mcp).
## Chạy

```bash
cd frontend
npm install
npm run dev          # http://localhost:5174
```

Khi dev, Vite proxy `/v1` sang backend `http://localhost:8080`, nên trình duyệt coi như cùng origin và không cần CORS. Đổi backend bằng `BEPAYLOT_API=http://host:port npm run dev`.

Lần đầu mở, nhập **API key** trong hộp thoại "Kết nối" (tạo key bằng `make seed` ở thư mục gốc). Key được lưu trong `localStorage`. Có thể đặt sẵn bằng biến `VITE_API_KEY`, còn `VITE_API_BASE` dùng khi frontend không chạy sau proxy.

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
| `/chat`, `/chat/:sessionId` | Hỏi đáp kiểu Gemini: lời chào gradient, thẻ gợi ý, ô nhập dạng pill. Các lời gọi tool gộp thành một dòng có thể mở ra. Chọn KB và ghim `kb_filter` bằng filter chip. Citation mở popover có ảnh trang và vùng được khoanh. |
| `/wiki/:slug` | Cây thư mục kiểu Drive **theo từng file** (file, rồi mục lục của file, rồi trang wiki được trích trong khoảng trang của mục); có chế độ "Theo loại" và ô tìm. Bài viết trên trang giấy kiểu Google Docs; panel phụ liệt kê file làm bằng chứng và các liên kết; sửa tay và lịch sử phiên bản. |
| `/graph?entity=` | Knowledge graph 3D (xem dưới). |

## Graph 3D (theo codebase-memory-mcp)

Cùng thư viện với `graph-ui` của codebase-memory-mcp: **three.js** qua `@react-three/fiber`, `@react-three/drei` (OrbitControls, Html) và `@react-three/postprocessing` (Bloom). Trang này được tải lazy, nên three.js (khoảng 1 MB) chỉ tải khi mở Graph.

- **Scene** ([GraphScene.tsx](src/pages/graph/GraphScene.tsx)):
  - nút là các khối cầu dùng chung một `InstancedMesh`, màu phóng quá 1.0 để bloom tạo quầng (hub xanh sáng nhất);
  - cạnh là `lineSegments` blend cộng, màu theo loại quan hệ, tự mờ dần khi số cạnh lớn;
  - tên quan hệ (`WORKS_FOR`, `LIVES_AT`…) nằm giữa cạnh, gộp khi một cặp nút có nhiều quan hệ. Nhãn là DOM đặt trên canvas (chiếu toạ độ mỗi frame), nên luôn sắc nét và không bị bloom. Cạnh của nút đang chọn luôn có nhãn; nút "Quan hệ" bật hoặc tắt nhãn cho mọi cạnh (mặc định bật khi ≤ 150 cạnh);
  - mũi tên hình nón ở đầu đích cho biết chiều quan hệ;
  - tên nút là sprite canvas cỡ cố định trên màn hình, cho 80 nút lớn nhất (hoặc các nút đang được làm nổi);
  - tooltip HTML khi hover;
  - camera bay tới vùng được chọn, tự xoay sau 60 giây không thao tác.
- **Layout** ([layout3d.ts](src/pages/graph/layout3d.ts)): port của `src/ui/layout3d.c`, chạy phía client vì API chỉ trả nút và cạnh.
  - Đặt nút ban đầu trên vòng tròn theo cụm (loại thực thể), trục z theo độ sâu BFS.
  - Tối ưu 40 vòng: lực đẩy Barnes-Hut dùng octree (θ = 1.2), lò xo theo cạnh, lò xo neo, mỗi bước dịch tối đa 8.
  - Khi mở rộng, nút đã có đứng yên, chỉ nút mới di chuyển.
- **Màu:** mặc định theo phổ sao theo số liên kết (M đỏ, …, O xanh lam), hoặc chuyển sang tô theo loại thực thể.
- **Tương tác:**
  - click để xem chi tiết (làm nổi hàng xóm);
  - Shift+click hoặc nút "Mở rộng" để thêm hàng xóm;
  - double-click để lấy nút làm tâm (1–3 bậc);
  - tìm đường đi giữa hai nút;
  - bộ lọc loại thực thể và loại quan hệ, có chế độ "chỉ" một loại;
  - thanh trượt độ sáng cạnh, quầng sáng nút và bloom (lưu trong localStorage).

## Cấu trúc

```
src/
  api/          client.ts (cấu hình, lỗi, ảnh blob, upload, SSE) · endpoints.ts · types.ts
  components/   Layout (khung Drive), AppContext (KB, kết nối), Citation (popover), Markdown, PageImage, Thumb, toast, ui (Icon, Menu, Modal…)
  lib/          format.ts (trạng thái, citation, fold tiếng Việt) · markdown.ts
  pages/
    documents/  DocumentsPage, DocumentDetail, UploadDialog, panels
    chat/       ChatPage, useChatStream (SSE → block), model (transcript → UI)
    wiki/       WikiPage, tree.ts (file → mục lục → trang wiki)
    graph/      GraphPage, GraphScene (three.js), layout3d.ts, density.ts
```
