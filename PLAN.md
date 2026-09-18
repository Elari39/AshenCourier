# AshenCourier — 短链接服务 MVP 实施计划

> 技术栈：Go 1.27 + PostgreSQL 18 + Redis 8 + Vue 3 + pnpm + Tailwind CSS v4 + Vue Router
> 形态：前后端分离（`backend/` + `frontend/`），单域名 Docker 一键部署
> 本机环境（已实测）：Go 1.27.1 · Node 22.22.2 · pnpm 11.15.1 · Docker 29.5.3 / Compose v5.1.4

---

## 1. 目标与范围

### 1.1 MVP 要做的（DoD 的核心）

1. **匿名即可用**：打开首页 → 粘贴长链接 → 拿到短链，无需注册。
2. **可选登录**：注册/登录后可集中管理自己的链接（列表、编辑、删除、看统计）。
3. **跳转**：`GET /{code}` → 302 到目标 URL，全程读 Redis（PG 只作回源），P99 目标 < 20ms。
4. **统计**：总点击数 + 按天趋势 + 来源/设备分布；跳转路径**不写数据库**，走 Redis 计数 + Stream 异步落库。
5. **一键部署**：`docker compose up -d --build` 拉起 PG + Redis + 迁移 + 后端 + 前端（nginx），打开 `http://localhost:8080` 即可用。
6. **安全底线**：只允许 `http/https` 目标（防开放重定向）、创建接口按 IP 限流、短码保留字保护、软删除。

### 1.2 明确不做的（MVP 之外，避免膨胀）

| 不做 | 原因 / 后续 |
| --- | --- |
| 自动抓取目标页标题 | 会引入 SSRF 风险，MVP 里标题由用户手填 |
| GeoIP / 国家维度统计 | 需要 mmdb 库，`click_events.country` 字段先留空占位 |
| 短链密码保护、二维码、A/B 分流 | P1 扩展点，数据模型预留 |
| 团队/多租户/权限体系 | MVP 只有"匿名"与"个人账号"两种身份 |
| Prometheus/Grafana 全套监控 | 只暴露 `/healthz` + 结构化日志（slog）+ 关键计数 |
| 独立子域（`link.xxx` 与前端分域） | MVP 单域名，用保留字黑名单隔离；分域时只需改 nginx |

---

## 2. 技术决策表

### 2.1 你已确认的四项

| 议题 | 决策 |
| --- | --- |
| 用户体系 | **匿名可用 + 可选登录**。匿名创建后返回一次性 **管理密钥**（`X-Manage-Key`），持有它即可管理该链接；登录用户创建的链接归属账号，支持"认领"匿名链接 |
| 统计深度 | **计数 + 异步事件明细**。跳转时 `INCR` 计数 + `XADD` 到 Redis Stream，后台 worker 批量落 `click_events` |
| 界面风格 | **套用仓库现有 `DESIGN.md`**（Claude 暖奶油 `#faf9f5` + 珊瑚 `#cc785c` + 深色 mockup 卡片 + 衬线大标题），token 映射为 Tailwind v4 `@theme` 变量 |
| 数据库迁移 | **golang-migrate CLI**，compose 里独立 `migrate` 服务；`migrations/` 目录挂载进容器 |

### 2.2 我按 MVP 最优点选的默认项（不同意可直接改）

| 议题 | 选择 | 理由 |
| --- | --- | --- |
| HTTP 框架 | **标准库 `net/http` + Go 1.22+ 方法感知 ServeMux** | 本项目路由不到 15 条，stdlib 完全够用；`GET /api/links/{code}` + `r.PathValue` 已是官方推荐写法，少一层依赖 |
| 数据库驱动 | **`jackc/pgx/v5` + `pgxpool`**，手写 SQL | 不用 ORM。MVP 的查询就 10 来条，SQL 直接可读可调 |
| Redis 客户端 | **`redis/go-redis/v9`** | 生态标准 |
| JSON | **`encoding/json/v2`**（Go 1.27 标准库，已实测可编译） | 新版默认更安全（拒绝非法 UTF-8、拒绝重复键） |
| UUID | **标准库 `uuid` + `uuid.NewV7()`**（已实测） | 时间有序，B-tree 索引友好；不引入 `google/uuid` |
| 日志 | **`log/slog`**（JSON handler） | 标准库 |
| JWT | `golang-jwt/jwt/v5`（HS256，7 天） | 唯一需要的外部鉴权依赖 |
| 密码哈希 | `golang.org/x/crypto/bcrypt`（cost 12） | MVP 简单可靠 |
| 前端语言 | **TypeScript** | 接口契约有类型护航，成本极低 |
| 状态管理 | **不引入 Pinia**，用 `composables/useAuth.ts` + `localStorage` | MVP 的全局状态只有 token 和当前用户 |
| 图表 | **自研轻量 SVG 组件**（折线/面积 + 进度条），零依赖 | 只需"按天趋势 + 分布"两种图，手写更贴 DESIGN.md 的克制风格；后续需要再换 ECharts |
| 字体 | `@fontsource/inter` + `@fontsource/cormorant-garamond`（npm 打包进产物） | Copernicus/StyreneB 是 Anthropic 授权字体；**离线可用**，不依赖 Google Fonts CDN |
| 兜底测试 | 冒烟测试写成 **Go 小程序 `cmd/smoke`**（不用 shell 脚本） | 本机 Git Bash 缺 coreutils，跨平台 Go 更稳 |

---

## 3. 系统架构

```
                         ┌──────────────────────────── Browser ────────────────────────────┐
                         │  Vue3 SPA (Vite build → nginx static)                            │
                         └───────┬───────────────────────────────┬─────────────────────────┘
                                 │ /api/*  (反代)                 │ /{code} (反代)
┌────────────────────────────────▼───────────────────────────────▼─────────────────────────┐
│ nginx (frontend 容器, 监听 80)                                                            │
│   location ^~ /api/   → backend:8080                                                      │
│   location ^~ /assets/→ 静态资源（必须先于短码正则）                                        │
│   location ~ ^/[A-Za-z0-9_-]{3,32}$ → backend:8080   ← 短码跳转                            │
│   location /          → try_files $uri /index.html  (SPA history 模式)                    │
└────────────────────────────────┬──────────────────────────────────────────────────────────┘
                                 │
        ┌────────────────────────▼──────────────────────────┐
        │ backend (api)  :8080                              │        ┌──────────────────────┐
        │  ServeMux 路由 → middleware(日志/恢复/限流/CORS)   │        │ worker 容器           │
        │  handler → service → store                        │        │  (同一镜像, 换 cmd)   │
        │    ├─ 读: Redis 缓存 → miss 回源 PG → 回填         │───────▶│  Stream 消费→批量入库 │
        │    └─ 写统计: INCR + XADD (非阻塞)                 │        │  计数增量同步 → PG    │
        └───┬─────────────────────────────┬─────────────────┘        └───┬──────────┬───────┘
            │                             │                              │          │
    ┌───────▼────────┐          ┌─────────▼─────────┐          ┌─────────▼──┐  ┌────▼─────┐
    │ PostgreSQL 18  │          │ Redis 8           │◀─────────│ 同一个实例  │  │ 同一个   │
    │ links/users/   │          │ cache / 计数增量 /  │          │            │  │ 实例     │
    │ click_events   │          │ Stream / 限流      │          └────────────┘  └──────────┘
    └────────────────┘          └───────────────────┘
```

**关键设计取舍**

1. **跳转不落库**：`GET /{code}` 只做 Redis `GET` + `INCR` + `XADD`，全程无 PG 写，天然扛并发。
2. **计数最终一致**：真实点击数 = `links.click_count`（PG 基线）+ `clicks:cnt:{code}`（Redis 增量）。worker 每 2s 把增量刷回 PG，正常情况下偏差 < 2s。
3. **缓存穿透防护**：不存在的短码写 60s 负缓存（`link:v1:-{code}`），避免被扫描器打穿到 PG。
4. **Redis 故障降级**：读缓存失败 → 当 miss 处理并回源 PG（跳转仍可用）；写统计失败 → 记日志丢弃，绝不阻塞 302。PG 挂掉则返回 503 + `Retry-After`。

---

## 4. 目录结构

```
AshenCourier/
├── DESIGN.md                     # 已有：设计系统基准（前端 token 来源）
├── PLAN.md                       # 本文档
├── README.md                     # 快速开始 / 架构说明 / 环境变量表
├── .env.example                  # compose 全部变量样例
├── .gitignore
├── docker-compose.yml            # 一键部署（prod 形态）
├── docker-compose.dev.yml        # 本地开发：只起 pg + redis
├── deploy/
│   └── nginx/nginx.conf
├── backend/
│   ├── Dockerfile                # golang:1.27-alpine → distroless static
│   ├── .dockerignore
│   ├── go.mod / go.sum           # module ashen-courier
│   ├── cmd/
│   │   ├── api/main.go           # HTTP 服务入口
│   │   ├── worker/main.go        # 点击事件消费者
│   │   └── smoke/main.go         # 端到端冒烟测试（创建→跳转→统计）
│   ├── migrations/
│   │   ├── 000001_init.up.sql
│   │   └── 000001_init.down.sql
│   └── internal/
│       ├── config/config.go        # env → Config（cmp.Or 给默认值）
│       ├── domain/                 # 实体 + 领域错误 + 仓储接口（不依赖外部库）
│       │   ├── link.go user.go click.go errors.go
│       ├── store/
│       │   ├── postgres/           # pgxpool：link.go user.go click.go db.go
│       │   └── redis/              # client.go cache.go counter.go stream.go limiter.go
│       ├── service/
│       │   ├── shortener.go        # 短码生成/创建/解析
│       │   ├── stats.go            # 统计聚合
│       │   └── auth.go             # 注册/登录/JWT
│       ├── handler/                # auth.go link.go redirect.go stats.go
│       ├── httpx/
│       │   ├── server.go           # http.Server + 平滑关闭 + 路由装配
│       │   ├── response.go         # JSON(v2) 读写 + 统一错误体
│       │   ├── middleware.go       # requestID / recover / accesslog / cors
│       │   └── ratelimit.go        # Redis 限流中间件
│       ├── worker/clicks.go        # Stream 消费 + 计数同步 + 过期清理
│       └── pkg/
│           ├── base62/             # 编解码（含基准测试）
│           ├── shortcode/          # 生成与校验 + 保留字表
│           ├── ua/                 # 轻量 UA 解析（浏览器/OS/设备）
│           └── validator/          # URL 规范化与白名单
└── frontend/
    ├── Dockerfile                # node:22-alpine + corepack → nginx:alpine
    ├── package.json              # packageManager: pnpm@11.15.1（锁版本）
    ├── vite.config.ts            # dev proxy: /api + 短码正则
    ├── tsconfig.json / tsconfig.node.json
    ├── index.html
    └── src/
        ├── main.ts App.vue
        ├── assets/main.css       # Tailwind v4 @theme（DEPLOY: DESIGN.md token）
        ├── router/index.ts
        ├── api/                  # client.ts（fetch 封装）+ types.ts
        ├── composables/          # useAuth.ts useToast.ts useCopy.ts
        ├── layouts/              # DefaultLayout.vue（top-nav + footer）
        ├── views/                # Landing / Dashboard / LinkDetail / Login / Register / NotFound
        └── components/
            ├── ShortenForm.vue ResultCard.vue LinkTable.vue
            ├── StatCard.vue TrendChart.vue DistributionList.vue
            └── ui/               # Button Input Card Badge EmptyState Spinner
```

---

## 5. 数据模型（`000001_init.up.sql` 全量）

> ID 全部由 **Go 侧 `uuid.NewV7()` 生成**，不依赖数据库默认值 → 兼容 PG 16/17/18。
> `click_events` 是高写入量表，主键用 `bigint identity`（顺序写）而非随机 UUID，避免索引页分裂。

```sql
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
    CONSTRAINT links_status_valid CHECK (status IN (1, 2, 3)),
    CONSTRAINT links_code_shape CHECK (short_code ~ '^[0-9A-Za-z_-]{3,32}$')
);
CREATE INDEX links_owner_created_idx ON links (owner_id, created_at DESC, id DESC);
CREATE INDEX links_expires_idx ON links (expires_at)
    WHERE expires_at IS NOT NULL AND status = 1;

-- ===== click_events（明细，供趋势/来源/设备聚合）=====
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

-- ===== 计数增量回刷用的辅助（可选视图，便于排查）=====
CREATE VIEW link_click_totals AS
SELECT l.id, l.short_code, l.click_count AS base_count,
       (SELECT count(*) FROM click_events e WHERE e.link_id = l.id) AS event_count
FROM links l;

-- ===== down =====
-- DROP VIEW link_click_totals; DROP TABLE click_events; DROP TABLE links; DROP INDEX users_email_lower_key; DROP TABLE users;
```

**为什么 `links.status` 用 smallint 而不是 enum**：MVP 里改状态机不用写 `ALTER TYPE`，Go 侧定义 `type LinkStatus int16` 更省事。

---

## 6. 核心流程设计

### 6.1 短码生成

- **自动生成**：`crypto/rand` 取 7 位 Base62（62⁷ ≈ 3.5 万亿），**不**用自增 ID 编码（可被爬虫枚举全量链接）。
- **冲突处理**：依赖 `links_short_code_key` 唯一约束，捕获 PG `23505` 错误后重试，最多 5 次；5 次仍冲突返回 500（概率极低）。
- **自定义别名**：3–32 位 `[0-9A-Za-z_-]`，额外校验：
  - **保留字黑名单**（与前端路由表严格对应，必须同步维护）：
    `api, assets, healthz, login, register, dashboard, links, admin, static, favicon.ico, robots.txt, sitemap.xml, index.html, …`
    → 实现为 `backend/internal/pkg/shortcode/reserved.go` 的常量集合，**并有单测断言集合内容**，防止误删。
  - 大小写敏感性：短码区分大小写，但查询时先精确匹配（避免 `AbC`/`abc` 互相干扰）。

### 6.2 跳转路径（`GET /{code}`）

```
1. 正则/路由层已保证 code 形态合法（^[0-9A-Za-z_-]{3,32}$），非法 → 404 HTML 页
2. Redis GET link:v1:{code}
     ├─ 命中正常缓存 → 反序列化 → 跳到 5
     ├─ 命中负缓存   → 直接 404
     └─ miss → PG SELECT → 回填缓存（TTL = min(1h, expires_at-now)）/ 写 60s 负缓存
3. 状态判定：status != active → 410 Gone；expires_at 已过 → 410 Gone
4. 方案白名单二次校验（防御历史脏数据）→ 非 http/https 一律 500 并告警日志
5. 立即 302 Location: target_url
6. 响应后异步（goroutine + 有界队列，队列满则丢弃并计数）：
     INCR clicks:cnt:{code}          → 真实计数增量
     SADD clicks:dirty {code}
     XADD clicks:stream * code=... link_id=... ts=... ip=... ua=... ref=...
```

细节：

- **用 302 不用 301**：301 会被浏览器永久缓存，导致后续点击拿不到统计，且目标 URL 无法修改。
- **IP 取值**：只信任 nginx 注入的 `X-Real-IP`（生产）/ `r.RemoteAddr`（本地），**不**直接信任 `X-Forwarded-For`（可伪造）。
- **统计写入失败**：只 `slog.Warn`，不影响跳转；用一个 `atomic.Int64` 统计丢弃数，`/healthz` 里暴露便于排查。
- **API 路径不走跳转**：ServeMux 注册顺序上 `GET /api/...` 等更具体的模式天然优先；`/{code}` 作为兜底模式注册，并在 handler 内再次排除保留字（双保险）。

### 6.3 点击事件落库（worker）

| goroutine | 职责 |
| --- | --- |
| A · Stream 消费 | `XREADGROUP GROUP clicks c1 BLOCK 2000 COUNT 500 STREAMS clicks:stream >` → `pgx.Batch` 批量 INSERT → `XACK`。启动时 `XGROUP CREATE ... MKSTREAM`（`BUSYGROUP` 视为正常） |
| B · 计数同步 | 每 2s：`SMEMBERS clicks:dirty` → 对每个 code `GETDEL clicks:cnt:{code}` 拿到增量 → `UPDATE links SET click_count = click_count + $1, updated_at = now() WHERE short_code = $2` → `SREM clicks:dirty` |
| C · 兜底认领 | 每 30s `XAUTOCLAIM` 认领 idle > 60s 的 pending 消息，重投并入库（防消费者崩溃丢事件） |
| D · 过期清理 | 每小时扫 `links.expires_at < now() AND status = 1`，批量改 status 并删对应缓存 key |

- **投递语义**：`at-least-once`。极端情况下（worker 在处理完、ACK 前重启）可能重复插入明细，**计数用 `INCR` 累加不受影响**，只有明细条数可能多算。P1 可加 `event_uid`（由 `XADD` 时的消息 ID 派生）唯一索引做幂等去重。
- **优雅停机**：worker 收到 SIGTERM 后停止拉取、把当前批次冲完、`XACK`、退出（`context.WithCancelCause` + `sync.WaitGroup.Go`）。

### 6.4 限流

- `POST /api/links`（创建）：**每 IP 10 次/分钟**，Redis 计数 + 过期；超限 → `429` + `Retry-After`。
- `POST /api/auth/login`：每 IP 20 次/10 分钟（防撞库）。
- 实现：Redis `INCR` + `EXPIRE`（Lua 保证原子）。
  > Redis 8.8+ 提供了原生窗口计数命令 `INCREX`，可替代 `INCR+EXPIRE`；实施时先用 `COMMAND DOCS INCREX` 确认语法，可用则优先，不可用则回落到 Lua 脚本（两种实现都封装在 `store/redis/limiter.go` 里，上层无感）。
- 前端在 `429` 时给出明确的"操作过于频繁，请 X 秒后重试"提示。

### 6.5 鉴权

- **登录态**：`Authorization: Bearer <JWT>`，HS256，payload `{sub: user_id, exp}`，有效期 7 天。
- **匿名管理**：创建时返回 `manage_key`（`crypto/rand` 32 字节 → base64url，43 字符，**仅在创建响应中出现一次**），前端存 `localStorage`；后续请求带 `X-Manage-Key`，后端比对 `key_hash`。
- **授权规则**：`link.owner_id == 当前用户` **或** `X-Manage-Key` 匹配 → 允许读/改/删/看统计；否则 `403`（不区分"不存在"与"无权限"的部分场景，避免信息泄露——详情查询对无权限的 code 统一返回 `404`）。
- **认领**：`POST /api/links/{code}/claim`，登录用户带 `X-Manage-Key` 可把匿名链接挂到自己账号下（`owner_id` 更新，`key_hash` 清空）。

---

## 7. API 契约

统一前缀 `/api`；请求/响应 `application/json; charset=utf-8`（编解码走 `encoding/json/v2`）。

**成功**：直接返回资源对象 / 列表对象；**失败**：

```json
{ "error": { "code": "invalid_url", "message": "仅支持 http/https 链接", "request_id": "01J..." } }
```

| # | Method | Path | 鉴权 | 说明 | 主要错误码 |
| --- | --- | --- | --- | --- | --- |
| 1 | GET | `/healthz` | — | 存活+就绪（ping PG/Redis，返回各组件状态与统计丢弃计数） | 503 |
| 2 | POST | `/api/auth/register` | — | 注册，返回 user + token | 409 邮箱已存在 / 422 密码太弱 |
| 3 | POST | `/api/auth/login` | — | 登录 | 401 / 429 |
| 4 | GET | `/api/auth/me` | JWT | 当前用户 | 401 |
| 5 | POST | `/api/links` | 可选 | 创建短链。带 JWT → 归属账号；匿名 → 返回 `manage_key` | 400 参数 / 409 短码被占 / 429 限流 |
| 6 | GET | `/api/links` | JWT | 我的链接列表，游标分页 `?limit=20&cursor=`、`?q=` 搜索 | 401 |
| 7 | GET | `/api/links/{code}` | JWT 或 Key | 详情 | 404 |
| 8 | PATCH | `/api/links/{code}` | JWT 或 Key | 改 `target_url` / `title` / `status` / `expires_at`（改后**主动失效缓存**） | 404 / 422 |
| 9 | DELETE | `/api/links/{code}` | JWT 或 Key | 软删除（`status=3`）+ 删缓存 | 404 |
| 10 | GET | `/api/links/{code}/stats?days=30` | JWT 或 Key | 统计聚合（见下） | 404 |
| 11 | POST | `/api/links/{code}/claim` | JWT + Key | 认领匿名链接 | 404 / 409 |
| 12 | GET | `/{code}` | — | **302 跳转**（非 API，不出现在 `/api` 下） | 404 / 410 |

**创建请求/响应**

```http
POST /api/links
{ "target_url": "https://example.com/very/long/path?x=1",
  "custom_code": "go-blog",         // 可选
  "title": "我的博客",               // 可选
  "expires_at": "2026-12-31T23:59:59Z" }  // 可选
```

```json
201 Created
{ "link": { "id": "01J...", "short_code": "go-blog", "short_url": "http://localhost:8080/go-blog",
            "target_url": "https://example.com/very/long/path?x=1", "title": "我的博客",
            "status": "active", "click_count": 0,
            "created_at": "2026-09-18T04:43:00Z" },
  "manage_key": "kQ8...43chars" }
```

**统计响应**

```json
{ "total_clicks": 1284, "days": 30,
  "daily": [ { "date": "2026-09-01", "clicks": 42 } ],
  "top_referers": [ { "referer": "https://twitter.com", "clicks": 300 } ],
  "devices": [ { "device": "mobile", "clicks": 800 } ],
  "browsers": [ { "browser": "Chrome", "clicks": 900 } ] }
```

- `total_clicks` = PG 基线 + Redis 实时增量（保证界面数字"跟手"）。
- 聚合 SQL 走 `click_events`：`date_trunc('day', occurred_at)::date` 分组；来源取 `referer` 的 host（空值归为"直接访问"）。

---

## 8. Go 1.27 现代写法规范（本项目强制项）

> 以下由 Modern Go Guidelines CLI（`use-modern-go` skill）针对 **go 1.27** 解析得出，实施时逐条落到对应文件。

### 8.1 语言/标准库新特性（必须用）

| 规则 | 落点 |
| --- | --- |
| `json_v2` | `httpx/response.go`：全部用 `encoding/json/v2` 的 `MarshalWrite` / `UnmarshalRead`（已实测编译通过） |
| `stdlib_uuid` | 全仓 ID 生成 `uuid.NewV7()`；**禁止** `google/uuid` |
| `json_omitzero` | 响应 DTO：`time.Time`、数值、bool 用 `omitzero`；字符串/切片/Map 用 `omitempty` |
| `http_servemux_patterns` | `mux.HandleFunc("GET /api/links/{code}", h)` + `r.PathValue("code")`，不引路由库 |
| `errors_as_type` | 领域错误判定：`errors.AsType[*domain.ConflictError](err)` |
| `errors_is` | `errors.Is(err, pgx.ErrNoRows)` / `domain.ErrNotFound` |
| `errors_join` | 平滑关闭时聚合 server.Shutdown / worker.Stop 的多个错误 |
| `cmp_or` | `config`：`cfg.HTTPAddr = cmp.Or(os.Getenv("HTTP_ADDR"), ":8080")` |
| `sync_waitgroup_go` | `server`/`worker` 里并发启动多个后台组件：`wg.Go(func(){...})` |
| `context_cancel_cause` + `context_after_func` + `context_timeout_deadline_cause` | 优雅关闭与 DB/Redis 调用超时（区分超时/用户取消，便于日志归因） |
| `atomic_types` | 统计丢弃计数 `atomic.Int64`、限流器开关 `atomic.Bool` |
| `range_over_int` | `for i := range n` 循环 |
| `min_max` / `slices_contains` / `slices_sorted` / `maps.Keys` 迭代器 | 短码重试计数、保留字校验、聚合结果排序 |
| `strings_cut` / `strings_cut_prefix_suffix` / `strings_bytes_cut_last` | 短码与 URL 解析、Referer 取 host |
| `url_clone` | URL 规范化前先 `u.Clone()`，避免污染调用方对象 |
| `new_expression` | `link := &domain.Link{ ExpiresAt: new(exp) }` 之类可选字段 |
| `time_until` / `time_since` | 缓存 TTL 计算、请求耗时打点 |
| `clear` / `maps_clone` / `slices_clone` | 缓存刷新、请求上下文拷贝 |
| `any` | 一律用 `any`，不写 `interface{}` |
| `testing_t_context` | 单测里 `httptest.NewRequestWithContext(t.Context(), ...)` |
| `testing_b_loop` | `base62` 基准测试用 `b.Loop()` |
| `generic_methods` / `promoted_field_literals` | 仓储通用方法、DTO 嵌入字段字面量写法（出现即遵循） |
| `time_tick_gc` | 内部 ticker 不再刻意 `Stop()`（Go 1.23+ 可回收未引用 ticker） |

### 8.2 代码风格约定

- 包名短小无下划线；错误统一 `fmt.Errorf("store.postgres: create link: %w", err)` 形式带上"包+操作"前缀。
- 领域层 `internal/domain` **不 import 任何第三方库**（含 pgx/redis），仓储接口在 domain 定义、在 store 实现。
- 所有外部调用都必须带 `context` 与超时（PG 3s / Redis 200ms）。
- 禁止 `panic` 传播到 handler 之外；`httpx/recover` 中间件兜底并记 `slog.Error`。
- 注释与文档用中文，标识符用英文。

---

## 9. 前端设计

### 9.1 路由（Vue Router，history 模式）

| 路径 | 视图 | 说明 |
| --- | --- | --- |
| `/` | `Landing` | DESIGN.md 的 `hero-band`（6-6 栅格：左侧衬线大标题 + 创建表单，右侧深色 `code-window-card` mockup 展示"短链预览"） |
| `/login` `/register` | `Login` / `Register` | 奶油画布 + `text-input` + 珊瑚主按钮 |
| `/dashboard` | `Dashboard` | 链接列表（`LinkTable`）+ 顶部 3 张 `StatCard` |
| `/links/:code` | `LinkDetail` | 详情 + `TrendChart` + 两个 `DistributionList`（来源/设备） |
| `/:pathMatch(.*)*` | `NotFound` | 兜底 |

> ⚠️ **硬约束**：前端所有顶级路径（上面这些 + `/assets`）都必须进后端 `shortcode/reserved.go` 保留字表，否则会被短码路由"吃掉"。改动前端路由时同步改后端并跑单测。

### 9.2 Tailwind v4 主题（`assets/main.css`，CSS-first，无 config 文件）

```css
@import "tailwindcss";

@theme {
  /* 颜色：直接来自 DESIGN.md */
  --color-canvas: #faf9f5;          --color-primary: #cc785c;
  --color-primary-active: #a9583e;  --color-ink: #141413;
  --color-body: #3d3d3a;            --color-muted: #6c6a64;
  --color-hairline: #e6dfd8;        --color-surface-card: #efe9de;
  --color-surface-dark: #181715;    --color-surface-dark-elev: #252320;
  --color-on-dark: #faf9f5;         --color-on-dark-soft: #a09d96;
  --color-success: #5db872;         --color-error: #c64545;
  /* 字体：Copernicus/StyreneB 的公开替代方案（见 DESIGN.md 说明） */
  --font-display: "Cormorant Garamond", "Tiempos Headline", serif;
  --font-sans: "Inter", -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  --font-mono: "JetBrains Mono", ui-monospace, monospace;
  --radius-md: 8px; --radius-lg: 12px; --radius-xl: 16px;
  --spacing-section: 96px;
}
```

**必须遵守的 DESIGN.md 规则**（写进组件注释，避免后续走偏）：

- 画布永远是暖奶油 `#faf9f5`，**不能**用纯白或冷灰。
- 大标题一律衬线 `font-display`、字重 400、负字距（h1 `-1.5px`）；正文 `Inter` 400。
- 珊瑚色只用于主 CTA 和整块珊瑚 callout，不做局部装饰色；**不允许**第四个色系。
- 相邻区块不复用同一 surface 模式：奶油 → 奶油卡片 → 深色 mockup → 珊瑚 callout → 深色 footer。
- 圆角层级：按钮/输入 `md(8px)`、内容卡片 `lg(12px)`、hero 容器 `xl(16px)`、徽章 `pill`。
- 只用 default / active 两态，不做花哨 hover 动画。

### 9.3 前端工程要点

- `pnpm create vue@latest` 生成（TS + Router），随后 `pnpm add tailwindcss @tailwindcss/vite`、字体包；`package.json` 写死 `packageManager: "pnpm@11.15.1"`。
- `vite.config.ts` dev proxy：

  ```ts
  server: { proxy: {
    '/api': { target: 'http://localhost:8080', changeOrigin: true },
    '^/[A-Za-z0-9_-]{3,32}$': { target: 'http://localhost:8080', changeOrigin: true },
  }}
  ```

- `api/client.ts`：统一 `fetch` 封装 —— 自动附加 `Authorization` / `X-Manage-Key`、统一解析错误体、`401` 自动登出、读取 `VITE_API_BASE_URL`（默认 `/api`）。
- `useAuth.ts`：`localStorage` 存 `token` / `user` / `manageKeys`（按 code 记匿名密钥）。
- 图表 `TrendChart.vue`：纯 SVG 手写（面积 + 折线 + 稀疏刻度），珊瑚描边、`surface-card` 填充，移动端水平滚动。
- 空态/加载态/错误态都要有（`EmptyState` / `Spinner` / toast），MVP 也不能是"白屏 + 无反馈"。

---

## 10. Docker 一键部署

### 10.1 `docker-compose.yml`（草案，实施时按此落地）

```yaml
services:
  postgres:
    image: postgres:18-alpine            # 18.6 为当前稳定版；实施时钉到 18.6-alpine
    environment:
      POSTGRES_USER: ashen
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?required}
      POSTGRES_DB: ashen
    volumes:
      # ⚠️ PG18 起数据目录改为 /var/lib/postgresql/18/docker，
      #    必须挂父目录 /var/lib/postgresql —— 挂旧的 /data 路径容器会直接启动失败
      - pgdata:/var/lib/postgresql
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ashen -d ashen"]
      interval: 5s
      timeout: 3s
      retries: 10
      start_period: 10s
    restart: unless-stopped

  redis:
    image: redis:8-alpine                # 实施时钉到 8.10-alpine
    command: ["redis-server", "--appendonly", "yes", "--requirepass", "${REDIS_PASSWORD:?required}"]
    volumes:
      - redisdata:/data
    healthcheck:
      test: ["CMD-SHELL", "redis-cli -a $$REDIS_PASSWORD ping | grep -q PONG"]
      interval: 5s
      timeout: 3s
      retries: 10
    restart: unless-stopped

  migrate:                                # 一次性任务：跑完退出
    image: migrate/migrate:latest         # 实施时钉具体版本
    volumes:
      - ./backend/migrations:/migrations:ro
    command: ["-path", "/migrations", "-database", "${DATABASE_URL}", "up"]
    depends_on:
      postgres: { condition: service_healthy }
    restart: "no"

  backend:
    build: ./backend
    environment:
      HTTP_ADDR: ":8080"
      DATABASE_URL: ${DATABASE_URL}
      REDIS_ADDR: redis:6379
      REDIS_PASSWORD: ${REDIS_PASSWORD}
      JWT_SECRET: ${JWT_SECRET:?required}
      PUBLIC_BASE_URL: ${PUBLIC_BASE_URL:-http://localhost:8080}
      WORKER_ENABLED: "false"            # 生产由独立 worker 服务承担
      LOG_LEVEL: info
    depends_on:
      migrate: { condition: service_completed_successfully }
      redis:   { condition: service_healthy }
    expose: ["8080"]
    restart: unless-stopped

  worker:
    build: ./backend
    command: ["/app/worker"]             # 同镜像，换入口
    environment: *backend-env           # YAML 锚点复用
    depends_on:
      migrate: { condition: service_completed_successfully }
      redis:   { condition: service_healthy }
    restart: unless-stopped

  frontend:
    build: ./frontend
    ports: ["8080:80"]                   # 对外唯一入口
    depends_on: [backend]
    restart: unless-stopped

volumes:
  pgdata:
  redisdata:
```

### 10.2 镜像构建

**`backend/Dockerfile`**：多阶段
`golang:1.27-alpine`（`CGO_ENABLED=0 GOOS=linux`，`-trimpath -ldflags="-s -w"`，用 `go mod download` 先分层缓存）→ `gcr.io/distroless/static-debian12:nonroot`，只 COPY `api` / `worker` 两个二进制 + `ca-certificates`，以 nonroot 运行，配 `HEALTHCHECK` 或交给 compose。

**`frontend/Dockerfile`**：
`node:22-alpine` + `corepack enable pnpm`（版本由 `packageManager` 字段锁定）→ `pnpm install --frozen-lockfile && pnpm build` → `nginx:alpine` 托管 `dist/` + 自定义 `nginx.conf`。构建时需要 `--mount=type=cache` 或 `.dockerignore` 排除 `node_modules` 以加速。

### 10.3 `deploy/nginx/nginx.conf`（关键片段）

```nginx
location ^~ /api/     { proxy_pass http://backend:8080; include proxy_params; }
location = /healthz   { proxy_pass http://backend:8080; }
location ^~ /assets/  { try_files $uri =404; }              # 必须先于短码正则，否则 /assets 会被当短码
location ~ "^/[A-Za-z0-9_-]{3,32}$" {
    proxy_pass http://backend:8080;
    proxy_set_header X-Real-IP $remote_addr;                # 后端只信这个头
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
}
location / { try_files $uri $uri/ /index.html; }            # SPA fallback
```

### 10.4 一键启动

```bash
cp .env.example .env      # 改 POSTGRES_PASSWORD / REDIS_PASSWORD / JWT_SECRET
docker compose up -d --build
# → 打开 http://localhost:8080
```

`.env.example` 需要包含：`POSTGRES_PASSWORD`、`REDIS_PASSWORD`、`DATABASE_URL`（`postgres://ashen:<pwd>@postgres:5432/ashen?sslmode=disable`）、`JWT_SECRET`、`PUBLIC_BASE_URL`。**`JWT_SECRET` 给出 `openssl rand -base64 32` 的生成提示**，并在后端启动时校验"非默认值"（默认值直接拒绝启动，避免生产裸奔）。

### 10.5 已识别的部署坑（实施时必须避开）

| 坑 | 对策 |
| --- | --- |
| **PG18 数据目录变更**：挂 `/var/lib/postgresql/data` 会让容器 exit 1 | 挂父目录 `/var/lib/postgresql`（已在 compose 里注释说明） |
| nginx 层 `location` 优先级：`/assets` 会被短码正则误吃 | 用 `^~ /assets/`；短码正则放最后一道且单独 `include` |
| `migrate` 服务比 PG 早跑 → 连接失败 | `depends_on: service_healthy`（PG 的 healthcheck 用 `pg_isready`） |
| `backend` 比迁移早跑 → 表不存在 | `depends_on: migrate: service_completed_successfully` |
| Redis 无密码暴露 | 强制 `--requirepass`；compose 内**不 publish** 6379，只在开发 compose 里映射 |
| Windows 换行/权限：挂载的 SQL 文件带 CRLF | `.gitattributes` 里对 `*.sql` / `*.sh` 设 `eol=lf` |

---

## 11. 本地开发流程（非 Docker）

```bash
docker compose -f docker-compose.dev.yml up -d          # 只起 pg(5432) + redis(6379)
cd backend && go run ./cmd/api                          # :8080（WORKER_ENABLED=true 内嵌 worker）
cd frontend && pnpm install && pnpm dev                 # :5173，Vite 代理 /api 与短码到 :8080
cd backend && go run ./cmd/smoke -base http://localhost:8080   # 端到端冒烟
```

`WORKER_ENABLED=true` 时 api 进程内嵌启动同一套 worker 循环（代码复用 `internal/worker`），生产则用独立容器 —— 一套代码两种部署形态。

---

## 12. 里程碑与验收标准

| 阶段 | 内容 | 验收标准（可执行） |
| --- | --- | --- |
| **M0 骨架** | 目录、`go.mod`、`.env.example`、两个 compose、DRYRUN 迁移文件、nginx.conf、`/healthz` | `docker compose -f docker-compose.dev.yml up -d` 后 `go run ./cmd/api` 能起；`curl :8080/healthz` 返回 `{"status":"ok","postgres":"ok","redis":"ok"}`；`migrate up` 后 `\dt` 能看到 3 张表 |
| **M1 数据层** | config、domain、pgx 仓储、redis 客户端、迁移 up/down 可重复执行 | 仓储单测通过；`migrate down 1 && migrate up` 可重复执行不报错 |
| **M2 核心链路** | 短码生成/校验/保留字、创建（匿名+登录）、跳转+缓存、限流、JWT 鉴权 | `curl` 创建 → 拿到短链 → `curl -i` 看到 302 且 `Location` 正确；命中缓存时 PG 无查询（看 SQL 日志）；第 11 次创建返回 429 |
| **M3 统计** | Stream 生产/消费、批量落库、计数同步、`/stats` 聚合 | 跳转 10 次后 `total_clicks` 在 3s 内变为 10；`click_events` 条数正确；kill -9 worker 后重启能自动认领未 ACK 消息 |
| **M4 前端** | Tailwind token、布局、落地页、创建流、Dashboard、详情统计、登录注册 | `pnpm build` 零 TS 报错；手动走通"匿名创建 → 复制 → 打开短链 → 看统计"；移动端（<768px）布局不破 |
| **M5 部署收口** | 两个 Dockerfile、compose 一键起、README、冒烟工具、`go vet`/`go test` 全绿 | 在**干净目录**（删除 volume）执行 `docker compose up -d --build`，一条命令后浏览器能完成完整流程；`docker compose down && up` 数据仍在（volume 持久化） |

**全局 DoD**：`go vet ./...`、`go test ./...`、`gofmt -l` 无输出；`pnpm lint && pnpm build` 通过；README 里"3 条命令跑起来"部分实测有效。

---

## 13. 风险与对策

| 风险 | 影响 | 对策 |
| --- | --- | --- |
| 单域名下短码与 SPA 路由冲突 | 某些短码打不开前端页 | 保留字黑名单 + 后端单测断言 + nginx 优先级（已在 10.5 覆盖）；README 明确"新增前端顶级路由需同步后端保留字" |
| Redis 崩溃 | 统计丢失、缓存穿透到 PG | 降级为直查 PG（跳转仍可用）；统计失败计数暴露在 `/healthz`；限流失效时自动切"宽松模式"（放行但记日志），不做熔断自锁 |
| Stream 内存增长 | 消费跟不上导致 OOM | `XADD MAXLEN ~ 100000` 近似裁剪；worker 消费速率与丢弃计数打点，README 给排查命令 |
| 计数与明细不一致（重复投递 / 丢失） | 界面数字与明细对不上 | 计数以 `INCR` 为准（不受重投影响）；`link_click_totals` 视图便于人工比对；P1 加 `event_uid` 幂等去重 |
| 开放重定向被滥用 | 服务被当跳板 | 只允许 `http/https`（DB CHECK + 应用层双重校验）；目标 URL 长度上限 2048；保留字防 `api`/`login` 之类被注册成恶意跳转 |
| 短码被扫描器批量探测 | PG 被负缓存兜住但 CPU 上升 | 负缓存 60s + 跳转接口按 IP 宽松限流（如 600/min） |
| Go 1.27 太新，第三方库兼容性 | 编译失败 | 已实测 `json/v2` + stdlib `uuid` 可用；**只引入 3 个第三方依赖**（pgx、go-redis、golang-jwt），风险面极小；`go.mod` 里写 `go 1.27` |
| 部署机 docker 版本过旧 | compose 语法不支持 | `docker-compose.yml` **不写顶层 `version:` 字段**；`depends_on.condition` 需要 Compose v2+，README 注明最低版本 |

---

## 14. 需要你拍板的次要点（不影响开工，随时可改）

1. **项目名 / Go module 名**：已定 **AshenCourier**，`go mod init ashen-courier`（裸小写 kebab，与 `Notes-of-Ashen` 的 `module notes-of-ashen` 一致）。仓库名用 PascalCase、module 路径不带 `github.com/` 前缀：本项目是应用不是公开库，`internal/` 下的包不会被外部 import，前缀无收益；`go.mod` 根目录不放 `.go` 文件，连字符不会造成包名问题。若将来要把 `internal/pkg/base62` 等拆成公开库，再单独改 module 路径。
2. **短码长度**：默认 7 位（3.5 万亿空间）。追求更短可以 6 位（568 亿，配合冲突重试也够用）。
3. **对外端口**：默认 `8080:80`。你本机 8080 若被占用，改哪一边？
4. **是否需要 `pnpm lint`（ESLint + Prettier）**：默认加上，会在 M4 多花一点点时间。
5. **匿名链接默认有效期**：默认**永久**（`expires_at = NULL`）；是否改成默认 90 天？

---

## 15. 开工顺序（确认后我立刻执行）

1. 建目录骨架 + `go.mod` + `.env.example` + 两个 compose + migrations（M0）
2. Go 数据层 + 仓储 + 单测（M1）
3. 短码/创建/跳转/缓存/限流/鉴权（M2）
4. Stream + worker + 统计接口（M3）
5. 前端 token + 页面 + 联调（M4）
6. Dockerfile + nginx + 一键验证 + README（M5）
