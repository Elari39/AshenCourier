-- ============================================================
-- 000002：click_events 幂等去重（PLAN-NEXT M2-1）
--
-- 投递语义是 at-least-once：worker「处理完但 ACK 前」重启时，同一批明细会重复插入。
-- 界面上的 total_clicks 走 Redis INCR 不受影响，但明细条数会多算，
-- 于是「基线 vs 明细」两个口径永远对不上。
--
-- event_uid 取自 Redis Stream 消息 ID（形如 1712345678901-0）：重投的同一批消息
-- 带着同一个 ID，唯一索引就能把重复行挡在门外。
--
-- ⚠️ partial 索引（WHERE event_uid IS NOT NULL）是有意的：
--    1. 历史行没有 uid，不该参与唯一性判定，索引也不必为它们留位置
--       （click_events 是大表，索引小一半就是实打实的写放大与磁盘差异）
--    2. 插入侧的 ON CONFLICT 必须复述同一个谓词：
--         ON CONFLICT (event_uid) WHERE event_uid IS NOT NULL DO NOTHING
--       少写这个 WHERE，PG 会报
--       「there is no unique or exclusion constraint matching the ON CONFLICT specification」
-- ============================================================

ALTER TABLE click_events ADD COLUMN event_uid text;

COMMENT ON COLUMN click_events.event_uid IS
    '由 Redis Stream 消息 ID 派生，用于 at-least-once 投递下的幂等去重；000002 之前的历史行为 NULL';

CREATE UNIQUE INDEX click_events_event_uid_key
    ON click_events (event_uid)
    WHERE event_uid IS NOT NULL;