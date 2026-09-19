-- ============================================================
-- 000005：短链访问口令（PLAN-NEXT M5-1 / 批次 N2）
--
-- password_hash 存 bcrypt 摘要（cost 12），空串 = 不设口令。
-- 用 NOT NULL DEFAULT '' 而不是可空：与 tags 同理 —— 可空会让每个查询都要写 coalesce，
-- 而 Update 里那条 COALESCE($n, password_hash) 的「不动就保持原值」语义在 NULL 下会变得微妙。
--
-- 不加索引：没有按口令查询的场景。
--
-- 跳转路径（GET /{code}）只读缓存里的「有没有口令」布尔，从不读这一列；
-- 摘要只在 POST /{code} 上被比对一次，而那条路径有按 IP 的限流兜着。
-- ============================================================

ALTER TABLE links ADD COLUMN password_hash text NOT NULL DEFAULT '';

COMMENT ON COLUMN links.password_hash IS
    '访问口令的 bcrypt 摘要（cost 12）；空串 = 不设口令。跳转路径只读缓存里的「有没有口令」布尔，摘要只在 POST /{code} 上校验一次';
