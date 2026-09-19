-- ============================================================
-- 000006_domains —— 自定义域名（分域）
--
-- 目标：让「同一条短链属于哪个域名」变成可表达的数据，而不是只写在 README 里的一句
-- 「分域只需改 nginx」。
--
-- 兼容性设计（这是本迁移刻意选择的形态）：
--   * links.domain_id 可空，NULL **不是**「没设置」，而是一个明确语义：**默认域名**
--     （即 PUBLIC_BASE_URL 指向的那个）。因此历史行不需要回填，
--     迁移窗口内也不需要双写。
--   * 不新建 PARTITION / 不重写 links：加可空列在 PG18 上是仅改元数据的操作。
--   * 不动 links.short_code 的全局唯一约束。短码仍然全局唯一，
--     于是**所有既有的按短码查询（管理接口、口令校验、计数回刷）都不受影响** ——
--     这一批只做「读路径按域解析」与「创建时可指定域」，
--     放宽容量的那一步单独一个迁移（见 PLAN-NEXT.md §18）。
-- ============================================================

CREATE TABLE domains (
    id          uuid        PRIMARY KEY,
    -- domain 是**纯主机名**：小写、不含 scheme / 端口 / 路径 / 前缀点。
    -- 归一化在应用层（internal/pkg/hostname）做，这里用 CHECK 兜住直接写库的情况 ——
    -- 一旦库里混进 "https://X.com"，按 Host 查就会永远查不到，而症状是「域名已登记但短链 404」。
    domain      text        NOT NULL,
    -- owner_id 可空：自建场景下域名通常由运维登记，没有归属账号。
    -- ON DELETE SET NULL 而不是 CASCADE：删掉账号不该连带删掉域名 ——
    -- 那会让所有挂在该域名下的短链失去域，跳转直接 404。
    owner_id    uuid        REFERENCES users (id) ON DELETE SET NULL,
    -- verified_at 为 NULL 表示「尚未验证归属」。当前实现不校验 DNS TXT，
    -- 但把字段先留着：登记处（运维写库）自己负责只登记已指向本服务的域名。
    verified_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT domains_domain_len CHECK (char_length(domain) BETWEEN 1 AND 253),
    CONSTRAINT domains_domain_shape CHECK (
        domain ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$'
    )
);

COMMENT ON TABLE domains IS '自定义域名；links.domain_id 为 NULL 表示默认域名（PUBLIC_BASE_URL）';
COMMENT ON COLUMN domains.verified_at IS '归属验证时间；NULL = 未验证（当前由运维保证）';

-- 按主机名查是唯一的查询形态（应用层会把它整表读进内存做主机名匹配）
CREATE UNIQUE INDEX domains_domain_key ON domains (domain);

-- 挂链：可空 = 默认域名。ON DELETE RESTRICT：域名下还有短链时不许删域，
-- 否则这些短链会静默地「掉回默认域」并在默认域上被解析 —— 那是比报错更难查的行为。
ALTER TABLE links
    ADD COLUMN domain_id uuid REFERENCES domains (id) ON DELETE RESTRICT;

COMMENT ON COLUMN links.domain_id IS '所属自定义域名；NULL = 默认域名（PUBLIC_BASE_URL）';
