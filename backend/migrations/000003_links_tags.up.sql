-- ============================================================
-- 000003：链接标签（PLAN-NEXT M4-1）
--
-- tags 用 text[] 而不是关联表：标签是「链接的一个属性」，不需要独立的生命周期，
-- 用数组省掉一次 JOIN 与一套 CRUD。筛选走 GIN 索引的数组包含（tags @> ARRAY['ops']）。
--
-- NOT NULL DEFAULT '{}' 而不是可空：空数组与 NULL 表示同一件事时，
-- 每个查询都要写 coalesce(tags, '{}')，而 @> 对 NULL 的结果是 NULL（不是 false），
-- 漏写就会静默筛不出东西。
-- ============================================================

ALTER TABLE links ADD COLUMN tags text[] NOT NULL DEFAULT '{}';

COMMENT ON COLUMN links.tags IS
    '标签，统一小写存储（列表筛选走 tags @> ARRAY[...] 的 GIN 索引，数组包含是大小写敏感的）；上限 10 个、每个 32 字符，在 service 层校验';

-- 数组包含查询的默认 GIN opclass 就是 array_ops
CREATE INDEX links_tags_gin ON links USING gin (tags);