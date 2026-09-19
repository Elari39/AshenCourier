-- 000004 回滚：恢复 (link_id, occurred_at DESC)，删掉带 id 的分页索引。
-- 顺序与 up 相反（先建后删），保证任何时刻都至少有一棵能服务明细查询的索引。

CREATE INDEX click_events_link_time_idx ON click_events (link_id, occurred_at DESC);

DROP INDEX click_events_link_time_id_idx;