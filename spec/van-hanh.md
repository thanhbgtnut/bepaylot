# BePaylot — Tài liệu vận hành

> Bàn giao cho: đội vận hành OPN · Phiên bản dịch vụ: spec 0.7 · Ngày: 2026-09-27
>
> Kèm theo: [`van-hanh/healthcheck.sh`](van-hanh/healthcheck.sh) (kiểm tra nhanh toàn bộ dịch vụ) và [`van-hanh/kiem-tra.sql`](van-hanh/kiem-tra.sql) (15 câu SQL chỉ đọc để xem trạng thái xử lý).
> Thiết kế chi tiết: [spec.md](spec.md). Tích hợp cho hệ thống khác: [README.md](../README.md#tích-hợp-từ-backend-khác-ví-dụ-bpm-payment).

Mục lục

1. [Dịch vụ làm gì](#1-dịch-vụ-làm-gì)
2. [Thành phần và phụ thuộc](#2-thành-phần-và-phụ-thuộc)
3. [Triển khai, khởi động, dừng, nâng cấp](#3-triển-khai-khởi-động-dừng-nâng-cấp)
4. [Kiểm tra trạng thái dịch vụ](#4-kiểm-tra-trạng-thái-dịch-vụ)
5. [Vận hành từng tính năng](#5-vận-hành-từng-tính-năng)
6. [Xử lý sự cố (runbook)](#6-xử-lý-sự-cố-runbook)
7. [Công việc định kỳ](#7-công-việc-định-kỳ)
8. [Bảo mật khi vận hành](#8-bảo-mật-khi-vận-hành)
9. [Giới hạn đã biết khi bàn giao](#9-giới-hạn-đã-biết-khi-bàn-giao)

---

## 1. Dịch vụ làm gì

BePaylot nhận file hồ sơ (PDF, ảnh scan), đọc nội dung từng trang bằng OCR, dựng mục lục và một "wiki" cho mỗi **bộ hồ sơ** (case, ví dụ mã thanh toán `RT112233`). Người dùng hoặc hệ thống khác (ví dụ BPM Payment) hỏi **agent** AI để tra cứu, bóc tách thông tin hoặc kiểm tra tuân thủ trong đúng một bộ hồ sơ; câu trả lời có trích dẫn về trang và dòng gốc.

Luồng chính của một file:

```
upload → split → render trang (PDFium) → OCR (TurboOCR [+VLM]) → assemble → index (mục lục, LLM tóm tắt)
       → wiki của hồ sơ (LLM trích xuất 1 lần/file) → callback cho hệ thống gọi (nếu có)
```

Người dùng: giao diện web (`/login`, quản lý tài liệu, hỏi đáp, wiki, graph) và API `/v1/*` (API key).

---

## 2. Thành phần và phụ thuộc

### 2.1 Thành phần của BePaylot

Một binary `server`, chạy theo vai trò (`-role` hoặc `BEPAYLOT_ROLE`):

| Vai trò | Việc | Image | Scale |
|---|---|---|---|
| `api` | HTTP API `/v1/*`, SSE, giao tiếp người dùng | `deploy/Dockerfile --target api` | theo số request; stateless |
| `worker` | chạy task nền: render, OCR, index, wiki, xoá, callback, housekeeping | `--target worker` (kèm libpdfium + `pdfium-worker`) | theo tải OCR; stateless |
| `all` | cả hai trong một process | — | **chỉ dev** |

Frontend là trang tĩnh (`frontend/dist`), phục vụ bằng nginx/CDN, proxy `/v1` sang `api` (fallback mọi đường dẫn khác về `index.html`).

Worker chia thành các **pool** độc lập (một pool nghẽn không chặn pool khác). Concurrency cấu hình ở `workers.concurrency`:

| Pool | Queue (Redis) | Task | Concurrency mặc định |
|---|---|---|---|
| core | `default`, `interactive`, `callback` | `document:split`, `document:assemble`, `document:callback` | 4 |
| render | `render`, `render_interactive` | `page:render` | `parser.render.workers` (0 = min(CPU−1, 4)) |
| ocr | `page`, `page_interactive` | `page:ocr` | 8 |
| index | `index`, `index_interactive` | `index:build`, `index:tree` | 6 |
| wiki | `wiki` | `wiki:ingest`, `wiki:lint`, `wiki:index` | 8 |
| maintenance | `low` | `document:delete`, `case:delete`, `document:gen_cleanup`, `housekeeping:sweep` | 2 |

Queue `*_interactive` (file đính kèm trong chat) có trọng số 3, được ưu tiên hơn queue thường (trọng số 1).

### 2.2 Phụ thuộc bên ngoài

| Phụ thuộc | Dùng cho | Khi hỏng thì |
|---|---|---|
| **PostgreSQL 16+** (pgvector, `pg_trgm`, `unaccent`, `citext`, `pgcrypto`) | toàn bộ dữ liệu: user, token, hồ sơ, trang, wiki, phiên hỏi đáp, trạng thái task | **toàn bộ dịch vụ dừng**; `/readyz` trả 503 |
| **Redis 7** | hàng đợi task (asynq) | upload vẫn nhận nhưng không xử lý; worker dừng. Task trong Redis mất nếu Redis mất dữ liệu → housekeeping tự đẩy lại tài liệu đứng yên (§5.8) |
| **S3 / MinIO** | file gốc, ảnh trang, kết quả OCR thô, markdown | upload lỗi; xem ảnh trang lỗi; worker không render/OCR được |
| **TurboOCR** (`TURBOOCR_URL`, `POST /ocr/raw`) | OCR mặc định | file mới lỗi OCR → `partial`/`failed`; tài liệu cũ, hỏi đáp vẫn chạy |
| **VLM** (tuỳ chọn, provider `vlm` = `VLM_BASE_URL`, OpenAI-compatible; hoặc `BEPAYLOT_VLM_PROVIDER`) | engine `turboocr_vlm` đọc vùng ảnh qua agent (streaming): mỗi trang mỗi nhóm class một lần gọi, tiêu đề/bảng gọi riêng, con dấu không gọi | chỉ ảnh hưởng hồ sơ dùng engine này (`on_error: fallback` giữ text OCR). Số lần gọi và token từng trang: cột `document_pages.raw -> 'calls'` |
| **LLM** (`OPENAI_BASE_URL`/`ANTHROPIC_API_KEY`, `BEPAYLOT_DEFAULT_MODEL`) | agent hỏi đáp, search `reasoning`, tóm tắt mục lục, trích xuất wiki | agent/search lỗi; mục lục và wiki hạ về chế độ không LLM (§5.4–5.5) |
| **OIDC provider** (tuỳ chọn) | đăng nhập SSO | chỉ nút "Đăng nhập bằng …" lỗi; email + mật khẩu và API key vẫn chạy |

### 2.3 Cổng mặc định

| Thành phần | Cổng |
|---|---|
| API | `8080` (`BEPAYLOT_HTTP_ADDR`) |
| Web (dev, Vite) | `5174` |
| Postgres / Redis / MinIO (docker-compose dev) | `5433` (hoặc `BEPAYLOT_PG_PORT`) / `6380` / `9110`, console `9111` |

---

## 3. Triển khai, khởi động, dừng, nâng cấp

### 3.1 Biến môi trường bắt buộc và quan trọng

Cấu hình đầy đủ ở `configs/config.yaml`; mọi `${VAR}` lấy từ môi trường (mẫu: `.env.example`).

| Biến | Bắt buộc | Ghi chú vận hành |
|---|---|---|
| `DATABASE_URL` | có | Postgres |
| `BEPAYLOT_ENV` | có | `production` ở môi trường thật (khác `development` thì thiếu S3/Redis là lỗi, không lặng lẽ dùng RAM) |
| `BEPAYLOT_ROLE` | có | `api` hoặc `worker` |
| `REDIS_ADDR`, `REDIS_USERNAME`, `REDIS_PASSWORD`, `REDIS_DB` | có | rỗng = chạy task trong process, **chỉ dev** |
| `S3_ENDPOINT`, `S3_REGION`, `S3_BUCKET`, `S3_ACCESS_KEY`, `S3_SECRET_KEY` | có | bucket rỗng = lưu trong RAM, **chỉ dev** |
| `TURBOOCR_URL` | có | |
| `BEPAYLOT_DEFAULT_PROVIDER`, `BEPAYLOT_DEFAULT_MODEL`, `OPENAI_BASE_URL`, `OPENAI_API_KEY` (hoặc `ANTHROPIC_API_KEY`) | có | provider `fake` chỉ dùng cho test |
| `BEPAYLOT_JWT_SECRET` | nên đặt | khoá ký phiên đăng nhập web; rỗng = tự sinh một lần và lưu trong bảng `app_secrets` |
| `BEPAYLOT_REGISTRATION` | | `open` (ai cũng tự tạo tài khoản được) \| `closed` |
| `BEPAYLOT_ADMIN_EMAILS` | nên đặt | danh sách email (phân cách dấu phẩy) được gọi `/v1/admin/*`; rỗng = tắt |
| `BEPAYLOT_AUTH_BYPASS` | | **phải là `false`** ngoài dev |
| `BEPAYLOT_CALLBACK_SECRET` | nên đặt | ký HMAC callback gửi cho hệ thống tích hợp |
| `BEPAYLOT_CALLBACK_ALLOW_PRIVATE` | | `false` ngoài dev; `true` nếu hệ thống nhận callback nằm trong mạng nội bộ |
| `BEPAYLOT_OIDC_*` | | xem §5.1 |
| `BEPAYLOT_RENDER_MODE`, `BEPAYLOT_PDFIUM_WORKER` | | image `worker` đã đặt `multi_threaded` + `/app/pdfium-worker` |
| `BEPAYLOT_LOG_FORMAT`, `BEPAYLOT_LOG_LEVEL` | | `json` + `info` ở production |

### 3.2 Khởi động

- Migration DB **tự chạy khi khởi động** (`db.auto_migrate: true`, goose, file `migrations/postgres/*.sql`). Migration **không có khoá** giữa các process: khi có migration mới, chạy trước một lần `server -config configs/config.yaml -migrate-only` (job/init container), rồi mới khởi động các replica.
- Thứ tự: Postgres, Redis, S3 sẵn sàng → `api` → `worker`. Process `api` báo sẵn sàng khi `/readyz` trả 200.
- Log khởi động bình thường: `migrations applied`, `workers started pools=[core render ocr index wiki maintenance]` (worker), `bepaylot listening addr=:8080 role=api` (api).
- Nếu thấy `document modules disabled err=…` ở `api`: thiếu hoặc sai S3/Redis; các API tài liệu sẽ trả 503.

### 3.3 Dừng

- Gửi `SIGTERM`; server dừng nhận request, chờ tối đa `http.shutdown_timeout` (20 s).
- Task đang chạy trên worker bị dừng sẽ được asynq trả lại hàng đợi; trang đang OCR dở được housekeeping đưa lại sau khoảng 8 phút. Không mất dữ liệu.

### 3.4 Scale

- `api`: thêm replica sau load balancer; không cần sticky session (phiên web là JWT, dùng chung khoá ký qua `BEPAYLOT_JWT_SECRET` hoặc `app_secrets`).
- `worker`: thêm replica khi hàng đợi `page` tồn lâu. Mỗi replica chạy đủ các pool. Nên đặt `resources.limits` (image đã có `GOMEMLIMIT=700MiB`); mỗi process PDFium con giới hạn `parser.render.max_worker_rss_mb` (1 GB, chỉ kiểm được trên Linux).

### 3.5 Nâng cấp phiên bản

1. Đọc mục "Thay đổi so với…" ở đầu [spec.md](spec.md) và thư mục `migrations/postgres` (migration mới).
2. **Backup Postgres** (§7.1) trước khi nâng cấp có migration.
3. Chạy `server -config configs/config.yaml -migrate-only` bằng image mới (một lần), rồi deploy `api` và `worker`.
4. Chạy `healthcheck.sh` (§4.1) và smoke test (§4.2).
5. Rollback: deploy lại image cũ. Migration mới thường chỉ **thêm** cột/bảng nên image cũ vẫn chạy được; nếu migration có xoá dữ liệu (ví dụ `0014` xoá graph cũ) thì phải khôi phục từ backup.

---

## 4. Kiểm tra trạng thái dịch vụ

### 4.1 Kiểm tra nhanh bằng script

```bash
# Tối thiểu: API + phụ thuộc (đọc cấu hình từ file env của dịch vụ)
ENV_FILE=/etc/bepaylot/.env BP_URL=https://bepaylot.congty.vn spec/van-hanh/healthcheck.sh

# Đầy đủ: thêm engine OCR, hàng đợi, dead-letter 24h (cần API key của email trong BEPAYLOT_ADMIN_EMAILS)
ENV_FILE=/etc/bepaylot/.env BP_URL=https://bepaylot.congty.vn BP_ADMIN_KEY=sk-bepaylot-… spec/van-hanh/healthcheck.sh
```

Mã thoát `0` = OK, `1` = có mục FAIL (dùng được cho cron/monitoring). Kết quả mẫu:

```
1. API
  OK    /healthz (process sống)
  OK    /readyz (Postgres) model=qwen/qwen3.8-27b
  OK    /v1/auth/config (xác thực bật)
2. Phụ thuộc
  OK    Redis localhost:6380
  OK    S3/MinIO http://localhost:9110
  FAIL  TurboOCR http://10.215.122.20:30189 không kết nối được — tài liệu mới sẽ lỗi OCR
  OK    LLM https://api.groq.com/openai/v1
3. Hàng đợi và engine (cần BP_ADMIN_KEY)
  OK    engine OCR sẵn sàng
        page                 pool=ocr pending=0 active=0 retry=0 archived=1
        …
  OK    hàng đợi: 0 task chờ, 0 đang retry
  WARN  2 dead-letter trong 24 giờ qua:
        #6 page:ocr bcb14bcb-… — page 1: turboocr: request: … connect: connection refused
```

Cần `curl`; `jq` và `redis-cli` nếu có sẽ cho kết quả chi tiết hơn.

### 4.2 Smoke test sau deploy (5 phút)

1. `GET /healthz` → `{"status":"ok"}`; `GET /readyz` → `"status":"ready"`.
2. Mở web `/login`, đăng nhập bằng tài khoản vận hành → thấy trang Tài liệu.
3. Upload 1 file PDF nhỏ vào một hồ sơ thử (ví dụ `OPS-SMOKE`, loại Mặc định) → trạng thái đi tới **Hoàn tất** trong vài phút.
4. Mở **Hỏi đáp**, chọn hồ sơ thử, hỏi "File này nói về gì?" → có câu trả lời kèm trích dẫn.
5. Xoá hồ sơ thử: `DELETE /v1/cases/{id}` (id lấy từ `GET /v1/kbs/{kb_id}/cases/by-code/OPS-SMOKE`).

### 4.3 Endpoint trạng thái

| Endpoint | Xác thực | Kiểm | Dùng cho |
|---|---|---|---|
| `GET /healthz` | không | process còn sống | liveness probe |
| `GET /readyz` | không | ping Postgres (timeout 2 s); trả model/provider LLM mặc định | readiness probe; **không** kiểm Redis, S3, OCR, LLM |
| `GET /v1/auth/config` | không | cấu hình đăng nhập; `auth_bypass` phải là `false` | cảnh báo cấu hình sai |
| `GET /v1/parser/engines` | API key | engine OCR đăng ký, `available`, `error` | TurboOCR: chỉ báo **thiếu cấu hình** hoặc **circuit breaker đang mở** (5 lỗi liên tiếp → ngắt 30 s), không gọi thử mạng; VLM: chỉ kiểm provider có trong `llm.providers` (không gọi mạng; dùng `healthcheck.sh` để gọi `GET /models`) |
| `GET /v1/admin/queues` | admin | mỗi queue: `pending`, `active`, `scheduled`, `retry`, `archived` | độ tồn hàng đợi. `error: NOT_FOUND … does not exist` = queue chưa từng có task, bình thường |
| `GET /v1/admin/dead-letters?task_type=&scope_id=&limit=` | admin | task đã hết lượt retry | xem lỗi, retry (§6) |
| `POST /v1/admin/dead-letters/{id}/retry` | admin | đưa lại task vào hàng đợi | sau khi đã sửa nguyên nhân |

Probe Kubernetes gợi ý: liveness `GET /healthz` (period 10 s, fail 3), readiness `GET /readyz` (period 10 s). Worker không mở cổng HTTP khi `role=worker`: dùng liveness theo process, và theo dõi hàng đợi (§4.6).

### 4.4 Kiểm tra từng phụ thuộc bằng tay

```bash
pg_isready -d "$DATABASE_URL"
redis-cli -h <host> -p <port> ${REDIS_USERNAME:+--user $REDIS_USERNAME} ${REDIS_PASSWORD:+-a $REDIS_PASSWORD} ping           # PONG
curl -s -o /dev/null -w '%{http_code}\n' "$S3_ENDPOINT/minio/health/live"          # 200 (MinIO)
curl -s -X POST "$TURBOOCR_URL/ocr/raw" -H 'Content-Type: application/octet-stream' \
     --data-binary @trang-mau.jpg | head -c 300                                    # JSON có results[]
curl -s "$OPENAI_BASE_URL/models" -H "Authorization: Bearer $OPENAI_API_KEY" | head -c 300
curl -s "$VLM_BASE_URL/models" | head -c 300                                        # nếu dùng VLM
```

Thử agent đầu-cuối (tốn 1 lượt LLM):

```bash
curl -s $BP_URL/v1/messages -H "x-api-key: $KEY" -H 'Content-Type: application/json' \
  -d '{"max_tokens":64,"messages":[{"role":"user","content":"ping"}]}' | jq '.content[0].text, .usage'
```

### 4.5 Kiểm tra bằng SQL

`spec/van-hanh/kiem-tra.sql` gồm 15 câu **chỉ đọc**:

```bash
psql "$DATABASE_URL" -f spec/van-hanh/kiem-tra.sql
```

| # | Nội dung | Bình thường |
|---|---|---|
| 1 | tài liệu theo trạng thái | phần lớn `completed`; `failed` ít và có lý do |
| 2 | tài liệu đứng yên > 30 phút ở trạng thái đang xử lý | **rỗng** |
| 3–4 | dead-letter 24 giờ / 20 mới nhất | rỗng, hoặc có lý do đã biết |
| 5–6 | callback theo trạng thái; callback thất bại/quá hạn | `failed` = 0 hoặc đã báo bên nhận |
| 7 | wiki của hồ sơ theo trạng thái | `ready`; `building` chỉ trong lúc đang xử lý; `failed` cần xem |
| 8 | việc wiki còn tồn | rỗng hoặc `min(enqueued_at)` mới vài phút |
| 9 | trang lỗi gần nhất | rỗng |
| 10 | agent 24 giờ theo giờ: lượt, lỗi, độ trễ, token | `errors` ≈ 0 |
| 11–12 | tài khoản, phiên, API key và lần dùng cuối | key không dùng lâu → cân nhắc thu hồi |
| 13 | thời gian trung bình từng bước | `ocr` vài giây/trang (TurboOCR), vài chục giây/trang với VLM |
| 14 | file xong nhưng chưa vào wiki > 30 phút | rỗng |
| 15 | dung lượng DB | theo dõi xu hướng |

### 4.6 Chỉ số và ngưỡng cảnh báo gợi ý

| Chỉ số | Nguồn | Cảnh báo | Nghiêm trọng |
|---|---|---|---|
| `/healthz`, `/readyz` | HTTP | 1 lần lỗi | 3 lần liên tiếp |
| Tổng `pending` các queue | `/v1/admin/queues` | > 500 trong 15 phút | > 2000 hoặc tăng liên tục 1 giờ |
| Queue `page` `pending` | như trên | > 200 trong 15 phút | TurboOCR chậm/hỏng |
| Dead-letter mới | SQL #3 / API | ≥ 1 / giờ | ≥ 10 / giờ |
| Tài liệu đứng yên > 30 phút | SQL #2 | ≥ 1 | ≥ 10 |
| Callback `failed` | SQL #5 | ≥ 1 | — (báo bên nhận) |
| Tỉ lệ lỗi agent | SQL #10 | > 5% trong 1 giờ | > 20% |
| Log `level=ERROR` | log | > 10 / 5 phút | > 100 / 5 phút |
| TurboOCR, LLM, S3, Redis | `healthcheck.sh` | 1 lần FAIL | 3 lần liên tiếp |

### 4.7 Log

- `BEPAYLOT_LOG_FORMAT=json`: mỗi dòng một JSON (`time`, `level`, `msg`, và các trường ngữ cảnh). Mỗi request có một dòng `msg=http` với `method`, `path`, `status`, `dur_ms`, `request_id`; client có thể gửi `X-Request-Id` để nối log hai hệ thống.
- Thông báo cần theo dõi:

| `msg` | Ý nghĩa |
|---|---|
| `task dead-lettered` | một task hết lượt retry (kèm `task_type`, lỗi) → xem §6 |
| `panic recovered` | lỗi lập trình trong một request → báo đội phát triển kèm `request_id` |
| `document modules disabled` | `api` khởi động thiếu S3/Redis |
| `document points to a missing case` | dữ liệu lệch (document trỏ tới hồ sơ không tồn tại) → báo đội phát triển |
| `pdf: respawn instance failed` | không khởi động lại được process PDFium → kiểm `pdfium-worker`, RAM |
| `housekeeping: reset stale pages` | đã tự đưa lại các trang treo (thông tin) |
| `oidc sign-in failed` | đăng nhập SSO lỗi, kèm mã (`code=`) |
| `store dead letter failed`, `mark failed`, `persist assistant message failed` | lỗi ghi DB → kiểm Postgres |

---

## 5. Vận hành từng tính năng

Mỗi mục: tính năng làm gì, cấu hình, cách kiểm tra, sự cố thường gặp.

### 5.1 Đăng nhập, tài khoản, API key, OIDC

**Làm gì.** Người dùng tự tạo tài khoản và đăng nhập ở `/login` (email + mật khẩu), hoặc đăng nhập qua OIDC (SSO). Hệ thống khác gọi API bằng API key. Cơ chế giống WeKnora:

- Đăng nhập cấp **access token** (JWT, mặc định 24 giờ) và **refresh token** (7 ngày, dùng một lần). Web tự làm mới phiên; hết hạn hẳn thì về `/login`.
- Mọi token được ghi (dạng hash) vào bảng `auth_tokens`: **đăng xuất** hoặc **đổi mật khẩu** thu hồi **mọi** phiên web của tài khoản đó.
- **API key** (`sk-bepaylot-…`) **không hết hạn**: dùng được tới khi bị thu hồi. Đăng xuất, đổi mật khẩu, đổi `BEPAYLOT_JWT_SECRET` **không** ảnh hưởng API key.
- Dữ liệu (knowledge base, hồ sơ, phiên hỏi đáp) thuộc về **tài khoản** tạo ra nó; tài khoản khác không thấy.

**Cấu hình:** `auth.*` trong `config.yaml`; `BEPAYLOT_JWT_SECRET`, `BEPAYLOT_REGISTRATION`, `BEPAYLOT_OIDC_ENABLED`, `BEPAYLOT_OIDC_DISPLAY_NAME`, `BEPAYLOT_OIDC_ISSUER_URL`, `BEPAYLOT_OIDC_CLIENT_ID`, `BEPAYLOT_OIDC_CLIENT_SECRET`, `BEPAYLOT_OIDC_SCOPES`, `BEPAYLOT_OIDC_REDIRECT_URL`, `BEPAYLOT_OIDC_FRONTEND_URL`.

**Bật OIDC (Keycloak, Azure AD, Google…):**

1. Tạo client loại *confidential*, grant *authorization code*, scope `openid email profile`.
2. Redirect URI ở provider = `https://<domain-web>/v1/auth/oidc/callback`. Đặt đúng giá trị này vào `BEPAYLOT_OIDC_REDIRECT_URL` ở production (không để hệ thống tự suy từ header).
3. Nếu web và API khác domain: thêm domain web vào `http.cors_origins`, và đặt `BEPAYLOT_OIDC_FRONTEND_URL=https://<domain-web>/login`.
4. Khởi động lại `api`; trang login hiện nút "Đăng nhập bằng <DISPLAY_NAME>".
5. Người dùng OIDC được nhận ra theo `sub`, rồi theo email (gắn vào tài khoản cùng email có sẵn); chưa có thì tự tạo. Provider phải trả email đã xác minh.

**Tác vụ vận hành thường gặp:**

| Việc | Cách làm |
|---|---|
| Cấp tài khoản khi đăng ký đang tắt | `make seed EMAIL=… PASSWORD='…'` (hoặc `/app/seed -config … -email … -password …` trong container), hoặc tạm mở đăng ký |
| Đặt lại mật khẩu cho người dùng quên | `/app/seed -email <email> -password '<mới>'` (đặt mật khẩu, kèm in ra một API key mới — thu hồi key đó nếu không dùng). Người dùng đổi lại ở **Tài khoản → Đổi mật khẩu** |
| Khoá tài khoản | `UPDATE users SET is_active = false WHERE email = '…';` rồi `UPDATE auth_tokens SET revoked_at = now() WHERE user_id = (SELECT id FROM users WHERE email='…') AND revoked_at IS NULL;` (mọi request tiếp theo, kể cả bằng API key, bị từ chối) |
| Mở khoá | `UPDATE users SET is_active = true WHERE email = '…';` |
| Đăng xuất một người khỏi mọi thiết bị | câu `UPDATE auth_tokens …` ở trên |
| Thu hồi API key | web: **Tài khoản → API key → Thu hồi**, hoặc `DELETE /v1/auth/api-keys/{id}` bằng phiên của chủ key; khẩn cấp: `UPDATE api_keys SET revoked_at = now() WHERE key_prefix = 'sk-bepaylot-xxxx';` |
| Xem key nào còn dùng | SQL #12 |
| Đổi khoá ký JWT | đổi `BEPAYLOT_JWT_SECRET` trên **mọi** replica `api` rồi khởi động lại; nếu đang để trống thì `DELETE FROM app_secrets WHERE name='jwt_secret';` rồi khởi động lại mọi `api`. Mọi người phải đăng nhập lại; API key không ảnh hưởng |

**Sự cố:**

| Triệu chứng | Nguyên nhân / xử lý |
|---|---|
| Web liên tục quay về `/login` | các replica `api` khác khoá ký (đặt `BEPAYLOT_JWT_SECRET` giống nhau, hoặc để trống cho tất cả); đồng hồ server lệch |
| `401 invalid api key` với hệ thống tích hợp | key đã thu hồi / gõ sai; kiểm SQL #12 theo `key_prefix` |
| `403 account disabled` | tài khoản bị khoá (`users.is_active=false`) |
| OIDC về `/login#oidc_error=invalid_state` | quá 10 phút giữa lúc bấm nút và lúc quay về, hoặc trình duyệt chặn cookie; thử lại |
| `oidc_error=exchange_failed` / `invalid_id_token` | sai `CLIENT_SECRET`, redirect URI ở provider khác `BEPAYLOT_OIDC_REDIRECT_URL`, lệch giờ server |
| `oidc_error=missing_email` / `email_not_verified` | provider không trả email hoặc email chưa xác minh; bổ sung scope `email`, xác minh email ở provider |
| `oidc_error=provider_unavailable` | `api` không gọi được `ISSUER_URL/.well-known/openid-configuration` (mạng, DNS, TLS) |
| `400 return_to is not a trusted origin` | domain web chưa có trong `http.cors_origins` |

### 5.2 Knowledge base và hồ sơ (case)

**Làm gì.** Knowledge base (KB) là kho của một tài khoản; trong KB, mỗi **hồ sơ** gom các file theo một mã (ví dụ `RT112233`). Agent chỉ tìm trong đúng một hồ sơ. Loại hồ sơ khai báo trong `configs/case_types/*.yaml` (quy tắc mã, metadata, engine OCR, bật wiki): có `default` (mã tự do), `thanh_toan` (`RT` + 6 số), `tin_dung_dn`.

**Kiểm tra:** web → chọn KB → lọc theo hồ sơ; API `GET /v1/kbs/{id}/cases`, `GET /v1/cases/{id}` (số file theo trạng thái, `wiki_status`).

**Tác vụ:**

- Thêm/sửa loại hồ sơ: sửa YAML trong `configs/case_types/`, deploy lại cấu hình, khởi động lại `api` và `worker`.
- Đóng hồ sơ (không nhận thêm file): `PATCH /v1/cases/{id}` `{"status":"closed"}`.
- Xoá hồ sơ: `DELETE /v1/cases/{id}` (web chưa có nút xoá hồ sơ) → xoá mềm, task `case:delete` xoá file, trang, wiki ở nền (queue `low`). Housekeeping chạy lại nếu dở dang.

**Sự cố:** `422` khi upload = mã hồ sơ không khớp mẫu của loại hồ sơ, hoặc thiếu mã; `409` = hồ sơ đã đóng.

### 5.3 Upload và xử lý tài liệu (Parser)

**Làm gì.** Nhận file (PDF, JPG, PNG, TIFF; tối đa 500 MB/file, 100 file/lần), lưu S3, rồi: `split` → `render` từng trang ra ảnh (PDFium) → `OCR` từng trang (TurboOCR; engine `turboocr_vlm` thêm VLM đọc từng vùng) → hợp nhất với text layer của PDF/A → `assemble` markdown theo trang. Xử lý song song theo **trang**, lỗi ở trang nào retry trang đó.

**Trạng thái tài liệu:** `queued → splitting → parsing → assembling → indexing → enriching → completed`; kết thúc khác: `partial` (một số trang lỗi, phần còn lại dùng được), `failed`, `cancelled`.

**Cấu hình chính:** `upload.*`, `parser.default_engine` (`BEPAYLOT_OCR_ENGINE`), `parser.render.*` (DPI 300, `page_timeout` 30 s, `max_worker_rss_mb`), `parser.engines.turboocr` (`timeout` 120 s, breaker 5 lỗi → mở 30 s), `parser.engines.vlm.*`, `workers.concurrency.ocr`.

**Kiểm tra:** SQL #1, #2, #9, #13; queue `page`, `render`; web → chi tiết tài liệu (tiến độ từng trang, trang lỗi).

**Retry tự động:** mỗi task được retry (`page:ocr` 3 lần, timeout 15 phút/trang; `page:render` 3 lần). Hết lượt → dead-letter, trang `failed`; tài liệu thành `partial` (hoặc `failed` nếu mọi trang lỗi).

**Tác vụ:**

| Việc | Cách làm |
|---|---|
| Parse lại cả file | web: **Parse lại**; API `POST /v1/documents/{id}/reparse` `{}` |
| Parse lại một số trang lỗi | `POST /v1/documents/{id}/reparse` `{"pages":[3,7]}` |
| Parse lại bằng engine khác | `{"engine":"turboocr_vlm"}` |
| Huỷ file đang xử lý | `POST /v1/documents/{id}/cancel` |
| Sau sự cố TurboOCR | sửa TurboOCR → retry dead-letter `page:ocr` (§6.2), hoặc reparse các file `partial`/`failed` |

**Sự cố:**

| Triệu chứng | Nguyên nhân / xử lý |
|---|---|
| Nhiều file `partial`/`failed`, lỗi `turboocr: request … connection refused` / `circuit open` | TurboOCR hỏng hoặc không tới được từ worker → kiểm §4.4, rồi retry |
| Queue `page` tồn nhiều, `ocr` chậm | TurboOCR quá tải; giảm `workers.concurrency.ocr` hoặc tăng năng lực TurboOCR; thêm worker không giúp nếu nghẽn ở TurboOCR |
| Lỗi render / `pdf: respawn instance failed` / worker bị OOM kill | PDF quá nặng: giảm `parser.render.workers`, `dpi`, tăng limit RAM; kiểm `pdfium-worker` có trong image |
| File nằm trong `rejected[]` của response upload | vượt `upload.max_bytes` (500 MB) hoặc loại file không cho phép; các file khác trong lần upload vẫn được nhận |
| Upload `503` | `api` thiếu S3/Redis (log `document modules disabled`) |

### 5.4 Index và tìm kiếm

**Làm gì.** Sau OCR: chia section, dựng **cây mục lục** có tóm tắt (LLM, `index.tree.llm`), chỉ mục full-text tiếng Việt. Tìm kiếm **không dùng embedding**: LLM đọc wiki/mục lục rồi đọc trang gốc (`mode=reasoning`), hoặc tìm từ khoá (`keyword`) / metadata. Mọi kết quả được đối chiếu lại với dòng gốc.

**Cấu hình:** `index.*`, `search.*` (`max_llm_calls` 12, `timeout` 60 s, cache 10 phút trong RAM từng instance).

**Kiểm tra:** `POST /v1/cases/{id}/search {"query":"…","mode":"keyword"}` (không tốn LLM) và `mode: reasoning`.

**Sự cố:** LLM lỗi khi tóm tắt mục lục → dùng ngay tóm tắt dạng trích đoạn (không LLM, log `summaries failed, using extractive`), file vẫn hoàn tất; search `reasoning` lỗi/timeout → kiểm LLM (§4.4), thử `keyword`.

### 5.5 Wiki của hồ sơ

**Làm gì.** Mỗi file xong index được **ingest** vào wiki của hồ sơ: LLM trích xuất thực thể/quan hệ **1 lần gọi mỗi file**, code dựng các trang wiki, kiểm từng giá trị với dòng gốc. Các file trong **cùng một hồ sơ** được ingest **tuần tự** (khoá theo hồ sơ); hồ sơ khác nhau chạy song song. **Lint** (mâu thuẫn, thiếu dữ liệu, liên kết hỏng) chạy sau ingest và định kỳ 24 giờ. Callback của file chỉ được gửi **sau khi** file vào wiki.

**Trạng thái:** hồ sơ `wiki_status`: `none` (chưa có), `building` (đang ingest), `ready`, `stale` (có lỗi trước đó), `failed`. File `wiki_status`: `pending`, `processing`, `done`, `partial`, `failed`, `skipped` (loại hồ sơ tắt wiki).

**Cấu hình:** `wiki.*` (`model`, `ingest.max_llm_calls` 8, `ingest.extract`, `lint.interval`), `workers.concurrency.wiki`.

**Kiểm tra:** SQL #7, #8, #14; web → Wiki của hồ sơ (tab Nhật ký, Kiểm tra); API `GET /v1/cases/{id}/wiki/log`, `GET /v1/cases/{id}/wiki/lint`.

**Retry:** `wiki:ingest` retry 10 lần (cả khi hồ sơ đang bị worker khác khoá). Lỗi 5 lần liên tiếp với một file → file vào wiki dạng trang nguồn mẫu (`partial`) kèm lint issue `gap`, để không chặn hồ sơ.

**Tác vụ:**

| Việc | Cách làm |
|---|---|
| Dựng lại wiki một hồ sơ (sau khi đổi wiki schema, hoặc wiki lỗi) | `POST /v1/cases/{id}/wiki/rebuild` (trang sửa tay và ghi chú được giữ) |
| Chạy lint ngay | `POST /v1/cases/{id}/wiki/lint` |
| Tắt LLM cho wiki (tiết kiệm chi phí, sự cố LLM kéo dài) | `wiki.ingest.extract: false` → wiki chỉ có trang nguồn + tổng quan, 0 lần gọi LLM |

**Sự cố:** SQL #8 có việc tồn lâu hoặc SQL #14 có file chờ lâu → pool `wiki` không chạy (worker chết, Redis) hoặc LLM lỗi; housekeeping tự đẩy lại sau 10 phút; nếu vẫn tồn, xem dead-letter `wiki:ingest`.

### 5.6 Agent hỏi đáp

**Làm gì.** `POST /v1/messages` (tương thích Anthropic Messages API, `stream` qua SSE) và `POST /v1/ag-ui/run` (giao diện web). Phiên hỏi đáp gắn với **một hồ sơ** (`metadata.case_id` hoặc `metadata.case`); agent dùng các tool `wiki_*`, `kb_*` chỉ trong hồ sơ đó. Mỗi lượt được ghi vào `agent_runs` (số bước, token, độ trễ, lỗi).

**Cấu hình:** `llm.*` (`default_provider`, `default_model`, `max_tokens`, `request_timeout` 5 phút), `agent.max_steps` (16), `agent.history_token_budget`. LLM được retry 2 lần khi lỗi tạm thời.

**Kiểm tra:** SQL #10; thử §4.4; web → Hỏi đáp.

**Sự cố:**

| Triệu chứng | Nguyên nhân / xử lý |
|---|---|
| Lỗi `401/403` từ provider trong log / `agent_runs.error` | API key LLM sai, hết hạn mức → cập nhật `OPENAI_API_KEY`/`ANTHROPIC_API_KEY`, khởi động lại |
| `429` từ provider | vượt rate limit của provider; giảm tải hoặc nâng gói |
| Câu trả lời chậm (> vài phút) | model chậm, hồ sơ lớn; hệ thống tích hợp cần timeout đủ dài hoặc dùng `stream` |
| `409` khi gửi tin | phiên đã gắn hồ sơ khác; tạo phiên mới |
| Agent trả "không tìm thấy" dù có file | file chưa `completed`/chưa vào wiki (kiểm §5.3, §5.5) |

### 5.7 Callback cho hệ thống tích hợp

**Làm gì.** Upload kèm `callback_url` → khi mỗi file kết thúc, BePaylot `POST` JSON tới URL đó (`document.completed | partial | failed | cancelled`), ký HMAC nếu có `BEPAYLOT_CALLBACK_SECRET`. Chỉ HTTP 2xx là thành công; lỗi → retry theo 10s, 30s, 1m, 5m, 15m, 30m, 1h (tối đa 8 lần) rồi `failed`. Trạng thái lưu trong `document_callbacks`.

**Cấu hình:** `callback.*`, `BEPAYLOT_CALLBACK_SECRET`, `BEPAYLOT_CALLBACK_ALLOW_PRIVATE` (mặc định chặn gửi tới IP nội bộ/localhost để tránh SSRF — **cần `true` nếu hệ thống nhận, ví dụ BPM, nằm trong mạng nội bộ**).

**Kiểm tra:** SQL #5, #6; API `GET /v1/documents/{id}/callbacks`.

**Tác vụ:** gửi lại ngay `POST /v1/documents/{id}/callbacks/retry` (thêm `max_attempts` lần thử); đổi URL khi reparse (`callback_url` trong body reparse).

**Sự cố:** `connection refused`/timeout → bên nhận down hoặc firewall; `destination address is not allowed (loopback/private/link-local)` → cần `BEPAYLOT_CALLBACK_ALLOW_PRIVATE=true`; `4xx` → bên nhận từ chối (sai chữ ký, sai định dạng) → báo bên nhận kèm `X-Bepaylot-Delivery`.

### 5.8 Housekeeping tự động

Mỗi `workers.housekeeping_interval` (5 phút) một worker chạy `housekeeping:sweep` (không chạy trùng giữa các replica):

- trang treo ở render/OCR lâu hơn ~8 phút → đưa lại hàng đợi;
- tài liệu không tiến triển > 10 phút → đẩy lại bước đang dở;
- tài liệu/hồ sơ đã xoá nhưng còn dữ liệu → xoá tiếp;
- callback đến hạn gửi lại;
- việc wiki còn tồn của mọi hồ sơ, và file xong index > 10 phút chưa vào wiki → đưa lại hàng đợi wiki;
- lint wiki định kỳ (`wiki.lint.interval`, 24 giờ);
- tài liệu trỏ tới hồ sơ không tồn tại → ghi log lỗi + dead-letter (không tự sửa).

Nhờ đó mất Redis, restart worker hay deploy giữa chừng **không cần can thiệp tay**; chỉ cần chờ 10–15 phút rồi kiểm SQL #2.

### 5.9 Skill và MCP

- **Skill** (`skills/*/SKILL.md`): hướng dẫn cho agent, tự đồng bộ khi khởi động (`skills.sync_on_startup`); đồng bộ tay: `POST /v1/skills/sync` hoặc `/app/skills-sync`.
- **MCP** (mặc định tắt, `BEPAYLOT_MCP_ENABLED`): kết nối tool bên ngoài cho agent; khai báo ở `configs/mcp.yaml` (đọc lại mỗi 10 s) hoặc `/v1/mcp/servers`. Tool MCP không bị sandbox: **chỉ gắn server tin cậy**. Lỗi kết nối server MCP chỉ làm mất tool đó, không làm dừng agent.

### 5.10 Giao diện web

- Trang tĩnh; mọi route trừ `/login` yêu cầu đăng nhập. Token lưu trong `localStorage` của trình duyệt.
- Góc dưới trái: "Đã kết nối / Mất kết nối" tới API. "Mất kết nối" → kiểm proxy `/v1` và `api`.
- Avatar → **Tài khoản & API key**: đổi mật khẩu, tạo/thu hồi API key, địa chỉ máy chủ API.

---

## 6. Xử lý sự cố (runbook)

### 6.1 Bảng tra nhanh

| Triệu chứng | Kiểm tra | Xử lý |
|---|---|---|
| Web/API không vào được | `/healthz` qua load balancer và trực tiếp pod | restart `api`; kiểm proxy/ingress |
| `/readyz` 503 | `error` trong body; `pg_isready` | khôi phục Postgres; kiểm `DATABASE_URL`, số kết nối (`db.max_conns` × số replica ≤ `max_connections`) |
| Upload được nhưng file đứng `queued` | queue `default` `pending` tăng, `active`=0 | worker không chạy hoặc không nối được Redis → kiểm pod worker, `REDIS_ADDR` |
| File đứng `parsing` lâu | queue `page`/`render`; SQL #2, #9; dead-letter `page:*` | §5.3 (TurboOCR, PDFium) |
| File `completed` nhưng agent không thấy nội dung wiki | SQL #7, #8, #14 | §5.5 |
| Hệ thống tích hợp không nhận callback | SQL #6 | §5.7 |
| Agent lỗi hàng loạt | SQL #10, log provider | §5.6 (key, hạn mức LLM) |
| Người dùng không đăng nhập được | log `msg=http path=/v1/auth/login status=…` | §5.1 |
| RAM worker tăng, bị OOM kill | metrics container | giảm `parser.render.workers`/`dpi`, tăng limit; PDF lỗi bị ngắt sau `page_timeout` |
| Postgres đầy dung lượng | SQL #15, `pg_total_relation_size` các bảng `page_lines`, `page_blocks`, `messages` | mở rộng đĩa; xoá hồ sơ không còn dùng qua API |

### 6.2 Xử lý dead-letter

Dead-letter là task đã hết lượt retry. Dữ liệu vẫn nhất quán; tài liệu liên quan ở `partial`/`failed`.

1. Xem: `GET /v1/admin/dead-letters?limit=50` (hoặc SQL #4). Trường `scope_id` là id tài liệu/hồ sơ, `last_error` là lỗi cuối.
2. Nhóm theo nguyên nhân (thường cùng một lỗi: TurboOCR down, LLM hết hạn mức…) và **sửa nguyên nhân trước**.
3. Retry từng mục: `POST /v1/admin/dead-letters/{id}/retry` (mục được lấy ra khỏi danh sách; trang của `page:ocr`/`page:render` được mở lại để xử lý). Retry khi nguyên nhân chưa sửa sẽ lại vào dead-letter.
4. Với nhiều file: reparse các tài liệu `partial`/`failed` (§5.3) thường nhanh hơn retry từng task.
5. Dead-letter có lỗi không rõ nguyên nhân, hoặc lặp lại sau khi retry → chuyển đội phát triển kèm `task_type`, `scope_id`, `last_error`, khoảng thời gian log.

Retry hàng loạt các dead-letter `page:ocr` sau sự cố TurboOCR:

```bash
curl -s "$BP_URL/v1/admin/dead-letters?task_type=page:ocr&limit=500" -H "x-api-key: $ADMIN_KEY" \
 | jq -r '.data[].id' | while read id; do
     curl -s -X POST "$BP_URL/v1/admin/dead-letters/$id/retry" -H "x-api-key: $ADMIN_KEY"; echo " $id"; done
```

> Ghi chú bàn giao: DB dev có một số dead-letter `document:gen_cleanup` lỗi `relation "kg_mentions" does not exist`, do bước dọn dữ liệu cũ sau reparse còn xoá ở bảng đã bỏ từ migration `0014`. Lỗi đã được sửa trong bản bàn giao; các dead-letter cũ loại này **không cần retry** (dữ liệu chính đã được dọn đúng), có thể xoá: `DELETE FROM task_dead_letters WHERE task_type='document:gen_cleanup' AND last_error LIKE '%kg_mentions%';`.

### 6.3 Khi một phụ thuộc hỏng rồi phục hồi

| Phụ thuộc | Trong lúc hỏng | Sau khi phục hồi |
|---|---|---|
| Postgres | toàn bộ dịch vụ lỗi | tự kết nối lại; kiểm SQL #2 sau 15 phút |
| Redis | upload có thể lỗi; không xử lý nền | tự kết nối lại; nếu Redis mất dữ liệu, housekeeping đẩy lại việc dở trong 10–15 phút |
| S3 | upload, render, xem ảnh lỗi | retry dead-letter/reparse file lỗi trong khoảng đó |
| TurboOCR | file mới `partial`/`failed` | retry dead-letter `page:ocr` hoặc reparse (§6.2) |
| LLM | agent, search `reasoning` lỗi; mục lục dùng tóm tắt trích đoạn; wiki retry rồi hạ về trang nguồn mẫu | reparse file cần tóm tắt đầy đủ; `POST /v1/cases/{id}/wiki/rebuild` cho hồ sơ cần wiki đầy đủ |

---

## 7. Công việc định kỳ

### 7.1 Backup và khôi phục

| Dữ liệu | Cách backup | Tần suất gợi ý |
|---|---|---|
| Postgres (mọi metadata, wiki, tài khoản, phiên) | `pg_dump -Fc "$DATABASE_URL" > bepaylot-$(date +%F).dump`, hoặc snapshot/PITR của hạ tầng | hằng ngày + WAL/PITR |
| S3 bucket `S3_BUCKET` (prefix `bepaylot/`: file gốc, ảnh trang, OCR thô) | versioning + replication của S3, hoặc `mc mirror` | liên tục / hằng ngày |
| Redis | không cần backup (chỉ hàng đợi; mất thì housekeeping tự phục hồi việc dở) | — |
| Cấu hình | `configs/`, file env, secret | theo mỗi thay đổi |

Khôi phục: dừng `api`/`worker` → `pg_restore -c -d "$DATABASE_URL" bepaylot-….dump` → khôi phục S3 cùng thời điểm (hoặc mới hơn) → khởi động → `healthcheck.sh` + SQL #2. Postgres và S3 phải khớp thời điểm: DB mới hơn S3 thì một số ảnh trang/file gốc thiếu (reparse sẽ lỗi cho file đó).

### 7.2 Lịch kiểm tra

| Tần suất | Việc |
|---|---|
| Liên tục (monitoring) | `healthcheck.sh` mỗi 5 phút; ngưỡng §4.6 |
| Hằng ngày | SQL #2, #3, #6, #10; xử lý dead-letter (§6.2) |
| Hằng tuần | SQL #11–#12 (tài khoản, key không dùng); dung lượng DB và S3; kiểm backup khôi phục được |
| Hằng quý | xoay API key của hệ thống tích hợp (tạo key mới → cập nhật bên kia → thu hồi key cũ); xoay `BEPAYLOT_CALLBACK_SECRET` (phối hợp bên nhận); cập nhật image |

### 7.3 Dọn dẹp

- Token đăng nhập hết hạn được tự xoá khi có người đăng nhập (sau 1 ngày quá hạn).
- Dead-letter không tự xoá: sau khi xử lý, có thể xoá bản ghi cũ hơn 30 ngày: `DELETE FROM task_dead_letters WHERE failed_at < now() - interval '30 days';`
- Cache ảnh PDF trên đĩa worker (`parser.render.cache_dir`, tối đa 20 GB) tự giới hạn; có thể xoá khi worker dừng.
- Không xoá trực tiếp bảng tài liệu/wiki bằng SQL: dùng API xoá tài liệu/hồ sơ để S3 và wiki được dọn cùng.

---

## 8. Bảo mật khi vận hành

- **Secret:** `DATABASE_URL`, `REDIS_PASSWORD`, `S3_SECRET_KEY`, `OPENAI_API_KEY`/`ANTHROPIC_API_KEY`, `VLM_API_KEY`, `BEPAYLOT_JWT_SECRET`, `BEPAYLOT_CALLBACK_SECRET`, `BEPAYLOT_OIDC_CLIENT_SECRET`. Lưu trong kho secret, không commit, không in ra log.
- `BEPAYLOT_AUTH_BYPASS` **phải `false`** (khi `true`, mọi request không cần xác thực). `healthcheck.sh` báo FAIL nếu bật.
- `BEPAYLOT_REGISTRATION=closed` nếu dịch vụ mở ra ngoài mạng nội bộ; khi đó cấp tài khoản qua OIDC hoặc `seed`.
- Chưa có giới hạn số lần đăng nhập sai: đặt rate limit ở reverse proxy/ingress cho `POST /v1/auth/login` và `/v1/auth/register` (ví dụ 10 request/phút/IP).
- Chỉ phục vụ qua HTTPS; đặt `X-Forwarded-Proto` ở proxy (cookie OIDC dùng `Secure` theo header này).
- `http.cors_origins`: liệt kê đúng domain web ở production (rỗng = mọi origin).
- API key chỉ hiện một lần khi tạo; DB chỉ lưu hash. Key bị lộ → thu hồi ngay (§5.1).
- Callback: bên nhận nên kiểm chữ ký HMAC; giữ `BEPAYLOT_CALLBACK_ALLOW_PRIVATE=false` trừ khi bên nhận ở mạng nội bộ.
- Tool MCP và `http_fetch` của agent gọi ra ngoài: giới hạn bằng `tools.http_allowlist` và chỉ gắn MCP tin cậy.

---

## 9. Giới hạn đã biết khi bàn giao

| Hạng mục | Tình trạng | Ảnh hưởng vận hành |
|---|---|---|
| TurboOCR thật | chưa chạy được từ môi trường phát triển (IP nội bộ) | **kiểm kỹ OCR trên môi trường OPN** bằng smoke test §4.2 trước khi đưa vào dùng |
| PDFium native (image `worker`, `multi_threaded`) | chưa build/chạy thử trên Linux | kiểm image `worker` với PDF nhiều trang; theo dõi RAM |
| OIDC với provider thật | mới thử với provider giả lập | thử đăng nhập SSO trên staging trước |
| `/readyz` | chỉ kiểm Postgres | dùng `healthcheck.sh` để kiểm Redis/S3/OCR/LLM |
| Chỉ số (metrics) | chưa có endpoint Prometheus | theo dõi bằng `healthcheck.sh`, SQL, log |
| Rate limit đăng nhập, quên mật khẩu, xác minh email | chưa có | rate limit ở proxy; đặt lại mật khẩu bằng `seed` (§5.1) |
| Cache search | trong RAM từng replica | kết quả giữa các replica có thể khác trong 10 phút |
| Reparse cả file | trong lúc reparse, file tạm không tìm kiếm được | báo người dùng khi reparse file đang dùng |
