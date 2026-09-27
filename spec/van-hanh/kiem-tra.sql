-- Bộ câu kiểm tra trạng thái BePaylot (chỉ đọc). Xem spec/van-hanh.md §4.5.
-- Chạy: psql "$DATABASE_URL" -f spec/van-hanh/kiem-tra.sql
-- hoặc: docker exec -i bepaylot-postgres psql -U bepaylot -d bepaylot < spec/van-hanh/kiem-tra.sql
\pset pager off
\echo '== 1. Số tài liệu theo trạng thái'
SELECT status, count(*) FROM documents WHERE deleted_at IS NULL GROUP BY 1 ORDER BY 2 DESC;
\echo '== 2. Tài liệu đứng yên > 30 phút ở trạng thái đang xử lý (housekeeping đẩy lại sau 10 phút; còn ở đây là bất thường)'
SELECT id, file_name, status, parse_status, index_status, pages_done, page_count, updated_at FROM documents WHERE deleted_at IS NULL AND status NOT IN ('completed','partial','failed','cancelled') AND updated_at < now() - interval '30 minutes' ORDER BY updated_at LIMIT 50;
\echo '== 3. Dead-letter 24 giờ qua theo loại task'
SELECT task_type, count(*) AS n, max(failed_at) AS last FROM task_dead_letters WHERE failed_at > now() - interval '24 hours' GROUP BY 1 ORDER BY 2 DESC;
\echo '== 4. 20 dead-letter mới nhất (id dùng cho POST /v1/admin/dead-letters/{id}/retry)'
SELECT id, task_type, scope, scope_id, fail_count, left(last_error, 160) AS err, failed_at FROM task_dead_letters ORDER BY failed_at DESC LIMIT 20;
\echo '== 5. Callback theo trạng thái'
SELECT state, count(*) FROM document_callbacks GROUP BY 1;
\echo '== 6. Callback thất bại hoặc quá hạn chưa gửi'
SELECT document_id, event, url, attempts, last_status_code, left(last_error, 120) AS err, next_attempt_at FROM document_callbacks WHERE state = 'failed' OR (state = 'pending' AND next_attempt_at < now() - interval '10 minutes') ORDER BY created_at DESC LIMIT 20;
\echo '== 7. Wiki của hồ sơ theo trạng thái'
SELECT wiki_status, count(*) FROM cases WHERE deleted_at IS NULL GROUP BY 1;
\echo '== 8. Việc wiki còn tồn (task_pending_ops); tồn lâu = worker wiki không chạy'
SELECT task_type, count(*), min(enqueued_at) FROM task_pending_ops GROUP BY 1;
\echo '== 9. Trang lỗi gần nhất (OCR/render)'
SELECT d.file_name, p.page_no, p.attempts, left(p.error, 120) FROM document_pages p JOIN documents d ON d.id = p.document_id AND d.gen = p.gen WHERE p.status = 'failed' AND d.deleted_at IS NULL ORDER BY p.finished_at DESC NULLS LAST LIMIT 20;
\echo '== 10. Agent 24 giờ qua theo giờ: số lượt, lỗi, độ trễ, token'
SELECT date_trunc('hour', created_at) AS h, count(*) AS turns, count(*) FILTER (WHERE error <> '') AS errors, round(avg(latency_ms)) AS avg_ms, sum(tokens_in) AS tok_in, sum(tokens_out) AS tok_out FROM agent_runs WHERE created_at > now() - interval '24 hours' GROUP BY 1 ORDER BY 1 DESC;
\echo '== 11. Tài khoản, phiên đăng nhập, API key'
SELECT (SELECT count(*) FROM users) AS users, (SELECT count(*) FROM users WHERE NOT is_active) AS disabled, (SELECT count(*) FROM auth_tokens WHERE revoked_at IS NULL AND expires_at > now()) AS live_tokens, (SELECT count(*) FROM api_keys WHERE revoked_at IS NULL) AS api_keys, (SELECT count(*) FROM api_keys WHERE revoked_at IS NULL AND last_used_at > now() - interval '24 hours') AS keys_used_24h;
\echo '== 12. API key đang hoạt động và lần dùng cuối'
SELECT u.email, k.name, k.key_prefix, k.last_used_at, k.created_at FROM api_keys k JOIN users u ON u.id = k.user_id WHERE k.revoked_at IS NULL ORDER BY k.last_used_at DESC NULLS LAST;
\echo '== 13. Thời gian trung bình từng bước xử lý 24 giờ qua'
SELECT stage, count(*), round(avg(extract(epoch FROM finished_at - started_at))::numeric, 1) AS avg_s FROM processing_spans WHERE started_at > now() - interval '24 hours' AND finished_at IS NOT NULL GROUP BY 1 ORDER BY 1;
\echo '== 14. File đã xử lý xong nhưng chưa vào wiki > 30 phút'
SELECT d.file_name, d.status, d.wiki_status, d.updated_at FROM documents d WHERE d.deleted_at IS NULL AND d.status IN ('completed','partial') AND d.wiki_status IN ('pending','processing') AND d.updated_at < now() - interval '30 minutes';
\echo '== 15. Dung lượng database'
SELECT pg_size_pretty(pg_database_size(current_database()));
