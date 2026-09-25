# BePaylot

Nền tảng xử lý tài liệu và agent AI viết bằng Go. File tải lên (PDF, ảnh scan) được
OCR thành nội dung có cấu trúc đến từng dòng và vị trí trên trang, tìm kiếm được
**không cần embedding**, trích xuất thành đồ thị tri thức kèm wiki, và đưa cho agent
dùng với trích dẫn chính xác về trang, dòng.

- **API:** [Hertz](https://github.com/cloudwego/hertz), tương thích Anthropic Messages API và AG-UI.
- **Agent:** [Eino](https://github.com/cloudwego/eino), streaming, skill, MCP.
- **Lưu trữ:** PostgreSQL (dữ liệu, full-text, graph), S3/MinIO (file gốc, ảnh trang).
- **Hàng đợi:** [asynq](https://github.com/hibiken/asynq) trên Redis, nhiều worker pool độc lập.
- **Render PDF:** [go-pdfium](https://github.com/klippa-app/go-pdfium) (PDFium), có giới hạn CPU/RAM.

Đặc tả đầy đủ: [spec/spec.md](spec/spec.md).

---

## Mục lục

1. [Bốn module](#bốn-module)
2. [Chạy nhanh](#chạy-nhanh)
3. [Luồng xử lý một tài liệu](#luồng-xử-lý-một-tài-liệu)
4. [Tìm kiếm](#tìm-kiếm)
5. [Graph và wiki](#graph-và-wiki)
6. [Agent](#agent)
7. [API](#api)
8. [Cấu hình](#cấu-hình)
9. [Triển khai](#triển-khai)
10. [Phát triển](#phát-triển)
11. [Cấu trúc thư mục](#cấu-trúc-thư-mục)
12. [Giới hạn hiện tại](#giới-hạn-hiện-tại)

---

## Bốn module

| Module | Làm gì |
|---|---|
| **1. Parser** | Nhận file (stream thẳng lên S3), render PDF thành ảnh từng trang, OCR bằng TurboOCR, ghép với text layer có sẵn trong PDF (ưu tiên PDF/A), rồi dựng thành block, dòng có bounding box và markdown có offset. Từ bất kỳ đoạn text nào cũng tra ngược được trang, dòng và vùng trên ảnh. |
| **2. Index** | Chia section, dựng **cây mục lục** (từ bookmark, heading hoặc theo trang) có tóm tắt từng nhánh và thẻ tài liệu. Tìm kiếm theo 3 chế độ: `reasoning` (LLM đọc cây rồi chỉ ra đúng dòng), `keyword` (full-text Postgres, không dấu vẫn khớp), `metadata`. **Không dùng embedding.** |
| **3. Graph / Wiki** | LLM trích xuất entity và quan hệ theo **schema cấu hình được**, bắt buộc có trích dẫn nguyên văn. Gộp entity trùng, truy vấn lân cận/đường đi, sinh trang wiki có chú thích nguồn. |
| **4. Agent** | Agent chat hiện có, thêm các tool `kb_*`, `graph_*`, `wiki_read` khi session gắn knowledge base. Mọi câu trả lời từ tài liệu kèm `citation_id`. |

Mỗi file có thể kèm **metadata tuỳ chọn** (ví dụ `ma_ho_so`), gán khi upload, kể cả upload nhiều file một lần, và lọc được ở mọi API tìm kiếm.

---

## Chạy nhanh

Cần Go 1.26+ và Docker. Không cần API key: mặc định dùng LLM giả (`fake`).

```bash
cp .env.example .env     # xem mục Cấu hình để sửa
make up                  # Postgres :5433, Redis :6380, MinIO :9110 (console :9111)
make migrate             # tạo schema
make skills-sync         # nạp skills/ vào Postgres
make seed                # in ra một API key, copy lại
make run                 # API + worker trên :8080
```

`make dev` gộp `up`, `migrate` và `run`. Swagger UI ở `http://localhost:8080/docs`.

> Nếu cổng 5433 đã bị dùng (ví dụ bởi WeKnora), đặt `BEPAYLOT_PG_PORT=5434` khi `make up`
> và sửa cổng trong `DATABASE_URL` tương ứng.

### Thử với tài liệu

```bash
KEY=sk-bepaylot-...      # từ make seed
H=(-H "x-api-key: $KEY")

# 1. Tạo knowledge base có metadata schema
KB=$(curl -s localhost:8080/v1/kbs "${H[@]}" -d '{
  "name": "Hồ sơ vay",
  "config": {"graph_enabled": true, "graph_schema": "ho_kinh_doanh"},
  "metadata_schema": {"fields": [
    {"key": "ma_ho_so", "type": "string", "normalize": "upper_trim", "indexed": true, "description": "Mã hồ sơ vay"},
    {"key": "loai_giay_to", "type": "enum", "values": ["GCN_HKD", "BCTC"]}
  ]}
}' | jq -r .id)

# 2. Upload nhiều file: metadata chung cho cả lô + metadata riêng từng file
curl -s localhost:8080/v1/kbs/$KB/documents "${H[@]}" \
  -F 'metadata={"ma_ho_so":"HS-2026-000123"}' \
  -F 'files_metadata={"gcn.pdf":{"loai_giay_to":"GCN_HKD"},"bctc.pdf":{"loai_giay_to":"BCTC"}}' \
  -F file=@gcn.pdf -F file=@bctc.pdf | jq

# 3. Theo dõi tiến độ (SSE), xem một trang
curl -sN localhost:8080/v1/documents/$DOC/events "${H[@]}"
curl -s  localhost:8080/v1/documents/$DOC/pages/1 "${H[@]}" | jq '.markdown, .lines[0]'

# 4. Tìm trong hồ sơ (gõ thường, có khoảng trắng vẫn khớp nhờ normalize)
curl -s localhost:8080/v1/search "${H[@]}" -d "{\"query\":\"Vốn kinh doanh là bao nhiêu?\",
  \"kb_ids\":[\"$KB\"],\"metadata\":{\"ma_ho_so\":\"hs-2026-000123 \"}}" | jq '.hits[] | {file_name, page_no, quote, citation_id}'

# 5. Chat với hồ sơ
curl -s localhost:8080/v1/messages "${H[@]}" -d "{\"model\":\"claude-sonnet-5\",\"max_tokens\":1024,
  \"metadata\":{\"kb_ids\":[\"$KB\"],\"kb_filter\":{\"ma_ho_so\":\"HS-2026-000123\"}},
  \"messages\":[{\"role\":\"user\",\"content\":\"Chủ hộ là ai?\"}]}" | jq
```

### Dùng LLM thật

Trong `.env`:

```bash
# Claude
BEPAYLOT_DEFAULT_PROVIDER=claude
BEPAYLOT_DEFAULT_MODEL=claude-sonnet-5
ANTHROPIC_API_KEY=sk-ant-...

# hoặc server tương thích OpenAI (LM Studio, vLLM, Ollama, llama.cpp), không cần key
BEPAYLOT_DEFAULT_PROVIDER=openai
BEPAYLOT_DEFAULT_MODEL=<model id>
OPENAI_BASE_URL=http://127.0.0.1:1234/v1
```

Tree, search và graph có thể dùng model riêng (`index.tree.model`, `search.model`,
`graph.model`); nên chọn model nhanh cho `search`. Với LLM giả, hệ thống vẫn chạy:
tóm tắt dùng trích đoạn, còn `reasoning` tự rơi về `keyword`.

---

## Luồng xử lý một tài liệu

```
upload ──► S3 ──► document:split ──► page:render (lô) ──► page:ocr (từng trang) ──► document:assemble
                  (đếm trang,        (PDFium: JPEG +       (TurboOCR + ghép           (header lặp lại,
                   bookmark, PDF/A)   text layer → S3)      text layer)                markdown toàn văn)
                                                                                           │
                    wiki:ingest ◄── graph:extract ◄── index:tree ◄── index:build ◄────────┘
                    wiki:finalize   (nếu KB bật graph)  (cây + tóm tắt) (section)
```

Các điểm chính (spec §4–§5):

- **File nặng.** Xử lý theo từng trang:
  - mỗi tài liệu chỉ có một cửa sổ trang đang render/OCR (`render_ahead_pages`, `ocr_inflight_pages`), nên nhiều file lớn chạy xen kẽ công bằng;
  - retry theo trang, trang hỏng hẳn vào dead-letter;
  - file kết thúc `partial` thay vì hỏng cả file.
- **Render PDF.**
  - Chạy trong process con PDFium; số process bằng số core dùng cho render.
  - DPI tự hạ cho trang khổ lớn; process được tái chế sau N trang; trang quá timeout thì process bị kill.
  - JPEG được encode ngay trong process con.
- **Text layer / PDF/A.**
  - PDF/A được nhận diện ngay lúc upload.
  - Text layer đạt chất lượng thì sửa dấu và số của OCR theo từng dòng, và bổ sung dòng OCR bỏ sót.
  - OCR hỏng hẳn thì trang được dựng từ text layer.
- **Khởi động lại.** Trang đã xong không bao giờ OCR lại; housekeeping định kỳ đưa việc bị treo trở lại hàng đợi.
- **Worker pool tách riêng:**
  - `core`, `render` (nặng CPU), `ocr` (nặng I/O, bảo vệ OCR), `index`, `enrichment`, `wiki`, `maintenance`;
  - mỗi pool có một lane `interactive` ưu tiên cho file đính kèm từ chat.

Trạng thái tài liệu: `queued → splitting → parsing → assembling → indexing → (enriching) → completed | partial | failed | cancelled`.

---

## Tìm kiếm

`POST /v1/search` với `mode`:

| Mode | Cách chạy | Khi nào dùng |
|---|---|---|
| `reasoning` (mặc định) | Lọc phạm vi theo KB và metadata. Nếu nhiều file, LLM đọc thẻ tài liệu để chọn file. LLM duyệt cây mục lục để chọn nhánh, rồi đọc các trang dạng dòng đánh số và chỉ ra dòng trả lời. Mỗi `quote` được đối chiếu với dòng thật, trích dẫn bịa bị loại. | Câu hỏi tự nhiên |
| `keyword` | Full-text Postgres + trigram trên section và dòng, không phân biệt dấu | Mã số, số tiền, tên riêng; nhanh và không tốn LLM |
| `metadata` | Chỉ lọc metadata, trả danh sách file | "Mọi file của hồ sơ X" |

Mỗi hit gồm `file_name`, `page_no`, `lines`, `quote`, `citation_id`
(`doc:<id>:p<trang>:l<a>-<b>`) và `bboxes` để tô sáng trên ảnh trang. Response có
`trace`, cho biết file nào và nhánh nào được chọn, số lần gọi LLM, số hit bị loại.

**Bộ lọc metadata** dùng ở `/v1/search`, `/v1/kbs/{id}/documents` và các tool của agent:

```json
{"ma_ho_so": "HS-2026-000123",
 "loai_giay_to": {"in": ["GCN_HKD", "CCCD"]},
 "ngay_nop": {"gte": "2026-01-01"},
 "chi_nhanh": {"prefix": "Binh"},
 "ghi_chu": {"exists": true}}
```

Các toán tử: `eq, ne, in, prefix, gte, gt, lte, lt, exists`. Giá trị lọc được chuẩn hoá theo
`metadata_schema` giống lúc ghi: `upper_trim`, ngày `dd/mm/yyyy` → `yyyy-mm-dd`, số.

Tìm trong một file: `POST /v1/documents/{id}/search` trả kết quả nhóm theo trang,
dùng như "Ctrl+F" cho PDF scan.

---

## Graph và wiki

- **Schema:** file YAML trong [`configs/graph_schemas/`](configs/graph_schemas/), ví dụ `ho_kinh_doanh.yaml`, được nạp vào DB khi khởi động. Có sẵn schema `generic` khi KB không cấu hình. Schema tạo/xem/chạy thử qua `/v1/graph/schemas`.
- **Trích xuất:**
  - LLM chỉ được dùng type và thuộc tính có trong schema; giá trị được ép kiểu (`money`: `"50.000.000 đồng"` → `50000000`).
  - Mỗi entity và quan hệ phải có câu trích nguyên văn nằm trong văn bản, thiếu thì bị loại.
  - Câu trích được đổi ra trang, dòng và bbox.
- **Gộp entity:**
  - Theo các thuộc tính định danh (`identity`) khi có; nếu không thì theo tên đã chuẩn hoá (bỏ dấu, bỏ "Công ty", "Ông", …).
  - Có thể giới hạn trong cùng một hồ sơ bằng `resolve_scope: [ma_ho_so]`.
  - Giá trị mâu thuẫn giữa các nguồn được lưu lại và đánh dấu `conflict`.
- **Wiki:**
  - Mỗi entity một trang, có chú thích `[^n]` trỏ về citation và liên kết chéo `[[slug]]`.
  - Trang do người dùng sửa tay không bị ghi đè; mỗi lần sửa lưu một revision.

---

## Agent

Agent giữ nguyên các khả năng sẵn có:

- **System prompt động** dựng lại mỗi lượt: ngữ cảnh môi trường, tóm tắt hội thoại, skill liên quan, hướng dẫn dùng tool.
- **Tính toán và JSON chính xác:** tool `calculate` tính bằng số hữu tỉ chính xác. Server còn tự tính các biểu thức sót trong giá trị JSON trước khi trả.
- **Skill tự tìm theo ngữ cảnh:** người dùng không cần gọi tên skill. `allowed_tools` của skill được bật tự động.
- **Tool search:** tool ngoài (MCP) chỉ hiện tên; model gọi `tool_search` để nạp schema khi cần, nên context luôn nhỏ.
- **Không chặn tin nhắn mới:** tin gửi khi đang chạy được chuyển vào lượt đang chạy (`202 steered`). Gõ "stop"/"dừng"/"hủy" để ngắt lượt.
- **Câu trả lời dài không bị cụt:** tự viết tiếp tối đa 4 lần mỗi lần gọi model. Lượt cuối không có tool để luôn kết thúc bằng câu trả lời.
- **Nhiều provider:** `claude`, `openai`-compatible, `ark`, và `fake` cho dev/test.
- **Lưu an toàn:** tin nhắn không mất khi client ngắt kết nối. Mỗi lượt ghi lại trong `agent_runs`.

**Làm việc với tài liệu.** Gắn KB vào session bằng `metadata.kb_ids` (trong `POST /v1/messages`
hoặc `PATCH /v1/sessions/{id}`). Có thể ghim `metadata.kb_filter` để mọi tìm kiếm trong session
chỉ nằm trong một hồ sơ. Khi đó agent có thêm:

| Tool | Dùng để |
|---|---|
| `kb_search` | tìm đoạn trả lời, có lọc metadata, chế độ `reasoning`/`keyword` |
| `kb_list_documents`, `kb_metadata_values` | liệt kê file theo metadata; xem có những mã hồ sơ nào |
| `kb_read_pages` | đọc trang dạng dòng đánh số `[L5] …` (tối đa 10 trang/lần) |
| `kb_document_tree`, `kb_find_in_document`, `kb_locate` | xem mục lục, tìm trong một file, giải citation |
| `graph_search_entities`, `graph_neighbors`, `wiki_read` | tra entity, quan hệ, trang wiki |

Prompt được bổ sung section `<knowledge_bases>` liệt kê KB và các trường metadata. Nhờ vậy,
khi người dùng nhắc một mã hồ sơ, model tự truyền bộ lọc. Mọi thông tin lấy từ tài liệu phải kèm `[citation_id]`.

File đính kèm trong chat: `POST /v1/sessions/{id}/attachments`. File được đưa vào KB tạm của
session, xử lý trên lane ưu tiên, và KB được gắn vào session ngay.

### MCP servers

Tắt mặc định (`BEPAYLOT_MCP_ENABLED=true` để bật). Có hai nguồn cấu hình, cả hai đều gắn/gỡ **không cần restart**:

- **File `configs/mcp.yaml`:** đọc lại mỗi `mcp.reload_interval`. Hỗ trợ transport `stdio`, `sse`, `streamable_http`, cùng `tool_allowlist` và `disabled`.
- **API `/v1/mcp/servers`:** cấu hình lưu trong Postgres và tự gắn lại khi khởi động. Mã lỗi: `502` khi không kết nối được server, `409` khi tên đã được file cấu hình giữ.

```yaml
servers:
  - name: github
    transport: stdio
    command: docker
    args: ["run", "-i", "--rm", "-e", "GITHUB_PERSONAL_ACCESS_TOKEN", "ghcr.io/github/github-mcp-server"]
    env: { GITHUB_PERSONAL_ACCESS_TOKEN: "${GITHUB_TOKEN}" }
  - name: search
    transport: streamable_http
    url: https://mcp.example.com/mcp
    headers: { Authorization: "Bearer ${SEARCH_MCP_TOKEN}" }
    tool_allowlist: ["web_search"]
```

Tool MCP có tên `mcp__<server>__<tool>`. Tool MCP **không bị sandbox**, chỉ gắn server bạn tin cậy.

### Skills

Mỗi skill là một thư mục trong `skills/` có file `SKILL.md` với frontmatter
`name`, `description` và `allowed_tools`. `description` quyết định việc tìm skill, nên viết theo
dạng "là gì + khi nào dùng". Đồng bộ bằng `make skills-sync` hoặc `POST /v1/skills/sync`.

---

## API

Tất cả nằm dưới `/v1` và cần `x-api-key` (hoặc `Authorization: Bearer`). Tài liệu đầy đủ
có ở Swagger `/docs` và `/openapi.yaml`.

| Nhóm | Endpoint |
|---|---|
| Chat | `POST /messages` (tương thích Anthropic, SSE khi `stream`), `POST /ag-ui/run` (AG-UI) |
| Session | `POST/GET /sessions`, `GET/PATCH/DELETE /sessions/{id}`, `GET /sessions/{id}/messages`, `POST /sessions/{id}/attachments` |
| Knowledge base | `POST/GET /kbs`, `GET/PATCH/DELETE /kbs/{id}`, `GET/PUT /kbs/{id}/metadata-schema`, `GET /kbs/{id}/metadata/values?key=` |
| Tài liệu | `POST/GET /kbs/{id}/documents`, `POST /kbs/{id}/documents/metadata/bulk-update`, `GET/DELETE /documents/{id}`, `GET /documents/{id}/events` (SSE), `POST /documents/{id}/cancel`, `POST /documents/{id}/reparse`, `PATCH /documents/{id}/metadata`, `GET /documents/{id}/file`, `GET /documents/{id}/markdown` |
| Trang | `GET /documents/{id}/pages`, `GET /documents/{id}/pages/{n}`, `GET /documents/{id}/pages/{n}/image` (302 → presigned S3), `POST /documents/{id}/locate`, `GET /parser/engines` |
| Tìm kiếm | `POST /search`, `POST /documents/{id}/search`, `GET /documents/{id}/tree`, `GET /citations?id=` |
| Graph / wiki | `GET /kbs/{id}/graph/entities`, `GET /graph/entities/{id}`, `GET /graph/entities/{id}/neighbors`, `GET /kbs/{id}/graph/path`, `POST /kbs/{id}/graph/rebuild`, `GET /kbs/{id}/wiki/pages`, `GET/PUT /kbs/{id}/wiki/pages/{slug}`, `GET …/revisions` |
| Schema graph | `GET/POST /graph/schemas`, `GET /graph/schemas/{name}`, `POST /graph/schemas/{name}/test` |
| Skill, MCP | `POST /skills/sync`, `GET /skills`, `GET/POST /mcp/servers`, `DELETE /mcp/servers/{name}` |
| Vận hành | `GET /admin/queues`, `GET /admin/dead-letters`, `POST /admin/dead-letters/{id}/retry`; chỉ cho email trong `http.admin_emails` |

Công khai, không cần key: `/healthz`, `/readyz`, `/docs`, `/openapi.yaml`.

Các API tài liệu trả `503` nếu server không cấu hình được S3 hoặc Redis
(ngoài môi trường `development`).

---

## Cấu hình

Cấu hình nằm trong `configs/config.yaml`; mọi giá trị `${VAR}` được lấy từ biến môi trường (file mẫu: `.env.example`).

| Biến | Ý nghĩa | Mặc định / ghi chú |
|---|---|---|
| `DATABASE_URL` | Postgres | bắt buộc |
| `BEPAYLOT_HTTP_ADDR` | địa chỉ API | `:8080` |
| `BEPAYLOT_ROLE` | `api` \| `worker` \| `all` | `all` |
| `REDIS_ADDR` | Redis cho asynq | rỗng = chạy task trong process (chỉ dev, 1 instance) |
| `S3_ENDPOINT`, `S3_BUCKET`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_REGION` | S3 / MinIO | bucket rỗng = lưu trong RAM (chỉ `development`) |
| `TURBOOCR_URL` | engine OCR mặc định (`POST /ocr/raw`) | |
| `BEPAYLOT_RENDER_MODE` | `multi_threaded` (production, cần `pdfium-worker`) \| `webassembly` | `webassembly` |
| `BEPAYLOT_PDFIUM_WORKER` | đường dẫn binary `pdfium-worker` | |
| `BEPAYLOT_DEFAULT_PROVIDER`, `BEPAYLOT_DEFAULT_MODEL` | LLM mặc định | `fake` |
| `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `OPENAI_BASE_URL` | khoá / endpoint LLM | |
| `BEPAYLOT_MCP_ENABLED` | bật MCP | `false` |
| `BEPAYLOT_AUTH_BYPASS` | bỏ qua xác thực (**chỉ dev**) | `false` |

Các nhóm tinh chỉnh chính trong `config.yaml` (giải thích chi tiết ở spec §11):

- `workers.*`: concurrency từng pool, cửa sổ trang cho mỗi tài liệu.
- `parser.render.*`: DPI, trần pixel, số worker, tái chế, timeout, cache.
- `parser.text_layer.*`, `index.*`, `search.*`: ngân sách token, số lần gọi LLM tối đa, cache.
- `graph.*`, `wiki.*`.

---

## Triển khai

Một binary, chọn vai trò bằng `-role`:

```bash
bin/server -config configs/config.yaml -role api      # chỉ HTTP
bin/server -config configs/config.yaml -role worker   # chỉ worker (scale theo tải OCR)
```

Chạy API và worker thành hai deployment riêng, dùng chung Postgres, Redis và S3.

Docker (`deploy/Dockerfile`):

```bash
docker build -f deploy/Dockerfile --target api    -t bepaylot-api .
docker build -f deploy/Dockerfile --target worker -t bepaylot-worker .   # kèm libpdfium + pdfium-worker
```

Image `worker` bật `BEPAYLOT_RENDER_MODE=multi_threaded`: mỗi trang được render trong process
PDFium riêng. Nên đặt `resources.limits` cho container; image đã có `GOMEMLIMIT`.

Build `pdfium-worker` ngoài Docker cần libpdfium và `pkg-config pdfium`:

```bash
make pdfium-worker        # CGO_ENABLED=1 go build -tags pdfium_cgo ./cmd/pdfium-worker
```

---

## Phát triển

```bash
make test          # unit test, không cần mạng hay DB
make test-db       # thêm test tích hợp, chạy trên DB riêng bepaylot_test
make bench-render  # đo tốc độ render trang (BEPAYLOT_RENDER_MODE=multi_threaded để đo PDFium native)
make swag          # sinh lại OpenAPI sau khi thêm/sửa endpoint
make vet
```

- **Không bao giờ** trỏ `TEST_DATABASE_URL` vào database dev hoặc production. Test tích hợp tạo và xoá dữ liệu của riêng nó, nhưng vẫn phải chạy trên DB riêng.
- Thêm endpoint: viết handler có annotation swag, đăng ký route trong `internal/router`, rồi `make swag`. Test `TestRoutesMatchOpenAPISpec` fail nếu route thiếu tài liệu. Xem [docs/adding-endpoints.md](docs/adding-endpoints.md).
- Quy tắc module (spec §3.3), được `internal/archtest` kiểm tra:
  - service không import service khác, mà đi qua `internal/types/interfaces`;
  - `internal/parser` là thư viện thuần, không đụng DB, queue, storage hay HTTP.
- Test đầu-cuối dùng `internal/testkit`: gồm Postgres thật, storage trong RAM, queue in-process, OCR giả và LLM kịch bản.

---

## Cấu trúc thư mục

```
cmd/
  server/          API và/hoặc worker (-role), -migrate-only
  pdfium-worker/   process con PDFium cho render multi_threaded (cgo, tag pdfium_cgo)
  seed/            tạo user + API key
  skills-sync/     đồng bộ skills/ vào Postgres
configs/           config.yaml, mcp.yaml, graph_schemas/*.yaml
migrations/        goose SQL migrations (postgres/), được nhúng vào binary
internal/
  container/       nối các module, vai trò api/worker, housekeeping
  router/          Hertz engine và bảng route
  handler/         HTTP handler (+ dto/, sse/)
  middleware/      auth, request id, recovery, CORS
  types/           entity, topology hàng đợi, kiểu search/graph; interfaces/ = hợp đồng giữa module
  application/
    repository/postgres/   repository pgx (+ bộ dựng SQL lọc metadata)
    service/document/      Module 1: upload, split → render → ocr → assemble, locate
    service/index/         Module 2: section, cây mục lục, search
    service/graph/         Module 3: schema, trích xuất, gộp entity, truy vấn graph
    service/wiki/          Module 3: trang wiki
    service/metadata/      kiểm tra và chuẩn hoá metadata
  parser/          thư viện thuần: turboocr/, assemble/, textlayer/, pdf/ (go-pdfium), imagefile/
  queue/           asynq theo pool, dead-letter, queue in-process cho dev/test
  storage/         S3, store trong RAM, cache file nguồn trên đĩa
  textutil/        bỏ dấu tiếng Việt, độ tương đồng, ước lượng token
  agent/ llm/ tools/ skills/ mcp/ retrieval/   Module 4 (agent)
  archtest/        kiểm tra ranh giới module
  testkit/         harness cho test tích hợp
deploy/            docker-compose (Postgres, Redis, MinIO), Dockerfile (target api / worker)
docs/              OpenAPI sinh bởi swaggo + Swagger UI nhúng
skills/            tài liệu skill
spec/              đặc tả (spec.md) và dữ liệu mẫu parser
```

---

## Giới hạn hiện tại

Trạng thái chi tiết có ở spec §15.

- **Chưa kiểm với TurboOCR thật, PDFium native và LLM thật.** Các luồng này đã chạy đầu-cuối với OCR giả, WebAssembly PDFium và LLM giả/kịch bản; cần đánh giá chất lượng khi cắm dịch vụ thật.
- **Chưa làm:**
  - `parser.pdf_mode=auto` (bỏ qua OCR cho trang có text tốt);
  - DOCX/XLSX;
  - TIFF nhiều trang;
  - kiểm tra citation phía server trước khi stream câu trả lời của agent.
- **Hạn chế đã biết:**
  - Cache search nằm trong RAM của từng instance.
  - Trong lúc reparse toàn bộ, tài liệu tạm thời không tìm được.
  - Chưa có rate limit hay phân quyền nhiều tenant; KB thuộc về một user.
  - `http_fetch` chặn host nội bộ; `web_search` là stub khi chưa cấu hình backend.
