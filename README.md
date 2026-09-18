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
- **界面上「总点击」不会卡住。** 数字 = `links.click_count`（PG 基线）+ `clicks:cnt:{code}`
  （Redis 待同步增量），worker 每 2 秒回刷，正常情况下偏差小于 2 秒。
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
- [五条踩过的坑](#五条踩过的坑)
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
衬线大标题）。实施计划与全部技术取舍见 [`PLAN.md`](./PLAN.md)。

## 架构

```
Browser ──┬─ /api/*         ─┐
          ├─ /{code}        ─┤ nginx (frontend 容器 :80)
          └─ 其余（SPA 路由） │   ├─ /assets  → 本地静态资源（优先于短码正则）
                             │   └─ /        → index.html（history fallback）
                             ▼
                        backend:8080 ──┬── PostgreSQL 18（links / users / click_events）
                                       └── Redis 8（缓存 / 计数增量 / Stream / 限流）
                                                      ▲
                                          worker 容器 ─┘（消费 Stream、回刷计数）
```

### 三条关键设计

1. **跳转不落库**：`GET /{code}` 只做 Redis `GET` + `INCR` + `XADD`，全程无 PostgreSQL 写入。
   缓存 miss 才回源一次，并把结果回填。
2. **计数最终一致**：界面上的「总点击」= `links.click_count`（PG 基线）+ `clicks:cnt:{code}`
   （Redis 待同步增量）。worker 每 2 秒把增量刷回 PG，正常情况下偏差 < 2 秒。
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

三张表 + 一个视图，全部在 [`backend/migrations/000001_init.up.sql`](./backend/migrations/000001_init.up.sql)：

| 对象 | 作用 | 关键约束 |
| --- | --- | --- |
| `links` | 短链主体 | `short_code` 全局唯一；`status` 用 `smallint` 而非 PG enum（改状态机不用 `ALTER TYPE`）；`key_hash bytea` 存匿名管理密钥的 SHA-256 |
| `users` | 账号 | `email` 用 `citext`（大小写不敏感唯一） |
| `click_events` | 点击明细 | `ip inet`；`device` / `browser` / `os` 由 worker 解析 UA 后写入 |
| `link_click_totals` | 视图 | `links.click_count + count(click_events)`，用于人工对账 |

## API

统一前缀 `/api`，`application/json; charset=utf-8`。

失败响应统一为：

```json
{ "error": { "code": "invalid_url", "message": "仅支持 http/https 链接", "field": "target_url", "request_id": "01J..." } }
```

| # | Method | Path | 鉴权 | 说明 |
| --- | --- | --- | --- | --- |
| 1 | GET | `/healthz` | — | 存活 + 就绪（PG / Redis / Stream 积压 / 丢弃计数） |
| 2 | POST | `/api/auth/register` | — | 注册，返回 user + token |
| 3 | POST | `/api/auth/login` | — | 登录（限流 20 次 / 10 分钟 / IP） |
| 4 | GET | `/api/auth/me` | JWT | 当前用户 |
| 5 | POST | `/api/links` | 可选 JWT | 创建短链；匿名会返回一次性 `manage_key`（限流 10 次 / 分钟 / IP） |
| 6 | GET | `/api/links` | JWT | 我的链接列表，游标分页 `?limit=20&cursor=&q=` |
| 7 | GET | `/api/links/{code}` | JWT 或 Key | 详情（无权限一律 404，不泄露资源是否存在） |
| 8 | PATCH | `/api/links/{code}` | JWT 或 Key | 改 `target_url` / `title` / `status` / `expires_at`（改后主动失效缓存） |
| 9 | DELETE | `/api/links/{code}` | JWT 或 Key | 软删除（`status=3`）+ 删缓存 |
| 10 | GET | `/api/links/{code}/stats?days=30` | JWT 或 Key | 统计聚合 |
| 11 | POST | `/api/links/{code}/claim` | JWT + Key | 把匿名短链认领到账号下 |
| 12 | GET | `/{code}` | — | **302 跳转**（不在 `/api` 下） |

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
  "daily":        [{ "date": "2026-09-01", "clicks": 12 }],
  "top_referers": [{ "referer": "twitter.com", "clicks": 300 }],
  "devices":      [{ "device": "mobile", "clicks": 800 }],
  "browsers":     [{ "browser": "Chrome", "clicks": 900 }]
}
```

`total_clicks` 是全量口径（PG 基线 + Redis 待同步增量）；
`daily` 与三个分布是窗口口径。`daily` 一定补齐成连续的 `days` 天，缺失日期为 0。

## 目录结构

```
.
├── docker-compose.yml          # 生产形态：pg + redis + migrate + backend + worker + frontend
├── docker-compose.dev.yml      # 本地开发：只起 pg(5432) + redis(6379)；migrate 在 tools profile 下
├── deploy/nginx/nginx.conf     # 反代 + 短码正则 + SPA fallback
├── backend/
│   ├── migrations/             # golang-migrate 迁移（up / down 严格互逆）
│   ├── cmd/{api,worker,smoke}/ # 两个服务入口 + 端到端冒烟工具
│   └── internal/
│       ├── config/             # env → Config（cmp.Or 给默认值 + 启动前强制校验）
│       ├── domain/             # 实体 / 领域错误 / 仓储与端口接口（不 import 任何第三方库）
│       ├── store/postgres/     # pgxpool 仓储实现
│       ├── store/redis/        # 缓存 / 计数 / Stream / 限流
│       ├── service/            # 应用服务（短链、统计、鉴权、管理密钥）
│       ├── handler/            # HTTP 处理器 + 路由装配
│       ├── httpx/              # JSON 读写、统一错误体、中间件、限流中间件、http.Server
│       ├── worker/             # Stream 消费 + 计数回刷 + 过期清理
│       └── pkg/                # base62 / shortcode / ua / validator
└── frontend/
    └── src/
        ├── api/                # fetch 封装 + 类型契约 + 会话存储
        ├── composables/        # useAuth / useToast / useCopy（不引 Pinia）
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
pnpm lint && pnpm build
```

冒烟工具覆盖：健康检查 → **SPA 顶级路由** → 匿名创建 → 302 跳转 → 统计收敛
（同时校验 Redis 计数与落库明细）→ 鉴权边界 → 修改后缓存失效 → 保留字与开放重定向防护
→ 注册/登录 → 认领 → 分页 → 限流。**用 Go 写而不是 shell**：跨平台，且能做真正的 JSON 断言。

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
| `rate_limit_degraded` | 限流器因 Redis 故障降级的累计次数 |
| `rate_limit_native_increx` | 限流走的是 Redis 8.8+ 原生 `INCREX` 还是 Lua 回落实现 |

## 五条踩过的坑

这五条都是「设计稿上看不出来、只有真跑容器才暴露」的，写在这里省得别人再踩一遍。

### 1. 新增前端顶级路由，必须同步四处

单域名下短码与 SPA 路由共用路径空间。新增顶级路由（例如 `/settings`）时要改**四个地方**：

1. `deploy/nginx/nginx.conf` 加一行 `location = /settings { try_files $uri /index.html; }`
2. `backend/internal/pkg/shortcode/reserved.go` 的 `reserved` 集合
3. `backend/internal/pkg/shortcode/shortcode_test.go` 里 `TestReservedSetContents` 的断言
4. `frontend/vite.config.ts` 的 `SPA_ROUTES`（dev proxy bypass）

漏掉第 1 步 → 生产环境刷新 `/settings` 会 404；
漏掉第 2 步 → 别人可以注册 `settings` 当短码，把真实页面吃掉；
漏掉第 4 步 → 开发环境刷新该页面会 404。

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

worker 在「处理完但 ACK 前」重启时，同一批明细可能重复插入。
**计数走 Redis `INCR` 累加，不受重复投递影响**；只有明细条数可能多算。
若要精确对账，可以加 `event_uid`（由 Stream 消息 ID 派生）唯一索引做幂等去重。

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

## 已知限制

MVP 有意不做的部分：

| 不做 | 原因 |
| --- | --- |
| 自动抓取目标页标题 | 会引入 SSRF 风险，标题由用户手填 |
| GeoIP / 国家维度统计 | 需要 mmdb 库；`click_events.country` 字段已预留，恒为 NULL |
| 短链密码、二维码、A/B 分流 | P1 扩展点，数据模型已预留 |
| 团队 / 多租户 / 权限体系 | 只有「匿名」与「个人账号」两种身份 |
| Prometheus / Grafana | 只暴露 `/healthz` + JSON 结构化日志 + 关键计数 |
| 顶点域名分离（`link.xxx`） | 单域名用保留字黑名单隔离；分域时只需改 nginx |

欢迎提 Issue 讨论优先级。

## 验收记录

以下都是本机实测结果，不是设计意图：

| 项 | 结果 |
| --- | --- |
| `gofmt -l .` | 无输出 |
| `go vet ./...` | 通过 |
| `go test ./...` | 通过（base62 / shortcode / ua / validator） |
| `go run ./cmd/smoke -base http://localhost:8080 -expect-spa` | **24 / 24 通过** |
| `pnpm typecheck` / `pnpm lint` / `pnpm build` | 通过（lint 0 error 0 warning） |
| `docker compose up -d --build` | 5 个容器全部 healthy（PG / Redis / backend / worker / frontend） |
| `docker compose down && docker compose up -d` | 数据仍在（volume 持久化），`/healthz` 立即 200 |
| 计数一致性 | `links.click_count` 与 `click_events` 条数一致；`clicks:dirty` 与 `clicks:cnt:*` 回刷后清空 |
| Stream 消费 | 落库后 0 pending；worker 日志无 HTTP 记录（确认跑的是 worker 而非 api） |

## 许可

[MIT](./LICENSE) © 2026 Elari39

---

<div align="center">
<sub>短码字符集 <code>[0-9A-Za-z_-]</code> · 只允许 http/https 目标地址 · 302 不缓存</sub>
</div>
