-- ============================================================
-- 000004：点击明细的 keyset 分页索引（PLAN-NEXT M4-2）
--
-- 原有的 click_events_link_time_idx (link_id, occurred_at DESC) 少了兜底键：
-- 明细页按 (occurred_at, id) 倒序翻页，只比较 occurred_at 时，
-- 同一毫秒内的多条点击会卡在翻页边界上 —— 要么漏行，要么重复。
-- id 是 identity 主键（单调递增），正好当稳定兜底键。
--
-- 顺手删掉旧索引：它是新索引的**前缀**（同 link_id、同 occurred_at DESC，
-- 只是没有 id），所有走旧索引的查询都能走新索引，留着只会让 click_events
-- 这条最热的追加路径每次插入多维护一棵 B-tree。
-- down 里按原样重建，保证严格互逆。
-- ============================================================

CREATE INDEX click_events_link_time_id_idx
    ON click_events (link_id, occurred_at DESC, id DESC);

DROP INDEX click_events_link_time_idx;