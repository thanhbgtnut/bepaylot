#!/usr/bin/env bash
# Kiểm tra nhanh trạng thái BePaylot và các phụ thuộc. Xem spec/van-hanh.md §4.
#
# Biến môi trường (đọc thêm từ file .env nếu truyền ENV_FILE=đường/dẫn/.env):
#   BP_URL          địa chỉ API, mặc định http://localhost:8080
#   BP_ADMIN_KEY    API key của một tài khoản có trong BEPAYLOT_ADMIN_EMAILS
#                   (không có thì bỏ qua phần queue / dead-letter / engine)
#   REDIS_ADDR, REDIS_USERNAME, REDIS_PASSWORD, S3_ENDPOINT, TURBOOCR_URL, VLM_BASE_URL,
#   OPENAI_BASE_URL  lấy từ cấu hình của dịch vụ
#
# Mã thoát: 0 = mọi kiểm tra OK, 1 = có kiểm tra FAIL (WARN không làm fail).
set -uo pipefail

if [ -n "${ENV_FILE:-}" ] && [ -f "$ENV_FILE" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  set +a
fi

BP_URL="${BP_URL:-http://localhost:${BEPAYLOT_HTTP_ADDR##*:}}"
[ "$BP_URL" = "http://localhost:" ] && BP_URL="http://localhost:8080"
FAILS=0

ok()   { printf '  \033[32mOK\033[0m    %s\n' "$*"; }
warn() { printf '  \033[33mWARN\033[0m  %s\n' "$*"; }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$*"; FAILS=$((FAILS + 1)); }
code() { curl -s -o /dev/null -w '%{http_code}' --max-time "${2:-5}" "$1" 2>/dev/null; }

echo "BePaylot healthcheck — $BP_URL — $(date '+%Y-%m-%d %H:%M:%S')"

echo "1. API"
if [ "$(code "$BP_URL/healthz")" = 200 ]; then ok "/healthz (process sống)"; else fail "/healthz không trả 200"; fi
ready=$(curl -s --max-time 5 "$BP_URL/readyz" 2>/dev/null)
case "$ready" in
  *'"status":"ready"'*) ok "/readyz (Postgres) $(printf '%s' "$ready" | sed -n 's/.*"default_model":"\([^"]*\)".*/model=\1/p')" ;;
  *) fail "/readyz: ${ready:-không phản hồi}" ;;
esac
cfg=$(curl -s --max-time 5 "$BP_URL/v1/auth/config" 2>/dev/null)
case "$cfg" in
  *'"auth_bypass":true'*) fail "auth_bypass đang BẬT — mọi request không cần xác thực (chỉ được dùng ở dev)" ;;
  *'"auth_bypass":false'*) ok "/v1/auth/config (xác thực bật)" ;;
  *) fail "/v1/auth/config: ${cfg:-không phản hồi}" ;;
esac

echo "2. Phụ thuộc"
if [ -n "${REDIS_ADDR:-}" ]; then
  if command -v redis-cli >/dev/null; then
    host=${REDIS_ADDR%:*}; port=${REDIS_ADDR##*:}
    if [ "$(redis-cli -h "$host" -p "$port" ${REDIS_USERNAME:+--user "$REDIS_USERNAME"} ${REDIS_PASSWORD:+-a "$REDIS_PASSWORD"} --no-auth-warning ping 2>/dev/null)" = PONG ]; then ok "Redis $REDIS_ADDR"; else fail "Redis $REDIS_ADDR không trả PONG"; fi
  else
    if (exec 3<>"/dev/tcp/${REDIS_ADDR%:*}/${REDIS_ADDR##*:}") 2>/dev/null; then ok "Redis $REDIS_ADDR (mở cổng TCP; cài redis-cli để kiểm tra PING)"; else fail "Redis $REDIS_ADDR không kết nối được"; fi
  fi
else
  warn "REDIS_ADDR rỗng — task chạy trong process (chỉ hợp lệ ở dev)"
fi
if [ -n "${S3_ENDPOINT:-}" ]; then
  c=$(code "$S3_ENDPOINT/minio/health/live")
  if [ "$c" = 200 ]; then ok "S3/MinIO $S3_ENDPOINT"
  elif [ "$c" != 000 ]; then ok "S3 $S3_ENDPOINT phản hồi HTTP $c (không phải MinIO: kiểm tra bucket bằng công cụ của S3)"
  else fail "S3 $S3_ENDPOINT không kết nối được"; fi
else
  warn "S3_ENDPOINT rỗng (AWS S3 mặc định, hoặc lưu trong RAM nếu S3_BUCKET cũng rỗng)"
fi
if [ -n "${TURBOOCR_URL:-}" ]; then
  c=$(code "$TURBOOCR_URL/" 5)
  if [ "$c" != 000 ]; then ok "TurboOCR $TURBOOCR_URL phản hồi (HTTP $c)"; else fail "TurboOCR $TURBOOCR_URL không kết nối được — tài liệu mới sẽ lỗi OCR"; fi
else
  fail "TURBOOCR_URL rỗng — không OCR được"
fi
if [ -n "${VLM_BASE_URL:-}" ]; then
  c=$(code "$VLM_BASE_URL/models" 5)
  if [ "$c" = 200 ]; then ok "VLM $VLM_BASE_URL"; else warn "VLM $VLM_BASE_URL/models trả $c (chỉ ảnh hưởng engine turboocr_vlm)"; fi
fi
if [ -n "${OPENAI_BASE_URL:-}" ]; then
  c=$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 ${OPENAI_API_KEY:+-H "Authorization: Bearer $OPENAI_API_KEY"} "${OPENAI_BASE_URL%/}/models" 2>/dev/null)
  if [ "$c" = 200 ]; then ok "LLM ${OPENAI_BASE_URL}"; else fail "LLM ${OPENAI_BASE_URL}/models trả $c — agent, search, wiki sẽ lỗi"; fi
fi

echo "3. Hàng đợi và engine (cần BP_ADMIN_KEY)"
if [ -n "${BP_ADMIN_KEY:-}" ]; then
  H=(-H "x-api-key: $BP_ADMIN_KEY")
  eng=$(curl -s --max-time 10 "${H[@]}" "$BP_URL/v1/parser/engines" 2>/dev/null)
  if printf '%s' "$eng" | grep -q '"available":false'; then
    warn "engine OCR không sẵn sàng: $(printf '%s' "$eng" | grep -o '"name":"[^"]*","default":[a-z]*,"available":false[^}]*' | tr '\n' ' ')"
  elif printf '%s' "$eng" | grep -q '"available":true'; then ok "engine OCR sẵn sàng"
  else fail "/v1/parser/engines: ${eng:-không phản hồi}"; fi

  q=$(curl -s --max-time 10 "${H[@]}" "$BP_URL/v1/admin/queues" 2>/dev/null)
  if printf '%s' "$q" | grep -q '"data"'; then
    if command -v jq >/dev/null; then
      printf '%s' "$q" | jq -r '.data[] | "        \(.name|.+(" "*20)|.[0:20]) pool=\(.pool) pending=\(.pending) active=\(.active) retry=\(.retry) archived=\(.archived)\(if .error and (.error|startswith("NOT_FOUND")|not) then " error="+.error else "" end)"'
      backlog=$(printf '%s' "$q" | jq '[.data[].pending] | add // 0')
      retry=$(printf '%s' "$q" | jq '[.data[].retry] | add // 0')
      if [ "$backlog" -gt 500 ]; then warn "tồn $backlog task đang chờ"; else ok "hàng đợi: $backlog task chờ, $retry đang retry"; fi
    else ok "hàng đợi: $q"; fi
  else
    fail "/v1/admin/queues: ${q:-không phản hồi} (email của key có trong BEPAYLOT_ADMIN_EMAILS?)"
  fi

  dl=$(curl -s --max-time 10 "${H[@]}" "$BP_URL/v1/admin/dead-letters?limit=500" 2>/dev/null)
  if command -v jq >/dev/null && printf '%s' "$dl" | jq -e .data >/dev/null 2>&1; then
    since=$(date -u -v-24H '+%Y-%m-%dT%H:%M:%S' 2>/dev/null || date -u -d '24 hours ago' '+%Y-%m-%dT%H:%M:%S')
    n=$(printf '%s' "$dl" | jq --arg s "$since" '[.data[] | select(.failed_at >= $s)] | length')
    if [ "$n" -gt 0 ]; then
      warn "$n dead-letter trong 24 giờ qua:"
      printf '%s' "$dl" | jq -r --arg s "$since" '.data[] | select(.failed_at >= $s) | "        #\(.id) \(.task_type) \(.scope_id) — \(.last_error[0:100])"' | head -10
    else ok "không có dead-letter trong 24 giờ qua"; fi
  fi
else
  warn "BP_ADMIN_KEY chưa đặt — bỏ qua"
fi

echo
if [ "$FAILS" -gt 0 ]; then echo "KẾT QUẢ: $FAILS kiểm tra FAIL"; exit 1; fi
echo "KẾT QUẢ: OK"
