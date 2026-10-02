# AshenCourier

把长链接变短，集中管理分享。支持匿名创建、账号认领、访问口令、二维码与点击统计，使用 Vue 3 + Go + PostgreSQL + Redis，可通过 Docker Compose 自托管。

[线上服务](https://shorten.miku831.fun/) · [源码仓库](https://github.com/Elari39/AshenCourier) · [项目详解](https://elari39.github.io/projects/ashen-courier/) · [灰烬女巫的魔典](https://elari39.github.io/)

![首页](docs/screenshots/neo-brutalism/landing-1440.png)

截图来自仓库中的运行记录，重新生成方式见下方浏览器验收说明。

## 目录

- [能力](#能力)
- [技术栈与目录](#技术栈与目录)
- [Docker 启动](#docker-启动)
- [本地开发](#本地开发)
- [配置](#配置)
- [架构与一致性](#架构与一致性)
- [接口](#接口)
- [GeoIP 国家维度](#geoip-国家维度)
- [开发与验证](#开发与验证)
- [部署与运维](#部署与运维)
- [文档与许可](#文档与许可)

## 能力

- 匿名创建、账号注册和登录；登录后可使用管理密钥认领匿名链接。
- 自动生成 7 位短码；自定义短码支持 3–32 位字母、数字、下划线和连字符，并排除保留路由名。
- 标签、搜索、游标分页、链接编辑、停用和软删除。短码全局唯一，已删除短码不复用。
- 网页新建默认 **24 小时**后过期，从提交时起算；可选 1 小时、7 天、30 天、永久或自定义时间。直接 API 创建时不传 `expires_at` 仍表示永久有效。
- 访问口令、二维码展示与 PNG 下载、公开 SVG 二维码接口。
- 点击趋势、来源、设备、浏览器及可选 GeoIP 国家分布；明细 API 对 IP 做网段遮罩。
- 初音青绿 Neo-brutalism 界面，包含移动端布局、键盘操作、焦点管理、加载状态及错误重试。

## 技术栈与目录

| 层 | 当前实现 |
| --- | --- |
| API / worker | Go 1.27.1、标准库 `net/http` ServeMux、pgx v5、go-redis v9 |
| 存储 | PostgreSQL 18、Redis 8；数据库迁移使用 golang-migrate |
| 前端 | Vue 3、Vue Router、TypeScript、Vite 8、Tailwind CSS 4 |
| 测试 | Go 单元与真实存储测试、Vitest、Chrome CDP 浏览器检查、HTTP 冒烟 |
| 部署 | Docker Compose、Nginx；生产入口可接 Cloudflare 与 1Panel OpenResty |

Go 版本以 `backend/go.mod` 为准；前端建议 Node.js **22.12+**，pnpm **11.15.1** 由 `frontend/package.json` 固定。依赖解析版本以锁文件为准。

```text
backend/
  cmd/api/               HTTP 服务，可选内嵌 worker
  cmd/worker/            独立点击消费、计数回刷与过期清理
  cmd/smoke/             真实 HTTP 冒烟工具
  internal/config/       环境变量、默认值与启动校验
  internal/domain/       领域类型与存储接口
  internal/handler/      路由、鉴权、短链与统计接口、口令页
  internal/service/      短链、管理密钥、口令和计数一致性
  internal/store/        PostgreSQL、Redis、GeoIP 实现
  internal/worker/       Stream 消费与后台任务
  migrations/            数据库 up/down 迁移
frontend/
  src/api/               接口客户端与会话处理
  src/views/             首页、登录、注册、看板、链接详情、404
  src/components/        表单、结果卡、图表与通用组件
  e2e/                   浏览器行为、可访问性与截图验收
deploy/                  Nginx、1Panel 示例、GeoIP、隔离验收编排
docker-compose.yml      完整运行栈
docker-compose.dev.yml  仅开发数据库与 Redis，迁移为 tools profile
```

## Docker 启动

需要 Docker Engine / Docker Desktop 与 Docker Compose。在仓库根目录执行：

```bash
git clone https://github.com/Elari39/AshenCourier.git
cd AshenCourier
cp .env.example .env
# PowerShell：Copy-Item .env.example .env
```

编辑 `.env`，填写三个独立值：`POSTGRES_PASSWORD`、`REDIS_PASSWORD`、`JWT_SECRET`。JWT 密钥可用 `openssl rand -base64 32` 生成。`DATABASE_URL` 中的用户名、密码和库名必须匹配 PostgreSQL；密码含 URL 保留字符时，连接串中的密码需要百分号编码。

```bash
docker compose -f docker-compose.yml config --quiet
docker compose -f docker-compose.yml up -d --build
docker compose -f docker-compose.yml ps
```

打开 [http://localhost:8080](http://localhost:8080)，通过 `/healthz` 检查依赖状态。完整栈包含 PostgreSQL、Redis、一次性 migrate、API、独立 worker 和前端 Nginx；迁移成功后才启动后端。

默认只将前端绑定到宿主 `127.0.0.1:8080`。改变访问入口时同步设置 `PUBLIC_BASE_URL`；该值同时参与前端构建，需要重新构建镜像。生产部署见 [1Panel 部署与回滚](docs/DEPLOY-1PANEL.md)。

## 本地开发

开发形态只用 Docker 运行 PostgreSQL 和 Redis，Go 与 Vite 在宿主启动。它与完整 Docker 栈是两种启动方式，不要同时占用 8080。

在仓库根目录启动依赖并迁移：

```bash
docker compose -f docker-compose.dev.yml up -d --wait
docker compose -f docker-compose.dev.yml --profile tools run --rm migrate
```

该开发编排使用固定的 `ashen` 数据库账号与开发口令，仅绑定回环端口；不用于生产。**Go 进程只读取环境变量，不自动加载 `.env`**。PowerShell 7 示例：

```powershell
$env:HTTP_ADDR = '127.0.0.1:8080'
$env:DATABASE_URL = 'postgres://ashen:ashen@127.0.0.1:5432/ashen?sslmode=disable'
$env:REDIS_ADDR = '127.0.0.1:6379'
$env:REDIS_PASSWORD = 'ashen'
$env:JWT_SECRET = [Convert]::ToBase64String([System.Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
$env:PUBLIC_BASE_URL = 'http://localhost:5173'
$env:WORKER_ENABLED = 'true'
$env:TRUST_PROXY = 'false'
Set-Location backend
go mod download
go run ./cmd/api
```

另开终端，在仓库根目录执行：

```bash
cd frontend
pnpm install --frozen-lockfile
pnpm dev
```

访问 [http://localhost:5173](http://localhost:5173)。Vite 代理 `/api`、`/healthz` 和短码跳转到 8080，跳过 `/login`、`/register`、`/dashboard`、`/links` 等前端路由。若 5173 被占用，先释放端口或同步调整基址与代理配置。

`WORKER_ENABLED=true` 启动内嵌 worker，便于本地同时验证统计和过期清理；生产 Compose 使用独立 worker。示例生成的 JWT 密钥只在当前终端有效，重新生成会使旧登录令牌失效。

## 配置

完整入口见 [.env.example](.env.example)、[frontend/.env.example](frontend/.env.example) 与 [后端配置](backend/internal/config/config.go)。Compose 插值文件和 Go 进程环境不是同一层。

| 变量 | 作用与默认行为 |
| --- | --- |
| `DATABASE_URL` | PostgreSQL 连接串，API 与 worker 均必需 |
| `JWT_SECRET` | API 签名密钥，拒绝空值、常见占位值及少于 16 字节的值；建议随机 32 字节 |
| `REDIS_ADDR` / `REDIS_PASSWORD` / `REDIS_DB` | Redis 连接；直跑默认 `localhost:6379`、空口令、DB 0；Compose 强制口令 |
| `HTTP_ADDR` | API 监听地址，默认 `:8080` |
| `PUBLIC_BASE_URL` | 短链对外 origin，默认 `http://localhost:8080`；不可带路径、凭据或查询串 |
| `WORKER_ENABLED` | API 是否内嵌 worker，默认 `false` |
| `TRUST_PROXY` | Go 是否采用 `X-Real-IP`，默认 `true`；宿主直连应显式设 `false` |
| `ALLOW_PRIVATE_TARGETS` | 默认 `false`，创建 / 修改时拒绝内网、回环及保留主机目标；历史链接仍可跳转 |
| `RATE_LIMIT_DISABLED` | 默认 `false`，仅用于限流故障的应急启动开关 |
| `GEOIP_DB_PATH` | 可选国家库路径；未配置时国家字段为空 |
| `LOG_LEVEL` | 默认 `info`，支持 `debug` / `warn` / `error` |
| `FRONTEND_BIND` / `FRONTEND_PORT` | Compose 宿主入口，默认 `127.0.0.1:8080` |
| `APP_VERSION` | Compose 构建期版本标识，进入诊断与指标；修改后需重新 build |
| `VITE_API_BASE_URL` | 前端 API 基址，默认 `/api` |
| `VITE_SHORT_BASE_URL` | 落地页演示短链前缀；真实短链地址由后端返回 |

超时、缓存 TTL、页大小和各接口限流配额属于代码默认值，并非任意同名环境变量都能覆盖。生产 Compose 只传递其中显式声明的变量，增加进程配置时也要核对编排。

## 架构与一致性

```text
浏览器 → Nginx → /api/* → Go API → PostgreSQL
               └ /{code} → Redis 缓存 → 未命中时回源 PostgreSQL
                              │
                         返回 302，投递点击到有界内存队列
                              ↓
                       Redis 活动计数 + Stream
                              ↓
                  worker → 固定批次计数回刷 / 明细入库
```

跳转路径不同步写 PostgreSQL；同一域与短码的并发缓存未命中由 singleflight 合并。队列默认容纳 4096 次点击，满时丢弃并计数，统计写入失败也有独立指标。正常跳转使用 302 和 `no-store`，避免浏览器缓存固定目标。

计数回刷采用 **Redis 冻结固定批次 → PostgreSQL 事务登记批次并累加 → Redis 按 ID 确认**。同一批重复投递不会重复累加；查询结合 Redis 代际快照与数据库提交记录，避免把已提交批次再次展示为待同步增量。Redis 不可读或快照持续变化时退回数据库基线。Stream 明细通过唯一 `event_uid` 去重，并包含 pending 认领与毒消息清理。

这不是逐次请求无损计数的保证：内存队列满、Redis 故障或持久化数据丢失仍可能少计；历史计数不自动校正。`link_click_totals` 的 `base_count` 与 `event_count` 是两种对账口径，不能相加。

访问口令只在数据库保存 bcrypt 摘要；缓存保存保护标志及版本。更换或清除口令会递增版本，旧解锁 Cookie 随之失效。匿名管理密钥只在创建响应返回一次，数据库保存 SHA-256 摘要；浏览器存储不可用时仅当前页面会话可用，应另行保管密钥。

## 接口

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| POST | `/api/auth/register`、`/api/auth/login` | 账号与登录 |
| GET | `/api/auth/me` | 当前用户 |
| POST / GET | `/api/links` | 创建 / 我的链接列表 |
| GET / PATCH / DELETE | `/api/links/{code}` | 查询 / 编辑 / 软删除 |
| POST | `/api/links/{code}/claim` | 登录用户使用管理密钥认领 |
| GET | `/api/links/{code}/stats` | 聚合统计 |
| GET | `/api/links/{code}/clicks` | 游标分页点击明细 |
| GET | `/api/links/{code}/qr.svg` | 公开二维码 |
| GET / POST | `/{code}` | 302 跳转或口令页 / 提交口令 |
| GET | `/healthz` | 公开精简健康状态 |
| GET | `/healthz/details`、`/metrics` | 内部诊断与 Prometheus 指标，Nginx 公网入口返回 404 |

鉴权使用 Bearer token；匿名链接管理使用 `X-Manage-Key`。我的链接列表及认领必须登录。携带失效令牌会返回 401，不会静默退化成匿名创建。

错误响应示例结构：

```json
{
  "error": {
    "code": "错误代码",
    "message": "错误说明",
    "field": "相关字段（可选）",
    "request_id": "请求标识"
  }
}
```

字段和路由以 [路由注册](backend/internal/handler/router.go)、[DTO](backend/internal/handler/dto.go) 与 [前端接口类型](frontend/src/api/types.ts) 为准。自定义域名由运维登记到 `domains` 表，目前无管理 API、自动 DNS 验证或 `is_active` 列；单域名部署无需登记。

## GeoIP 国家维度

将合法取得的 MaxMind DB 格式国家库 `*.mmdb` 放入 `deploy/geoip/`，设置容器路径，例如 `GEOIP_DB_PATH=/geoip/dbip-country-lite.mmdb`，重启消费该配置的 worker。宿主直跑使用宿主文件路径。

可选择 [DB-IP IP to Country Lite](https://db-ip.com/db/download/ip-to-country-lite) 或兼容国家库。数据下载、更新周期和署名以提供方当前许可为准；使用 DB-IP Lite 时保留其要求的来源署名，不能将数据许可视为项目 MIT 许可的一部分。库文件不入 Git，也不会由程序自动下载或更新。

未配置或无法打开库时国家字段为空，其余跳转和统计照常工作；配置错误会记录日志。真实 GeoIP 测试另外使用 `GEOIP_TEST_DB` 指向测试库。

## 开发与验证

前端在 `frontend/` 执行：

```bash
pnpm test
pnpm typecheck
pnpm lint
pnpm audit:refs
pnpm build
```

后端在 `backend/` 执行：

```bash
go test ./...
go vet ./...
# 支持 CGO / C 编译器的环境（CI 使用 Linux）
go test -race -count=1 ./...
```

真实存储测试需要 `POSTGRES_TEST_DSN`、`REDIS_TEST_ADDR`，以及按需配置的 `REDIS_TEST_PASSWORD`、`REDIS_TEST_DB`。未提供连接信息时对应用例跳过。PostgreSQL 测试会重置 schema，库名必须以 `_test` 结尾；Redis 也必须使用隔离实例或独立测试库，不能指向业务数据。

在已运行的隔离测试栈上，从仓库根目录依次执行（示例端口 18083）：

```bash
node frontend/e2e/browser-check.mjs --base http://localhost:18083
node frontend/e2e/redesign-check.mjs http://localhost:18083
cd backend
go run ./cmd/smoke -base http://localhost:18083 -expect-spa
```

浏览器检查需要本机 Chrome，可用 `CHROME_BIN` 指定。它们会创建测试账号和链接，冒烟还会触发限流，因此只用于隔离测试实例，且冒烟放在最后。`redesign-check.mjs` 会更新 `docs/screenshots/neo-brutalism/` 的截图。隔离编排示例见 `deploy/compose.audit.yml` 和部署文档的本地验证章节。

CI 在 PR 中执行后端竞态 / 存储测试与前端全部检查，main 或手动触发另外执行容器级浏览器检查与冒烟。结果以实际运行日志为准。

## 部署与运维

线上入口为 [https://shorten.miku831.fun/](https://shorten.miku831.fun/)，部署配置对应 Cloudflare → 1Panel OpenResty → 前端 Nginx → Go API。

- `TRUST_PROXY=true` 依赖前方 Nginx 已正确校验并重写来源头，不能将 Go API 直接公开。`deploy/nginx/trusted-proxies.conf` 默认不信任外层转发头；生产按实际 1Panel 对端地址配置，详见部署文档。
- `/metrics` 和 `/healthz/details` 由 Nginx 阻止公网访问；Go 端点自身不承担外网鉴权。
- 更换 `PUBLIC_BASE_URL` 后重新 build，并核对真实短链域名、口令 Cookie 和 CORS。
- 备份 profile `ops` 可启用 PostgreSQL 定期备份，默认在 `deploy/backup/` 保存约 14 天；计数升级与恢复还需考虑 Redis 持久化数据的一致性。
- 从旧计数实现升级时，先停入口和旧 API，保留旧 worker 排空后再停；禁止新旧 worker 混跑。回滚不是清空 Redis 或直接执行 down 迁移。

具体部署、代理、备份与批次迁移步骤以 [docs/DEPLOY-1PANEL.md](docs/DEPLOY-1PANEL.md) 为准。

## 文档与许可

- [设计规范](DESIGN.md)
- [审计与验证记录](docs/AUDIT-2026-10-02.md)
- [部署、备份、升级与回滚](docs/DEPLOY-1PANEL.md)
- [GeoIP 文件目录](deploy/geoip/README.md)

历史 PLAN、RESUME、INTERVIEW、AUDIT-TDD 和本地工作笔记不是当前实现规范；行为以源码、测试和当前部署文档为准。

项目源码采用 [MIT](LICENSE) 许可；GeoIP 数据及第三方依赖遵循各自许可。
