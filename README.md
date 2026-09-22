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
  丢弃数在 `/healthz/details` 里可见 —— 宁可少记一次点击，也不让 302 慢 1 毫秒。
- **界面上「总点击」不会卡住。** 详情页与列表页的数字都是 `links.click_count`（PG 基线）
  + `clicks:cnt:{code}`（Redis 待同步增量）：详情页单键 `GET`，列表页一次 `MGET` 批量取，
  worker 每 2 秒回刷，正常情况下偏差小于 2 秒。统计侧读不到时列表退回纯基线，不报 5xx。
- **匿名也能管理。** 不注册就能建短链，返回一次性管理密钥（数据库只存 SHA-256，
  明文只在创建响应里出现一次）。登录后可以用它把链接**认领**到自己账号下。
- **限流降级而不是熔断。** Redis 挂了就全量放行并累计降级次数，绝不因为限流组件故障把整站打成 5xx。

## 界面

下面这些截图取自 `docker compose up -d --build` 起来的**真实实例**（不是设计稿）：
数据经 API 播种，页面由无头 Chrome 经 CDP 采集 —— 采集脚本用的就是
`frontend/e2e/` 里那套零依赖工具链（Node 内置 `fetch` + `WebSocket` 直连 CDP）。

### 落地页：粘贴即得短链

<img src="docs/screenshots/landing.png" alt="落地页" width="880">

### 创建成功：一次性管理密钥默认打码

匿名创建会返回一次性管理密钥，**默认打码**，需要时手动点「显示」。
早期版本在这里有个真实缺陷：结果卡被复用后密钥会默认明文摊在屏幕上
（原因与修法见「验收记录」B5）。

<img src="docs/screenshots/create-result.png" alt="创建成功的结果卡" width="880">

### 看板：标签筛选、搜索、窄屏换卡片列表

<img src="docs/screenshots/dashboard.png" alt="链接看板" width="880">

### 链接详情：二维码、按天趋势、来源 / 设备 / 浏览器分布

IP 只显示到网段（IPv4 `/24`、IPv6 `/64`），原始地址不出库。
趋势图是真实数据 —— 采集时所有点击都发生在当天，所以折线是单点跃升，不是示意曲线。

<img src="docs/screenshots/link-detail.png" alt="链接详情与点击明细" width="760">

### 确认框：自绘 `alertdialog`，不是 `window.confirm`

焦点陷阱、`aria-modal`、Esc 关闭、标题与正文都通过 `aria-labelledby` / `aria-describedby` 关联。

<img src="docs/screenshots/confirm-dialog.png" alt="删除确认框" width="820">

### 移动端 390px

<table>
<tr>
<td width="50%"><img src="docs/screenshots/mobile-dashboard.png" alt="移动端看板" width="260"></td>
<td width="50%"><img src="docs/screenshots/mobile-menu.png" alt="移动端全屏菜单" width="260"></td>
</tr>
<tr>
<td align="center"><sub>看板：表格换成卡片列表</sub></td>
<td align="center"><sub>菜单：整屏铺满，不再留边</sub></td>
</tr>
</table>

## 目录

- [界面](#界面)
- [快速开始](#快速开始)
- [技术栈](#技术栈)
- [架构](#架构)
- [API](#api)
- [目录结构](#目录结构)
- [本地开发](#本地开发)
- [环境变量](#环境变量)
- [部署与运维排查](#部署与运维排查)
- [九条踩过的坑](#九条踩过的坑)
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
衬线大标题）。

> **关于 `PLAN.md` / `PLAN-NEXT.md`**：这两份是**本地工作笔记**，不随仓库分发（已在 `.gitignore` 里）。
> 代码注释里的「`PLAN.md` §x」「`PLAN-NEXT.md` §y」指的就是它们 —— 每条结论都已经
> 在注释或本文件里写明，不依赖那两份文件也能读懂代码与运维。

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
   回源次数在 `/healthz/details` 的 `pg_fallbacks` 里可见。
2. **计数最终一致**：详情页与列表页的「总点击」都是 `links.click_count`（PG 基线）
   + `clicks:cnt:{code}`（Redis 待同步增量），**两个口径一致** —— 不会出现「详情有数、列表没数」。
   worker 每 2 秒把增量刷回 PG，正常情况下偏差 < 2 秒。
   列表页用一次 `MGET` 批量读本页所有短码（不逐条查，延迟不随页大小线性增长）；
   统计侧读失败只记 warn 并退回纯基线，绝不把列表打成 5xx。
   回刷是**补偿式**的（见下面第 3 条）：增量只读不删、写库成功后才结算，
   进程崩溃最多让基线重复累加一批，不会丢计数。
3. **统计不阻塞跳转**：统计写入走**有界队列**（默认 4096），队列满直接丢弃并计数，
   丢弃数在 `/healthz/details` 的 `dropped_clicks` 里可见。

### 降级行为

| 故障 | 行为 |
| --- | --- |
| Redis 读缓存失败 | 当作未命中处理，回源 PostgreSQL；跳转仍可用 |
| Redis 写统计失败 | 记 warn 日志并丢弃该次统计，**不影响 302** |
| Redis 限流不可用 | 全量放行并累计降级次数（`/healthz/details` 的 `rate_limit_degraded`），不熔断自锁 |
| PostgreSQL 不可用 | 返回 **503 + `Retry-After`**。「不可用」不只指「连不上」：服务端报的**连接类**（`08xxx`）与**资源类**（`53xxx`，含 `53300` 连接数打满）`SQLSTATE`、以及 `57P01` / `57P02` / `57P03`（服务端正在关停 / 启动中）同样归为依赖不可用 —— 详见「统一错误体」表里的 `unavailable` |
| 某条点击消息永远写不进库（外键冲突等永久性失败） | 重投 5 次后 ACK 丢弃并记 WARN（worker 打点的 `dead_lettered`），不再无限重投 —— 否则它会每 30 秒被捞回来一次，刷屏日志并卡住同批的正常消息 |

### 数据模型

四张表 + 一个视图，初始结构在 [`backend/migrations/000001_init.up.sql`](./backend/migrations/000001_init.up.sql)，
后续迁移按编号递增（见 [`backend/migrations/`](./backend/migrations)）：

| 对象 | 作用 | 关键约束 |
| --- | --- | --- |
| `links` | 短链主体 | `short_code` **全局唯一**（短码生成与所有管理端接口都按它定位；**这是一条拍板结论**——PLAN-NEXT §18.5 决定不做「同码跨域共存」，不是遗留项）；`domain_id uuid`（000006 起，可空）指向所属自定义域名，`NULL` = 默认域名（`PUBLIC_BASE_URL` 指向的那个）；`status` 用 `smallint` 而非 PG enum（改状态机不用 `ALTER TYPE`）；`key_hash bytea` 存匿名管理密钥的 SHA-256；`tags text[]`（000003 起）配 GIN 索引做标签筛选 |
| `domains` | 自定义域名（000006 起） | `domain` 唯一且**存归一化后的小写、无端口、无尾点**（`A.LOCAL:8080` 与 `a.local.` 是同一个域）；`is_active` 可关停而不删行（保留历史短链的归属）。**没有管理接口**：目前只能由运维写库登记，见「已知限制」 |
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

**错误码**

| code | 状态 | 含义 / 客户端该怎么做 |
| --- | --- | --- |
| `invalid_<字段>` | 422 | 字段级校验失败，带 `field`（`target_url` 用更短的 `invalid_url`）。前端据此把提示挂到对应输入框 |
| `invalid_json` | 400 | 请求体不是合法 JSON，或含未知字段 / 类型不匹配 / 重复键 / 非法 UTF-8 |
| `body_too_large` | 413 | 请求体超过 64 KiB |
| `unauthorized` | 401 | 未认证、或所持令牌失效 → **清空本地会话并送回登录页** |
| `invalid_credentials` | 401 | 登录的邮箱或密码不对 → **只提示改输入，不要登出**。它与 `unauthorized` 是**分开的两个 code**，判定靠 code 而不是文案 |
| `forbidden` | 403 | 无权限操作该资源（改 / 删走 403，查询类走 404 —— 分工见各 handler 注释） |
| `not_found` | 404 | 资源不存在，或 `/api` 下该路径未注册 |
| `method_not_allowed` | 405 | `/api` 下路径存在但方法不对，响应带 `Allow` |
| `conflict` | 409 | 唯一约束冲突（邮箱已注册、短码被占） |
| `gone` | 410 | 短链已失效 |
| `unavailable` | 503 | 依赖（PG / Redis）不可用，带 `Retry-After`，可重试。**PG 侧不是一个「连不上」的开关，而是一次 SQLSTATE 分类**：拿不到 SQLSTATE 的失败（拨号失败 / 连接被断 / 池已关闭 / 超时）算不可用；拿到 SQLSTATE 的，只有**连接类（`08xxx`）、资源类（`53xxx`，含 `53300`）与 `57P01`/`57P02`/`57P03`** 算不可用。业务类（`23505` 唯一冲突、`42601` 语法错、`40001` 序列化失败……）仍是 4xx/500 —— 库是健康的，重试无用，回 503 会让上游连累同实例上无关的请求一起退避 |
| `internal` | 500 | 服务端内部错误（并打 error 日志，可用 `request_id` 定位） |

> **这条契约覆盖整个 `/api` 命名空间**，包括两条由 `net/http` 内建产生的错误：
> 未注册路径的 404（`404 page not found`）与方法不对的 405（`Method Not Allowed`）。
> 它们默认都是 `text/plain`，会绕过统一错误体 —— 由 `internal/httpx/APIErrorContract`
> 在**响应侧**改写。**刻意不注册 `/api/` 兜底模式**：不带方法前缀的模式匹配任意方法，
> 会把 405 降级成 404，而 405 携带的 `Allow` 头正是客户端唯一能知道「该用哪个方法」
> 的地方（`router_test.go` 的 `TestRouterMethodAwareness` 盯着这一点）。
> `/api` 之外的路径保持 `net/http` 原样：那里的 404 / 405 面向浏览器，
> 而短码失效页是本项目的 HTML 页面。

| # | Method | Path | 鉴权 | 说明 |
| --- | --- | --- | --- | --- |
| 1 | GET | `/healthz` | — | 存活 + 就绪，**只回 `status` / `postgres` / `redis`**（PG 或 Redis 异常 → **503** + `Retry-After`）。它匿名可达，所以内部诊断字段**一律不出现在这里** —— 完整快照见第 17 项 |
| 2 | POST | `/api/auth/register` | — | 注册，返回 user + token |
| 3 | POST | `/api/auth/login` | — | 登录（限流 20 次 / 10 分钟 / IP）。字段级校验与注册**同一套口径**（邮箱格式 / 口令为空 → 422 `invalid_email` / `invalid_password` + `field`）；凭据不对 → 401 `invalid_credentials` +「邮箱或密码不正确」，与「未认证」的 `unauthorized` 分开。「邮箱不存在」与「口令错误」刻意**不可区分**（防账号枚举，且在邮箱不存在时也走一次 bcrypt 抹平时间差） |
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
| 16 | GET | `/metrics` | — | **Prometheus 文本格式**（`text/plain; version=0.0.4`），与 `/healthz/details` 同一次采集。**不对外**（nginx 里 `= /metrics` 直接 404）、**不加鉴权也不限流**（只在内网可达）；探针异常时仍回 **200**，故障由 `ashen_*_up 0` 表达。详见「`/metrics`」一节 |
| 17 | GET | `/healthz/details` | — | **完整诊断快照**（JSON）：版本、存活时长、队列 / Stream 积压、回源次数、丢弃计数、是否内嵌 worker、限流是否降级……**与 `/metrics` 是同一批数字，因此同一套可见性**：nginx 里 `= /healthz/details` 直接 404。探针异常时仍回 **200**（degraded 那一刻的数字最该被交出去，理由同 `/metrics`），故障由响应体里的 `status: "degraded"` 表达 |

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
    ├── scripts/                # check-zero-ref.mjs：零引用守卫（CI 跑，见「验收」㉛）
    └── src/
        ├── api/                # fetch 封装 + 类型契约 + 会话存储
        ├── composables/        # useAuth / useToast / useConfirm / useScrollLock / useMaskedSecret / useCopy（不引 Pinia）
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
[九条踩过的坑](#6-渲染库写的行内尺寸会盖过-tailwind-类) 第 6 条。

| 变量 | 作用 |
| --- | --- |
| `CHROME_BIN` | 指定 Chrome 可执行文件（默认按平台猜常见位置；CI 用 runner 预装的 `/usr/bin/google-chrome`） |
| `CHROME_FLAGS` | 追加启动参数，空格分隔（以 root 运行的容器里需要 `--no-sandbox`） |

冒烟工具覆盖：健康检查 → **SPA 顶级路由** → **缓存头 / 安全响应头** → **`/api` 错误体契约**
（未注册路径的 404 与方法不对的 405 都必须是 JSON）→ 匿名创建 → 302 跳转 → 统计收敛
（同时校验 Redis 计数与落库明细）→ 鉴权边界 → 修改后缓存失效 → 保留字与开放重定向防护
→ 注册/登录（含字段级校验与 `invalid_credentials`）→ 认领 → 分页 → 限流。
**用 Go 写而不是 shell**：跨平台，且能做真正的 JSON 断言。

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
| `POSTGRES_PASSWORD` | ✅ | — | 数据库口令。`.env.example` 里**留空**，`${VAR:?…}` 对空值与未设置一视同仁 → 不填就不让启动 |
| `REDIS_PASSWORD` | ✅ | — | Redis 口令（同上，模板留空） |
| `JWT_SECRET` | ✅ | — | ≥16 字节，拒绝占位值 |
| `DATABASE_URL` | ✅ | — | `postgres://ashen:<pwd>@postgres:5432/ashen?sslmode=disable` |
| `PUBLIC_BASE_URL` | | `http://localhost:8080` | 短链前缀 + CORS 允许来源 |
| `FRONTEND_PORT` | | `8080` | 对外端口 |
| `LOG_LEVEL` | | `info` | `debug` / `info` / `warn` / `error` |
| `APP_VERSION` | | `dev` | **构建期**注入的镜像版本号（compose 把它作为 build arg `VERSION` 传给 backend / worker）。它出现在 `/healthz/details` 的 `version` 与 `/metrics` 的 `ashen_build_info{version="…"}` 里，是排障时确认「线上跑的是哪一版」的唯一入口。生产部署建议填 `$(git describe --tags --always)`。⚠️ 改了要重新 build，只 restart 无效（值已烤进二进制） |
| `GEOIP_DB_PATH` | | 空 | 国家库在**容器内**的路径（如 `/geoip/dbip-country-lite.mmdb`）。留空或文件打不开都只是让 `country` 留空，见「GeoIP 国家维度」 |

### 后端进程（本地开发需自己 export）

下表是**二进制读取**的环境变量。中间一列写明它到底从哪里来 ——
只有标了「compose」的那些才由 `docker-compose.yml` 的 `x-backend-env` 注入，
其余在容器里取的是**进程默认值**（这一列原先没写清，对照 `docker compose config` 会对不上）。

| 变量 | 默认 | 来自 | 说明 |
| --- | --- | --- | --- |
| `HTTP_ADDR` | `:8080` | compose（仅 backend） | 监听地址。worker 不开 HTTP，不需要它 |
| `REDIS_ADDR` | `localhost:6379` | compose | Redis 地址 |
| `REDIS_DB` | `0` | 进程默认值 | 逻辑库编号 |
| `WORKER_ENABLED` | `false` | compose（仅 backend） | `true` 时 api 进程内嵌同一套 worker 循环（本地开发用） |
| `TRUST_PROXY` | `true` | 进程默认值 | 从 `X-Real-IP` 取客户端 IP；**不**信任 `X-Forwarded-For` |
| `RATE_LIMIT_DISABLED` | `false` | 进程默认值 | `true` 时启动即全量放行（限流应急开关），状态见 `/healthz/details` 的 `rate_limit_disabled` |
| `ALLOW_PRIVATE_TARGETS` | `false` | compose | `true` 时允许短链目标指向内网/回环地址。**内网部署必须打开**，否则创建一律 422「不支持指向内网或本机的地址」。只影响创建与修改，跳转路径不做这个判定 —— 见「已知限制」 |
| `POSTGRES_TEST_DSN` | 空 | 只给测试用 | 进程不读它：store 集成测试的 DSN，未设置时整体跳过（见「store 层集成测试」） |
| `GEOIP_TEST_DB` | 空 | 只给测试用 | 进程不读它：`internal/store/geoip` 里唯一会真查库的用例的库文件路径，未设置时该用例 SKIP |
| `REDIS_TEST_ADDR` | 空 | 只给测试用 | 进程不读它：`internal/store/redis` 集成测试的 Redis 地址（形如 `localhost:6379`），未设置时整体跳过。配套口令用 `REDIS_TEST_PASSWORD`（无口令的本地 Redis 不用填）。那段结算逻辑是一段 Lua，只有跑在真 Redis 上才有意义 |

### 前端（可选）

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `VITE_API_BASE_URL` | `/api` | API 基址（短链前缀不用它配 —— 后端按 `PUBLIC_BASE_URL` 与所属域名拼进 `short_url` 返回） |
| `VITE_SHORT_BASE_URL` | `window.location.origin` | 短链基址。**只在落地页演示区用到** —— 那是用户还没创建任何链接、手上没有后端响应可展示的时刻。留空时回退到当前访问域名，同源部署下就对了；短链域名与控制台域名分离时才需要配。compose 已从 `PUBLIC_BASE_URL` 带过来 |

> 只有 `VITE_` 前缀会打进产物，**绝不要往里面放密钥**。
>
> ⚠️ 两者都是**构建期**替换：改它们必须重新 `docker compose build frontend`。
> 给已构建好的容器加 `environment: VITE_...=...` 是**无效的** —— 值已经烤进 JS 里，
> 运行时产物里没有任何 `import.meta.env` 残留。需要「同一份镜像部署到不同域名」
> 的话得另加一套运行时注入（nginx 启动时写 `config.js` 或 `envsubst` 占位）。

## 部署与运维排查

```bash
# 服务健康（**公开**探针，只回 status/postgres/redis）
curl -s localhost:8080/healthz | jq

# 完整诊断快照（丢弃数、队列积压、Stream 积压、回源次数、限流是否走原生 INCREX）
# ⚠️ 它**不对外**：nginx 里 `= /healthz/details { return 404; }`，所以走 localhost:8080 拿到的是 404。
#    要从本机读得借同一网络内的容器：
docker compose exec frontend wget -qO- http://backend:8080/healthz/details

# 同一批数字的 Prometheus 文本形态。⚠️ 它**不对外**：
# nginx 里 `location = /metrics { return 404; }`，走 localhost:8080 会拿到 404。
# 要从本机读，得借同一网络内的容器（前端镜像是 nginx:alpine，自带 busybox wget）：
docker compose exec frontend wget -qO- http://backend:8080/metrics

# 容器状态（5 个都该是 healthy）
docker compose ps

# 镜像 tag 漂移自检：backend 与 worker 必须来自同一次构建、且容器跑的就是当前 tag。
# 重建镜像后跑一次（改了源码只 build 一个服务是合法命令，但会让另一个继续跑旧二进制）。
node deploy/check-image-drift.mjs

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

# 毒消息：重投次数超限、被丢弃（明细不会入库）的点击消息。这个数不该长期增长
docker compose logs worker | grep '毒消息'

# 最近失败的请求
docker compose logs backend | grep '"level":"ERROR"'
```

> **`docker compose ps` 里出现 `unhealthy`、但容器并没有被重启时，先看是不是「启动期还在等依赖」。**
>
> api / worker 启动时会对 PG、Redis 各做一次探测；探测失败**不再立刻退出**，而是在进程内退避重试
> （起步 250ms 翻倍、封顶 2s，总窗口 30 秒）。这与原先「直接退出，交给 `restart: unless-stopped`
> 一轮轮重启」的区别是：日志从每条依赖失败一条 `ERROR` 降级成 `WARN`，收敛窗口也可控了
> —— 否则编排层自己的退避会一路翻倍到 1 分钟，而每轮都盖住真正的根因。
>
> 代价是**「进程活着但还没开始监听」的窗口最长 30 秒**，所以 api 镜像的 `HEALTHCHECK`
> `start-period` 已相应从 5s 提到 35s：否则每 10 秒一次的探针会在这个窗口里攒够 3 次失败，
> 报出一个假的 `unhealthy`（容器其实正在正常等依赖，`docker compose ps` 却显示不健康）。
>
> 窗口用尽**仍然会退出**并打出带根因的错误 —— 「口令写错 / 端口填错」这类永久性失败
> 不会变成无限等待。实现与设计取舍见 `backend/internal/startup`。

`/healthz` 在公开面上**只回三项**：`status`、`postgres`、`redis` —— 这是编排系统判断
「能不能接流量、是哪个依赖不行」所需的全部。其余全是**内部诊断字段**，只出现在
`/healthz/details`（与 `/metrics` 同策略：nginx 里显式 404）。

> 为什么要拆：这些数字和 `/metrics` 是**同一次采集的同一批数据**。原先 `/healthz` 匿名可读、
> `/metrics` 却被 404 挡着 —— 同一批数字两套可见性，是配置不对称而不是设计取舍。
> 另外它们本身也有价值可被利用：`version` 是版本指纹（便于按已知漏洞定位）、
> `uptime_seconds` 泄露部署节奏（什么时候重启过）、`stream_len` / `pg_fallbacks` 是积压水位
> （可以据此判断「什么时候加压最有效」，也能确认自己的压测是否起了作用）。
> 守卫：`handler/healthz_surface_test.go` 用**键集合白名单**断言公开面恰好三项 ——
> 白名单能挡住**将来**新增的字段（黑名单不能），并且测试用一份「所有诊断字段都非零」的探针，
> 否则 `omitzero` 会让漏字段的断言必然通过（一个永远绿的守卫）。

| 字段（`/healthz/details`，`status` 同时也出现在 `/healthz`） | 含义 |
| --- | --- |
| `status` | `ok` / `degraded`（PG 或 Redis 异常时 degraded，`/healthz` 回 HTTP 503） |
| `dropped_clicks` / `failed_clicks` | 统计因队满 / 写失败而丢弃的次数 |
| `queue_len` | 统计写入队列积压长度 |
| `stream_len` / `stream_pending` | Stream 长度 / 未 ACK 条数 |
| `pg_fallbacks` | 短码缓存未命中、**真正回源 PG** 的累计次数（缓存击穿的观测口径：同一个冷短码被 N 个并发请求打过来时，它只该 +1） |
| `rate_limit_degraded` | 限流器因 Redis 故障降级的累计次数 |
| `rate_limit_native_increx` | 限流走的是 Redis 8.8+ 原生 `INCREX` 还是 Lua 回落实现 |
| `rate_limit_disabled` | 限流应急开关是否被打开（`RATE_LIMIT_DISABLED=true`） |

### CDN 前置部署（Cloudflare）

仓库默认的部署形态是「**nginx 是唯一入口**」：客户端直连 frontend 容器，`$remote_addr`
就是访客地址。**但如果前面还挂了一层 CDN，这个假设就不成立了** —— 直接对端变成 CDN 的边缘节点，
而 `$remote_addr` 也随之变成边缘节点的地址。本节记录这件事，因为它是**部署后才暴露**的那一类问题。

**实测症状**（2026-09-22，在启用了 Cloudflare 的实例上）：

```
GET /api/links/{code}/clicks → 明细里 ip = 172.71.158.0/24
```

`172.71.0.0/16` 落在 Cloudflare 的 `172.64.0.0/13` 内，而访客并非从 Cloudflare 访问 ——
也就是说记进 `click_events.ip` 的是**边缘节点**的地址。三个后果，按严重度排：

| 后果 | 说明 |
| --- | --- |
| **按 IP 的限流配额被全网共享** | 创建 10 次/分/IP、登录与解锁各 20 次/10 分/IP 的键都由客户端 IP 构造，于是**同一边缘节点下的所有访客共用一个令牌桶**：一个人刷满，所有人吃 429 |
| 明细 / 日志 IP 失真 | `click_events.ip`、`links.created_ip`、访问日志里的 `ip` 都是 CDN 节点，排障时看不到真实来源 |
| **GeoIP 国家分布整块错** | worker 用**原始 IP** 查 mmdb，得到的是 CDN 机房所在国，不是访客所在国（未部署 mmdb 时该维度为空，所以这条只在开了 GeoIP 之后才显形） |

**仓库已经做的**：`deploy/nginx/nginx.conf` 里用 real_ip 模块从 `CF-Connecting-IP` 还原访客地址
（15 个 IPv4 段 + 7 个 IPv6 段，取自 <https://www.cloudflare.com/ips/>）。
`internal/httpx/realip_test.go` 会把这段配置钉住 —— 删掉它、或把 `real_ip_header`
改成客户端可伪造的 `X-Forwarded-For`，`go test ./...` 就会红。

**必须自己做的三件运维事**（代码管不到）：

1. **源站只允许 Cloudflare 的地址段访问 80/443。** 后端 `TRUST_PROXY` 默认为 `true`
   且**只认 `X-Real-IP`**；源站一旦能被直连，任何人都可以伪造这个头，从而**绕过全部按 IP 的限流**
   并往点击明细里写任意 IP。只加 `set_real_ip_from` 而不封源站，等于把信任边界从 CDN
   挪给了"任何能连上源站的人"。
2. **CF 的地址段会增删**，部署后请定期核对上表来源并同步 `nginx.conf`。
3. **CF 会改写缓存头** —— 它会把**偏小的** `max-age` 抬高。实测二维码端点（源码
   `backend/internal/handler/qr.go` 写的是 `public, max-age=300`）在客户端收到的是
   `max-age=14400`；对一个**从未存在过**的短码取图，它的 404 响应**也**带同一个值 ——
   而后端的 404 分支（`writeLinkNotFound`）**根本不设** `Cache-Control`，所以这个值不是后端
   产生的，最可能是 CF 的 *Browser Cache TTL*。影响：`qr.go` 里「短链被删 / 域名改名后，
   最多 5 分钟就能换图」的设计意图，在线上实际是 **4 小时**（48 倍）。要按设计意图走，
   就把面板上的 *Browser Cache TTL* 改成 *Respect Existing Headers*。
   > 它**不是**「二维码删不掉」：源站确实回 404（已实测），问题只是**最长 4 小时内的陈旧图片**。
   > 另：`/assets/*` 的 `max-age=31536000` 被**原样保留** —— 这条改写的规则是「抬高偏小的值」，
   > 不是统一覆盖，所以内容哈希资源的长期缓存不受影响。

### 缓存头与安全响应头

两件事都写在 `deploy/nginx/security-headers.conf` 与 `nginx.conf` 里，都在**部署后才会暴露**：
测试环境每轮都是干净浏览器，测不出「老外壳 + 已失效的哈希资源」，也测不出「响应头静默消失」。

**① SPA 外壳必须 `Cache-Control: no-cache`**

没有它时 nginx 只发 `Last-Modified`（实测 `Cache-Control` 与 `ETag` 都缺），浏览器按启发式规则
缓存（约 `(now - Last-Modified) × 10%`）；而 `/assets/` 是**内容哈希 + `immutable`**、
且 `try_files $uri =404` **刻意不设 fallback** —— 于是部署后，手里还开着旧页面的用户会拿
旧外壳去请求**已经不存在的哈希资源**，拿到硬 404。结果是**白屏**，不是降级。

实现上只需要一条 location：

```nginx
location = /index.html {
    include /etc/nginx/security-headers.conf;   # 见下面的「add_header 不继承」
    add_header Cache-Control "no-cache" always;
}
```

它能覆盖 `/`、`/login`、`/dashboard`、`/links/{code}` 全部入口，靠的是 try_files 的语义：
**最后一个参数不是普通回退，而是一次内部重定向** —— nginx 会拿 `/index.html` 重新做一遍
location 匹配，落到这个 `=` 精确匹配上。所以外壳只有一个出口，**将来新增 SPA 路由也自动生效**。

用 `no-cache` 而不是 `no-store`：允许缓存，但每次使用前必须带条件请求回源验证，
内容没变仍是 304 —— 既省流量，又不会出现「旧外壳 + 新资源」。

**② 全站安全响应头**

| 头 | 取值 | 为什么不设它是个问题 |
| --- | --- | --- |
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains` | 首次访问可被明文降级劫持。CF 的 *Always Use HTTPS* 只做 HTTP→HTTPS 跳转，**不等于** HSTS |
| `X-Frame-Options` + CSP `frame-ancestors` | `DENY` / `'none'` | 控制台里那个「显示一次性管理密钥」的按钮**只显示一次**：被任意站点 iframe 嵌套 + 遮罩诱导点击就能把密钥骗走（点击劫持）。老浏览器只认前者、新标准只认后者，所以两条都给 |
| `X-Content-Type-Options` | `nosniff` | 静态产物（`/assets/*.js`、`*.css`）靠它挡 MIME 混淆 |
| `Referrer-Policy` | `strict-origin-when-cross-origin` | 外壳的引用信息会随出站请求外泄。⚠️ 302 那条**单独也设了同一个值**（`handler/redirect.go` 的 `referrerPolicy`）—— 重复的 Referrer-Policy 是逗号列表、**以最后一条为准**，而 nginx 的 `add_header` 追加在上游之后，也就是 nginx 那份总是赢；两处不一致时 Go 里那一行就是死代码（有守卫逐字比对） |
| `Content-Security-Policy` | 见配置 | XSS 的第二道防线（走查未发现 XSS 面，所以它是纵深防御）。`style-src` 用的是**哈希**而不是 `'unsafe-inline'`：哈希对应口令页 / 失效页那段内联 `<style>`，从 `pageCSS` 常量算出来写入配置，`internal/handler/nginx_headers_contract_test.go` 会在常量改了而哈希没跟时变红（否则表现为「口令页静默变成没有样式的裸 HTML」） |

全部带 `always`：nginx 默认只给 `2xx/3xx/204/301/302/304` 加头，而 404（短链失效页）、
401（令牌过期）、503（依赖降级）恰恰是最需要它们的地方。

> ⚠️ **`add_header` 在 nginx 里是替换而不是合并。** 父层的 `add_header` **只在「当前层级没有
> `add_header`」时**才被继承 —— 所以 `location ^~ /assets/` 为了 immutable 缓存头自己写了一条，
> server 层那批安全头就会在那里**整批消失**，而 `nginx -t`、页面渲染、缓存行为全都看不出异常。
> 这正是安全头必须收进一个文件、再在每个写了 `add_header` 的层级 `include` 的原因。
> 守卫 `internal/httpx/nginx_headers_test.go` 会解析 `nginx.conf` 的块结构，
> 断言「**任何**含 `add_header` 的块里都有那个 include」，并检查外壳的 `no-cache` 仍在。
>
> **HSTS 是一扇单向门**：浏览器记住之后，在 `max-age` 内对该主机（以及
> `*.shorten.miku831.fun`）会拒绝明文 HTTP，撤不回来。它只在「HTTPS 已经确定可用」时才该在场；
> 本地走 `http://localhost:8080` 时发送它没有副作用（按 RFC 6797，浏览器必须忽略
> 经非安全传输收到的 HSTS 头）。刻意**不加 `preload`**（撤出要等下一个浏览器版本，更不可逆）。

**实测到的两处「同名头出现两次」，都不要当 bug 去重**：

- `X-Content-Type-Options` 在**代理响应**上是两条（应用自己也设，`httpx/response.go`）。
  应用那份保证「直连后端」也安全，nginx 这份保证它自己吐的静态文件也安全；取值相同。
- `Cache-Control` 在 `/assets/` 上是两条（`expires 1y` 加一条 `max-age=…`，
  `add_header` 再加一条 `public, immutable`）。这是**改动前就有的**既有行为，语义等价。
  ⚠️ 但它有个真实的副作用：Go 的 `Header.Get` 只返回**第一条**，所以断言缓存头时必须
  拼上全部取值（`cmd/smoke` 里那个 `headerValue` 就是为此而存在 —— 第一版断言只看第一条，
  于是把正确配置报成了「没有 immutable」）。

> **CDN 前置时请复验一次**：CF 会改写缓存头（见上一节），也可能改写或剥掉安全头。
> 部署后拿 `curl -sSI` 对着线上跑一遍上表，确认它们原样出现。

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

## 九条踩过的坑

这八条都是「设计稿上看不出来」、只有真跑起来（跑容器、真在浏览器里看一眼、或者真跑一遍 CI）
才暴露的，写在这里省得别人再踩一遍。

### 1. 新增前端顶级路由，必须同步四处

单域名下短码与 SPA 路由共用路径空间。**清单的唯一事实来源是 `frontend/src/router/index.ts`**；
新增顶级路由（例如 `/settings`）时要改四处：

1. `frontend/src/router/index.ts` —— 加路由（事实来源）
2. `deploy/nginx/nginx.conf` 加一行 `location = /settings { try_files $uri /index.html; }`
3. `frontend/vite.config.ts` 的 `SPA_ROUTES`（dev proxy bypass 白名单）
4. 想让这个词不能被别人抢注成短码，再加进
   `backend/internal/pkg/shortcode/reserved.go` 的 `reserved` 集合（连带
   `shortcode_test.go` 里 `TestReservedSetContents` 的断言）

漏掉第 2 步 → 生产环境刷新 `/settings` 会 404；
漏掉第 3 步 → 开发环境刷新该页面会 404；
漏掉第 4 步 → 别人可以注册 `settings` 当短码，把真实页面吃掉。

> **前三条现在有自动化守卫了**：`backend/internal/pkg/shortcode/routes_sync_test.go`
> 同时读 `src/router/index.ts`、`nginx.conf` 与 `vite.config.ts` 做交叉比对
> （三者的顶级路由清单必须**完全一致、不多不少**），并断言它们都在保留字表里。
> 漏改哪一处 `go test ./...` 就会红并打印差异。
> 加它的原因不是「防患于未然」，是真的漂移过：`terms` / `privacy` 在 nginx 与保留字表里
> 都有，只有 vite 那份漏了 —— 表现是本地 `pnpm dev` 刷新 `/terms` 被当成短码打到后端 404，
> 而生产 nginx 返回 200。靠注释提醒是拦不住的。

> ⚠️ **约束是「不多不少」，不是「越多越好」。** 2026-09-22 的审计发现 nginx 与 vite 里
> 各有 **10 条前端并不存在**的占位路由（`/logout` `/settings` `/account` `/profile`
> `/admin` `/about` `/help` `/docs` `/terms` `/privacy`）：它们被 `try_files` 兜到
> `index.html`，于是**「不存在的页面」以 200 返回**（渲染的是 NotFound 视图）——
> 爬虫与可用性监控会把不存在当成正常，真实的 404 被掩盖。想预留将来要做的页面，
> 正确做法是**等页面做出来再加那一行**。
> 保留字表不受影响：那一份是**超集**，`/admin` `/terms` 这类词仍然刻意留着防止抢注。
> 顺带：删掉这些占位之后，保留字真的会落到后端，于是那里也补上了更准确的一套 404 文案
> （对保留字说「页面不存在」而不是「这条短链不存在」—— 访问者找的是页面，不是短链）。

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
`/healthz/details` 的 `worker_errors`；生产形态下 worker 是独立容器，看它的容器日志）。

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

> **同一份 Dockerfile 的两个服务各自持有一个镜像 tag，只 build 一个会让另一个继续跑旧二进制。**
> backend 与 worker 的 `build` 定义完全相同（都是 `context: ./backend`，无 build args），
> 但 compose 会产出两个 tag：`ashencourier-backend` 与 `ashencourier-worker`。
> `docker compose up -d --build backend` 只重建前者，`up -d` 又只会重建**镜像 ID 变了**的容器
> —— 于是 worker 会毫无提示地继续跑旧代码（2026-09-21 实测到：worker 镜像停在两天前，
> 中间修掉的结算 Lua 与毒消息清理根本没在运行中的容器里生效，而 `docker compose ps`
> 显示 `healthy`，看不出任何异常）。
> **判断方法（已自动化）**：`node deploy/check-image-drift.mjs` —— 它打印两个 tag 的构建时间与层数，
> 并断言 **`RootFS.Layers` 逐项相等**（内容层是内容寻址的：COPY 进去的二进制一变，那一层的
> `diff_id` 就变 ⇒ 「层序列相同」与「同一份源码构建的」等价，且与构建时间、缓存状态、构建顺序都无关）。
> 时间戳只作参考而不作判据：冷缓存下两次独立 build 会差几秒（误报），热缓存下又可能完全相同（漏报）。
> **刻意不比 image id** —— compose 会给每个服务加自己的 label（`com.docker.compose.service=…`），
> label 属于镜像 config，所以两个 id 天生就不同（实测：backend / worker 的 id 一直不等，而层一直相等）。
> 它还断言**容器跑的就是当前 tag**（挡「build 了但没 `up -d`」）：`up -d` 只重建**镜像 ID 变了**的容器，
> 漏掉时 `docker compose ps` 仍显示 `healthy`，光看状态看不出来。
> 同一条命令接在 CI 的 `smoke` job 里作回归网（防 compose 文件被改成「两个服务不再同源」）。
> 要重建就重建两个，或者直接用文首那条 `docker compose up -d --build`（不带服务名）。
>
> ⚠️ 本守卫探测不到「**两个都旧**」（改了源码、两个 tag 都没重建）：那时两者层仍相等、
> 容器也仍与 tag 相符，内部是自洽的。要覆盖它得比源码与产物的时间，属于另一件事，刻意不做。

> **「镜像摘要」有三种，别混着比。** 2026-09-21 实测到一次自己吓自己：同一份源码依次重建两次，
> 构建日志里的 `exporting manifest list` 从 `eb0b9d2b` 变成 `992007d7`，
> 而 `docker inspect` 拿到的 `.Id` 也跟着变了，看着像「源码被改坏了」。
> 真相是三者本来就不是同一个东西：
> - `exporting manifest sha256:…` —— **单平台镜像内容**的摘要。同源码重建**逐字节不变**，这才是该拿来判「产物是不是同一份」的；
> - `exporting manifest list sha256:…` —— OCI index 的摘要。它包着上面的 manifest 之外还包着证明/attestation 元数据，**同一份内容重建也会变**；
> - `docker inspect -f '{{.Id}}'` —— 用 containerd 镜像存储时返回的恰好是**上面那个 manifest list** 的摘要。
>
> 所以「容器 `.Image` == 镜像 tag `.Id`」只能证明「容器跑的就是这个 tag 指着的镜像」，
> 不能证明「这次构建产出了新东西」。要判断产物有没有真的变，比 `exporting manifest`，
> 或者更直接 —— 去比对产物本身（`docker exec … md5sum /usr/share/nginx/html/assets/*`）。
> 附带一条：镜像是 Alpine、本机是 Windows 时，本地 `vite build` 与容器内构建的 **chunk 文件名哈希天然不同**，
> 拿本地 `dist/` 去比镜像产物只会得出错误结论。

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

### 9. `t.Cleanup` 里不能再使用 `t.Context()`

给 Redis Stream 的集成测试写清理时，第一版是这么写的：

```go
t.Cleanup(func() { _ = c.Ack(t.Context(), ids...) })
```

而 `t.Context()` 返回的 context **在该测试的 Cleanup 函数运行之前就已被取消** ——
于是这次 ACK 静默失败（错误还被 `_ =` 丢掉了），那几条消息留在 PEL 里，
把下一个用例的 `PendingCount` 断言带偏。症状很迷惑：单独跑每个用例都绿，
一起跑就有 3 条「幽灵消息」。

```go
t.Cleanup(func() {
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()
    if err := c.Ack(ctx, ids...); err != nil {   // 别再吞掉错误
        t.Errorf("清理测试消息失败（会污染下一个用例）：%v", err)
    }
})
```

> 一般化的教训：**清理路径上的错误不要吞**。清理失败在当时往往看不出症状，
> 但它会悄悄改变下一个用例的前置条件，而那时报错的是别人。
> 顺带一条：凡是「用例之间共享外部状态」的测试（PEL、数据库表、临时目录），
> 都要在开跑前先排空一遍，否则上一次运行（尤其是失败中断的那次）的残留会一直咬人。

## 已知限制

MVP 有意不做的部分：

| 不做 | 原因 |
| --- | --- |
| 自动抓取目标页标题 | 会引入 SSRF 风险，标题由用户手填 |
| 出口过滤 / DNS 解析（目标只挡**字面量**内网地址） | 创建与修改时会拒掉指向非公网地址的目标：`127.0.0.1`（含 `127.1` / `2130706433` / `0x7f.1` / `0177.0.0.1` 这些浏览器照收的宽松写法）、`10.x` / `172.16-31.x` / `192.168.x` / `100.64.x`、`169.254.169.254`（云元数据端点）、`::1` / `fc00::/7` / `fe80::/10`，以及 `localhost` / `*.local` / `*.internal` 这类保留名（内网部署可用 `ALLOW_PRIVATE_TARGETS=true` 整体关掉）。**但它不解析域名**：`evil.example` 可以解析到内网，也可以用「先解析到公网、随后改指内网」的 DNS rebinding 绕过。之所以不做解析：① 创建路径不该引入网络依赖（延迟、失败分支、又一条超时路径）；② 应用层拦不住 —— 目标 URL 是发给**访问者浏览器**的，真正的出口控制在网络策略层。所以它是「挡住最廉价的一类滥用」（把访问者的浏览器指到自己的 127.0.0.1 或内网设备上），**不是 SSRF 防护**，别拿它当 SSRF 的替代品 |
| A/B 分流 / 短链轮换 | 需要 `link_targets` 表与「目标页归属」的新语义，收益不明（二维码已两路提供：详情页前端 `qrcode` 画 canvas，后端 `GET /api/links/{code}/qr.svg` 给外部引用） |
| **回收已删除的短码**（让删掉的 code 能被重新领取） | 技术上门槛很低：删除是**软删除**（`status = 3`，行留着以便明细与统计可追溯），而 `links.short_code` 是**全局唯一**的，所以行还在、这个码就永远被占着；把唯一约束换成 partial unique index（`WHERE status <> 3`）即可放行。**不做的原因是收益不成立**：旧二维码 / 印刷品 / 邮件里已经发出去的链接会指到**别人**的新链接 —— 这是品牌与安全问题，不是技术问题。另外它还会让「码 → 目标」不再单调、`link:v2:{域}:{code}` 的缓存含义不再稳定（现在「码永不复用」是缓存正确性的隐含前提）。顺带一提，回收**不是为了省码**：自动码 7 位 base62 ≈ 3.5 万亿用不完，它只为腾出手工抢注的好码（自定义别名允许 3–32 位） |
| 口令的重置流程 / 提示语 | 没有邮箱找回，也没有 `hint`：口令只由所有者设置与清除（忘了就重新设一条） |
| 团队 / 多租户 / 权限体系 | 只有「匿名」（`manage_key`）与「个人账号」两种身份。做的话是 `orgs` + `memberships(role)` + `links.org_id`，鉴权从 `owner_id = $1` 变成「owner 或 org 成员」—— **难点不在表结构而在语义**（`X-Manage-Key` 的认领、软删除的权限、统计的可见范围都要重新定），且迁移是**破坏性**的（要回填 + 双写窗口）。决定等真实多人协作需求再做 |
| Prometheus / Grafana 全套 | 只做零依赖的 `/metrics` **文本端点**（不引客户端库、不带 Grafana），且默认**不对外**（nginx 里 `= /metrics` → 404）。详见「`/metrics`」一节 |
| 自定义域名的**管理接口 / UI** | 数据模型与解析路径已就绪（000006 的 `domains` 表 + `links.domain_id`，`Host` 归一化后按域定位，缓存键按域分开），但「登记一个域名」目前只能由运维写库、再在 nginx 加一个 `server_name` + 证书。做管理端要先回答「谁来验证域名归属」（DNS TXT / 文件校验），不是表结构问题 |

欢迎提 Issue 讨论优先级。

## 验收记录

以下都是实测结果，不是设计意图。基线快照：**2026-09-18**（⑫ 起为 2026-09-19 / ⑲ 起为 2026-09-20 /
㉔ 起为 2026-09-21 的增量），Windows 本机 + Docker Desktop
（Go 1.27.1 / Node 24.19.0 / pnpm 11.15.1）。

「在哪跑过」一列区分**本机实测**与 **CI 实测**——两者会得出同一结论，但覆盖面不同：
CI 每次都跑，本机记录的是基线快照与 CI 里不好做的项（比如停掉 PG、重启 backend 看关停顺序）。

| 项 | 结果 | 在哪跑过 |
| --- | --- | --- |
| `gofmt -l .`（backend） | 无输出 | 本机 + CI `backend` |
| `go vet ./...` | 通过 | 本机 + CI `backend` |
| `go test ./...` | 通过（config / domain / httpx / base62 / ipmask / shortcode / ua / validator / service / **startup** / store.postgres / worker） | 本机 |
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
| **容器级 ⑦**：缓存击穿防护（M3-2） | 用一个刚创建（缓存已被主动失效）的冷短码，`curl --parallel-immediate` 同时打 **20** 个请求 → 20 个 **302**；`/healthz/details` 的 `pg_fallbacks` 增量 = **1**（而不是 20）。单测侧：50 个 goroutine 并发 miss + 回源处设屏障，断言仓储只被调用 1 次 | 本机 |
| **容器级 ⑧**：备份与恢复（M3-3） | `--profile ops` 起 backup → 产出 `ashen-20260918-154740.dump`；`pg_restore` 到临时库 `restore_check` 后与主库逐项一致（`links` 14、`click_events` 169、`sum(base_count)` = `sum(event_count)` = 169）；删临时库后 `pg_database` 里不再有它。保留策略实测：造一个 2000 年的假备份 → 清理后旧文件被删、当天的留下 | 本机 |
| **容器级 ⑨**：标签（M4-1） | 创建时传 `["Ops","  ops  ","Dev"]` → 返回 `["ops","dev"]`（归一化 + 去重）；`?tag=ops` 只命中该条，`?tag=DEV`（大写）也能命中（按小写比较）；11 个标签 / 33 字符标签都返回 422 `invalid_tags`；`EXPLAIN` 下 `tags @> ARRAY['ops']` 走 **`links_tags_gin`**（Bitmap Index Scan）。浏览器侧：无头 Chrome 在 `/dashboard` 输入 `ops` 后列表从 2 条变 1 条 | 本机 |
| **容器级 ⑩**：点击明细页（M4-2） | 跳转 3 次（手机 / 桌面 / 爬虫 UA）→ `?limit=2` 拿到 2 行 + 游标，带游标翻到第 2 页拿到剩下的 1 行、`next_cursor` 为空；时间倒序且两页无重叠无缺口（26 行 = 首屏 20 + 「加载更多」6，逐行核对无重复）。IP 掩码：库里 `host(ip)` = `172.20.0.1`，响应里是 `172.20.0.0/24`，且响应体里搜不到原始地址。`device=mobile` 命中 1 条、`device=unknown` 命中 0 条（与设备分布口径一致）；`limit=0` / `days=abc` / `device=tv` / 坏游标都返回 422 且 `field` 正确；无凭据 404。`EXPLAIN` 下 `(occurred_at, id) < (…)` 被下推进 **`click_events_link_time_id_idx`** 的 Index Cond，且 Index Only Scan **不带 Sort 节点**（索引本身给出倒序）。浏览器侧：无头 Chrome 打开 `/links/{code}`，首屏 20 行 + 「加载更多」，点一下变 26 行、按钮换成「已经到底了」，两页拼接处无重复行 | 本机 |
| **容器级 ⑪**：二维码（M4-3） | 详情页把 `short_url` 画进 canvas（前端 `qrcode` 生成，无后端接口）。用 **jsQR 真的去扫**：页面 canvas 取回的 PNG 解码 = `http://localhost:8080/{code}`，与 `short_url` 逐字相等；点「下载二维码」落盘的 `ashencourier-{code}.png` 是 **1024×1024**，解码结果同样相等。配色为深墨 `#141413` + 暖奶油 `#faf9f5`（≈19:1，不用珊瑚色当前景），下载件用纯白底 | 本机 |
| **容器级 ⑪ 补**：二维码版式 | 初版画布撑破容器（`qrcode` 写的行内 `320px` 盖过 Tailwind 的 `h-full w-full`，见「九条踩过的坑」第 6 条）。修正后实测：容器 **160×160** @ (158.5, 366.9)、画布 **142×142** @ (167.5, 375.9)（正好等于容器减 padding 与 1px 边框）、右边缘 309.5 < 文字列 327.5（不压字）、位图仍是 **320px**、inline style 已清空；采样像素同时含 `#141413` 与 `#faf9f5`。解码两处仍全对 | 本机 |
| `docker compose down && docker compose up -d` | 数据仍在（volume 持久化：`links` 8 → 8），`/healthz` 立即 200 | 本机 |
| **集成测试 ⑫**：store 层迁移与 SQL（N1 / 自动化缺口 16.3-1） | 带 `POSTGRES_TEST_DSN` 时 20 个用例全绿（迁移形状与索引清单 / links 往返 / Update 的三种语义 / 与权威 SQL 逐项比对的 keyset 两处 / `tags @> ARRAY[...]` 走 `links_tags_gin` / `event_uid` 幂等 / 聚合的 UTC 日界 / 计数累加 / 过期扫描）；不带 DSN 时 9 个集成用例全部 SKIP、整包仍绿。**变异验证**：删掉 `ON CONFLICT ... WHERE event_uid IS NOT NULL` → 报 `42P10 no unique or exclusion constraint matching`；把 keyset 的 `(occurred_at, id) <` 退化成 `occurred_at <` → 报 `got=[12 11 10 9 8 6 5 4 3 2] want=[12 11 10 9 8 7 6 5 4 3 2 1]`（并列时间上漏掉第 7 与第 1 条）。第一次跑还发现 `links.created_ip` 读出来带 `/32` 掩码长度，已与 `click_events.ip` 一样改用 `host()` | 本机（PG 18.6 容器）+ CI `backend` |
| **容器级 ⑬**：短链访问口令（M5-1 / N2） | 建带口令的短链 → `password_protected=True`；库里 `password_hash` 是 `$2a$12$…`（60 字符，且 `= 'smoke-pass-9f3a'` 为 `f`）。未解锁 `GET /{code}` = **200 + text/html** 且 `total_clicks` 仍 0；错误口令 = **401** 且 `total_clicks` 仍 0；正确口令 = **303 + Set-Cookie**（`HttpOnly` / `SameSite=Lax` / `Path=/`，http 下不带 `Secure`）；带 cookie 的 GET = **302**，`total_clicks` = **1**、`click_events` = **1**（解锁那次没被重复计）；`clear_password` 后立刻 302（缓存被主动失效）。迁移 000005 往返两轮：`down 1` 后列消失、`up` 后回来，无报错且之后新跳转仍 302。冒烟 **27 / 27**（三条口令用例逐条 ✓）。浏览器侧（无头 Chrome + CDP）：创建表单展开高级选项后有「访问口令」；详情页显示「受口令保护」徽章，编辑面板有「访问口令」输入与「清除口令」按钮；短链未解锁渲染口令页、输错显示「口令不对，请再试一次。」、输对**真的落到目标地址** | 本机（Docker + 无头 Chrome） |
| **前端单测 ⑭**：纯函数（16.3-3） | `vitest run` **31 个用例全绿**（`format.ts` 24 个 / `tags.ts` 7 个），约 0.8s；同时把 `splitTags` 从两个组件里提到 `src/utils/tags.ts`（原来是一模一样的两份）。**变异验证**：把 `truncateMiddle` 的 `head + tail + 1` 退化成 `head + tail` → 边界用例红；把 `splitTags` 的 `length > 0` 改成 `length > 1` → 第一次**没被抓住**（用例里没有单字符标签），补上「`书` 这种单字符标签不能丢」后变红。时间断言用**不带时区后缀**的输入串，因此本机（Asia/Shanghai）与 CI（UTC）结果一致 | 本机 + CI `frontend` |
| **容器级 ⑮**：浏览器级验收（16.3-2） | 无头 Chrome + CDP（**零 npm 依赖**，只用 Node 内置 fetch / WebSocket），**19 项全绿**：二维码 6 条（行内尺寸已清空 / 位图 320×320 / 画布真的画过：深墨 4.5 万 px + 暖奶油 5.7 万 px / 画布在容器内 / 显示宽 = 容器宽 − padding → `142.0 = 158 − 8 − 8` / 右边缘 309.5 < 文字列 342.5）、明细 6 条（接口只回 `/24` 网段、首屏 20 行、时间到秒、逐行与接口核对、页面文本无原始 IP、翻页 `20 + 6 = 26` 行且无重复）、口令 6 条（只回布尔不回摘要、未解锁不给 cookie、错口令留在口令页、对口令 303→落到目标 `/login`、`ac_unlock` 是 HttpOnly + `Path=/`、解锁恰好只多一条明细）、全程零 console 错误。**变异验证**：去掉 `canvas.style.width = ''` → **4 条版式断言全红而像素断言仍全绿**（见坑第 6 条）。顺序约束也实测过：**e2e 19/19 之后紧接 smoke 27/27**（两者都不会把对方的创建配额打满）。⚠️ 反过来不行：先跑 smoke 会把同一条 per-IP 的创建配额打满，紧跟其后的 e2e 第一步建短链就 429（`操作过于频繁，请 30 秒后重试`）—— 不是缺陷，等约 1 分钟窗口翻篇再跑即可。B3 又补了 11 项（确认框与 toast，见 ㉘）后是 **30 / 30**；B4 再补 7 项（版式与可访问性、移动端菜单，见 ㉙）后是 **37 / 37**；B5 补 2 项（趋势图配色取自 token、渐变 id 唯一，见 ㉚）后是 **39 / 39**。| 本机（Docker + 无头 Chrome）+ CI `smoke` |
| **单测/集成 ⑯**：GeoIP 解析（M5-2） | `internal/store/geoip` 与 `internal/worker` 的用例全绿：非法输入（空串 / 非 IP / 带端口 / 网段 / 主机名 / 坏 IPv6）一律空串、零值 Locator 与 nil 都安全、打开不存在的文件与非 mmdb 文件都报错；**带真库**（`GEOIP_TEST_DB` = 本机 GeoLite2-Country）时 `81.2.69.142` 解析出两位大写国家码、私网 `10.11.12.13` 为空、`::ffff:81.2.69.142` 与原生 IPv4 结果一致（`Unmap` 生效）。worker 侧断言国家码取自 `GeoLocator` 且**用原始 IP** 去查（不是掩码后的），未配置时留空不 panic。集成测试 `TestAggregateCountriesExcludesEmpty` 覆盖国家聚合 SQL：按点击数降序、**不含 `unknown` 桶**、无已知国家时是空切片（而非 nil）。**变异验证**：把国家 SQL 退回 `coalesce(nullif(country,''),'unknown')` → 结果多出 `{unknown 1}`，用例变红。降级日志也断言了级别：留空是 `INFO` 且不含 `WARN`，路径打不开是**恰好一条** `WARN` | 本机（真 PG 18.6 + 真 mmdb）|
| **容器级 ⑰**：GeoIP 端到端与降级（M5-2） | worker 启动日志 `GeoIP 库文件已加载 /geoip/GeoLite2-Country.mmdb`。往 Stream 投两条**显式 ID + 公网 IP** 的点击（本机 curl 的客户端 IP 是 Docker 网关 `172.20.0.1`，私网段解析不出国家，所以必须直接投递）：`8.8.8.8` → 库里 `country=US`、`114.114.114.114` → `CN`；`GET /api/links/{code}/stats` 回 `countries=[{CN,1},{US,1}]`。降级实测两轮：`GEOIP_DB_PATH=/geoip/does-not-exist.mmdb` → worker 日志**恰好一条 WARN**、`8.8.8.8` 仍落库且 `country` 为 NULL、`/healthz` 200 `ok`；`GEOIP_DB_PATH` 留空 → 一条 INFO、零 WARN、行为相同 | 本机（Docker + 真 mmdb）|
| **容器级 ⑱**：自定义域名分域解析（N6-1） | 跑完迁移 000006 后库内登记 `a.local`（此时 `domains` + `links.domain_id` 就位）→ 创建带 `domain=a.local` 的短链，`short_url` = `http://a.local/{code}`。`curl -H 'Host: a.local'` 得 **302**，而同一短码在默认 Host 上是 **404**；反向（默认域名的短链拿到 `a.local` 上）同样是 **404**；`Host: A.LOCAL:8080`（大写 + 端口）也能命中（归一化生效）。真 Redis 里键确实按域分开：`link:v2:{域 UUID}:{code}` 与 `link:v2:-:{code}`，跨域那条**没有**落在默认域前缀下。共 **24 / 24**。**变异验证**：把缓存键改回不分域 + `sameDomain` 改成恒 true → **8 / 24 红**，失败的正是要害 —— 「紧接着在 `a.local` 上访问该短码」变成 404（跨域探测写下的负缓存把正确域的访问挡死）、反向那条变成 302（串味，访问者被送到另一个域的目标）；还原后回到 24 / 24 | 本机 |
| **容器级 ⑲**：后端二维码 SVG 端点（N8 / M4-3 方案 B） | `GET /api/links/{code}/qr.svg` 返回 `image/svg+xml`（3243 字节），带 `Cache-Control: public, max-age=300` 与 `nosniff`，响应体里搜不到短码与 `short_url`（**没有任何用户可控字节**）。**真扫两轮尺寸**（无头 Chrome 光栅化 → jsQR）：512px 与 128px 解码都 = `http://localhost:8080/{code}`，与 `short_url` 逐字相等；两种情况都有深墨前景 `#141413` + 纯白底，四条边采样全白（静默区），定位图案落在**第 5 个模块**（证明静默区恰好 4，不是 8）。404 语义三种都验过：不存在的短码 / 保留字 / **已软删除**（删完再取图 → 404）。共 **19 / 19**。**变异验证**：`symbol.DisableBorder = true`（去掉静默区）→ 静默区断言红；子路径写成 `h-%d`（方向反）→ 闭合/模块数断言红。冒烟侧固化了两条（公开可读 + SVG 形状 + 不含用户可控字节；删除后 **404**），使 `cmd/smoke` 从 27 项变 **28 项** | 本机（Docker + 无头 Chrome） |
| **容器级 ⑳**：`/metrics` 文本端点（N9） | **经 nginx 访问 `localhost:8080/metrics` → 404**（响应体 146 字节是 nginx 的 404 页、搜不到 `ashen_`、Content-Type 是 `text/html`）—— 这才是不对外的证明；同一路径从网络内部（借前端容器的 busybox wget 打 `backend:8080`）→ **200 + `text/plain; version=0.0.4; charset=utf-8` + `nosniff`**，**17 条指标**（每条都有 `# HELP` 与 `# TYPE`），零值计数器 `ashen_dropped_clicks_total 0` 在场，`ashen_build_info{version="dev"} 1`。`custom_code=metrics` → 422 `invalid_custom_code`（不是 409、不是静默成功）。**停掉 PG**：`/healthz` 503 而 `/metrics` **仍 200**，`ashen_postgres_up 0`、`ashen_up 0`、**`ashen_redis_up 仍为 1`**（单探针可定位 —— 这一条同时是这个 bug 的回归守卫，修前实测它是 0）。共 **24 / 24**，恢复 PG 后 `/healthz` 回到 200；`cmd/smoke` 仍 **28 / 28**。**变异验证**：删掉保留字表的 `metrics` → `TestReservedSetContents` 报 `保留字表缺少 "metrics"`；把 `ashen_up` 写死成 1 → `TestRenderMetricsDegraded` 报 `ashen_up = 1，期望 0` | 本机（Docker + busybox wget） |
| **集成 ㉑**：计数结算脚本（真 Redis） | 新增 `internal/store/redis/counter_integration_test.go`。`SettleDelta` 是一段 Lua，语义全在 Redis 里执行，纯 Go 单测只能断言脚本源码的字符串形状 —— 改坏了也未必红。三个用例覆盖正常路径 / **有残留** / `delta=0`。有残留那一条钉住本次修掉的缺陷：结算窗口内来了新点击时，dirty 标记必须保留给下一轮。**变异验证**：把 `SREM` 改回无条件 → 报「有残留时 dirty 不该被摘掉」；还原后自证为绿。门控沿用 `REDIS_TEST_ADDR`（未设置则 SKIP），CI 的 backend job 加了 redis service | 本机（真 Redis 8.10 容器） |
| **集成 ㉒**：毒消息清理（真 Redis） | 新增 `internal/store/redis/stream_integration_test.go`。`ReapDeadLetters` 靠 XPENDING 翻页，游标推不动是死循环、推过头就漏消息，两种错在 fake 上都看不出来。用 `XCLAIM` 反复认领把重投次数抬上去，断言的是 **Redis 自己记的** delivery count。三个用例：达上限被丢弃且 PEL 归零 / 未达上限必须留着（不误杀，一次长 PG 故障不该丢正常消息）/ 单轮上限 2 时每轮都有进展。**变异验证两条都被真实断言抓住**：阈值 `>=` 改成 `>` → 「丢弃了 0 条，期望 5 条」；只掐掉计数 → `DeadLettered = 0, want 2` | 本机（真 Redis 8.10 容器） |
| **前端 ㉓**：并发守卫与趋势图时区 | vitest **43** 个用例全绿（新增 `utils/request.ts` 9 个、`formatShortDate` 3 个）。**变异验证三条**：去掉 `abort` → 「发起新一轮会取消上一轮」红；`isStale` 恒 false → 三条过期判定全红；`formatShortDate` 改回 `Date` 解析 → `expected '08-31' to be '09-01'`（America/New_York 下纯日期串提前一天）。最后这条尤其值得记 —— 老用例给的是带时间的串，那种串在任何时区下都是绿的，**真正会踩的恰恰是后端实际返回的纯日期串** | 本机 |
| **前端 ㉔**：Dashboard 分页卡死 + 「四处同步」守卫（N18 之后的一轮审计） | ① `reload` 与 `loadMore` 共用并发守卫，`reload` 的 `begin()` 会把在飞的 `loadMore` 判成 stale，于是后者唯一复位 `loadingMore` 的 `finally` 不执行 → 按钮永久转圈且再点无效。**真 Chrome + CDP 实测**：撤掉那行复位 → 按钮 `disabled + aria-busy + spinner` 不再恢复、行数停在 20、再点「加载更多」无效（2 项断言红）；恢复后 → 20 → 25，5 项断言全绿。② 详情页补上 `watch(route.params.code)`（`links/:code` 在 `/links/A → /links/B` 时会复用组件实例，原来只有 `onMounted`）。③ 新增 `routes_sync_test.go` 把「新增顶级路由四处同步」变成断言：把 `terms`/`privacy` 从 vite 白名单去掉 → 用例红并打印「只在 nginx.conf 里：[privacy terms]」；还原后绿。④ ⚠️ 复现脚本第一次跑出的是**假绿**：本机 frontend 容器跑的是 2 小时前的旧构建（镜像创建 10:31，早于引入并发守卫的 `2afdecd` 12:40，产物里 `AbortController` 出现 **0** 次），**验证前端行为前必须先 `docker compose build frontend` + `up -d frontend`**，否则测的是旧代码 | 本机（Docker + 无头 Chrome） |
| **后端 ㉕**：启动期退避重试（N18 之后那一轮审计的 P2 项） | api / worker 启动时对 PG、Redis 的探测，从「失败即 `return err` → 进程退出」改成**进程内**退避重试（新增 `backend/internal/startup`：首轮立即执行，之后 250ms → 500ms → 1s → 2s 封顶，窗口 30 秒）。**容器实测**（拿一个必然连不通的 PG 地址跑 `docker compose run --rm --no-deps backend`）：17 条 `WARN`（都带 `dependency=postgres` / `attempt` / 可读的 `backoff`）+ 1 条最终 `ERROR`，`backoff` 序列实测为 `250ms, 500ms, 1s, 2s, 2s…`，重试循环耗时 **29.8s**（窗口 30s）、**尝试 18 次**、退出码 1，错误链里带根因：`startup: postgres 在 30s 内未就绪（尝试 18 次）: store.postgres: ping: … dial tcp 172.20.0.6:1: connect: connection refused`。**语义仍是 fail fast**：窗口用尽照旧退出，只是把「依赖暂时没起来」与「根本连不上」区分开了。**变异验证三条**：去掉封顶（`min(d*2,maxBackoff)` → `d*2`）→ 两条封顶用例红；去掉 `window<=0` 回落 `DefaultWindow` → 「第 3 次已就绪却仍返回错误」红；去掉 `ctx.Err() != nil` 的提前返回 → 「ctx 取消后不该再记 WARN」红；三条还原后均绿。**顺带修掉两处**：① `backoff` 原本按 slog 对 `time.Duration` 的默认编码输出纳秒整数（实测就是 `"backoff":250000000`），改为 `.String()` 输出 `"250ms"` 并加断言；② api 镜像 `HEALTHCHECK --start-period` 5s → **35s** —— 否则每 10s 一次的探针会在「进程活着但还没开始监听」的 30s 窗口里攒够 3 次失败，报出一个假的 `unhealthy`。重建后 5 个容器全 healthy、冒烟 **28 / 28** | 本机（Docker + `go test`） |
| **前端 ㉖**：字体子集化 + 零引用清理（B1） | 产物里的字体从 **48 个文件 / 782KB** 降到 **8 个 / 213KB**（只留 Inter 与 Cormorant Garamond 的 latin 子集 × 400/500 × woff/woff2，本次 Docker 构建日志里这 8 条字体产物逐条可数）。`color-scheme: light` 同时写进 `index.html` 的 `<meta name="color-scheme">` 与 CSS 的 `html` 规则 —— 少写任一处，深色系统下的原生下拉箭头与滚动条仍会是深色。`prefers-reduced-motion` 的兜底原来只关 `animate-breathe`，现在把 `animate-spin` 一并关掉（转圈与呼吸是同一类动效，只关一半等于没关）。**变异验证**：把 `@fontsource/inter/latin-400.css` 换回全量 `400.css` → 产物变 `20 个 / 375KB`；还原后 `8 个 / 213KB`，且 `main.ts` 与备份逐字节一致（脚本自带这步自证，防「还原时把当轮改动覆盖掉」）。删掉的三处零引用：`format.ts` 的 `formatCompact` / `toISODate`、`StatCard` 与 `EmptyState` 的 `onDark`、`Input` 的 `prefix`（删前都先全仓库检索、含 `frontend/e2e/` 脚本确认零引用） | 本机 + CI `frontend` |
| **前端 ㉗**：UI 基础件补齐与组件层收敛（B2） | 问题不是「少了几个组件」，而是组件层只覆盖了一半场景：`ui/` 里有 Button / Input / Card / Spinner，但视图里还在手写 `card-dark`、`btn btn-text`、裸 `<select>` —— 同一个设计 token 在两处各解释一遍。这轮补上 **4 个基础件**：`SegmentedControl`（`role="group"` + 每个按钮 `aria-pressed`，选中态由 `[aria-pressed='true']` 驱动，不另开一套「选中」样式）、`Select`（`appearance:none` + 用 `currentColor` 画箭头，`inheritAttrs:false` 让 `aria-*` 落到真正的 `<select>` 上）、`IconButton`（`label` 必填，同时喂 `aria-label` 与 `title`）、`Skeleton`（复用已有的 `animate-breathe`，不新增动效）。`Badge` 补 `error` 变体；`LinkTable` 里靠字符串拼接的 `statusClass()` 换成返回 `'pill' | 'quiet' | 'error'` 的 `statusVariant()`。**踩到一个 vue-tsc 报错，值得记**：`SegmentedControl` 的 prop 起初叫 `ariaLabel`，模板里写 `aria-label="…"` —— `aria-label` 是 Vue 类型里的保留属性名，会**遮蔽**同名 camelCase prop，绑定根本到不了 prop（`TS2345`）；改名 `label` 后 lint / vue-tsc / build 三关全 0。⚠️ B1 与 B2 的前端改动在**同一个镜像**里一起验收（两轮都只动前端，重建一次即覆盖两者）：镜像 `16831276` 下 e2e **19 / 19**、smoke **28 / 28**，且容器 `.Image` == 镜像 tag `.Id` == 构建日志里的 manifest 摘要（三者一致才算「跑的是这次构建的产物」） | 本机（Docker + 无头 Chrome） |
| **前端 ㉘**：确认框替代 window.confirm + toast 强化（B3） | 两处 `window.confirm`（Dashboard 与详情页的删除）换成设计系统内的 `alertdialog`。**行为全部按 WAI-ARIA 的 alertdialog 模式自己实现** —— 原生确认框的焦点归位与 Esc 是浏览器白送的，换成自绘 div 之后全部要自己写：`role="alertdialog"` + `aria-modal` + `aria-labelledby` / `aria-describedby` 关联标题与正文；初始焦点落在**「取消」**上（破坏性操作的默认落点必须是「不做什么」）；Tab / Shift+Tab 在框内首尾循环；Esc 与点遮罩都算取消、点面板本身不算；关闭后焦点还给触发元素，**它已经不在文档里时退到 `<main>`**（`tabindex="-1"`）而不是掉回 `<body>`。`useConfirm` 做成 Promise 风格，同一时刻只保留一个：被新的顶掉时上一个按「取消」结算 —— 否则那个调用方的 `await` 会永远挂着，而界面上看不出任何异常。toast 侧：拆成**两个 live region**（错误 assertive / 提示 polite —— 混在一个里要么全打断、要么全排队）；同屏上限 3 条且**优先丢最早的非错误项**（错误是要用户读完的，被后面一条 success 顶掉说不过去）；每条带 `aria-label="关闭提示"` 的关闭按钮，不再是「点整条才关」。**容器实测**：e2e 从 19 项涨到 **30 / 30**（新增 11 项全是真浏览器行为，在 node 环境的单测里一条都验不到），冒烟 **28 / 28**；`useToast` 的上限规则另有 6 个单测（vitest 51 项）。**变异验证 5 条，全部只红对应的那一条**：初始焦点改到「删除」→「初始焦点落在取消上」红（连带 1 条）；去掉 Tab 拦截 → 焦点陷阱那条红；`role=alertdialog` 退化成 `role=dialog` → 语义那条红；错误区 `assertive` 改成 `polite` → 两个 live region 那条红；去掉「退回主区域」分支 → 焦点兜底那条红。⚠️ 两条工程细节值得记：① e2e 的点击用 **CDP 合成鼠标事件**而不是 `element.click()` —— 后者不移动焦点，而「关闭后焦点还给触发元素」这条断言的前提正是焦点真的到过那个按钮；② 定位前必须 `scrollIntoView({behavior:"instant"})`，全局 CSS 的 scroll-behavior 是 smooth，平滑滚动途中量到的矩形会点到别的元素上。另：第一次跑变异时 2 条失败**不是断言的问题，是变异体自己写坏了**（`document.querySelector(...)` 不带类型参数 → `Element` 上没有 `focus` → `TS2339`，同时 `el` 变成未使用 → `TS6133`，构建直接挂）—— 这属于「分不清是断言没验住还是变异体有错」，已改成能通过 tsc 的变异体重跑，两条都如期变红。⚠️ 本组最后一步会**真的删掉**那条验收短链，所以它必须排在 browser-check.mjs 的最后（在「零 console 错误」之前）。| 本机（Docker + 无头 Chrome） |
| **前端 ㉙**：版式收敛 + 可访问性（B4） | 三件事，都不是「多写几个组件」，而是把散在视图里的版式与浏览器默认行为收回一处。① **版式件**：新增 `PageHeader`（eyebrow + 标题 + 三种字号 + `#actions` 插槽，详情页的短码用 `titleClass` 走等宽）与 `Section`（eyebrow 落地成 `<h2>` 而不是 `div`，页面大纲连得上），把 Dashboard / LinkDetail / Login / Register / NotFound 五个视图里各自手写的标题块与分区标题迁过去。② **跳过导航**：`.skip-link` 平时 `top:-100px` 藏在视口上方、靠 `:focus` 移进视口 —— 用 `display:none` 会让它**无法聚焦**，等于把键盘用户的第一个落点也删了。③ **焦点与当前位置**：客户端路由切换后把焦点送到新页面的 `h1`（`tabindex="-1"` + `preventScroll`），读屏才会念出「这是哪一页」；判定用 `route.path` 而非 `route.fullPath` —— 只改 hash（比如点跳过链接）绝不该抢走焦点。导航的当前项用 `aria-current="page"`，且**只打给一个链接**（详情页算在「我的链接」这一组里，不再出现两处同时高亮）。④ **顺带修掉一个真缺陷**：移动端菜单面板原本写的是 `inset-16 top-16`，等于在 390px 的视口上四周各留 64px（不是「铺满」而是「悬空一块」），改成 `inset-x-0 top-16 bottom-0`；滚动锁也从两处各存一个 `previousOverflow` 改成 `useScrollLock` 的计数器式 `lock()/unlock()`（对话框与移动菜单叠在一起时，后者解锁不该把前者的锁也解掉），并且**恢复的是原始内联值而不是硬编码的 `visible`**。**容器实测**：e2e 从 30 项涨到 **37 / 37**（新增 7 项：单 h1 且可聚焦 / 跳过链接平时在视口外、聚焦后进到 `top=12` / 激活后焦点落到 `<main>` / `aria-current` 只打一处 / 路由切换后焦点落到新 h1 / 390px 下面板 0…390 × 64…844 真的铺满 / Esc 关菜单并把焦点还给汉堡），冒烟 **28 / 28**；改动源码后重建镜像，容器 `.Image` == 镜像 tag `.Id` == 构建日志 manifest 摘要（三者一致） | 本机（Docker + 无头 Chrome） |
| **前端 ㉚**：视图改用组件层 + 图表 token 化（B5） | 这一轮的主线是**消双轨**：同一个设计 token 之前有两套写法，一套是 `ui/` 组件、另一套是视图里直接写 CSS 类。① 视图侧：`LandingView` 的裸 `card-dark`/`card-coral`/`card-feature`/`badge badge-coral`/`btn btn-secondary*`、`LinkTable` 与 `LinkDetailView` 的裸 `btn`/`card-cream`/`badge` 全部收回 `Card` / `Button` / `Badge`。`Card` 为此开了一个 `tag` 逃生口（列表项得渲染成 `<li>` 才语义正确），只换标签不换外观。② 补三个基础件，都是「同一段东西抄了三遍」：`BrandMark`（4 辐星的路径数据在顶栏 / footer / 空态各一份，颜色还各写一个内联 hex —— 现在颜色走 `currentColor`，由调用方的 `text-ink` / `text-on-dark` / `text-primary` 决定，组件自己一个色值都不碰）、`CodeWindow`（`.code-window-inner` 那层深色内嵌面板在三个地方各写一遍，带 `label` 时才渲染外框与三个圆点）、`CopyButton`（「写剪贴板 → 成功/失败提示 + 文案回闪 1.8 秒」这段**行为**原先在三个组件里各写一遍，其中 `LinkTable` 的桌面与窄屏是同一份逻辑写了两遍）。③ 顺带修掉一个静默缺陷：`LinkTable` 原先整张表共用一份 `useCopy()`，`copied` 也就是共享的 —— **点任意一行的「复制」，所有行的按钮文案都会变成「已复制」**；`CopyButton` 每个实例自带状态，这个问题由构造方式消灭。④ 图表 token 化：`TrendChart` 的 `#cc785c` / `#e6dfd8` / `#8e8b82` 换成 `.chart-line` / `.chart-guide` / `.chart-tick` / `.chart-stop`（SVG 的 `stroke`/`fill`/`stop-color` 写在**呈现属性**里引不到 `var()`，所以必须落成组件层的具名类，这同时也让「换主色只改 `@theme` 一处」成立）；`id="trend-fill"` 是**文档级**的全局 id，两张趋势图会互相顶掉，改成 `useId()`。⑤ 修掉 `ResultCard` 的密钥泄漏：`revealKey` 是组件内部状态，而结果卡是**被复用**的（落地页与列表页都只把 `latest` 换成新 payload，同一位置的实例不重建），于是第一次点过「显示」之后，下一条短链的一次性管理密钥会**默认明文**摊在屏幕上 —— 与「默认打码」的注释意图正相反且毫无报错。抽出 `useMaskedSecret`（默认收起 + 换来源自动收起）并补 5 个单测。**为什么没走 e2e**：那一组至少要再创建 2 条短链，而创建接口是 10 次/分钟/IP 的硬配额、`cmd/smoke` 自己要用掉约 7 次，余量本来只有 2 —— 为验一个纯状态规则吃掉余量，换来的是 CI 一旦时序偏移就偶发 429，不划算。**遗留**：`DESIGN.md` 的 never-inline-hex 现在只剩一处例外 —— 详情页二维码的前景/底色（`#141413` / `#faf9f5`），那是交给 `qrcode` 库的画布渲染器用的，读不到 CSS 变量，只能给字面量，代码里已注明。**验收**：eslint / vue-tsc / build 均 0，vitest **56**（+5），e2e **39 / 39**（+2：折线与渐变解析成 `rgb(204, 120, 92)` 而**不是**回落的黑、渐变引用命中唯一节点且 id 不是写死的常量），冒烟 **28 / 28**；镜像内容摘要（单平台 manifest）`566c45b9`，容器 `.Image` == 镜像 tag `.Id` == `992007d7`。⚠️ 两条工程细节：**e2e 的点击与按键用 CDP 合成事件**，而页面里 `element.click()` 不移动焦点、造的 `KeyboardEvent` 不触发浏览器默认行为；**变异验证 4 条全部只红对应的那一条**（`useMaskedSecret` 去掉 watch / 默认值翻成 true 走 vitest；`.chart-stop` 换成写死的黑、渐变 id 退回常量走 e2e，各自只红 1 项）| 本机（Docker + 无头 Chrome） |
| **前端 ㉛**：零引用清仓 + 把「清一次」变成 CI 守卫（B6） | ① **清仓清单**：组件 31 个**全部有引用**；`main.css` 具名类 52 个里 **2 个零引用**（`.title-sm` 与 `.caption` —— DESIGN.md 的字阶表里有这两档，但本仓库从没用过）；npm 依赖无孤儿。另有 11 个「零引用**类型导出**」逐个核对后**全部不是死码**：`RequestTicket` / `RequestGuard` 是 `createRequestGuard()` 的返回类型，`ConfirmOptions` / `PendingConfirm` / `ConfirmVariant` 被 `useConfirm` 的签名用着，`ToastKind` 被 `Toast` 与 `Record<ToastKind, number>` 用着，`LinkStatus` 与四个 bucket 接口是 `Link` / `Stats` 的字段类型 —— 删掉 `export` 只会让调用方再也引用不到这些字段类型，属倒退，故保留。② **删两个类省 161 字节**（对照构建：HEAD 源码 31491 → 改后 31330，逐字节可控）。③ 顺带查出**两个反直觉事实**，都写进了 `main.css` 的行内注释：**(a)** Tailwind v4 会把**未引用**的 `@theme` 变量从产物里剪掉，所以「标着却没人用的 token」留着成本是 **0**，删它反而是纯改动；**(b)** 但它判断「一个 token 有没有被用到」靠的是**扫源码文本**，于是**在 JS/TS 的注释里写出 token 全名就等于把它钉进 `:root`** —— 本轮我自己在新建的守卫脚本头部为了举例写了三个 token 名，产物就凭空多出 **68 字节**。这条是**用「把 `frontend/scripts/` 整个移出 Vite 根再构建」隔离出来的**：移出后恰好那三个变量从产物消失、移回来又复现，完全可逆（`--radius-sm` 与 `--color-success` 因为在用而始终在场，作为对照）。④ **纠正两条被旧产物误导的结论**：本机磁盘上的 `frontend/dist/` 是**陈旧构建**（27615 字节），而同一份源码在本机重建是 31491 字节 —— 差 3.8KB，说明那份 dist 早于好几轮改动，拿它做的测量都不可信；以后测产物一律先重建。⑤ **把清扫变成守卫**：新增 `frontend/scripts/check-zero-ref.mjs`（零 npm 依赖）+ `pnpm audit:refs` + CI `frontend` job 里的一步，查三类：零引用组件、零引用的 `main.css` 具名类、「未引用的 token 被非 CSS 源码提到」；**刻意不判**未引用 token 本身（零成本）与类型导出（不是死码）。判定口径也写清：类的「使用」只认非 CSS 文件**且不含 `scripts/`**（否则守卫会被自己的注释糊住），token 的「被用到」则要**连 `scripts/` 一起看**（Tailwind 也扫它）。⑥ **变异验证 5 条，只红对应的那一条**：注入零引用类 / 新增零引用组件 / 断开 `ToastHost` 对 `ToastItem` 的**相对导入**（这条专门钉住历史盲区 —— 第一版审计只匹配 `ui/ToastItem.vue` 字面量，于是把还在用的 `ToastItem` 误报成零引用）/ 在 `.ts` 里提及未引用的 token / 一条负对照（同样加一个类但模板里用上 → 必须仍然绿）。**M5 第一次跑是红的，原因是守卫自己真有缺陷**：扫描集漏了 `scripts/` —— 恰恰是泄漏发生的那一个目录；补上后转红。**验收**：eslint / vue-tsc / audit:refs / build 均 0，vitest **56**；镜像重建后三方一致（容器 `.Image` == tag `.Id` == 构建 manifest list `f2d4b9e9`），e2e **39 / 39**、冒烟 **28 / 28**；**从容器里读那份被服务的 CSS** 确认：31240 字节、六个未引用 token 一个都不在、`.title-sm` 与 `.caption` 已消失。⚠️ 容器是 31240、本机是 31262 —— 差的 22 字节来自 `.dockerignore` 排除了 `frontend/e2e`（容器扫不到它），本地测产物时要记得这个差。⚠️ 另记三条本机工程坑：**(a)** 变异脚本「写-还原」的自证这次**撒过谎**（脚本自己的 sha 比对报告还原成功，文件里其实还留着变异内容），所以现在除 sha 外还**独立读文件找残留标记** —— 正是这一步抓到了残留的探针文件；**(b)** 变异脚本若把「探针文件」算进基线，`restore()` 会在每个变异前后都把它**写回来**，越跑越脏（已改成探针一律按「基线不存在」处理）；**(c)** 本机 Git Bash 的 `rm` 是个 shim，内部调 `dirname` 而 PATH 里没带 coreutils —— 它**报错却返回 0、文件根本没删**，删除后必须独立读回复核。 | 本机（Docker + 无头 Chrome）+ CI `frontend` |
| **文档 ㉜**：界面截图与 README（B6 之后） | 7 张截图全部采自**运行中的实例**（Docker 全栈，frontend 镜像是当前源码的构建），落在 `docs/screenshots/`：`landing.png` 1440×1978 / `create-result.png` 1440×652（裁 hero 块）/ `dashboard.png` 1440×1595 / `link-detail.png` 1440×3117 / `confirm-dialog.png` 1440×900 / `mobile-dashboard.png` 390×2174 / `mobile-menu.png` 390×470，合计 **704KB**。采集用无头 Chrome + CDP（**零 npm 依赖**，只复用 `frontend/e2e/cdp.mjs` 的 `launchChrome`，另补 `Page.captureScreenshot` / `Emulation.setDeviceMetricsOverride` / `setScrollbarsHidden` 与 `Input.dispatch*`）。**图里的数据是真的**：演示账号建 4 条带标签短链、真打 30 次跳转，趋势图与明细就是这些点击，不是 mock 数字。两处刻意为之：`create-result` 必须在**未登录态**采（登录态后端不回 `manage_key`，结果卡根本不渲染）；`confirm-dialog` 只按 Esc、不点确认（不真删演示链接）。顺手复核了 B5 那两处修复在真实页面上的样子：一次性管理密钥默认打码（点「显示」才明文）、明细 IP 只到 `/24` 网段；`mobile-menu` 用的是 B4 修好的面板（390px 视口下 0…390 × 64…844 铺满，不是悬空一块）。另加 `.dockerignore` 的 `docs` 一行，并用 scratch 探针**实测**排除生效：当前构建上下文 **841.71kB**（`docs/screenshots` 自己就有 704KB，可见它确实不在其中）；往 `docs/` 塞 1MiB 后 `COPY` 层**仍命中缓存**（上下文摘要未变），而同样 1MiB 放进 `deploy/` 会让上下文涨到 **1.06MB** 且该层重建 —— 正反对照。**门禁**：eslint / vue-tsc / audit:refs / build 全 0，vitest **56**，e2e **39 / 39**，冒烟 **28 / 28**；重建 frontend 镜像后，容器里被服务的 **25** 个资源与重建前**逐字节一致**（三方一致：容器 `.Image` == tag `.Id` == manifest list `d5a598fb`）；推送后 CI 同样全绿（[run 35566729860](https://github.com/Elari39/AshenCourier/actions/runs/35566729860)，`frontend` 48s / `backend` 54s / `smoke` 1m47s —— 冒烟 job 里的 `docker compose up -d --build` 用的正是这份排除了 `docs` 的上下文） | 本机（Docker + 无头 Chrome）+ CI `frontend` / `backend` / `smoke` |
| **部署 ㉝**：CDN 前置下的真实客户端 IP（本轮审计修复） | **线上实测确认了缺陷**：建链并跳转一次后，`GET /api/links/{code}/clicks` 返回 `ip = 172.71.158.0/24`（第二次独立复现是 `172.71.154.0/24`），落在 Cloudflare 的 `172.64.0.0/13` 内，而访客并非从 CF 访问 —— 即 `$remote_addr` 是 CF 边缘节点的地址，后果是「按 IP 的限流配额被全网共享」+「明细/日志 IP 失真」。修法：`nginx.conf` 用 real_ip 模块从 `CF-Connecting-IP` 还原访客地址（15 个 IPv4 + 7 个 IPv6 段，取自 <https://www.cloudflare.com/ips/>，并拉官方 `ips-v4` / `ips-v6` **逐条比对过：22/22 完全一致，无多无缺**），并新增守卫 `internal/httpx/realip_test.go`。**本机（前面没有 CDN）能验的是「不改变既有行为」**：没有 CF 段的对端 ⇒ `set_real_ip_from` 不匹配 ⇒ `$remote_addr` 仍是 Docker 网关；重建 `frontend` 镜像后 `nginx -t` 为 `syntax is ok`，e2e **39 / 39** 紧接冒烟 **28 / 28**。**变异验证 3 条 + 1 条负对照**：删掉全部 `set_real_ip_from` → `TestNginxRestoresRealClientIP` 红；`real_ip_header` 换成客户端可伪造的 `X-Forwarded-For` → 红；删掉 `proxy_set_header X-Real-IP $remote_addr` → 红；只改注释（负对照）→ 仍绿；三条还原后 sha256 与基线逐字节一致，并**独立读文件**复核了配置仍在场（不只信脚本自证）。⚠️ **该配置只在真实 CF 前置下才真正生效**，部署后需复验一次：把明细里的 IP 与访客实际出口比对。运维侧的两件事写在「CDN 前置部署」一节（源站只允许 CF 段访问；CF 段增删要同步） | 本机（Docker，`frontend` 镜像重建，单平台 manifest `cbdbcc56`）+ 线上（缺陷复现） |
| **部署 ㉞**：删掉 10 条幽灵 SPA 路由，并让保留字的 404 说实话（本轮审计修复） | 审计发现 nginx 与 vite **各预留 10 条前端并不存在**的顶级路由（`/logout` `/settings` `/account` `/profile` `/admin` `/about` `/help` `/docs` `/terms` `/privacy`），它们被 `try_files` 兜到 `index.html` ⇒ **「不存在的页面」以 200 返回**，掩盖真实的 404。修法：清单收敛到前端真实存在的 4 条（login / register / dashboard / links），并给守卫加一条「**不多不少**」的不变量（`routes_sync_test.go` 现在同时读 `src/router/index.ts`、`nginx.conf` 与 `vite.config.ts` 做三向比对）。**容器实测**（全部重建镜像后）：4 条真实路由仍 **200** + SPA 外壳（含 `/links/AbCdEf`）；10 条幽灵路由全部 **404**；`/metrics` 仍由 nginx 回 404、`/healthz` 仍 200；形态非法的 `/zzzzzzz` 仍是「这条短链不存在」—— 幽灵路由返回码不正确的条数 = **0**。**顺带**：删掉占位后保留字会真的落到后端，而那里原本对保留字也说「这条短链不存在」—— 改成「页面不存在」（访问者找的是页面，不是短链）。**⚠️ 同时纠正了自己的一条审计结论**：`reserved.go` 分两组，`admin`/`about`/`terms` 等属「**易被误用 / 品牌与合规页**」的**刻意预留**，不是「白占短码」—— 所以本轮**只删 nginx/vite 的占位，保留字表一条没动**。**变异验证 3 条 + 1 条负对照**：加回一条幽灵路由 → 红；删掉一条真实路由 → 红（下限探针）；vite 白名单少一条 → 红；只改注释 → 绿；还原后两文件逐字节一致。e2e **39 / 39** 紧接冒烟 **28 / 28**。⚠️ 过程中踩到两个「变异脚本自己撒谎」的坑（替换锚点漏了行尾注释、以及用字面 `\n` 匹配 CRLF 文件），两次都表现为「测试没守住」的**假结论** —— 已按「变异必须先确认真的改到了东西」修掉 | 本机（Docker；`backend`/`worker`/`frontend` 全部重建，两个后端 tag 时间戳一致 `2026-09-22T04:19:11Z`） |
| **部署 ㉟**：SPA 外壳缓存头 + 全站安全响应头（本轮审计修复） | 修的是审计里 A2 与 A4 两条，都属于「部署后才暴露」：CI 每轮都是干净浏览器，测不出「老外壳 + 已失效的哈希资源」，也测不出「响应头静默消失」。**① 外壳缓存头**：新增 `location = /index.html` 一条，带 `Cache-Control: no-cache always`。它能覆盖全部入口靠的是 try_files 语义 —— **最后一个参数是内部重定向**，nginx 会拿 `/index.html` 重新匹配 location，所以外壳只有一个出口、新增 SPA 路由自动生效。实测（`/`、`/login`、深链 `/links/AbCdEf`、`/index.html` 四条）全部 `CC=['no-cache']` **且恰好一条**。**② 安全头**：5 条（HSTS / X-Frame-Options / nosniff / Referrer-Policy / CSP）收进新文件 `deploy/nginx/security-headers.conf`，在 server 层与每个自带 `add_header` 的层级 `include`。带 `always` 实测在 200 / **401** / **404**（含 nginx 自己的 `/metrics` 404）上都出现。**③ CSP 直接强制、不是 Report-Only**：真浏览器 e2e **39 / 39** 且「全程零 console 错误」那条绿 —— CSP 违规会在控制台报错，所以这一次的浏览器验收同时是 CSP 的实测。`style-src` 用**哈希**（从 `pageCSS` 常量算，非 `'unsafe-inline'`），只为放行口令页 / 失效页那段内联 `<style>`。**④ 过程中实测到并记录的三个事实**：(a) 代理响应上 `X-Content-Type-Options` 是**两条**（应用 + nginx），取值相同、刻意保留；(b) `/assets/` 上 `Cache-Control` 本来就是**两条**（`expires 1y` + `add_header`，改动前就如此），而 **Go 的 `Header.Get` 只看第一条** —— 第一版冒烟断言因此把正确配置报成「没有 immutable」，加了 `headerValue`（拼接全部取值）后转绿；(c) `nginx.conf` 是 CRLF、其余文件是 LF，两者各自一致、没有混合。**⑤ 新增 5 条守卫**：`internal/httpx/nginx_headers_test.go`（解析 `nginx.conf` 的**块结构**，断言「任何含 `add_header` 的块里都有那个 include」+ 外壳 `no-cache` 在场 + 每条头都带 `always`）与 `internal/handler/nginx_headers_contract_test.go`（CSP 哈希与 `pageCSS` 一致；`referrerPolicy` 与 nginx 逐字一致 —— 因为 nginx 的 `add_header` 追加在上游之后，重复的 Referrer-Policy 以最后一条为准，不一致时 Go 那行是死代码）。**变异验证 6 条 + 1 条负对照，全部按预期**：删掉 Referrer-Policy / CSP 去掉 `always` / `/assets` 块漏 include / 外壳缓存头改成可长期缓存 / `pageCSS` 改了哈希没跟 / Go 与 nginx 的 Referrer-Policy 不一致 —— 六条各自只红对应的用例；只改注释的负对照仍绿；还原后四份文件逐字节一致并**独立读文件**复核。**验收**：`gofmt` / `go vet` / `go test ./...` 全绿；`nginx -t` syntax is ok；e2e **39 / 39** 紧接冒烟 **31 / 31**（+3：外壳 no-cache、三种状态码上的安全头、内容哈希资源同时有 immutable 与安全头）；`backend`/`worker`/`frontend` 全部重建，两个后端 tag 时间戳一致（`2026-09-22T04:39:24Z`），并且**三方一致**（容器 `.Image` == 镜像 tag `.Id` == 构建日志里的 manifest list：`frontend` `e88aa49e` / `backend` `09bddc99` / `worker` `01113067`）。⚠️ **单向门提醒**：HSTS 一旦被浏览器记住，在 `max-age` 内撤不回来，所以刻意不加 `preload`；线上是 CF 提供 TLS 才敢带 `includeSubDomains` | 本机（Docker + 无头 Chrome） |
| **后端 ㊱**：默认拒绝内网 / 回环目标（本轮审计修复 A5） | 公网短链被拿来把访问者的浏览器指向 `127.0.0.1` / `192.168.x` / 云元数据端点 `169.254.169.254` 是最廉价的一类滥用，而原先只校验 scheme。**分层是关键**：新增 `NormalizePublic(raw, allowPrivate)` 叠在纯语法层的 `Normalize` 之上，`IsAllowedTarget`（跳转路径）**一点没动** —— 收紧创建只是「以后不许再建」，若语法层也跟着拒，策略收紧前建的历史链接会在某次部署后成片失效（那不是安全，是摧毁数据；`TestNormalizeStaysPure` 把这个前提钉住了）。**判定只针对字面量与保留名，不做任何 DNS 解析**：创建路径不该引入网络依赖，而且应用层也拦不住（`evil.example` 可以解析到内网，也能用「先解析到公网、随后改指内网」的 rebinding 绕过）—— 所以它是「挡住最廉价的一类滥用」，**不是 SSRF 防护**，这条边界已写进「已知限制」。**宽松写法是本批的重点**：`127.1` / `2130706433` / `0x7f.1` / `0x7f000001` / `0177.0.0.1` / `127.0.0.1.` 指向的都是同一个地址，而 `netip.ParseAddr` 一个都识别不出来（inet_aton 语义要自己实现）；`::ffff:127.0.0.1` 必须先 `Unmap` 再比前缀，否则地址族不同、`Contains` 全返回 false。表 **26 段**（RFC 6890 特殊用途 + 云元数据 + 已弃用的 6to4/NAT64）+ **5 个保留域名后缀**（`.localhost` / `.local` / `.localdomain` / `.internal` / `.home.arpa`）。**容器实测 10 / 10 全绿**：6 条被拒（点分十进制 / 单整数 / RFC1918 / 云元数据 / IPv6 回环 / `localhost:3000` 缺 scheme）全部 **422 `invalid_url` + `field=target_url`**；正对照公网目标仍 **201** 且跳转 **302 Location 正确**；`PATCH` 改成云元数据同样 422 且**数据未被改动**（只拦创建拦不住「建完再改」）。⚠️ 该验证脚本必须先跑 —— 被拒的请求同样消耗 per-IP 创建配额。**新增 7 条守卫**：validator 表驱动 4 个（`TestIsPrivateHost` 60+ 子用例，含段边界 `172.32.0.1` / `100.128.0.1` / `9.255.255.255` 与「`08.8.8.8` 不是合法八进制、不该误伤」；`TestNormalizePublic`；`TestNormalizeStaysPure`；`TestNonPublicPrefixesAreSane` 是前缀表本身的下限守卫）+ handler 1 个 + service 2 个（开关两侧 + `Update` 也要拦）。**变异验证 6 条 + 1 条负对照全部按预期**：策略整体失效（`false && IsPrivateHost(...)`）/ 去掉 `Unmap` / 只认规范 IP / 保留域名表漏掉 `.internal` / 前缀表漏掉 `169.254.0.0/16` / `Update` 退回纯语法层 —— 各自只红对应用例，只改注释的负对照仍绿；还原后两份文件逐字节一致。**⚠️ 本批最大的坑不在产品而在验收工具**：新策略把 **e2e 自己的播种**也挡住了 —— 它的目标刻意是本栈的 SPA 路由（口令解锁那步要断言「真的落到目标」），也就是 `localhost`。修法**不是**放宽策略，而是让 e2e 的**浏览器侧整体**换一个「对服务端是普通域名、对 Chrome 被 `--host-resolver-rules` 映射到回环」的名字（`e2e.ashen.test`，RFC 2606 保留 TLD）—— 这样 CI 验收的仍是**默认最严配置**，不必为了跑测试打开 `ALLOW_PRIVATE_TARGETS`。过程中连着踩两个坑：① 只换目标、不换页面源时，**CSP 的 `form-action 'self'` 正确拦下**了「表单提交后重定向到别的源」，症状是提交口令后 `net::ERR_ABORTED`、既不导航也不报错（CSP 违规走 Log 域、不进 `console.error`，「零 console 错误」照样绿），看着像 303 没生效 —— 所以浏览器侧的 origin 必须整体统一；② 本机配了系统代理时 Chrome 会把 `e2e.ashen.test` 交给**代理**解析（`--host-resolver-rules` 只管 Chrome 自己的解析），实测症状是目标页变成「HTTP ERROR 502」，加 `--no-proxy-server` 才通（CI 没有代理，但两边行为必须一致）。**门禁**：`gofmt` / `go vet` / `go test ./...`（16 包）全绿；前端 eslint 0 / vitest **56** / `audit:refs` ✓ / vue-tsc 0 / build ✓；e2e **39 / 39** 紧接冒烟 **31 / 31**；`backend`/`worker`/`frontend` 全部重建，两个后端 tag 时间戳一致（`2026-09-22T05:06:40Z`） | 本机（Docker + 无头 Chrome） |
| **后端 ㊲**：`/api` 命名空间的错误体契约 + 登录失败的三档语义（本轮审计修复 A6 / A8） | **① A6**：`GET /api/<未知路径>` 原先回 `net/http` 默认的纯文本 `404 page not found`（19 字节，`text/plain`），而 README 承诺「失败响应统一为 `{"error":{…}}`」、前端 `src/api/client.ts` 也按 `error.code` 分支 —— 契约在**未注册路径**上不成立。改法刻意**不是**注册一条 `/api/` 兜底模式：不带方法前缀的模式匹配**任意方法**，于是 `PUT /api/links` 会从 405 变成 404（`TestRouterMethodAwareness` 正是盯着这一点，而 405 的 `Allow` 头是客户端唯一能知道「该用哪个方法」的地方）。改为在**响应侧**改写：`internal/httpx/APIErrorContract` 只认「状态码 ∈ {404,405} 且 `Content-Type` 是 `http.Error` 的默认值 `text/plain; charset=utf-8`」这个组合（本包自己的 404 是 `application/json`、短码失效页是 `text/html`，因此不会误伤），命中就换成本包的 JSON 错误体并**吞掉**随后那句纯文本。容器实测：未注册路径 404 `not_found`、方法不对 405 `method_not_allowed` **且 `Allow` 原样保留**（`PUT /api/links` → `Allow: GET, HEAD, POST`）、错误体**带 `request_id`**（说明改写发生在 `RequestID` 中间件之内）；作用域实测：`PUT /healthz` 仍是 `text/plain`、`GET /zzzzzzz` 仍是 HTML 失效页。**② A8**：登录失败原先一律 401 `unauthorized` +「登录状态无效，请重新登录」—— 在登录页输错密码会被提示去清 cookie，且与「该回登录页」的 `unauthorized` 同码同因。改为三档：字段级（邮箱格式 / 空口令）走 422 + `field`（**与注册同口径**，且判定在任何查库动作之前，不引入「账号是否存在」的耗时侧信道）、凭据错误用新的 `domain.ErrInvalidCredentials` → 401 `invalid_credentials` +「邮箱或密码不正确」（两条失败路径文案完全一致，仍是防枚举）、未认证保持 `unauthorized`。**刻意不在登录侧校验口令强度**：策略是注册侧的事，将来上调 `MinPasswordLength` 会让老用户被自己的合法密码挡在登录页外（`TestLoginValidationIsNotRegisterPolicy` 钉住）。**③ 连带修掉客户端一处「注释描述了、代码没实现」的保护**：`client.ts` 原本只在 `token !== null` 时不登出，与「登录接口的 401 不算会话过期」只是**间接**成立（靠 `/login` 的 `guestOnly` 守卫）；现在按 `code !== 'invalid_credentials'` 判定，将来去掉守卫或加「重新验证身份」流程都不会踩坑，并补了 3 条 vitest（`src/api/client.test.ts`）。**④ 新增守卫 9 条**：`httpx/api_errors_test.go` 3 个（含直接盯「吞掉纯文本」与「重复 `WriteHeader` 被丢弃」的两个细节用例，且刻意用**真实 ServeMux** 而不是手写模仿）、handler 2 个（`TestRouterAPIErrorContract` / `TestRouterKeepsNonAPIErrorBehavior`，前者同时是「Go 改了内建错误写法就会红」的守卫）、`service/auth_test.go` 5 个（此前 `auth` **完全没有单测**）、前端 3 条 vitest。**变异验证 7 条 + 1 条负对照全部按预期**：摘掉 `APIErrorContract` / 只改写 404 不管 405 / 不吞纯文本 / `isAPIPath` 放宽成前缀匹配 / 登录不校验邮箱格式 / 「邮箱不存在」退回 `ErrUnauthorized` / 映射表删掉 `ErrInvalidCredentials` —— 各自只红对应用例，只改注释的负对照仍绿；还原后 4 份文件逐字节一致并**独立读文件**复核。**验收**：`gofmt` / `go vet` / `go test ./...`（16 包，其中 handler 与 service 非缓存）全绿；前端 eslint / vitest **59**（+3）/ `audit:refs` ✓ / vue-tsc / build 全绿；容器级 **17 / 17**；e2e **39 / 39** 紧接冒烟 **34 / 34**（+3）；`backend`/`worker`/`frontend` 全部重建，两个后端 tag 时间戳一致（`2026-09-22T05:38:19Z`） | 本机（Docker，全部重建） |
| **后端 ㊳**：`/healthz` 收敛为存活/就绪子集，完整诊断移到 `/healthz/details`（本轮审计修复 A3） | 修的是「同一批数字两套可见性」：`/healthz` 匿名可达，却把与 `/metrics` **同一次采集**的完整诊断一起回了出去（`version` 版本指纹、`uptime_seconds` 部署节奏、`stream_len` / `stream_pending` 积压水位、`pg_fallbacks` 回源次数、丢失计数、是否内嵌 worker、限流是否降级），而 `/metrics` 在 nginx 里被显式 404、README 还专门用一节论证「不对外」。改法**不是在 handler 里做 IP 过滤** —— 这个端点必须继续被编排系统使用（api 镜像的 `HEALTHCHECK` 走 `api -healthcheck`，只看状态码、从不解析响应体），而是把响应体裁成两个面：`GET /healthz` 只回 `status` / `postgres` / `redis`，新增 `GET /healthz/details` 回完整快照，**与 `/metrics` 同策略**（nginx 里 `= /healthz/details { return 404; }`，不加鉴权也不限流，只在内网可达）。两者在 degraded 时语义各自成立：`/healthz` 回 **503 + `Retry-After`**（编排系统据此不接流量），`/healthz/details` 仍回 **200**（诊断快照的职责是把 degraded 那一刻的数字交出去，回 503 会让脚本与监控把整份响应丢掉）。**新增 2 条守卫，都刻意用「能挡住将来」的写法**：`handler/healthz_surface_test.go` 用**键集合白名单**断言公开面恰好三项（黑名单挡不住将来新增的字段），且用一份「所有诊断字段都非零」的探针 —— 否则字段自带的 `omitzero` 会让「不该有 version」因为 version 本来就是空串而必然通过（一个永远绿的守卫）；`httpx/nginx_surface_test.go` 解析 `nginx.conf` 的**块结构**，断言 `/metrics` 与 `/healthz/details` **各自恰好一条** `location = … { return 404; }`，并做一条**反向断言**（`/healthz` 必须仍在 `proxy_pass`、不是 404）—— 少了它，一个「顺手一刀切」的改动会让容器永久 unhealthy 而无人发现。⚠️ 反向断言必须用**精确匹配**（为此新增 `exactBlocks`）而不能复用已有的 `findBlocks`：后者用 `HasPrefix`，而 `location = /healthz` 恰好是 `location = /healthz/details` 的前缀，会把两个端点混成一对（守卫自己算错比漏测更难发现）。**变异验证 2 条全部按预期**：把 `report.Public()` 退回完整 `report` → 键集合白名单红并逐条打印泄漏的字段名；删掉 nginx 那一行 → 「应当**恰好一条**，实际 0 条」红；还原后两份文件 sha256 与基线**逐字节一致**（另起进程独立读文件复核，不只信脚本自证），两条守卫复绿。**验收**：`gofmt` / `go vet` / `go test ./...`（16 包）全绿；`backend`/`worker`/`frontend` 全部重建，两个后端 tag 时间戳一致（`2026-09-22T06:03:47Z`）；e2e **39 / 39** 紧接冒烟 **36 / 36**（+2，正是上面这两条新检查：`/healthz` 只回三项、内部诊断端点经 nginx 回 404） | 本机（Docker，全部重建） |
| **后端 ㊴**：`SQLSTATE` 分类 —— 连接类 / 资源类回落 503（本轮审计修复 B3，定性后 E1） | 审计里 `storageError` 的判据是「**凡带 SQLSTATE 就算库健康**」→ 500，于是**过载**（`53300` 连接数打满）与 **PG 正在关停/启动**（`57P01` / `57P03`）这两个窗口里站点回 500 而不是 503 + `Retry-After`，客户端与上游不会退避、反而加重拥堵 —— 恰好发生在最需要退避的时刻。**先定性再动手**：把 PG 的 `max_connections` 由 100 改成 **5** 并重启，再用 **128 路并发**打 `/api/auth/me`（每条都要向 PG 取用户，api 连接池 `MaxConns=16` 被迫扩容）。**修前实测捕获 1 条 500**，后端日志根因正是 `FATAL: sorry, too many clients already (SQLSTATE 53300)` —— 审计的 E2 由此升到 E1。**改法刻意按「类」而不是逐码枚举**：类 08（connection_exception）、类 53（insufficient_resources，含 `53300`）、`57P01`/`57P02`/`57P03`（服务端关停 / 启动中）三类算「暂时不可用」—— 逐码枚举会在服务端新增码时悄悄退回 500，正是这个缺陷本身；**刻意不含** `40001` serialization_failure / `40P01` deadlock_detected（那是「库健康、这次事务撞车了」，正确做法是应用层重试整个请求；回 503 会让上游连累同一实例上与之无关的请求一起退避），类 57 也只认那三个码（`57014` query_canceled 多半是 statement_timeout、`57P04` database_dropped 成因不同）。**修后同一压测**：128 路里 **5 条 503**、`Retry-After: 2`、响应体 `{"error":{"code":"unavailable",…}}`，日志变成 `dependency unavailable: … (SQLSTATE 53300)` 且 `"status":503`。⚠️ 503 仍按既有的 `levelFor` 规则记 **ERROR**（5xx→error / 4xx→warn）—— 这是**刻意**的既有设计，不是本轮引入，改它会淹没真正的 500。**新增守卫 2 个**：`TestStorageError` 扩到 **20** 个子用例（9 条原有 + 6 条带 SQLSTATE 的正例 + 5 条负例），`TestPgUnavailableCode` 用 **26** 个子用例把这张表本身钉住 —— 后者专盯两个容易写错的方向：**子串**匹配（`22008` 含 `08` 但类不是 08）与**整类 57**（`57014` / `57P04` 必须为 false）。**变异验证 2 条**：`storageError` 退回「凡带 SQLSTATE 即上抛」→ 只红 6 条正例；`pgUnavailableCode` 放宽成「任何非空码都算」→ 只红负例；两次都先跑 `go vet` 确认变异体本身能编译 —— 避免把「变异体自己写坏」误读成「断言没守住」（验收工具的一类老坑）。**验收**：`gofmt` / `go vet` / `go test ./...`（16 包）全绿；`backend`/`worker` 重建，两个 tag 时间戳一致（`2026-09-22T06:13:27Z`）；PG 的 `max_connections` 已 `ALTER SYSTEM RESET` 并重启复核回 **100**；e2e **39 / 39** 紧接冒烟 **36 / 36** | 本机（Docker，`max_connections` 实测 100 → 5 → 100） |
| **部署 ㊵**：镜像 tag 漂移守卫（本轮审计「已知缺口」第 2 条） | 这一条原本是「**两个镜像 tag 之间的漂移没有守卫**」：api 与 worker 共用一份 Dockerfile，但 compose 会产出两个 tag，于是 `docker compose build backend` 是完全合法的命令，而 worker 会继续跑旧二进制 —— 2026-09-21 实际踩到过（worker 停在两天前的构建，而所有测试与 CI 都是绿的）。新增 `deploy/check-image-drift.mjs`（零 npm 依赖、**刻意不经 shell**：本机 Git Bash 会吃掉 `--format '{{.Created}}'` 这类单字段模板的花括号，把模板变成字面量 `.Created`），两条判据：① **两个 tag 的 `RootFS.Layers` 逐项相等** —— 内容层是内容寻址的（COPY 进去的二进制一变，那层的 `diff_id` 就变），因此与构建时间、缓存状态、构建顺序都无关；**刻意不比 image id**，因为 compose 给每个服务加了自己的 label（`com.docker.compose.service=…`）而 label 属于镜像 config，两个 id 天生就不同（实测 id 一直不等、层一直相等）；② **容器跑的就是当前 tag**（挡「build 了但没 `up -d`」—— `up -d` 只重建**镜像 ID 变了**的容器，漏掉时 `docker compose ps` 仍是 `healthy`）。已接进 CI 的 `smoke` job 作回归网（防 compose 文件被改成「两个服务不再同源」）。**变异验证 2 条 + 1 条负对照**：把 worker 的 tag 用 `docker tag` 指到 frontend 镜像 → 层数 **14 vs 11** 红（同轮也报出「容器 ≠ tag」）；把 backend 的 tag 指到 worker 镜像 → **只**红「容器 ≠ tag」（两者层本来就相等，判据 1 正确保持沉默 —— 这一条专门证明两条判据彼此独立、不是一条判据的两种说法）；还原后两个 tag 的 `.Id` 与变异前**逐一相等**且守卫复绿（负对照）。⚠️ **第一次变异跑出了假绿，值得记**：脚本最初把 `docker compose ps --format json` 的 `Image` 字段当 tag 用，而那个字段给的是「**容器实际镜像的引用**」—— tag 一旦被指到别的镜像，compose 就把该字段解析成镜像 ID，于是「容器 == tag」退化成恒真，守卫报「无漂移」。这正是「永远绿的守卫」那一类病（与 ㊳ 里 `omitzero` 那条同源）；改为从 `docker compose config` 推导 `<project>-<service>` 后立刻转红。**刻意记录边界**：本守卫探测不到「**两个都旧**」（改了源码、两个 tag 都没重建）—— 那时两者层仍相等、容器也仍与 tag 相符，内部自洽；要覆盖它得比源码与产物的时间，属于另一件事，不做 | 本机（Docker；用 `docker tag` 造漂移，实测 id 均已还原） |
| **部署 ㊶**：构建期注入镜像版本号（本轮审计修复 A9） | 审计在线上实测到 `/metrics` 的 `ashen_build_info{version="dev"}` —— 承诺的「版本可定位」落空。**机制其实一直都在**：`backend/Dockerfile` 有 `ARG VERSION=dev` 并把它落到 `-ldflags "-X main.version=${VERSION}"`，`cmd/api/main.go` 也把它接进了 `HealthReport.Version`；缺的只是**没人给它传值** —— compose 的 `build:` 段只写了 `context`，从没有 `args`。改法顺带把「两个服务同源」写进配置本身：新增 `x-backend-build` 锚点（`context` + `args.VERSION`），backend 与 worker 都 `build: *backend-build` —— 将来改 context / args 只可能改这一处，不会只改一半（与 ㊵ 的守卫互补：一个是发现漂移，一个是让漂移难以发生）。值取自 `.env` 的 `APP_VERSION`，不填 = `dev`（本地开发的合理默认）。**实测**：`APP_VERSION=0.9.9-a9test docker compose build backend worker` 后，容器内读 `/healthz/details` 的 `version` = `0.9.9-a9test`、`/metrics` 的 `ashen_build_info{version="0.9.9-a9test"}` 一致；随后按推荐方式用 `APP_VERSION=$(git describe --tags --always)` 重建，两者都变成当前短 sha（本仓库无 tag，故退化为 `10fed74`）。⚠️ **验证方式本身有个细节**：这两个端点现在都**不对外**（nginx 里 404），所以只能从同一网络内的容器读 —— `docker compose exec frontend wget -qO- http://backend:8080/metrics`。**验收**：`docker compose config` 校验通过且 backend / worker 都拿到 `VERSION`；漂移守卫绿；e2e **39 / 39** 紧接冒烟 **36 / 36** | 本机（Docker；`APP_VERSION` 实测 `dev` → `0.9.9-a9test` → `10fed74`） |
| **部署 ㊷**：容器层资源上界（本轮审计修复 B5） | 审计的一条是「compose 里 `mem_limit` / `deploy.resources` **全缺**」—— README 把「跳转 / 统计」的内存面控得很细（有界队列 + 队满即丢 + 计数），唯独**容器这一层**一个上界都没有：于是任何一处失控（内存泄漏、连接暴增、一次异常大响应）都会吃满**宿主**内存，连带把同机的 PostgreSQL 拖进 OOM —— 症状是**整站不可用**，而不只是坏掉一个容器。改成 5 个常驻服务各带上界：postgres `1g` / 2 CPU、redis `512m` / 1 CPU、backend `512m` / 2 CPU、worker `512m` / 1 CPU、frontend `256m` / 1 CPU。**取值不是拍脑袋**：先量空闲（`docker stats --no-stream`）backend 约 3–7M / worker 约 3M / frontend 约 8M / postgres 约 27–36M / redis 约 6–13M —— 同一次 `up -d` 后不同时刻量到的值本来就会变（日志缓冲、GC 时机、连接数都会动），所以给的是**量级上的上界**（对空闲值留 30 倍以上余量），要求只有两条：真实负载下跑得住；真失控时表现为**这一个容器被 kill**。**为什么用 `deploy.resources.limits` 而不是 `mem_limit` / `cpus`**：两者在本机 compose v2 下都会生效，但前者是 Compose 规范的写法、只写一处 —— 而且**实测确认它真的被应用**（见下），不靠文档承诺。**配套的 `GOMEMLIMIT` 才是关键的一半**：容器硬上限单独存在时 **Go 运行时并不知道它**，堆会一路涨到被 cgroup **OOM kill**，表现是「进程无故重启」、日志里没有任何 Go 侧线索；`GOMEMLIMIT`（取容器上限的 ~90% ⇒ `460MiB`，与 backend / worker 的 `512m` **耦合**，改一处要改两处）让它在触界前先做 GC：代价是 GC 变频繁（CPU 涨），收益是从「被杀」变成「变慢」——而变慢是可观测、可告警的。**验 `GOMEMLIMIT` 真的被消费**（而不是「设了但没人看」）：`GOMEMLIMIT=bogus` → 进程在 init 阶段直接 panic（`runtime.gcinit` 解析失败），证明这个变量确实被运行时读取。**落地证据**：`docker inspect --format '{{.HostConfig.Memory}} {{.HostConfig.NanoCpus}}'` 五个服务分别 `536870912/2000000000`、`536870912/1000000000`、`1073741824/2000000000`、`536870912/1000000000`、`268435456/1000000000`；`Config.Env` 里 backend 与 worker 都有 `GOMEMLIMIT=460MiB`（distroless 镜像里**没有 `env` 二进制**，只能这样看）。**两处刻意不做**：① **不加 `pids_limit`** —— backend / worker 是单进程 Go（几乎不 fork），而 PostgreSQL 在 `max_connections=100` 时会**合法地**用掉 100+ 进程，一个统一的 `pids_limit` 要么形同虚设、要么在正常负载下误杀 PG；守卫一个具体数值容易，守卫「为什么是这个数值」很难。② **redis 刻意不设 `--maxmemory`** —— Stream 里是**尚未 ACK 的点击**，一旦配上淘汰策略（就算 `noeviction`，写失败同样会丢新点击），症状就从「内存被限住」变成「静默少记数据」；内存面在别处已经收过口（`XADD ... MAXLEN ~ 100000` 兜住 Stream），这里只兜住容器。**验收**：`docker compose config` 通过；5 容器全部 `healthy`；e2e **39 / 39** 紧接冒烟 **36 / 36**（在带限后的容器上重跑，不止「配置解析通过」） | 本机（Docker；上界经 `docker inspect` 实测，非文档承诺） |
| **部署 ㊸**：CDN 改写 `max-age`（本轮审计 A10 —— 只能文档化） | 审计实测：源码 `handler/qr.go` 写的是 `public, max-age=300`，客户端收到的是 `max-age=14400`；对一个**从未存在过**的短码取图，它的 404 响应**也**带同一个值 —— 而后端的 404 分支**根本不设** `Cache-Control`，所以这个值不是后端产生的（`/assets/*` 的 `31536000` 被原样保留，说明 CF 是「抬高偏小的值」而不是统一覆盖）。影响：`qr.go` 里「域名改名 / 短链被删后最多 5 分钟换图」的设计意图，在线上实际是 **4 小时**（48 倍）。**这条审计项在仓库里修不了** —— 它由 Cloudflare 面板的 *Browser Cache TTL* 决定，改它需要登面板（改成 *Respect Existing Headers*）。所以**处置 = 文档化 + 把它提成一条可执行的运维项**：原本它只是「CDN 前置」一节末尾的一段引用块（读的人容易略过），现在并进「必须自己做的运维事」清单第三条，与「封源站」「同步 CF 段」同级，并写清症状（陈旧二维码 ≠ 删不掉，源站确实回 404 —— 见 §4.2 的实测）。**为什么不改源码把值调成 14400 去「对齐线上」**：那是把 CF 的改写行为当成设计意图固化下来，等于承认「5 分钟」那条注释是假的 | 本机（文档）；CF 面板侧未改（如实记录） |
| 计数一致性 | `link_click_totals` 中 `base_count <> event_count` 的链接数 = 0；`clicks:dirty` 与 `clicks:cnt:*` 回刷后清空 | 本机 |
| Stream 消费 | `/healthz/details` 不含 `stream_pending`（零值 ⇒ 0 pending）；worker 日志无 `"msg":"http"` 记录（确认跑的是 worker 而非 api） | 本机 |

⚠️ **「计数一致性 = 0」这条要这样读**：它指的是**只经过跳转路径**（`GET /{code}`）产生的点击。
`容器级 ⑰` 的 GeoIP 验收是**手工往 Stream 投显式 ID 的消息**，那条路径绕过了跳转里的
`INCR clicks:cnt:{code}`，于是那些明细永远追不上基线 —— 那个库现在能查到 1 条
`base_count = 0 / event_count = 4` 的 `geoacc1`，就是这个手工注入留下的。**它不是缺陷**：
补偿式计数的方向是「宁可重复累加也不丢」，所以不变式是 `base_count >= event_count`，
`base_count < event_count` 只可能来自「绕过跳转路径的写入」。复跑这条验收时请手工排除
这类造出来的行，否则会假报红。

⚠️ **做变异验证时，基线必须每次从「当前代码」取。** 2026-09-21 实测到一个更隐蔽的假结论：
复跑变异脚本时它复用了上一次留下的源码快照当基线，而那份快照**早于当轮的改动** ——
于是「变异后仍然绿」其实是基线自己过期了；更糟的是脚本还原时**把当轮的真实改动覆盖掉了**
（`delay.String()` 那两处就是这么丢过一次，靠新加的断言才发现）。
两条对策：① 基线每次都从工作区当前文件刷新；② 刷新前先校验所有变异锚点都在场，
防「上次崩在变异中途、把变异体当成了基线」。
这与本表 ㉔④ 那条是同一类错误（拿旧产物/旧基线去做验证），值得放在一起看。

**自定义域名的本机验收**不需要真域名与证书：`curl -H 'Host: a.local' localhost:8080/{code}` 就能走到后端，因为 nginx 的短码规则是按**路径**匹配的，`Host` 只影响后端把请求算到哪个域上。域名记录目前只能用一条
`INSERT INTO domains (domain) VALUES ('a.local')` 登记（没有管理接口，见「已知限制」）。真上线时除了登记域名，还要在 `deploy/nginx/nginx.conf` 里为该域名加 `server_name` 与证书 —— 那是配置工作，代码侧不用改。

**责任划分**：容器级验收（起全栈 + 浏览器级验收 + 端到端冒烟）由 CI 的 `smoke` job 承担 ——
每次推 `main` 与手动触发（`workflow_dispatch`）都会真跑一遍，失败时自动 dump 容器日志。
PR 只跑 `backend` 与 `frontend` 两个快 job（约 1 分钟），因为 `docker compose up -d --build`
要几分钟。

`frontend/e2e/` 这一步排在 `cmd/smoke` **之前**，不是随意的顺序：创建接口是
10 次/分钟/IP 的硬配额（写死在 `config`，没有对应的环境变量），而 `cmd/smoke` 自己
要用掉约 **7** 次（匿名创建 / javascript 被拒 / 自定义短码 / 重复短码 / 保留字 /
登录用户创建 / 限流那一项的循环），最后一项还会故意把配额打满来断言 429。
留给浏览器验收的余量只有 2 次，所以**本套件只创建 1 条短链**，而且它在套件一开始就建好 ——
等 smoke 跑起来时早已滚出限流窗口。想在浏览器验收里新增一次创建之前，先把这笔账算一遍；
验不到的行为宁可下沉到 vitest（`ResultCard` 的「换链接就收起密钥」就是这么处理的，
见 ㉚）。顺序一旦颠倒，浏览器验收第一步就会拿到 429。

⚠️ **无头 Chrome 里 `:focus` 默认不匹配，必须先开焦点模拟。** B4 验「跳到主要内容」
那条链接时踩到：脚本已经 `el.focus()`，量出来 `top` 却还是 `-100`（那条链接平时就藏在
视口上方 100px、靠 `:focus{top:12px}` 移进来），于是断言看着像「CSS 没生效」—— 但
`dist/assets/*.css` 里那条规则明明在。真正的原因是**无头页面拿不到窗口焦点**：
`document.hasFocus()` 是 false，`:focus` 这个伪类根本不会匹配。
对策是在 `frontend/e2e/cdp.mjs` 的 `launchChrome` 里补一句
`Emulation.setFocusEmulationEnabled { enabled: true }`（放在 `Runtime.enable` 之后）。
这一条影响面不止跳过链接：任何「聚焦后才变样式 / 才进视口」的断言在无头下都会假红，
而任何「聚焦后不该发生什么」的断言则可能假绿。

最近的实测：[run 35564212221](https://github.com/Elari39/AshenCourier/actions/runs/35564212221)
—— B0–B6 六批推上 `main` 之后的验证，三个 job 全绿（`backend` 1m1s / `frontend` 38s / `smoke` 1m29s）。
四个关键证据：`backend` 里 `internal/store/postgres` 跑了 **1.287s**（未设置 `POSTGRES_TEST_DSN` 时
集成测试会整体跳过，那时只有零点几秒 —— 所以它是真的连上了 service 容器里的 PG）；`smoke` 里
**`frontend/e2e/` 39/39 之后紧接 `cmd/smoke` 28/28**（在 runner 自带的 Chrome 上真跑，不靠本机的
`CHROME_BIN` 探测），按 step 分组计数 **39 + 28 = 67**，与日志里那两个 step 的 ✓ 行数相等；
`frontend` 的 vitest **56 个用例 / 6 个文件**全绿；B6 新增的零引用守卫在 CI 里真跑过 ——
日志里是 `✓ 没有零引用的组件 / 样式类，也没有未引用的 token 被源码提到`。

（再往前依次是 [run 35485587539](https://github.com/Elari39/AshenCourier/actions/runs/35485587539)
—— 那时 e2e 还是 19/19、vitest 31 个用例；[run 35484563846](https://github.com/Elari39/AshenCourier/actions/runs/35484563846)
—— N9 `/metrics` 落地的验证；[run 35483953065](https://github.com/Elari39/AshenCourier/actions/runs/35483953065)
—— N8 二维码端点的验证。这四次都是三个 job 全绿。）

**CI 自身也实测过「会红」**（不是只看过绿灯）：其中两次是故意造出来的 —— 故意破坏一个文件的
gofmt → `backend` job 红并列出文件名；故意改错冒烟工具的期望值 → `smoke` job 红、日志里能看到
断言失败与容器日志。**还有一次是自然发生的**：N10 那个纯文档提交把
`TestOpCtxTimesOutWithCause` 的调度依赖断言撞红了（run `35484924568`），于是多了两个教训 ——
「红灯未必由本次改动引起，但必须先查清楚再重跑」以及「本机空转跑不出来的抖动，要把 CPU
压满才复现」，后者写在「九条踩过的坑」第 8 条。
配置见 [`.github/workflows/ci.yml`](./.github/workflows/ci.yml)。

## 许可

[MIT](./LICENSE) © 2026 Elari39

---

<div align="center">
<sub>短码字符集 <code>[0-9A-Za-z_-]</code> · 只允许 http/https 目标地址 · 302 不缓存</sub>
</div>
