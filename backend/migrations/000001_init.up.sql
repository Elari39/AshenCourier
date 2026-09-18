-- ============================================================
-- AshenCourier 初始迁移（up）
-- ID 全部由 Go 侧 uuid.NewV7() 生成，不依赖数据库默认值 → 兼容 PG 16/17/18
-- ============================================================

-- ===== users =====
CREATE TABLE users (
    id            uuid        PRIMARY KEY,
    email         text        NOT NULL,
    password_hash text        NOT NULL,
    display_name  text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email));

-- ===== links =====
CREATE TABLE links (
    id          uuid        PRIMARY KEY,
    short_code  text        NOT NULL,
    target_url  text        NOT NULL,
    title       text        NOT NULL DEFAULT '',
    owner_id    uuid        REFERENCES users(id) ON DELETE SET NULL,  -- NULL = 匿名创建
    key_hash    bytea,                       -- 匿名管理密钥的 SHA-256（密钥本身高熵，无需慢哈希）
    status      smallint    NOT NULL DEFAULT 1,  -- 1=active 2=disabled 3=deleted
    click_count bigint      NOT NULL DEFAULT 0,  -- PG 基线计数（worker 增量刷入）
    expires_at  timestamptz,
    created_ip  inet,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT links_short_code_key UNIQUE (short_code),
    CONSTRAINT links_target_url_scheme CHECK (target_url ~* '^https?://'),
    CONSTRAINT links_target_url_len CHECK (char_length(target_url) <= 2048),
    CONSTRAINT links_status_valid CHECK (status IN (1, 2, 3)),
    CONSTRAINT links_code_shape CHECK (short_code ~ '^[0-9A-Za-z_-]{3,32}$')
);
CREATE INDEX links_owner_created_idx ON links (owner_id, created_at DESC, id DESC);
CREATE INDEX links_expires_idx ON links (expires_at)
    WHERE expires_at IS NOT NULL AND status = 1;

-- ===== click_events（明细，供趋势 / 来源 / 设备聚合）=====
CREATE TABLE click_events (
    id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    link_id     uuid        NOT NULL REFERENCES links(id) ON DELETE CASCADE,
    short_code  text        NOT NULL,
    occurred_at timestamptz NOT NULL,
    referer     text,
    user_agent  text,
    ip          inet,
    country     text,          -- 预留：GeoIP，MVP 恒为 NULL
    device      text,          -- desktop | mobile | tablet | bot
    browser     text,
    os          text
);
CREATE INDEX click_events_link_time_idx ON click_events (link_id, occurred_at DESC);

-- ===== 计数增量回刷用的排查视图 =====
CREATE VIEW link_click_totals AS
SELECT l.id,
       l.short_code,
       l.click_count AS base_count,
       (SELECT count(*) FROM click_events e WHERE e.link_id = l.id) AS event_count
FROM links l;
