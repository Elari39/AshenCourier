-- ============================================================
-- 000006_domains —— 回滚（与 up 严格互逆）
--
-- 顺序：先摘外键列，再删表。反过来的话 links.domain_id 会指向一张正在被删的表。
-- 索引 domains_domain_key 随表一起消失，不需要单独 DROP INDEX。
-- ============================================================

ALTER TABLE links DROP COLUMN domain_id;

DROP TABLE domains;
