<div align="center">

# AshenCourier

**把长链接，收成一条短链。**

匿名即可用，登录后可集中管理，点击统计跟手。

[![License: MIT](https://img.shields.io/badge/License-MIT-cc785c.svg)](./LICENSE)
[![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-18-4169E1?logo=postgresql&logoColor=white)](https://www.postgresql.org)
[![Redis](https://img.shields.io/badge/Redis-8-DC382D?logo=redis&logoColor=white)](https://redis.io)
[![Vue](https://img.shields.io/badge/Vue-3-4FC08D?logo=vuedotjs&logoColor=white)](https://vuejs.org)
[![Tailwind CSS](https://img.shields.io/badge/Tailwind-v4-06B6D4?logo=tailwindcss&logoColor=white)](https://tailwindcss.com)
[![Docker](https://img.shields.io/badge/Docker%20Compose-ready-2496ED?logo=docker&logoColor=white)](./docker-compose.yml)

</div>

---

## 这个项目有什么不一样

大部分短链项目的跳转路径会顺手往数据库写一行点击记录。AshenCourier 不这么做：

- **跳转路径零数据库写入。** `GET /{code}` 只做 Redis `GET` + `INCR` + `XADD`，
  缓存未命中才回源一次 PostgreSQL。数据库挂了对已缓存的短链都没有影响。
- **统计不阻塞跳转。** 点击写入一个有界队列（默认 4096），队满直接丢弃并计数。
  丢弃数在 `/healthz` 里可见 —— 宁可少记一次点击，也不让 302 慢 1 毫秒。
- **界面上「总点击」不会卡住。** 详情页与列表页的数字都是 `links.click_count`（PG 基线）
  + `clicks:cnt:{code}`（Redis 待同步增量）：详情页单键 `GET`，列表页一次 `MGET` 批量取，
  worker 每 2 秒回刷，正常情况下偏差小于 2 秒。统计侧读不到时列表退回纯基线，不报 5xx。
- **匿名也能管理。** 不注册就能建短链，返回一次性管理密钥（数据库只存 SHA-256，
  明文只在创建响应里出现一次）。登录后可以用它把链接**认领**到自己账号下。
- **限流降级而不是熔断。** Redis 挂了就全量放行并累计降级次数，绝不因为限流组件故障把整站打成 5xx。

## 目录

- [快速开始](#快速开始)
- [技术栈](#技术栈)
- [架构](#架构)
- [API](#api)
- [目录结构](#目录结构)
- [本地开发](#本地开发)
- [环境变量](#环境变量)
- [部署与运维排查](#部署与运维排查)
- [八条踩过的坑](#八条踩过的坑)
- [已知限制](#已知限制)
- [验收记录](#验收记录)
- [许可](#许可)

---

## 快速开始

```bash
git clone https://github.com/Elari39/AshenCourier.git
cd AshenCourier
cp .env.example .env      # 然后填写 JWT_SECRET：openssl rand -base64 32
docker compose up -d --build
# 打开 http://localhost:8080
```

首次构建需要拉镜像 + 编译前后端，约 3–5 分钟；之后 `up` 是秒级。

`.env` 里 **必须** 填的三项：

| 变量 | 说明 |
| --- | --- |
| `POSTGRES_PASSWORD` | 数据库口令 |
| `REDIS_PASSWORD` | Redis 口令（compose 内强制 `--requirepass`） |
| `JWT_SECRET` | `openssl rand -base64 32`；后端会拒绝空值/占位值并直接退出 |

`PUBLIC_BASE_URL` 决定返回的短链前缀（默认 `http://localhost:8080`），
`FRONTEND_PORT` 可改对外端口（默认 8080）。

> **环境要求**：Docker Compose v2+（本项目不使用顶层 `version:` 字段；
> `depends_on.condition` 需要 Compose v2）。实测环境：Docker 29.5.3 / Compose v5.1.4。

## 技术栈

| 层 | 选型 | 为什么 |
| --- | --- | --- |
| HTTP | Go 1.27 标准库 `net/http` ServeMux | 需要的方法感知路由 1.22 就有了，不引框架 |
| JSON | `encoding/json/v2` | 默认拒绝非法 UTF-8 与重复键 |
| 数据库 | PostgreSQL 18 + pgx v5 | 手写 SQL，唯一的视图是 `link_click_totals` |
| 缓存 / 队列 | Redis 8 | 缓存、计数增量、Stream、限流四类用法，都封装在 `store/redis` |
| 主键 | 标准库 `uuid.NewV7()` | 时间有序，对 B-tree 索引友好 |
| 日志 | `log/slog`（JSON） | 结构化，字段统一带 `request_id` |
| 前端 | Vue 3 + TypeScript + Vite | 不引 Pinia：composable + localStorage 就够 |
| 样式 | Tailwind CSS v4 | 设计 token 走 `@theme`，组件类走 `@layer components` |
| 图表 | 手写 SVG | 只需要「面积 + 折线 + 稀疏刻度」，不值得引几百 KB |
| 部署 | Docker Compose + nginx | 单域名同时托管 SPA、反代 `/api`、承接短码跳转 |

设计系统的来源是仓库里的 [`DESIGN.md`](./DESIGN.md)（暖奶油画布 + 珊瑚主色 + 深色产品面板 +
衬线大标题）。实施计划与全部技术取舍见 [`PLAN.md`](./PLAN.md)；后续迭代计划（M0–M5：CI、
幂等去重、明细页、标签、密码保护、GeoIP……）见 [`PLAN-NEXT.md`](./PLAN-NEXT.md)。

## 架构

```
Browser ──┬─ /api/*         ─┐
          ├─ /{code}        ─┤ nginx (frontend 容器 :80)
          └─ 其余（SPA 路由） │   ├─ /assets  → 本地静态资源（优先于短码正则）
                             │   └─ /        → index.html（history fallback）
                             ▼
                        backend:8080 ──┬── PostgreSQL 18（links / users / click_events / domains）
                                       └── Redis 8（缓存 / 计数增量 / Stream / 限流）
                                                      ▲
                                          worker 容器 ─┘（消费 Stream、回刷计数）
```

### 三条关键设计

1. **跳转不落库**：`GET /{code}` 只做 Redis `GET` + `INCR` + `XADD`，全程无 PostgreSQL 写入。
   缓存 miss 才回源一次，并把结果回填 —— 且同一短码的并发 miss 由 `singleflight`
   合并成**一次**回源（负缓存只能挡住「已确认不存在」，挡不住「刚出现的热点」）。
   回源次数在 `/healthz` 的 `pg_fallbacks` 里可见。
2. **计数最终一致**：详情页与列表页的「总点击」都是 `links.click_count`（PG 基线）
   + `clicks:cnt:{code}`（Redis 待同步增量），**两个口径一致** —— 不会出现「详情有数、列表没数」。
   worker 每 2 秒把增量刷回 PG，正常情况下偏差 < 2 秒。
   列表页用一次 `MGET` 批量读本页所有短码（不逐条查，延迟不随页大小线性增长）；
   统计侧读失败只记 warn 并退回纯基线，绝不把列表打成 5xx。
   回刷是**补偿式**的（见下面第 3 条）：增量只读不删、写库成功后才结算，
   进程崩溃最多让基线重复累加一批，不会丢计数。
3. **统计不阻塞跳转**：统计写入走**有界队列**（默认 4096），队列满直接丢弃并计数，
   丢弃数在 `/healthz` 的 `dropped_clicks` 里可见。

### 降级行为

| 故障 | 行为 |
| --- | --- |
| Redis 读缓存失败 | 当作未命中处理，回源 PostgreSQL；跳转仍可用 |
| Redis 写统计失败 | 记 warn 日志并丢弃该次统计，**不影响 302** |
| Redis 限流不可用 | 全量放行并累计降级次数（`/healthz` 的 `rate_limit_degraded`），不熔断自锁 |
| PostgreSQL 不可用 | 返回 503 + `Retry-After` |

### 数据模型

四张表 + 一个视图，初始结构在 [`backend/migrations/000001_init.up.sql`](./backend/migrations/000001_init.up.sql)，
后续迁移按编号递增（见 [`backend/migrations/`](./backend/migrations)）：

| 对象 | 作用 | 关键约束 |
| --- | --- | --- |
| `links` | 短链主体 | `short_code` **全局唯一**（短码生成与所有管理端接口都按它定位；**这是一条拍板结论**——PLAN-NEXT §18.5 决定不做「同码跨域共存」，不是遗留项）；`domain_id uuid`（000006 起，可空）指向所属自定义域名，`NULL` = 默认域名（`PUBLIC_BASE_URL` 指向的那个）；`status` 用 `smallint` 而非 PG enum（改状态机不用 `ALTER TYPE`）；`key_hash bytea` 存匿名管理密钥的 SHA-256；`tags text[]`（000003 起）配 GIN 索引做标签筛选 |
| `domains` | 自定义域名（000006 起） | `name` 唯一且**存归一化后的小写、无端口、无尾点**（`A.LOCAL:8080` 与 `a.local.` 是同一个域）；`is_active` 可关停而不删行（保留历史短链的归属）。**没有管理接口**：目前只能由运维写库登记，见「已知限制」 |
| `users` | 账号 | `email` 存 `text` + `unique index (lower(email))` 做大小写不敏感唯一（不引入 `citext` 扩展，省掉一次 `CREATE EXTENSION`） |
| `click_events` | 点击明细 | `ip inet`；`device` / `browser` / `os` 由 worker 解析 UA 后写入；`country` 由 worker 查 GeoIP 库文件后写入（未部署则恒为 NULL）；`event_uid`（000002 起）取自 Stream 消息 ID，配合部分唯一索引做幂等去重；`(link_id, occurred_at DESC, id DESC)`（000004 起）服务明细页的 keyset 翻页，旧的 `(link_id, occurred_at DESC)` 是被它覆盖的前缀索引，已删除 |
| `link_click_totals` | 视图 | `links.click_count + count(click_events)`，用于人工对账 |

## API

统一前缀 `/api`，`application/json; charset=utf-8`。

失败响应统一为：

```json
{ "error": { "code": "invalid_url", "message": "仅支持 http/https 链接", "field": "target_url", "request_id": "01J..." } }
```

> 请求体里的**未知字段会被拒绝**（400 `invalid_json`）：`encoding/json/v2` 的默认是静默
> 忽略未知成员，字段名拼错（`titel` / `targetUrl`）会「成功但没生效」，所以这里显式打开了
> `RejectUnknownMembers`。重复键、非法 UTF-8、类型不匹配同样是 400；请求体超过 64 KiB 是 413。

| # | Method | Path | 鉴权 | 说明 |
| --- | --- | --- | --- | --- |
| 1 | GET | `/healthz` | — | 存活 + 就绪（PG / Redis / Stream 积压 / 丢弃计数） |
| 2 | POST | `/api/auth/register` | — | 注册，返回 user + token |
| 3 | POST | `/api/auth/login` | — | 登录（限流 20 次 / 10 分钟 / IP） |
| 4 | GET | `/api/auth/me` | JWT | 当前用户 |
| 5 | POST | `/api/links` | 可选 JWT | 创建短链；匿名会返回一次性 `manage_key`（限流 10 次 / 分钟 / IP）。可选 `tags`（≤10 个、每个 ≤32 字符）、`password`（≥8 位，只落 bcrypt 摘要）与 `domain`（**必须已在 `domains` 表登记**，否则 422 `invalid_domain`；不传 = 默认域名） |
| 6 | GET | `/api/links` | JWT | 我的链接列表，游标分页 `?limit=20&cursor=&q=&tag=`（`tag` 按小写比较，走 GIN 索引） |
| 7 | GET | `/api/links/{code}` | JWT 或 Key | 详情（无权限一律 404，不泄露资源是否存在） |
| 8 | PATCH | `/api/links/{code}` | JWT 或 Key | 改 `target_url` / `title` / `tags` / `status` / `expires_at` / `password`（改后主动失效缓存）；`tags: []` 表示清空标签，`clear_password: true` 表示清除口令（`password` 传空串是 422） |
| 9 | DELETE | `/api/links/{code}` | JWT 或 Key | 软删除（`status=3`）+ 删缓存 |
| 10 | GET | `/api/links/{code}/stats?days=30` | JWT 或 Key | 统计聚合 |
| 11 | POST | `/api/links/{code}/claim` | JWT + Key | 把匿名短链认领到账号下 |
| 12 | GET | `/{code}` | — | **302 跳转**（不在 `/api` 下），**按请求的 `Host` 定位域**：未登记的主机名按默认域名处理。带口令且未解锁时改为 **200 口令页**（HTML，**不计点击**） |
| 13 | GET | `/api/links/{code}/clicks?limit=20&cursor=&days=30&device=` | JWT 或 Key | 点击明细，`(occurred_at, id)` keyset 分页（**时间倒序**）；`device` 取 `desktop` / `mobile` / `tablet` / `bot` / `unknown`（与分布口径一致）。**IP 只回掩码网段**：IPv4 → `/24`、IPv6 → `/64` |
| 14 | POST | `/{code}` | — | **口令校验**（表单 `password`）：正确 → **303** 回 `GET /{code}` 并下发解锁 cookie；错误 → **401** 重新渲染口令页。两者都**不计点击**（计点击的是随后那个 GET）。限流 20 次 / 10 分钟 / IP |
| 15 | GET | `/api/links/{code}/qr.svg` | — | **二维码 SVG**（`image/svg+xml`），给邮件模板 / 印刷品 / 第三方系统引用。**公开可读**（二维码的内容就是 `short_url` 本身，而 `GET /{code}` 本来就公开）；只要求「短链存在且未被软删除」，已停用/过期的链接**仍能取图**（印好的二维码不该因此失效）。`Cache-Control: public, max-age=300`。限流沿用统计那一档（IP + 路径哈希） |
| 16 | GET | `/metrics` | — | **Prometheus 文本格式**（`text/plain; version=0.0.4`），与 `/healthz` 同一次采集。**不对外**（nginx 里 `= /metrics` 直接 404）、**不加鉴权也不限流**（只在内网可达）；探针异常时仍回 **200**，故障由 `ashen_*_up 0` 表达。详见「`/metrics`」一节 |

**访问口令**（`POST /{code}`）

- 摘要用 **bcrypt cost 12** 存在 `links.password_hash`，**绝不进缓存、绝不回响应**：跳转路径只读缓存里的「有没有口令」这一个布尔（`linkDTO.password_protected`，`omitzero`，缺席即「不需要口令」）
- 比对只在被限流的 `POST /{code}` 上做一次**库读**（缓存里没有摘要，也不该有）
- 解锁 cookie 名 `ac_unlock`，值把短码装进签名体（HMAC-SHA256，子密钥由 `JWT_SECRET` 域分离派生），因此「A 链的解锁」在 B 链上一律无效；`HttpOnly` + `SameSite=Lax` + `Path=/`，**站点是 https 时**才加 `Secure`（写死它会让 `http://localhost:8080` 永远解锁不了）。一个浏览器只记一条链接的解锁状态
- 解锁后 **303 而不是 302**：303 让浏览器改用 GET 去取，于是解锁本身不计点击、也不会刷新重放提交

**鉴权方式**

- 登录态：`Authorization: Bearer <JWT>`（HS256，7 天）
- 匿名管理：创建时返回的 `manage_key`（32 字节随机 → base64url 43 字符），
  后续请求带 `X-Manage-Key`。**数据库只存它的 SHA-256**，明文只在创建响应里出现一次。

**统计响应**（`/api/links/{code}/stats`）

```json
{
  "total_clicks": 1284,
  "window_clicks": 342,
  "days": 30,
  "since": "2026-08-28T00:00:00Z",
  "daily":        [{ "date": "2026-09-01", "clicks": 12 }],
  "top_referers": [{ "referer": "twitter.com", "clicks": 300 }],
  "devices":      [{ "device": "mobile", "clicks": 800 }],
  "browsers":     [{ "browser": "Chrome", "clicks": 900 }],
  "countries":    [{ "country": "CN", "clicks": 512 }]
}
```

`total_clicks` 是全量口径（PG 基线 + Redis 待同步增量）；
`daily` 与四个分布是窗口口径。`daily` 一定补齐成连续的 `days` 天，缺失日期为 0。
`countries` 只含**已知国家** —— 没部署 GeoIP 库文件时是空数组（见「GeoIP 国家维度」）。

标记为 `omitzero` 的字段（`window_clicks` / `since` 等）在零值时不出现——
`window_clicks` 为 0 就是「窗口内还没有明细落库」，前端按 `?? 0` 兜底。
`frontend/src/api/types.ts` 里这些字段都是可选的，正是这个原因。

**明细响应**（`/api/links/{code}/clicks`）

```json
{
  "clicks": [
    {
      "id": 188,
      "occurred_at": "2026-09-19T02:36:32Z",
      "referer": "https://news.example/post/1",
      "user_agent": "Mozilla/5.0 (iPhone; …) Safari",
      "ip": "172.20.0.0/24",
      "device": "mobile",
      "browser": "Safari",
      "os": "iOS"
    }
  ],
  "next_cursor": "MjAyNi0wOS0xOVQwMjozNjoxMS4xNjI4MjJafDE4Nw",
  "days": 30,
  "since": "2026-08-21T00:00:00Z"
}
```

`ip` 是**掩码后的网段**（IPv4 抹掉最后一段、IPv6 只留前 4 组），原始地址只留在
`click_events.ip` 里供风控 / 排障直接查库 —— 明细页要定位到「哪个网段」就够了，
而 API 响应会经浏览器缓存、截图、共享看板流转。`next_cursor` 为空表示已到底；
游标是 `RFC3339Nano|id` 的 base64url，对前端不透明（结构随时可换而不破坏兼容）。

## 目录结构

```
.
├── docker-compose.yml          # 生产形态：pg + redis + migrate + backend + worker + frontend
│                               #   （另有 backup 服务，在 ops profile 下按需启动）
├── docker-compose.dev.yml      # 本地开发：只起 pg(5432) + redis(6379)；migrate 在 tools profile 下
├── deploy/nginx/nginx.conf     # 反代 + 短码正则 + SPA fallback
├── deploy/backup/              # pg_dump 产物目录（*.dump 已被 .gitignore 忽略）
├── deploy/geoip/               # GeoIP 国家库目录（*.mmdb 已被 .gitignore 忽略）
├── backend/
│   ├── migrations/             # golang-migrate 迁移（up / down 严格互逆）
│   ├── cmd/{api,worker,smoke}/ # 两个服务入口 + 端到端冒烟工具
│   └── internal/
│       ├── config/             # env → Config（cmp.Or 给默认值 + 启动前强制校验）
│       ├── domain/             # 实体 / 领域错误 / 仓储与端口接口（不 import 任何第三方库）
│       ├── store/postgres/     # pgxpool 仓储实现
│       ├── store/redis/        # 缓存 / 计数 / Stream / 限流
│       ├── store/geoip/        # MaxMind DB 国家库解析（mmdb，未配置时降级为空）
│       ├── service/            # 应用服务（短链、统计、鉴权、管理密钥）
│       ├── handler/            # HTTP 处理器 + 路由装配
│       ├── httpx/              # JSON 读写、统一错误体、中间件、限流中间件、/metrics 文本渲染、http.Server
│       ├── worker/             # Stream 消费 + 计数回刷 + 过期清理
│       └── pkg/                # base62 / shortcode / hostname / ua / validator
└── frontend/
    ├── e2e/                    # 浏览器级验收（无头 Chrome + CDP，零 npm 依赖）
    └── src/
        ├── api/                # fetch 封装 + 类型契约 + 会话存储
        ├── composables/        # useAuth / useToast / useCopy（不引 Pinia）
        ├── utils/              # 纯函数（格式化、标签拆分），有 vitest 单测
        ├── views/              # Landing / Dashboard / LinkDetail / Login / Register / NotFound
        └── components/         # 业务组件 + ui/ 基础组件
```

## 本地开发（非 Docker）

```bash
# 1) 只起 PG + Redis
docker compose -f docker-compose.dev.yml up -d

# 2) 跑迁移（migrate 挂在 tools profile 下，不会被 up -d 拉起）
docker compose -f docker-compose.dev.yml --profile tools run --rm migrate

# 3) 后端（内嵌 worker）
cd backend
export DATABASE_URL='postgres://ashen:ashen@localhost:5432/ashen?sslmode=disable'
export REDIS_ADDR='localhost:6379'
# 开发形态的 redis 也强制了 requirepass（口令与 postgres 一样是 ashen），
# 且两个端口都只绑 127.0.0.1 —— 不这么做等于把一台无口令的 Redis 开在局域网上
export REDIS_PASSWORD='ashen'
export JWT_SECRET="$(openssl rand -base64 32)"
export WORKER_ENABLED=true
go run ./cmd/api

# 4) 前端
cd frontend && pnpm install && pnpm dev     # http://localhost:5173
```

Vite 的 dev proxy 会把 `/api`、`/healthz` 与短码正则 `^/[A-Za-z0-9_-]{3,32}$` 转发到 `:8080`；
**SPA 顶级路由（`/login`、`/dashboard` 等）在 `vite.config.ts` 里显式 bypass**，
否则开发时刷新这些页面会打到后端拿 404。

### 质量校验

```bash
cd backend
gofmt -l .            # 必须无输出
go vet ./...
go test ./...         # base62 / shortcode / ua / validator 的单测

# 端到端冒烟。走 nginx（http://localhost:8080）时加 -expect-spa，
# 它会额外断言 /login、/dashboard 这类顶级路由返回 HTML 而不是被短码正则截走
go run ./cmd/smoke -base http://localhost:8080 -expect-spa
go run ./cmd/smoke -base http://localhost:8080            # 直连后端时不要加（顶级路由本就是 404）

cd frontend
pnpm lint && pnpm test && pnpm build

# 浏览器级验收（需要全栈已经跑起来）。它只用 Node 内置的 fetch 与 WebSocket 驱动
# 无头 Chrome，不装任何 npm 包；默认连 http://localhost:8080
node e2e/browser-check.mjs --base http://localhost:8080
```

`frontend/e2e/` 断言的是**接口测不出来的那一类问题**：二维码画布有没有撑破容器、
明细翻页后两页有没有重叠、口令页在真浏览器里会不会按 303 换成 GET 并带上 cookie。
它只创建 1 条短链（创建接口是 10 次/分钟/IP 的硬配额），跑之前请先起全栈；详见
[八条踩过的坑](#6-渲染库写的行内尺寸会盖过-tailwind-类) 第 6 条。

| 变量 | 作用 |
| --- | --- |
| `CHROME_BIN` | 指定 Chrome 可执行文件（默认按平台猜常见位置；CI 用 runner 预装的 `/usr/bin/google-chrome`） |
| `CHROME_FLAGS` | 追加启动参数，空格分隔（以 root 运行的容器里需要 `--no-sandbox`） |

冒烟工具覆盖：健康检查 → **SPA 顶级路由** → 匿名创建 → 302 跳转 → 统计收敛
（同时校验 Redis 计数与落库明细）→ 鉴权边界 → 修改后缓存失效 → 保留字与开放重定向防护
→ 注册/登录 → 认领 → 分页 → 限流。**用 Go 写而不是 shell**：跨平台，且能做真正的 JSON 断言。

### store 层集成测试（迁移与手写 SQL）

`internal/store/postgres` 的集成测试默认**整体跳过**（`t.Skip`），需要一个真 PG。
它跑的是「迁移文件本身 + 手写 SQL 的真实行为」：迁移后的索引形状、keyset 分页在并列时间上
不漏行不重复、`tags @> ARRAY[...]` 确实走 GIN 索引、`event_uid` 幂等去重、聚合的 UTC 日界。

```bash
# 本机用开发形态的 PG（它映射了 5432；生产形态的 compose 刻意不映射）
docker compose -f docker-compose.dev.yml up -d postgres
docker compose -f docker-compose.dev.yml exec postgres createdb -U ashen ashen_test   # 首次

cd backend
POSTGRES_TEST_DSN='postgres://ashen:ashen@localhost:5432/ashen_test?sslmode=disable' \
  go test -race -count=1 ./internal/store/postgres/
```

⚠️ 库名**必须以 `_test` 结尾**：准备阶段会 `DROP SCHEMA public CASCADE` 再按序重放全部
迁移，护栏就是为了不让它落到开发库上。CI 里由 `backend` job 的 postgres service 提供，
所以每次 PR 都会真跑一遍。

⚠️ 顺带一提：`docker-compose.dev.yml` 与生产形态的 `docker-compose.yml` **共用同一个
compose 项目名**，两个文件里的 `postgres` 是同一个容器名。所以「起一个 dev 的 PG 来跑集成测试」
会把正在跑的 postgres 容器换成 dev 定义（连带换掉数据卷）—— 跑完用 `docker compose up -d`
把生产形态拉回来，别在只跑着 dev PG 的状态下调试主栈。

### GeoIP 国家维度（可选）

点击明细与统计都支持按国家分布，但**默认关闭**：它需要一个外部的国家库文件，
而库文件不入库 —— 体积数 MB、每月更新一次，进 Git 只会让仓库变大，
还会让人误以为它是项目源码。

```bash
# 1) 把 MaxMind DB 格式的国家库放进 deploy/geoip/（该目录下的 *.mmdb 已被 .gitignore 忽略）
#    推荐 DB-IP Lite（免注册，CC BY 4.0）：https://db-ip.com/db/download/ip-to-country-lite
#    GeoLite2-Country（MaxMind，需要账号）同样可用 —— 两者文件格式与 country.iso_code 字段一致

# 2) 在 .env 里指向**容器内**的路径
GEOIP_DB_PATH=/geoip/dbip-country-lite.mmdb

# 3) 重建 worker（解析发生在 worker，不在跳转路径上）
docker compose up -d worker
docker compose logs worker | grep GeoIP     # 期望看到 "GeoIP 库文件已加载"
```

**为什么解析放在 worker 而不是跳转路径**：跳转路径的承诺是「零数据库写入 + 只碰 Redis」。
mmdb 查询虽然只是一次内存映射读，但它会引入文件句柄与页缓存的不确定性；
而点击事件本来就是异步落库的，多解析一次国家码完全在 worker 的预算内。

**降级行为**（两种都是受支持的部署形态，任何一种都不会让进程起不来）：

| 配置 | 行为 | 日志 |
| --- | --- | --- |
| `GEOIP_DB_PATH` 留空 | 国家字段一律为空，跳转/计数/其余统计照旧 | 一条 `INFO` |
| 配了但文件打不开 | 同上 | 一条 `WARN` |

`country` 只落**已知国家**：查不到的点击不进国家分布，因此「没开这个功能」就等于
「国家列表为空」，前端据此整块隐藏 —— 而不是画一个 100% 的「未知」条
（那会让人以为是解析失败，而不是「我没开这项」）。国家码是 ISO 3166-1 alpha-2（`CN` / `US`）。

**数据来源与署名**：本仓库**不附带**任何 GeoIP 数据。使用
[DB-IP Lite](https://db-ip.com) 时，数据由 DB-IP 提供、按
[CC BY 4.0](https://creativecommons.org/licenses/by/4.0/) 授权，要求署名；
使用 MaxMind 的 GeoLite2 时同样需要按其许可署名。本节即项目的署名位置 ——
分发本项目的镜像或对外服务时请一并保留。

### 关于 `frontend/.npmrc` 的镜像源

`.npmrc` 里把 registry 指到了腾讯云镜像，这是实测结果而不是直觉选择 ——
同一个 8 MB 的原生绑定包，三个源的实测速度是：

| 源 | 速度 |
| --- | --- |
| `registry.npmjs.org` | 465 KB/s |
| `mirrors.cloud.tencent.com/npm` | **1.36 MB/s** |
| `registry.npmmirror.com` | 67 KB/s（会超时导致安装失败） |

镜像速度随网络环境变化很大，换环境时建议重新实测（下载任意一个大包对比
`%{speed_download}`），而不是沿用这里的结论。Docker 构建会复用同一份 `.npmrc`。

### 版本说明：TypeScript 被钉在 5.9.3

`vue-tsc` 3.3.11 依赖 `typescript/lib/tsc`，而 TypeScript 7（Go 原生编译器）
已经移除了这个导出路径 —— 用 TS 7 会让 `pnpm build` 直接报
`ERR_PACKAGE_PATH_NOT_EXPORTED`。因此 `package.json` 里显式钉住 `typescript@5.9.3`。
等 `vue-tsc` 支持 TS 7 之后可以放开。

## 环境变量

### compose 层（`.env`）

| 变量 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- |
| `POSTGRES_PASSWORD` | ✅ | — | 数据库口令 |
| `REDIS_PASSWORD` | ✅ | — | Redis 口令 |
| `JWT_SECRET` | ✅ | — | ≥16 字节，拒绝占位值 |
| `DATABASE_URL` | ✅ | — | `postgres://ashen:<pwd>@postgres:5432/ashen?sslmode=disable` |
| `PUBLIC_BASE_URL` | | `http://localhost:8080` | 短链前缀 + CORS 允许来源 |
| `FRONTEND_PORT` | | `8080` | 对外端口 |
| `LOG_LEVEL` | | `info` | `debug` / `info` / `warn` / `error` |

### 后端进程（compose 已注入，本地开发需自己 export）

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | 监听地址 |
| `REDIS_ADDR` | `localhost:6379` | Redis 地址 |
| `REDIS_DB` | `0` | 逻辑库编号 |
| `WORKER_ENABLED` | `false` | `true` 时 api 进程内嵌同一套 worker 循环（本地开发用） |
| `TRUST_PROXY` | `true` | 从 `X-Real-IP` 取客户端 IP；**不**信任 `X-Forwarded-For` |
| `RATE_LIMIT_DISABLED` | `false` | `true` 时启动即全量放行（限流应急开关），状态见 `/healthz` 的 `rate_limit_disabled` |
| `GEOIP_DB_PATH` | 空 | MaxMind DB 格式的国家库在**容器内**的路径（如 `/geoip/dbip-country-lite.mmdb`）。留空或文件打不开都只是让 `country` 留空，不影响跳转与统计，见「GeoIP 国家维度」 |
| `POSTGRES_TEST_DSN` | 空 | **只给测试用，进程不读它**：store 集成测试的 DSN，未设置时整体跳过（见「store 层集成测试」） |
| `GEOIP_TEST_DB` | 空 | **只给测试用，进程不读它**：`internal/store/geoip` 里唯一会真查库的用例的库文件路径，未设置时该用例 SKIP |

### 前端（可选）

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `VITE_API_BASE_URL` | `/api` | API 基址 |
| `VITE_PUBLIC_BASE_URL` | `window.location.origin` | 展示短链用 |

> 只有 `VITE_` 前缀会打进产物，**绝不要往里面放密钥**。

## 部署与运维排查

```bash
# 服务健康 + 诊断计数（丢弃数、队列积压、Stream 积压、限流是否走原生 INCREX）
curl -s localhost:8080/healthz | jq

# 同一批数字的 Prometheus 文本形态。⚠️ 它**不对外**：
# nginx 里 `location = /metrics { return 404; }`，走 localhost:8080 会拿到 404。
# 要从本机读，得借同一网络内的容器（前端镜像是 nginx:alpine，自带 busybox wget）：
docker compose exec frontend wget -qO- http://backend:8080/metrics

# 容器状态（5 个都该是 healthy）
docker compose ps

# Stream 长度 / 未 ACK 条数
docker compose exec redis redis-cli XLEN clicks:stream
docker compose exec redis redis-cli XPENDING clicks:stream clicks

# 待回刷计数的短码
docker compose exec redis redis-cli SMEMBERS clicks:dirty

# 计数与明细是否对得上（基线 + 明细数应等于界面上的总点击）
docker compose exec postgres psql -U ashen -d ashen -c \
  "select short_code, click_count as base,
          (select count(*) from click_events e where e.link_id = l.id) as events
   from links l order by created_at desc limit 20;"

# 最近失败的请求
docker compose logs backend | grep '"level":"ERROR"'
```

`/healthz` 关键字段：

| 字段 | 含义 |
| --- | --- |
| `status` | `ok` / `degraded`（PG 或 Redis 异常时 degraded，HTTP 503） |
| `dropped_clicks` / `failed_clicks` | 统计因队满 / 写失败而丢弃的次数 |
| `queue_len` | 统计写入队列积压长度 |
| `stream_len` / `stream_pending` | Stream 长度 / 未 ACK 条数 |
| `pg_fallbacks` | 短码缓存未命中、**真正回源 PG** 的累计次数（缓存击穿的观测口径：同一个冷短码被 N 个并发请求打过来时，它只该 +1） |
| `rate_limit_degraded` | 限流器因 Redis 故障降级的累计次数 |
| `rate_limit_native_increx` | 限流走的是 Redis 8.8+ 原生 `INCREX` 还是 Lua 回落实现 |
| `rate_limit_disabled` | 限流应急开关是否被打开（`RATE_LIMIT_DISABLED=true`） |

### `/metrics`（Prometheus 文本格式）

同一批数字的另一种形态，供 Prometheus 抓取。**零依赖**：没有客户端库，就是几十行手写的
文本序列化（`internal/httpx/metrics.go` 的 `RenderMetrics`）。

```bash
docker compose exec frontend wget -qO- http://backend:8080/metrics
```

| 指标 | 类型 | 来源字段 |
| --- | --- | --- |
| `ashen_build_info{version="…"}` | gauge（恒 1） | 构建版本 |
| `ashen_up` / `ashen_postgres_up` / `ashen_redis_up` | gauge | `status` / `postgres` / `redis` |
| `ashen_uptime_seconds` / `ashen_worker_enabled` | gauge | 同上 |
| `ashen_dropped_clicks_total` / `ashen_failed_clicks_total` | counter | 统计丢弃 / 写失败 |
| `ashen_consumed_clicks_total` / `ashen_worker_errors_total` | counter | 内嵌 worker 的消费与出错 |
| `ashen_pg_fallbacks_total` / `ashen_ratelimit_degraded_total` | counter | 回源 PG / 限流降级 |
| `ashen_click_queue_length` / `ashen_click_stream_length` / `ashen_click_stream_pending` | gauge | 队列与 Stream 积压 |
| `ashen_ratelimit_native_increx` / `ashen_ratelimit_disabled` | gauge | 限流实现与应急开关 |

三条刻意的设计（改之前先读一遍，都是有原因的）：

- **只在内网可达**。nginx 里 `location = /metrics { return 404; }` —— 用 `return 404` 而不是 `deny`，
  因为对外要表达的语义是「这里什么都没有」；`403` 等于告诉扫描器「这个路径存在，只是不给你看」。
  `=` 精确匹配优先级最高，必定先于短码正则命中（`metrics` 正好 7 位，落在 `^/[A-Za-z0-9_-]{3,32}$` 里，
  所以它**必须**同时在 `pkg/shortcode/reserved.go` 里，否则有人能把它注册成短码）
- **探针挂了也回 200**（`/healthz` 会回 503）。指标端点的职责是把当前观测值交出去，
  而 `ashen_postgres_up 0` 正是这一轮抓取里最有价值的数据 —— 回 503 会让抓取端整份丢弃，
  反而在最需要观测的时候失去观测能力
- **值为 0 的计数器仍然输出**。Prometheus 的 `rate()` / `increase()` 比较的是相邻样本，
  序列缺席就断成两段；「缺席」在查询侧另有含义（区分「没有数据」与「真的是 0」）。
  所以这里没有 `omitempty` 语义 —— 对照 `HealthReport` 上的 `omitzero`，那是给 JSON 看的

> 顺带修掉的一个误报：`Report` 原先让两个探针**共用**同一个 3 秒 deadline。PG 容器被停掉后，
> 到它的 TCP 连接不会立刻被拒（那个 IP 上已经没有东西在听了），而是一直重试到 deadline ——
> 3 秒被吃光，紧接着的 Redis ping 落在一个已过期的 context 上必然失败，于是 `/healthz` 报
> `redis: "error"`（Redis 明明是好的），`/metrics` 上就是 `ashen_redis_up 0`。
> 这会作废「靠单个探针定位是哪个依赖出问题」这个卖点。现在每个探针各自计时。

### 备份与恢复演练

备份服务放在 compose 的 `ops` profile 里 —— **默认不启动**，需要时显式拉起：

```bash
docker compose --profile ops up -d backup
```

它用与服务端**同一个 tag** 的 `postgres:18.6-alpine`（`pg_dump` 主版本必须 ≥ 服务端，
版本倒挂会直接报 `server version mismatch`），每 24 小时往 `./deploy/backup/` 写一个
`ashen-YYYYmmdd-HHMMSS.dump`（`-Fc` 自定义格式，可用 `pg_restore` 选择性恢复），
并删除 14 天前的旧备份。`pg_dump` 失败时不会傻等一天：打日志后 60 秒重试。

**恢复演练（必须真的跑一次，否则等于没有备份）**：

```bash
# 1) 建一个临时库
docker compose exec postgres createdb -U ashen restore_check

# 2) 把最新的备份恢复进去
#    /backup 只挂在 backup 服务上，所以借它的镜像与卷跑 pg_restore
DUMP=$(ls -1t deploy/backup/ashen-*.dump | head -1 | xargs basename)
docker compose run --rm --entrypoint pg_restore backup \
  -h postgres -U ashen -d restore_check "/backup/$DUMP"

# 3) 核对两个口径与主库一致
docker compose exec postgres psql -U ashen -d ashen -tAc \
  "select count(*) from links; select sum(base_count), sum(event_count) from link_click_totals;"
docker compose exec postgres psql -U ashen -d restore_check -tAc \
  "select count(*) from links; select sum(base_count), sum(event_count) from link_click_totals;"

# 4) 删掉临时库
docker compose exec postgres dropdb -U ashen restore_check
```

2026-09-18 实测：`links` 14 行、`click_events` 169 行、`sum(base_count)` 与
`sum(event_count)` 都是 169，主库与恢复库完全一致；保留策略也实测过（造一个 2000 年的
假备份 → 跑一次清理 → 旧文件被删、当天的留下）。

⚠️ 备份文件在宿主机的 `./deploy/backup/`（已被 `.gitignore` 忽略）。生产环境请把该目录
换成对象存储或异地卷 —— 和数据库放在同一块盘上的备份，在磁盘故障时一起没有。

## 八条踩过的坑

这八条都是「设计稿上看不出来」、只有真跑起来（跑容器、真在浏览器里看一眼、或者真跑一遍 CI）
才暴露的，写在这里省得别人再踩一遍。

### 1. 新增前端顶级路由，必须同步四处

单域名下短码与 SPA 路由共用路径空间。新增顶级路由（例如 `/settings`）时要改**四个地方**：

1. `deploy/nginx/nginx.conf` 加一行 `location = /settings { try_files $uri /index.html; }`
2. `backend/internal/pkg/shortcode/reserved.go` 的 `reserved` 集合
3. `backend/internal/pkg/shortcode/shortcode_test.go` 里 `TestReservedSetContents` 的断言
4. `frontend/vite.config.ts` 的 `SPA_ROUTES`（dev proxy bypass）

漏掉第 1 步 → 生产环境刷新 `/settings` 会 404；
漏掉第 2 步 → 别人可以注册 `settings` 当短码，把真实页面吃掉；
漏掉第 4 步 → 开发环境刷新该页面会 404。

> **同一个坑的反面：新增「不是 SPA 页面但对外存在」的路径，也要动第 1、2 步。**
> `/metrics` 就是例子 —— 它 7 位、形状与短码完全一致，于是同时踩两头：
> 不在 nginx 里显式处理，它会被短码正则截走转发到后端（「后端能出指标、公网也能读」的最坏组合）；
> 不进保留字表，别人就能把 `metrics` 注册成短码（症状是「他的短链永远打不开」）。
> 判据是形状，不是用途：**新路径只要匹配 `^/[A-Za-z0-9_-]{3,32}$`，这两步一个都不能省。**

> **为什么 nginx 那一步不能省**：nginx 的 location 优先级是
> `= 精确` → `^~ 前缀` → `~ 正则` → 最长普通前缀。正则一旦命中就**赢过** `location /`，
> 所以 `/login`、`/dashboard` 这类「看起来像短码的单段路径」会被短码正则截走，
> 转发到后端拿 404。必须用 `=` 精确匹配把它们拦下来。
> （不能用 `^~ /login`，那会连带把 `loginabc` 这种合法短码也误伤。）

### 2. 不要把短码长度改到 3 位以下

短码字符集是 `[0-9A-Za-z_-]`、长度 3–32，这三处必须一致：
`internal/pkg/shortcode` 的常量、`deploy/nginx/nginx.conf` 的 location 正则、
数据库约束 `links_code_shape`。

### 3. `click_events` 的投递语义是 at-least-once

worker 在「处理完但 ACK 前」重启时，同一批明细会被重投。两个口径都不因此走样：

- **计数**走 Redis `INCR` 累加，重投不会多算；
- **明细**靠 `event_uid`（取自 Stream 消息 ID，迁移 `000002`）的**部分唯一索引**
  做幂等去重，插入侧是
  `ON CONFLICT (event_uid) WHERE event_uid IS NOT NULL DO NOTHING`，
  重投的行被静默跳过（`RowsAffected=0`，不是错误）。

⚠️ 冲突目标必须复述同一个谓词（`WHERE event_uid IS NOT NULL`），否则 PG 会报
`no unique or exclusion constraint matching the ON CONFLICT specification`。
`000002` 之前的历史行 `event_uid` 为 NULL，不参与去重。

人工对账用视图 `link_click_totals` 比较 `base_count` 与 `event_count`。

### 3b. 计数回刷是补偿式的（崩溃只重复、不丢失）

worker 刷计数分三步，**增量只读不删**（M3-1）：

```text
读取：GET clicks:cnt:{code}                  ← 不删键
写库：UPDATE links SET click_count += delta
结算：INCRBY clicks:cnt:{code} -delta + SREM clicks:dirty {code}   ← MULTI/EXEC 原子
```

于是进程崩溃只剩两种结果：

| 崩在哪 | 结果 |
| --- | --- |
| `GET` 之后、写库之前 | 键里还有 delta、dirty 也还在 → 下一轮重做，**不丢** |
| 写库之后、结算之前 | 基线**重复累加一批**（≤ 一个批次），不是丢失 |
| 结算之后 | 正常 |

写库**报错**时不需要「归还」：值一直留在键里，只需保证 dirty 标记还在（下一轮重试）。
这与「宁可重复累加也不能丢」一致；用 `link_click_totals` 对账时，
`base_count` 比 `event_count` 略大属于已知情形，上限是一批。
结算失败会打 `基线可能重复累加（上限一批）` 的 error 日志（内嵌 worker 时还会进入
`/healthz` 的 `worker_errors`；生产形态下 worker 是独立容器，看它的容器日志）。

### 4. api 与 worker 共用镜像，换入口必须用 `entrypoint` 而不是 `command`

`backend/Dockerfile` 是 exec 形式的 `ENTRYPOINT ["/app/api"]`。
compose 里的 `command` **只会成为 ENTRYPOINT 的参数**，所以

```yaml
command: ["/app/worker"]        # ❌ 实际执行 /app/api /app/worker
entrypoint: ["/app/worker"]     # ✅
```

写错的症状很隐蔽：容器起得来、看起来也在跑，实际上是第二个 API 进程
（`/app/api` 忽略多余的位置参数照常启动），只是恰好没报错。
**判断方法**：看 worker 容器的日志 —— 如果出现 `"msg":"http"` 的访问日志，
说明它跑的是 API 而不是 worker。

### 5. nginx 必须用运行时 DNS 解析上游

nginx 默认只在**启动时**解析一次上游主机名。后端容器一旦重建
（`docker compose up -d --build backend`），IP 就变了，而 nginx 还黏着旧 IP，
对外表现为持续 **502 Bad Gateway**，且 nginx 自己不会恢复。

`deploy/nginx/nginx.conf` 里已经处理好了：

```nginx
resolver 127.0.0.11 valid=10s ipv6=off;   # Docker 内置 DNS
map $host $backend_upstream { default backend:8080; }
# 关键：proxy_pass 里带变量 → nginx 走运行时解析（按 valid= 缓存）
proxy_pass http://$backend_upstream;
```

刻意没有用 `upstream` 块 —— upstream 的 `server` 名同样只在启动时解析一次，
要动态化得依赖 nginx 1.27.3+ 的 `server ... resolve` 参数，兼容性更差。

### 6. 渲染库写的行内尺寸会盖过 Tailwind 类

详情页的二维码用 `qrcode` 画在 canvas 上。它的 canvas 渲染器为了像素对齐，会把尺寸
**写成行内样式**（`qrcode/lib/renderer/canvas.js` 的 `clearCanvas`：

```js
canvas.width = size
canvas.height = size
canvas.style.width = size + 'px'   // ← 这一行
canvas.style.height = size + 'px'
```

）。于是 `class="h-full w-full"` 完全不生效 —— **行内样式优先级高于任何类选择器**，
画布以 320px 撑破 160px 的容器，压住右边的文字和下方的卡片。

修法是画完之后把行内尺寸清掉（位图分辨率保持不变，显示尺寸交回 CSS）：

```ts
await QRCode.toCanvas(canvas, url, { width: 320, /* … */ })
canvas.style.width = ''
canvas.style.height = ''
```

**更值得记的是它怎么被发现的**：当时的验收只解码了 `toDataURL()` 的像素 —— 那永远是
320×320，解码必然成功，**版式坏了也照样全绿**。是有人在真浏览器里看了一眼才暴露的。
所以现在这条验收多了三个版式断言：画布矩形必须落在容器矩形内、显示宽必须等于
容器宽减去 padding、右边缘不得压到文字列。

> 一般化的教训：**像素级断言不等于版式断言**。「图能解码」「接口 200」这类结论，
> 和「页面对不对」是两件事 —— 前者可以全绿而后者的确歪了。

这几条断言现在**固化进了仓库**（`frontend/e2e/detail-page.mjs`），并且做过变异验证：
把 `canvas.style.width = ''` 这两行去掉后重跑，**4 条版式断言全红** ——
画布显示宽从 142px 变成 320px、右边缘从 309.5 变成 487.5（越过了容器右边缘 318.5
与文字列左边缘 342.5），而**像素断言依然全绿**。也就是说，它确实守着当初那个洞，
而不是「跑了、绿了、什么也没守住」。

### 7. 第三方库的默认值：先实测，再写进计划

`PLAN-NEXT §19.1` 的初稿断言「`skip2/go-qrcode` 的 `Bitmap()` **不含** quiet zone，SVG 要自己补 4 模块」。
照这句话实现，图**照样能扫** —— 多一圈白边不会让扫码器读不出来，只会让二维码在版面上白边更宽、
显得更小。这种错最难发现，因为它产出一个**合法、可解码**的结果。

库的源码注释其实写着 *"The bitmap includes the required quiet zone"*（`qrcode.go:264`），
只是计划里没写。真正的判据是两条**结构断言**：

```go
side := len(bitmap)   // 37 = 29（版本 3 的模块数）+ 两侧各 4 —— 静默区已经在里面了
// 断言 1：viewBox 边长就等于 Bitmap() 的行数，不要再 +8
// 断言 2：定位图案落在第 5 个模块（下标 4）
bitmap[4][4] == true  // ← 若多补一圈，它会落在下标 8，这条立刻红
```

> 一般化的教训：**第三方库的默认行为要看源码或实测，不要照「印象 / 常识」写进计划**。
> 「多补一圈静默区」与「少补一圈静默区」在肉眼与「能否解码」上都无法区分，
> 只有「定位图案在第几个模块」这种断言分得清 4 与 8。

### 8. 测试断言里不要夹带「调度不会慢」这种假设

`TestOpCtxTimesOutWithCause` 原本这么写：

```go
db := &DB{timeout: time.Millisecond}
ctx, cancel := db.opCtx(context.Background())
// ...
if remaining := time.Until(deadline); remaining <= 0 || remaining > time.Second {
```

`timeout` 只有 1ms，于是 `remaining > 0` 实际测的是「从 `opCtx()` 返回到读 `deadline`
之间，调度不会超过 1ms」—— 一个关于**机器**的假设。4 核 runner 上 `-race` 全量并行跑时
会真的踩中：CI 报 `-321.445µs`，本机把 16 核压满后 2000 次里红 7 次、最差 `-6ms`。

改法是把「此刻还剩多少」换成「整段预算是多少」，且下界必须是**硬不变量**：

```go
start := time.Now()
ctx, cancel := db.opCtx(t.Context())
if budget := deadline.Sub(start); budget < opTimeout || budget > time.Second {
```

`budget = deadline - start`，而 `opCtx` 内部那次取时必然不早于 `start`（单调钟不回退），
所以下界是真的不变量、与调度无关；上界则继续拦住「没按 `db.timeout` 设置」这类真回归
（写死 3s → `预算 = 3s` 红；写死 1ms → `预算 = 1ms` 红）。

> 一般化的教训：**断言要盯住被测对象的不变量，不要顺带断言运行环境。**
> 这类用例的症状是「本机怎么跑都绿、CI 偶发红」，而它吐出的失败信息（一个负的微秒数）
> 看起来像被测代码有 bug，很容易误判成真故障去改错地方。
> 复现手法：**把机器压满**再 `-count` 跑几百次 —— 空闲的 16 核上跑 300 次一次都不会红，
> 所以「跑过了」不能证明它不抖。

## 已知限制

MVP 有意不做的部分：

| 不做 | 原因 |
| --- | --- |
| 自动抓取目标页标题 | 会引入 SSRF 风险，标题由用户手填 |
| A/B 分流 / 短链轮换 | 需要 `link_targets` 表与「目标页归属」的新语义，收益不明（二维码已两路提供：详情页前端 `qrcode` 画 canvas，后端 `GET /api/links/{code}/qr.svg` 给外部引用） |
| 口令的重置流程 / 提示语 | 没有邮箱找回，也没有 `hint`：口令只由所有者设置与清除（忘了就重新设一条） |
| 团队 / 多租户 / 权限体系 | 只有「匿名」与「个人账号」两种身份 |
| Prometheus / Grafana 全套 | 只做零依赖的 `/metrics` **文本端点**（不引客户端库、不带 Grafana），且默认**不对外**（nginx 里 `= /metrics` → 404）。详见「`/metrics`」一节 |
| 自定义域名的**管理接口 / UI** | 数据模型与解析路径已就绪（000006 的 `domains` 表 + `links.domain_id`，`Host` 归一化后按域定位，缓存键按域分开），但「登记一个域名」目前只能由运维写库、再在 nginx 加一个 `server_name` + 证书。做管理端要先回答「谁来验证域名归属」（DNS TXT / 文件校验），不是表结构问题 |

欢迎提 Issue 讨论优先级。

## 验收记录

以下都是实测结果，不是设计意图。基线快照：**2026-09-18**（⑫ 起为 2026-09-19 / ⑲ 起为 2026-09-20 的增量），Windows 本机 + Docker Desktop
（Go 1.27.1 / Node 24.19.0 / pnpm 11.15.1）。

「在哪跑过」一列区分**本机实测**与 **CI 实测**——两者会得出同一结论，但覆盖面不同：
CI 每次都跑，本机记录的是基线快照与 CI 里不好做的项（比如停掉 PG、重启 backend 看关停顺序）。

| 项 | 结果 | 在哪跑过 |
| --- | --- | --- |
| `gofmt -l .`（backend） | 无输出 | 本机 + CI `backend` |
| `go vet ./...` | 通过 | 本机 + CI `backend` |
| `go test ./...` | 通过（config / domain / httpx / base62 / ipmask / shortcode / ua / validator / service / store.postgres / worker） | 本机 |
| `go test -race ./...` | 通过。⚠️ 本机需 `CGO_LDFLAGS=-static`：mingw-w64 8.1.0 的运行时 DLL 与 Go 1.27 的 race runtime 不匹配，裸跑会得到 `exit status 0xc0000139`（环境问题，不是代码问题） | 本机（带 `-static`）+ CI `backend`（ubuntu 原生） |
| `pnpm typecheck` / `pnpm lint` / `pnpm build` | 通过（lint 0 error 0 warning） | 本机 + CI `frontend` |
| `docker compose up -d --build` | 5 个容器全部 healthy（PG / Redis / backend / worker / frontend） | 本机 + CI `smoke` |
| **容器级 ①**：`docker compose stop postgres` + 删短码缓存后跳转 | **503 + `Retry-After: 2`**，体为 `{"error":{"code":"unavailable"}}`（不是 500；缓存 `DEL` 返回 1，确认真的回源） | 本机 |
| **容器级 ②**：`docker compose restart backend` 的关停顺序 | 日志顺序为 `收到退出信号 / 开始优雅关闭` → `统计写入队列已排空` → `api 已退出`；`dropped_clicks` 0 → 0；关停前 12 次跳转全部进流（`stream_len` 1 → 13） | 本机 |
| **容器级 ③**：`go run ./cmd/smoke -base http://localhost:8080 -expect-spa` | M0 时 **24 / 24 通过**（含「SPA 顶级路由经 nginx 返回 HTML」）；M5-1 加了 3 条口令用例后是 **27 / 27**；N8 又加了 1 条二维码用例（并给删除那一步补上「二维码也 404」）后是 **28 / 28**。⚠️ 不带 `-expect-spa` 时是 27 —— 那条 SPA 断言只在走 nginx 时才有意义，直连后端顶级路由本来就该 404 | 本机 + CI `smoke` |
| **容器级 ④**：明细幂等去重（M2-1） | 5 次跳转后 `count(event_uid) = count(distinct event_uid) = 5`（迁移前的 19 行历史数据为 NULL）；用**显式 Stream ID** 重投一条「已经插过」的消息 → 明细 7 → 7、该 `event_uid` 行数 1 → 1，worker 无 ERROR/WARN 且消息被 ACK | 本机 |
| 迁移往返（M2-1） | `migrate down 1` + `up` 连续两轮无报错；`version` = 2；列与部分唯一索引恢复，之后的新跳转仍写入 `event_uid` | 本机 |
| **容器级 ⑤**：列表口径 = 基线 + 待同步增量（M2-2） | 停掉 worker 后跳转 4 次：PG 基线仍 `0`、Redis 增量 `4`，而 `GET /api/links` 的 `click_count` = **4**，与详情 `total_clicks` 相等；恢复 worker 后基线刷成 `4`、增量键清空、列表仍为 `4`；把 Redis 停掉时列表仍 **200**（退回纯基线，不 5xx） | 本机 |
| **容器级 ⑥**：补偿式计数（M3-1） | 停 worker 后跳转 10 次：`clicks:cnt:{code}` = `10`、`clicks:dirty` 含该码、PG 基线 `0`（增量没被「取走」）；启动 worker 后基线 `10`、明细 `10`，**计数键被删除**（不是留一个 0）且 dirty 清空；再压 100 次跳转 → 基线/明细都是 `100`；全库 `base_count <> event_count` 的链接数 = 0，`dropped_clicks` / `failed_clicks` 均为 0 | 本机 |
| **容器级 ⑦**：缓存击穿防护（M3-2） | 用一个刚创建（缓存已被主动失效）的冷短码，`curl --parallel-immediate` 同时打 **20** 个请求 → 20 个 **302**；`/healthz` 的 `pg_fallbacks` 增量 = **1**（而不是 20）。单测侧：50 个 goroutine 并发 miss + 回源处设屏障，断言仓储只被调用 1 次 | 本机 |
| **容器级 ⑧**：备份与恢复（M3-3） | `--profile ops` 起 backup → 产出 `ashen-20260918-154740.dump`；`pg_restore` 到临时库 `restore_check` 后与主库逐项一致（`links` 14、`click_events` 169、`sum(base_count)` = `sum(event_count)` = 169）；删临时库后 `pg_database` 里不再有它。保留策略实测：造一个 2000 年的假备份 → 清理后旧文件被删、当天的留下 | 本机 |
| **容器级 ⑨**：标签（M4-1） | 创建时传 `["Ops","  ops  ","Dev"]` → 返回 `["ops","dev"]`（归一化 + 去重）；`?tag=ops` 只命中该条，`?tag=DEV`（大写）也能命中（按小写比较）；11 个标签 / 33 字符标签都返回 422 `invalid_tags`；`EXPLAIN` 下 `tags @> ARRAY['ops']` 走 **`links_tags_gin`**（Bitmap Index Scan）。浏览器侧：无头 Chrome 在 `/dashboard` 输入 `ops` 后列表从 2 条变 1 条 | 本机 |
| **容器级 ⑩**：点击明细页（M4-2） | 跳转 3 次（手机 / 桌面 / 爬虫 UA）→ `?limit=2` 拿到 2 行 + 游标，带游标翻到第 2 页拿到剩下的 1 行、`next_cursor` 为空；时间倒序且两页无重叠无缺口（26 行 = 首屏 20 + 「加载更多」6，逐行核对无重复）。IP 掩码：库里 `host(ip)` = `172.20.0.1`，响应里是 `172.20.0.0/24`，且响应体里搜不到原始地址。`device=mobile` 命中 1 条、`device=unknown` 命中 0 条（与设备分布口径一致）；`limit=0` / `days=abc` / `device=tv` / 坏游标都返回 422 且 `field` 正确；无凭据 404。`EXPLAIN` 下 `(occurred_at, id) < (…)` 被下推进 **`click_events_link_time_id_idx`** 的 Index Cond，且 Index Only Scan **不带 Sort 节点**（索引本身给出倒序）。浏览器侧：无头 Chrome 打开 `/links/{code}`，首屏 20 行 + 「加载更多」，点一下变 26 行、按钮换成「已经到底了」，两页拼接处无重复行 | 本机 |
| **容器级 ⑪**：二维码（M4-3） | 详情页把 `short_url` 画进 canvas（前端 `qrcode` 生成，无后端接口）。用 **jsQR 真的去扫**：页面 canvas 取回的 PNG 解码 = `http://localhost:8080/{code}`，与 `short_url` 逐字相等；点「下载二维码」落盘的 `ashencourier-{code}.png` 是 **1024×1024**，解码结果同样相等。配色为深墨 `#141413` + 暖奶油 `#faf9f5`（≈19:1，不用珊瑚色当前景），下载件用纯白底 | 本机 |
| **容器级 ⑪ 补**：二维码版式 | 初版画布撑破容器（`qrcode` 写的行内 `320px` 盖过 Tailwind 的 `h-full w-full`，见「八条踩过的坑」第 6 条）。修正后实测：容器 **160×160** @ (158.5, 366.9)、画布 **142×142** @ (167.5, 375.9)（正好等于容器减 padding 与 1px 边框）、右边缘 309.5 < 文字列 327.5（不压字）、位图仍是 **320px**、inline style 已清空；采样像素同时含 `#141413` 与 `#faf9f5`。解码两处仍全对 | 本机 |
| `docker compose down && docker compose up -d` | 数据仍在（volume 持久化：`links` 8 → 8），`/healthz` 立即 200 | 本机 |
| **集成测试 ⑫**：store 层迁移与 SQL（N1 / 自动化缺口 16.3-1） | 带 `POSTGRES_TEST_DSN` 时 20 个用例全绿（迁移形状与索引清单 / links 往返 / Update 的三种语义 / 与权威 SQL 逐项比对的 keyset 两处 / `tags @> ARRAY[...]` 走 `links_tags_gin` / `event_uid` 幂等 / 聚合的 UTC 日界 / 计数累加 / 过期扫描）；不带 DSN 时 9 个集成用例全部 SKIP、整包仍绿。**变异验证**：删掉 `ON CONFLICT ... WHERE event_uid IS NOT NULL` → 报 `42P10 no unique or exclusion constraint matching`；把 keyset 的 `(occurred_at, id) <` 退化成 `occurred_at <` → 报 `got=[12 11 10 9 8 6 5 4 3 2] want=[12 11 10 9 8 7 6 5 4 3 2 1]`（并列时间上漏掉第 7 与第 1 条）。第一次跑还发现 `links.created_ip` 读出来带 `/32` 掩码长度，已与 `click_events.ip` 一样改用 `host()` | 本机（PG 18.6 容器）+ CI `backend` |
| **容器级 ⑬**：短链访问口令（M5-1 / N2） | 建带口令的短链 → `password_protected=True`；库里 `password_hash` 是 `$2a$12$…`（60 字符，且 `= 'smoke-pass-9f3a'` 为 `f`）。未解锁 `GET /{code}` = **200 + text/html** 且 `total_clicks` 仍 0；错误口令 = **401** 且 `total_clicks` 仍 0；正确口令 = **303 + Set-Cookie**（`HttpOnly` / `SameSite=Lax` / `Path=/`，http 下不带 `Secure`）；带 cookie 的 GET = **302**，`total_clicks` = **1**、`click_events` = **1**（解锁那次没被重复计）；`clear_password` 后立刻 302（缓存被主动失效）。迁移 000005 往返两轮：`down 1` 后列消失、`up` 后回来，无报错且之后新跳转仍 302。冒烟 **27 / 27**（三条口令用例逐条 ✓）。浏览器侧（无头 Chrome + CDP）：创建表单展开高级选项后有「访问口令」；详情页显示「受口令保护」徽章，编辑面板有「访问口令」输入与「清除口令」按钮；短链未解锁渲染口令页、输错显示「口令不对，请再试一次。」、输对**真的落到目标地址** | 本机（Docker + 无头 Chrome） |
| **前端单测 ⑭**：纯函数（16.3-3） | `vitest run` **31 个用例全绿**（`format.ts` 24 个 / `tags.ts` 7 个），约 0.8s；同时把 `splitTags` 从两个组件里提到 `src/utils/tags.ts`（原来是一模一样的两份）。**变异验证**：把 `truncateMiddle` 的 `head + tail + 1` 退化成 `head + tail` → 边界用例红；把 `splitTags` 的 `length > 0` 改成 `length > 1` → 第一次**没被抓住**（用例里没有单字符标签），补上「`书` 这种单字符标签不能丢」后变红。时间断言用**不带时区后缀**的输入串，因此本机（Asia/Shanghai）与 CI（UTC）结果一致 | 本机 + CI `frontend` |
| **容器级 ⑮**：浏览器级验收（16.3-2） | 无头 Chrome + CDP（**零 npm 依赖**，只用 Node 内置 fetch / WebSocket），**19 项全绿**：二维码 6 条（行内尺寸已清空 / 位图 320×320 / 画布真的画过：深墨 4.5 万 px + 暖奶油 5.7 万 px / 画布在容器内 / 显示宽 = 容器宽 − padding → `142.0 = 158 − 8 − 8` / 右边缘 309.5 < 文字列 342.5）、明细 6 条（接口只回 `/24` 网段、首屏 20 行、时间到秒、逐行与接口核对、页面文本无原始 IP、翻页 `20 + 6 = 26` 行且无重复）、口令 6 条（只回布尔不回摘要、未解锁不给 cookie、错口令留在口令页、对口令 303→落到目标 `/login`、`ac_unlock` 是 HttpOnly + `Path=/`、解锁恰好只多一条明细）、全程零 console 错误。**变异验证**：去掉 `canvas.style.width = ''` → **4 条版式断言全红而像素断言仍全绿**（见坑第 6 条）。顺序约束也实测过：**e2e 19/19 之后紧接 smoke 27/27**（两者都不会把对方的创建配额打满） | 本机（Docker + 无头 Chrome）+ CI `smoke` |
| **单测/集成 ⑯**：GeoIP 解析（M5-2） | `internal/store/geoip` 与 `internal/worker` 的用例全绿：非法输入（空串 / 非 IP / 带端口 / 网段 / 主机名 / 坏 IPv6）一律空串、零值 Locator 与 nil 都安全、打开不存在的文件与非 mmdb 文件都报错；**带真库**（`GEOIP_TEST_DB` = 本机 GeoLite2-Country）时 `81.2.69.142` 解析出两位大写国家码、私网 `10.11.12.13` 为空、`::ffff:81.2.69.142` 与原生 IPv4 结果一致（`Unmap` 生效）。worker 侧断言国家码取自 `GeoLocator` 且**用原始 IP** 去查（不是掩码后的），未配置时留空不 panic。集成测试 `TestAggregateCountriesExcludesEmpty` 覆盖国家聚合 SQL：按点击数降序、**不含 `unknown` 桶**、无已知国家时是空切片（而非 nil）。**变异验证**：把国家 SQL 退回 `coalesce(nullif(country,''),'unknown')` → 结果多出 `{unknown 1}`，用例变红。降级日志也断言了级别：留空是 `INFO` 且不含 `WARN`，路径打不开是**恰好一条** `WARN` | 本机（真 PG 18.6 + 真 mmdb）|
| **容器级 ⑰**：GeoIP 端到端与降级（M5-2） | worker 启动日志 `GeoIP 库文件已加载 /geoip/GeoLite2-Country.mmdb`。往 Stream 投两条**显式 ID + 公网 IP** 的点击（本机 curl 的客户端 IP 是 Docker 网关 `172.20.0.1`，私网段解析不出国家，所以必须直接投递）：`8.8.8.8` → 库里 `country=US`、`114.114.114.114` → `CN`；`GET /api/links/{code}/stats` 回 `countries=[{CN,1},{US,1}]`。降级实测两轮：`GEOIP_DB_PATH=/geoip/does-not-exist.mmdb` → worker 日志**恰好一条 WARN**、`8.8.8.8` 仍落库且 `country` 为 NULL、`/healthz` 200 `ok`；`GEOIP_DB_PATH` 留空 → 一条 INFO、零 WARN、行为相同 | 本机（Docker + 真 mmdb）|
| **容器级 ⑱**：自定义域名分域解析（N6-1） | 跑完迁移 000006 后库内登记 `a.local`（此时 `domains` + `links.domain_id` 就位）→ 创建带 `domain=a.local` 的短链，`short_url` = `http://a.local/{code}`。`curl -H 'Host: a.local'` 得 **302**，而同一短码在默认 Host 上是 **404**；反向（默认域名的短链拿到 `a.local` 上）同样是 **404**；`Host: A.LOCAL:8080`（大写 + 端口）也能命中（归一化生效）。真 Redis 里键确实按域分开：`link:v2:{域 UUID}:{code}` 与 `link:v2:-:{code}`，跨域那条**没有**落在默认域前缀下。共 **24 / 24**。**变异验证**：把缓存键改回不分域 + `sameDomain` 改成恒 true → **8 / 24 红**，失败的正是要害 —— 「紧接着在 `a.local` 上访问该短码」变成 404（跨域探测写下的负缓存把正确域的访问挡死）、反向那条变成 302（串味，访问者被送到另一个域的目标）；还原后回到 24 / 24 | 本机 |
| **容器级 ⑲**：后端二维码 SVG 端点（N8 / M4-3 方案 B） | `GET /api/links/{code}/qr.svg` 返回 `image/svg+xml`（3243 字节），带 `Cache-Control: public, max-age=300` 与 `nosniff`，响应体里搜不到短码与 `short_url`（**没有任何用户可控字节**）。**真扫两轮尺寸**（无头 Chrome 光栅化 → jsQR）：512px 与 128px 解码都 = `http://localhost:8080/{code}`，与 `short_url` 逐字相等；两种情况都有深墨前景 `#141413` + 纯白底，四条边采样全白（静默区），定位图案落在**第 5 个模块**（证明静默区恰好 4，不是 8）。404 语义三种都验过：不存在的短码 / 保留字 / **已软删除**（删完再取图 → 404）。共 **19 / 19**。**变异验证**：`symbol.DisableBorder = true`（去掉静默区）→ 静默区断言红；子路径写成 `h-%d`（方向反）→ 闭合/模块数断言红。冒烟侧固化了两条（公开可读 + SVG 形状 + 不含用户可控字节；删除后 **404**），使 `cmd/smoke` 从 27 项变 **28 项** | 本机（Docker + 无头 Chrome） |
| **容器级 ⑳**：`/metrics` 文本端点（N9） | **经 nginx 访问 `localhost:8080/metrics` → 404**（响应体 146 字节是 nginx 的 404 页、搜不到 `ashen_`、Content-Type 是 `text/html`）—— 这才是不对外的证明；同一路径从网络内部（借前端容器的 busybox wget 打 `backend:8080`）→ **200 + `text/plain; version=0.0.4; charset=utf-8` + `nosniff`**，**17 条指标**（每条都有 `# HELP` 与 `# TYPE`），零值计数器 `ashen_dropped_clicks_total 0` 在场，`ashen_build_info{version="dev"} 1`。`custom_code=metrics` → 422 `invalid_custom_code`（不是 409、不是静默成功）。**停掉 PG**：`/healthz` 503 而 `/metrics` **仍 200**，`ashen_postgres_up 0`、`ashen_up 0`、**`ashen_redis_up 仍为 1`**（单探针可定位 —— 这一条同时是这个 bug 的回归守卫，修前实测它是 0）。共 **24 / 24**，恢复 PG 后 `/healthz` 回到 200；`cmd/smoke` 仍 **28 / 28**。**变异验证**：删掉保留字表的 `metrics` → `TestReservedSetContents` 报 `保留字表缺少 "metrics"`；把 `ashen_up` 写死成 1 → `TestRenderMetricsDegraded` 报 `ashen_up = 1，期望 0` | 本机（Docker + busybox wget） |
| 计数一致性 | `link_click_totals` 中 `base_count <> event_count` 的链接数 = 0；`clicks:dirty` 与 `clicks:cnt:*` 回刷后清空 | 本机 |
| Stream 消费 | `/healthz` 不含 `stream_pending`（零值 ⇒ 0 pending）；worker 日志无 `"msg":"http"` 记录（确认跑的是 worker 而非 api） | 本机 |

**自定义域名的本机验收**不需要真域名与证书：`curl -H 'Host: a.local' localhost:8080/{code}` 就能走到后端，因为 nginx 的短码规则是按**路径**匹配的，`Host` 只影响后端把请求算到哪个域上。域名记录目前只能用一条
`INSERT INTO domains (name) VALUES ('a.local')` 登记（没有管理接口，见「已知限制」）。真上线时除了登记域名，还要在 `deploy/nginx/nginx.conf` 里为该域名加 `server_name` 与证书 —— 那是配置工作，代码侧不用改。

**责任划分**：容器级验收（起全栈 + 浏览器级验收 + 端到端冒烟）由 CI 的 `smoke` job 承担 ——
每次推 `main` 与手动触发（`workflow_dispatch`）都会真跑一遍，失败时自动 dump 容器日志。
PR 只跑 `backend` 与 `frontend` 两个快 job（约 1 分钟），因为 `docker compose up -d --build`
要几分钟。

`frontend/e2e/` 这一步排在 `cmd/smoke` **之前**，不是随意的顺序：创建接口是
10 次/分钟/IP 的硬配额（写死在 `config`，没有对应的环境变量），而 `cmd/smoke` 的最后一项
检查会故意把这配额打满来断言 429。本套件只花掉 1 个配额，且自身耗时约一分钟
（26 次跳转 + 等明细落库），所以 smoke 开始时限流窗口已经滚过 —— 顺序一旦颠倒，
浏览器验收第一步就会拿到 429。

最近的实测：[run 35485587539](https://github.com/Elari39/AshenCourier/actions/runs/35485587539)
三个 job 全绿（`backend` 57s / `frontend` 37s / `smoke` 2m6s）。三个关键证据：`backend` 里
`internal/store/postgres` 跑了 **1.364s**（未设置 `POSTGRES_TEST_DSN` 时集成测试会整体跳过，
那时只有零点几秒 —— 所以它是真的连上了 service 容器里的 PG）；`smoke` 里 **`frontend/e2e/` 19/19
之后紧接 `cmd/smoke` 28/28**（在 runner 自带的 Chrome 上真跑，不靠本机的 `CHROME_BIN` 探测），
按 step 分组计数 19 + 28 = 47，与日志里的 ✓ 行数相等；`frontend` 的 vitest **31 个用例**全绿。

（前两次分别是 [run 35484563846](https://github.com/Elari39/AshenCourier/actions/runs/35484563846)
—— N9 `/metrics` 落地的验证；[run 35483953065](https://github.com/Elari39/AshenCourier/actions/runs/35483953065)
—— N8 二维码端点的验证。这三次都是三个 job 全绿。）

**CI 自身也实测过「会红」**（不是只看过绿灯）：其中两次是故意造出来的 —— 故意破坏一个文件的
gofmt → `backend` job 红并列出文件名；故意改错冒烟工具的期望值 → `smoke` job 红、日志里能看到
断言失败与容器日志。**还有一次是自然发生的**：N10 那个纯文档提交把
`TestOpCtxTimesOutWithCause` 的调度依赖断言撞红了（run `35484924568`），于是多了两个教训 ——
「红灯未必由本次改动引起，但必须先查清楚再重跑」以及「本机空转跑不出来的抖动，要把 CPU
压满才复现」，后者写在「八条踩过的坑」第 8 条。
配置见 [`.github/workflows/ci.yml`](./.github/workflows/ci.yml)。

## 许可

[MIT](./LICENSE) © 2026 Elari39

---

<div align="center">
<sub>短码字符集 <code>[0-9A-Za-z_-]</code> · 只允许 http/https 目标地址 · 302 不缓存</sub>
</div>
