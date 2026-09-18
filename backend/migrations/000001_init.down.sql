-- ============================================================
-- AshenCourier 初始迁移（down）
-- 与 000001_init.up.sql 严格互逆，保证 migrate down 1 && up 可重复执行
-- ============================================================

DROP VIEW IF EXISTS link_click_totals;
DROP TABLE IF EXISTS click_events;
DROP TABLE IF EXISTS links;
DROP INDEX IF EXISTS users_email_lower_key;
DROP TABLE IF EXISTS users;
