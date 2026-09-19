# AshenCourier — 后续迭代计划（M0 → M5）

> 起点：`8e46d96`（两轮审计全部落地，已推送）
> 承接：[`PLAN.md`](./PLAN.md)。`PLAN.md` 的 §1–§15 是**已实现**的契约（代码注释里引用了
> 它的 §6.3 / §7 / §8.1 / §9.1），本文件不改写它们，只记录**已确认但尚未实施**的改进项。
> 每一项都给出落点文件、契约/迁移影响、可执行验收标准与「为什么这么做」。
> 会话约定沿用：**一批次一提交**，每批必须先过门禁（`gofmt -l` 无输出 / `go vet` / `go test`）。

---

## 0. 执行进度（2026-09-19 更新）

> 本文件其余部分是**计划**；这一节记录**实际做到哪一步**。一个批次一个 commit，
> 全部已推到 `origin/main`，CI（[`.github/workflows/ci.yml`](./.github/workflows/ci.yml)）
> 三个 job 全绿。验收都是实测输出，不是「应该没问题」。

| 批次 | 内容 | 状态 | commit | 实测验收 |
| --- | --- | --- | --- | --- |
| — | 会话起点：`PLAN-NEXT.md` 入库 | ✅ | `6c29265` | — |
| **M0** | 三条容器级验收 | ✅ | `b10a20a` | ① 停 PG + 删缓存后跳转 = **503 + `Retry-After: 2`**；② 关停顺序 `收到退出信号/开始优雅关闭` → `统计写入队列已排空` → `api 已退出`，`dropped_clicks` 0→0；③ 冒烟 **24/24**。另：清掉了占用 8080 的孤儿栈（`F:\WorkSpace\Coding\Go\Link` 已不存在） |
| **M1-1** | CI 三个 job | ✅ | `4e3a611` | main 上全绿；临时分支实测「破坏 gofmt → backend 红」「改错 smoke 期望 → smoke 红并 dump 容器日志」。相对 §4 草稿修了 4 处（见下） |
| **M1-2** | handler / httpx 单测 | ✅ | `dfff9e8` | 新增 3 个测试文件（buildPatch / loadAuthorized / 路由表 12 条 + 405 / ClientIP / statusRecorder / RetryAfterHeader / sanitizeRequestID）；变异验证：删掉 `status:"deleted"` 拦截 → 用例变红 |
| **M1-3** | README 验收记录补「在哪跑过」 | ✅ | `7c447f4` | 区分本机 / CI；写明容器级验收由 smoke job 承担 |
| **M2-1** | `event_uid` 幂等去重（迁移 000002） | ✅ | `c69ff71` | 5 次跳转后 `count(event_uid)=count(distinct event_uid)=5`；用显式 Stream ID 重投一条已插过的消息 → 明细 7→7、uid 行数 1→1；`migrate down 1` + `up` 两轮无报错 |
| **M2-2** | 列表口径 = 基线 + 待同步增量 | ✅ | `b68f2a4` | 停 worker 跳 4 次：PG 基线 0 / Redis 增量 4，而列表 `click_count` = 4，与详情 `total_clicks` 相等；Redis 挂掉时列表仍 **200**（退回基线） |
| **M3-1** | 补偿式计数（读值不删 + 成功才结算） | ✅ | `bac1b19` | 停 worker 跳 10 次：键里 10、dirty 含该码、基线 0（没被取走）；结算后基线 10、明细 10、**计数键被删除**；100 次跳转后两口径都是 100，全库 `base_count<>event_count` = 0 |
| **M3-2** | singleflight 防击穿 + `pg_fallbacks` | ✅ | `52d6a98` | 冷短码 20 个并发请求（`curl --parallel-immediate`）全部 302，`pg_fallbacks` 增量 = **1**；单测 50 goroutine 并发 miss 只回源 1 次 |
| **M3-3** | 备份与恢复演练 | ✅ | `d134658` | dump → 恢复到临时库后与主库逐项一致（links 14 / events 169 / 两个口径 169）→ 删临时库；保留策略实测（2000 年的假备份被清理、当天的留下） |
| **M4-1** | 链接标签（迁移 000003） | ✅ | `ff67663` | 归一化 `Ops`+`  ops  `+`Dev` → `ops,dev`；`?tag=ops` 命中 1 条、`?tag=DEV` 命中 2 条；11 个 / 33 字符标签 → 422；`EXPLAIN` 走 `links_tags_gin`；无头 Chrome 里输入 `ops` 列表 2→1 |
| **M4-2** | 点击明细页（迁移 000004） | ✅ | `c1ba02c` | 跳 3 次 → `?limit=2` 两页读全（2+1，时间倒序、无重叠无缺口）；`device=mobile` 1 条 / `device=unknown` 0 条；坏 `limit`/`days`/`device`/`cursor` 都 422 且 `field` 正确，无凭据 404；库里 `172.20.0.1` → 响应 `172.20.0.0/24`（原始地址不在响应体里）；`EXPLAIN` 走 `click_events_link_time_id_idx` 且 **Index Only Scan 无 Sort**；无头 Chrome 首屏 20 行 + 「加载更多」→ 点一下 26 行、按钮变「已经到底了」 |
| **M4-3** | 二维码 | ✅ | `b5c9c18` | 前端 `qrcode` 画 canvas（无后端接口）：用 **jsQR 真扫**，页面 canvas 与下载的 1024×1024 PNG 都解码出 `http://localhost:8080/{code}`，与 `short_url` 逐字相等；配色深墨 + 暖奶油（≈19:1，非珊瑚）；无头 Chrome 里「下载二维码」按钮真的落盘 PNG |
| **M5** | 四个大件（需 §15 拍板） | ⏸ 按计划推迟 | — | — |

**与计划的偏离（7 条，逐条给理由）**

1. **删掉 `RestoreDelta`，而不是「保留给写库报错分支」**（§6 M3-1）。
   补偿式下 `TakeDelta` 只读不删，失败时值本来就在键里；此时再「按值归还」会让基线**翻倍**。
   §6 自己上面的崩溃推演表写的正是「键里还有 delta → 下一轮重做，不丢」，两者矛盾，按推演表实现。
2. **`SettleDelta` 用 Lua 脚本**（而不是 `INCRBY -delta` + `SREM` 两条命令）。
   非原子会留下「只摘不减 = 少计」的窗口；顺带在减到 0 时删键 —— 只 `DECRBY` 的话，
   每个被点过的短码都会永久留一个 0 值键（无 TTL），也就与「回刷后 `clicks:cnt:*` 清空」的验收冲突。
3. **标签统一小写存储**（而不是 §7 写的「保留原大小写、按小写比较」）。
   筛选走 `tags @> ARRAY[$1]`，**数组包含是大小写敏感的**：保留大小写就会出现
   「存了 `Ops`、按 `ops` 筛不到」。三者只能取其二，这里选了「比较一致」。
4. **本机 `go test -race` 需要 `CGO_LDFLAGS=-static`**：mingw-w64 8.1.0 的运行时 DLL 与
   Go 1.27 的 race runtime 不匹配（裸跑 `exit status 0xc0000139`）。CI 在 ubuntu 上原生可用。
5. **`docker-compose.override.yml`（未入库，`.gitignore` 已忽略）跳过 nginx 的 entrypoint 脚本**：
   本机网络下 `/docker-entrypoint.d/10-listen-on-ipv6-by-default.sh` 里的 `apk manifest nginx`
   会卡死，容器一直 unhealthy。我们的 `nginx.conf` 是整体替换的，那批脚本一个都不需要；
   CI 没有这个文件，走真实 entrypoint（也就是说 entrypoint 能跑通这件事仍由 CI 守着）。
6. **迁移 000004 顺带删掉旧索引 `click_events_link_time_idx`**（§9.1 写的是「只加索引」）。
   旧索引 `(link_id, occurred_at DESC)` 正是新索引 `(link_id, occurred_at DESC, id DESC)` 的**前缀**：
   任何走旧索引的查询都能走新索引，留着只是让 `click_events` 这条最热的追加路径每次插入多维护
   一棵 B-tree。down 里按原样重建，严格互逆 —— 已用 `pg_indexes` 核对迁移后的实际索引清单。
7. **IP 掩码用 CIDR 前缀写法**（`203.0.113.0/24`、`2001:db8:1234:5678::/64`），而不是
   §7 描述的「抹掉最后一段」那种 `203.0.113.x`。理由是语义不会读错：`.0` / 全零主机位容易被
   当成一台真实主机，而 `/24`、`/64` 明确表示「这是一个网段」；IPv4 与 IPv6 的展示形状也统一。

**顺带修掉的小问题**：`Input.vue` 之前把 `aria-label` 透传到外层 `<div>`，输入框本身没有可访问名
（屏幕阅读器只念「编辑框」）。M4-1 的浏览器验收发现后改为：`class`/`style` 留给外层容器，
其余属性绑到 `<input>`。

**M4-3 的后续修正（`36342a3`）**：二维码画布撑破了容器 —— `qrcode` 的 canvas 渲染器会把
`canvas.style.width/height` 写成行内 `320px`，**行内样式盖过 Tailwind 的 `h-full w-full`**，
画布压住右侧文字与下方卡片。原因是当时的验收只解码 `toDataURL()` 的像素（那永远是 320×320，
版式坏了也全绿），是在真浏览器里看了一眼才暴露的。修法是画完清掉行内尺寸；
验收也补了三个版式断言（画布落在容器内 / 显示宽 = 容器宽 − padding / 右边缘不压文字列）。
细节写进 README「六条踩过的坑」第 6 条。

**本机验收用到的临时工具（未入库）**：`.workbuddy/tmp/browser-check.mjs` —— 无头 Chrome + CDP
的小脚本（只用 Node 内置 fetch / WebSocket），用来在真实浏览器里截图并断言页面文本。
M4 剩下的两个批次会继续用它做 UI 验收。

---

## 1. 基线：现在是什么状态

### 1.1 已完成的（不要重复做）

| 能力 | 落点 |
| --- | --- |
| 跳转零落库 + 负缓存 + 保留字隔离 | `handler/redirect.go`、`service/shortener.go`、`pkg/shortcode` |
| 计数最终一致（PG 基线 + Redis 增量） | `worker/clicks.go`、`store/redis/counter.go` |
| 依赖故障可分类：PG 挂 → 503 + Retry-After | `store/postgres/db.go` 的 `storageError` + `httpx/response.go` 的映射顺序 |
| 每次 PG 调用有 op 超时（3s，`PG_TIMEOUT` 生效） | `store/postgres/db.go` 的 `opCtx` |
| worker 依赖 domain 端口（可单测） | `domain/click.go`、`worker/clicks.go` 的 `Deps` |
| 限流应急开关（`RATE_LIMIT_DISABLED`） | `config`、`cmd/api`、`/healthz` 的 `rate_limit_disabled` |
| 关停顺序：HTTP 优雅关闭后才排空统计队列 | `cmd/api/main.go` + `统计写入队列已排空` 日志 |
| 请求体拒绝未知字段、CORS 写 `Vary: Origin` | `httpx/response.go`、`httpx/middleware.go` |

### 1.2 测试覆盖现状（决定 M1 做什么）

| 包 | 状态 | 缺什么 |
| --- | --- | --- |
| `internal/config` | ✅ | — |
| `internal/domain` | ✅ | 邮箱/规范化；领域错误类型本身没测 |
| `internal/httpx` | ⚠️ 部分 | 只有错误映射/解码/CORS；缺 `ClientIP`、`statusRecorder`、中间件链顺序 |
| `internal/handler` | ❌ 零 | `buildPatch`、`loadAuthorized`、路由表、DTO 映射 |
| `internal/service` | ✅ | 缓存失效 / 短码耗尽 / 关停排空 |
| `internal/store/postgres` | ⚠️ 部分 | 错误归类 + opCtx；**SQL 本身没有集成测试** |
| `internal/store/redis` | ❌ 零 | 键构造、线格式、限流脚本 |
| `internal/worker` | ✅ | 计数回刷 / 落库 / ACK / 过期清理 |
| `cmd/`* | ❌ 零 | `cmd/smoke` 是端到端工具，不是单测 |

### 1.3 尚未验证的验收（本机沙箱没有 Docker）

三条容器级验收至今**没有跑过**（不是「设计上应该没问题」，是「没测」）：

1. 停掉 PG 后跳转返回 503 + `Retry-After`
2. `docker compose restart backend` 期间「drain 发生在 HTTP 关闭之后」
3. `go run ./cmd/smoke -base http://localhost:8080 -expect-spa` 的 24/24

→ 这是 M0 与 M1 存在的唯一理由：**本机不可用不该变成永远不验证**。

### 1.4 已知取舍（有意保留，别当 bug 修）

| 取舍 | 为什么保留 | 什么时候动 |
| --- | --- | --- |
| `TakeDelta` 是 `SREM`+`GETDEL`，进程被 SIGKILL 卡在中间会少计一批 | 改成补偿式只是把「少计」换成「重复累加」，需要单独的批次与测试 | M3-1 |
| `base62.Encode/Decode` 生产路径不调用 | 包注释承诺的是完整编解码契约，且有字母表映射测试护着 | 不做 |
| 内嵌 worker 随主 ctx 退出，drain 出的消息留给下次启动消费 | Stream 持久化 + 计数增量仍在 Redis，`total_clicks` 不受影响 | 不做 |

---

## 2. 里程碑总览

| 里程碑 | 主题 | 解决什么问题 | 工作量 | 依赖 |
| --- | --- | --- | --- | --- |
| **M0** | 补跑三条容器级验收 | 已实现但未验证 | 30 分钟 | 你本机有 Docker |
| **M1** | 可验证性闭环 | ① 验收自动化 ② handler/httpx 无测试 | 1–1.5 天 | — |
| **M2** | 口径可信 | 明细可能重复（at-least-once）、仪表盘滞后 | 1 天 | M1（CI 守住） |
| **M3** | 韧性 | 崩溃少计、缓存击穿、没有备份 | 1–1.5 天 | M2（迁移编号接续） |
| **M4** | 产品功能（小件） | 标签、点击明细页、二维码 | 2–3 天 | M2 |
| **M5** | 大件（先拍板） | 密码保护、GeoIP、自定义域名、多租户 | 按需 | §9 拍板 |

---

## 3. M0 — 补跑三条容器级验收（今天就能做，本机有 Docker）

> 用 PowerShell（本机 Git Bash 缺 coreutils）；`jq` 不假设存在，改用 `ConvertFrom-Json`。

```powershell
cd F:\WorkSpace\Coding\Go\AshenCourier
docker compose up -d --build
docker compose ps            # 5 个容器都该 healthy

# ① PG 挂掉 → 503 + Retry-After（而不是 500）
$created = curl.exe -s -XPOST localhost:8080/api/links -H 'content-type: application/json' -d '{"target_url":"https://example.com/pg-down"}' | ConvertFrom-Json
$code = $created.link.short_code
docker compose stop postgres
docker compose exec redis redis-cli DEL "link:v1:$code"        # 逼一次回源
curl.exe -si "localhost:8080/$code" | Select-Object -First 3   # 期望 HTTP/1.1 503 + Retry-After: 2
docker compose start postgres

# ② 关停顺序：drain 必须发生在 HTTP 优雅关闭之后
docker compose restart backend
docker compose logs backend | Select-String '收到退出信号|开始优雅关闭|统计写入队列已排空|api 已退出'
# 期望顺序：收到退出信号 → 开始优雅关闭 → 统计写入队列已排空 → api 已退出
curl.exe -s localhost:8080/healthz | ConvertFrom-Json | Select-Object dropped_clicks,failed_clicks,queue_len

# ③ 端到端冒烟（含 SPA 顶级路由断言）
go run ./cmd/smoke -base http://localhost:8080 -expect-spa      # 期望 24/24
```

**验收**：① 看到 503；② 日志顺序符合预期且 `dropped_clicks` 不因关停增长；③ 24/24。
跑完把结果补进 README 的「验收记录」表（那张表目前是改动前的结论）。

---

## 4. M1 — 可验证性闭环

### M1-1 CI（最高优先，一个 YAML）

落点：`.github/workflows/ci.yml`（新）

```yaml
name: ci
on:
  push: { branches: [main] }
  pull_request:
  workflow_dispatch:

jobs:
  backend:
    runs-on: ubuntu-latest
    defaults: { run: { working-directory: backend } }
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v5
        with:
          go-version-file: backend/go.mod          # 1.27.1；若 runner 未收录则退回 go-version: '1.27.x'
          cache-dependency-path: backend/go.sum
      - run: test -z "$(gofmt -l .)"
      - run: go vet ./...
      - run: go test -race ./...

  frontend:
    runs-on: ubuntu-latest
    defaults: { run: { working-directory: frontend } }
    steps:
      - uses: actions/checkout@v5
      - uses: pnpm/action-setup@v4
      - uses: actions/setup-node@v4
        with: { node-version: 22, cache: pnpm, cache-dependency-path: frontend/pnpm-lock.yaml }
      - run: pnpm install --frozen-lockfile
      - run: pnpm typecheck
      - run: pnpm lint

  smoke:                                            # 容器级验收，只在 main / 手动触发时跑
    if: github.ref == 'refs/heads/main' || github.event_name == 'workflow_dispatch'
    needs: [backend, frontend]
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v5
        with: { go-version-file: backend/go.mod, cache-dependency-path: backend/go.sum }
      - name: 造 .env（compose 需要三个必填口令）
        run: |
          cp .env.example .env
          sed -i "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=ci-postgres-pwd/" .env
          sed -i "s/^REDIS_PASSWORD=.*/REDIS_PASSWORD=ci-redis-pwd/" .env
          sed -i "s|^DATABASE_URL=.*|DATABASE_URL=postgres://ashen:ci-postgres-pwd@postgres:5432/ashen?sslmode=disable|" .env
          sed -i "s|^JWT_SECRET=.*|JWT_SECRET=$(openssl rand -base64 32)|" .env
      - run: docker compose up -d --build
      - name: 等 5 个容器 healthy（最多 3 分钟）
        run: |
          for i in $(seq 1 36); do
            unhealthy=$(docker compose ps --format json | grep -c '"Health":"starting"|"Health":"unhealthy"' || true)
            [ "$unhealthy" = "0" ] && break
            sleep 5
          done
          docker compose ps
      - run: go run ./cmd/smoke -base http://localhost:8080 -expect-spa
      - if: failure()
        run: docker compose logs --no-color --tail 200
```

**实施注意**

- `smoke` 只在 main 与手动触发时跑：`docker compose up -d --build` 要 3–5 分钟，放进每个 PR 会把反馈时间拖垮。PR 走 backend + frontend 两个快 job（约 1 分钟）。
- 首次跑 `pnpm lint` 可能会暴露既有告警（本轮只跑了 `typecheck`，没跑 lint）——把顺手修的改动并进 M1-1 这一个提交，别另开批次。
- `go test -race` 需要 cgo，`ubuntu-latest` 自带 gcc，无需额外装。
- runner 的 Docker 版本 ≥ 本机（Compose v2+），`depends_on.condition` 可用。

**验收**：故意改坏一个 `gofmt` 格式 → CI 红；改回 → 绿。故意把 smoke 的期望值改错 → smoke job 红且日志里能看到容器日志。

### M1-2 handler / httpx 测试补齐

落点：`internal/handler/link_test.go`、`internal/handler/router_test.go`、`internal/httpx/clientip_test.go`（均新建）

| 用例 | 断言 |
| --- | --- |
| `buildPatch`：`expires_at` 与 `clear_expires` 同时传 | 422 `invalid_expires_at`，且不落库 |
| `buildPatch`：`status: "deleted"` | 422 `invalid_status`（删除只能走 DELETE） |
| `buildPatch`：空 patch | 422 `empty_patch` |
| `buildPatch`：把已过期链接改回 `active` | 422 `expired_link`；同时传新的 `expires_at` 则放行 |
| `loadAuthorized`：无权限 + `forDetail` | 404（不泄露资源存在性） |
| `loadAuthorized`：无权限 + 非 detail | 403 |
| `loadAuthorized`：已删除的短链 | 404 |
| 路由表（表驱动，12 条） | 每条 `method+path` 命中的 handler 与鉴权中间件符合 README 的 API 表 |
| `ClientIP`：`TRUST_PROXY=true` + `X-Real-IP` | 取 `X-Real-IP` |
| `ClientIP`：伪造 `X-Forwarded-For` | **必须被忽略**（只信任 `X-Real-IP`） |
| `ClientIP`：`TRUST_PROXY=false` | 只认 `RemoteAddr`；非法 IP → 空串 |
| `statusRecorder` | 隐式 200、重复 `WriteHeader` 只记第一次、bytes 累加 |
| `RetryAfterHeader` | 1.2s → `2`、0 → `1`（向上取整，下限 1） |
| `sanitizeRequestID` | 控制字符/超长 → 空串；合法值原样 |

**为什么值得做**：`buildPatch` 与 `loadAuthorized` 是「状态码语义」的集中地（404 不泄露存在性、403 与 404 的分工），而这两条恰好是审计里反复咬人的地方；路由表测试则能挡住「改了路由忘了改文档」。
**验收**：`go test ./internal/handler/ ./internal/httpx/` 全绿；故意删掉 `status: "deleted"` 的拦截 → 测试红。

### M1-3 README 的「验收记录」补上环境与责任方

给那张表加一列「在哪跑过」，并写明「容器级验收由 CI 的 smoke job 承担」。理由：现在那张表分不清「本机实测」与「CI 实测」，M0/M1 之后两者会混在一起。

---

## 5. M2 — 口径可信

### M2-1 `event_uid` 幂等去重（`PLAN.md` §13 已承诺的 P1）

**问题**：投递语义是 at-least-once —— worker「处理完但 ACK 前」重启，同一批明细会重复插入。界面上的 `total_clicks` 不受影响（走 Redis `INCR`），但明细条数会多算，于是「基线 vs 明细」两个口径永远对不上。

**迁移 `000002_click_event_uid`**

```sql
-- up
ALTER TABLE click_events ADD COLUMN event_uid text;
-- partial：历史行没有 uid，不能让唯一索引把它们判成冲突
CREATE UNIQUE INDEX click_events_event_uid_key ON click_events (event_uid) WHERE event_uid IS NOT NULL;
```

```sql
-- down（严格互逆）
DROP INDEX click_events_event_uid_key;
ALTER TABLE click_events DROP COLUMN event_uid;
```

**代码改动**

| 落点 | 改动 |
| --- | --- |
| `domain/click.go` | `ClickEvent` 加 `EventUID string`（注释写明「由 Stream 消息 ID 派生，用于幂等」） |
| `worker/clicks.go` | `toClickEvent` 签名加消息 ID：`toClickEvent(msg.ID, msg.Event)`；`handleBatch` 里把 `msg.ID` 传下去 |
| `store/postgres/click.go` | `INSERT ... ON CONFLICT (event_uid) WHERE event_uid IS NOT NULL DO NOTHING`（partial 唯一索引的冲突推断必须带上同样的谓词，否则 PG 报「no unique or exclusion constraint matching」） |
| `worker/clicks_test.go` | 断言「同一条消息重投两次 → 两次 InsertBatch、事件 uid 相同」 |

**注意**：`ON CONFLICT DO NOTHING` 下 `InsertBatch` 无法区分「插入」与「跳过」，所以返回值保持 `error` 不变；如果将来要统计去重次数，用 `RETURNING` + `Query` 或另加计数。

**验收**

1. 起全栈 → 跳转 5 次 → `select count(*), count(distinct event_uid) from click_events` 两值相等
2. 人工重投：`redis-cli XADD clicks:stream '*' code <code> link_id <uuid> ts <同一个 ts>` 造一条 **相同 uid**（uid = 消息 ID，所以直接 `XADD` 同一个显式 ID 即可：`XADD clicks:stream 1712345678901-0 ...`）→ 明细条数不变
3. `migrate down 1 && migrate up` 可重复执行不报错（沿用 PLAN.md §1 的既有约定）

### M2-2 列表接口带「待同步增量」（仪表盘不再滞后）

**问题**：详情页 `total_clicks` = 基线 + Redis 增量，而 `GET /api/links` 只返回 PG 基线，仪表盘汇总最多滞后一个回刷周期（2s）。现在这层差异只写在 README 里。

**落点与改法**

| 落点 | 改动 |
| --- | --- |
| `domain/click.go` | 新增端口 `ClickDeltaBatchReader { PendingDeltas(ctx, codes []string) (map[string]int64, error) }`（与现有单键 `ClickDeltaReader` 并存） |
| `store/redis/counter.go` | 用一次 `MGET` 读 `clicks:cnt:{code}`（页大小 ≤100，一次往返）；缺失键跳过，解析失败按 0 处理并记 debug |
| `handler/link.go` | `list` 拿到 links 后叠加：读失败只记 warn 并退回纯基线（**不能让统计故障把列表打成 5xx**） |
| `internal/store/redis/client.go` | 补编译期断言 `_ domain.ClickDeltaBatchReader = (*Client)(nil)` |

**契约影响**：`linkDTO.click_count` 语义从「PG 基线」变成「基线 + 待同步增量」，与详情页一致 —— README 里「仪表盘只含基线」那段要改回来。

**测试**：`link_test.go` 用 fake reader 断言叠加；`counter` 侧断言 MGET 的键构造（`clicks:cnt:` 前缀 + 短码）。
**验收**：跳转后立刻 `GET /api/links`，`click_count` 已含增量，且与该链接详情的 `total_clicks` 相等。

---

## 6. M3 — 韧性

### M3-1 `TakeDelta` 改补偿式（消除崩溃少计窗口）

**现状**：`SREM` + `GETDEL` 取走即删。写库**报错**的路径已经用 `RestoreDelta` 按值归还；但进程在 `GETDEL` 之后被 SIGKILL，那批增量就永久少计（明细已经落库，两个口径再也对不上）。

**目标语义**（与 README「宁可重复累加也不能丢」一致）

```text
读取：GET clicks:cnt:{code}            → delta（不删键）
写库：UPDATE links SET click_count += delta
归还：INCRBY clicks:cnt:{code} -delta  + SREM clicks:dirty {code}
```

**崩溃推演**

| 崩在哪 | 结果 |
| --- | --- |
| `GET` 之后、写库之前 | 键里还有 delta，dirty 也还在 → 下一轮重做，**不丢** |
| 写库之后、`INCRBY -delta` 之前 | 基线多算一批（≤ 一个批次），**重复累加**而非丢失 |
| `INCRBY -delta` 之后 | 正常 |

**改动** | 落点 | 内容 |
| `store/redis/counter.go` | `TakeDelta` 改为「读值 + 保留键」；新增 `SettleDelta(ctx, code, delta)`（`INCRBY -delta` + `SREM`）；`RestoreDelta` 保留给「写库报错」分支 |
| `domain/click.go` | `ClickCounter` 端口加 `SettleDelta`（注释写明「成功落库后结算」） |
| `worker/clicks.go` | 成功分支调 `SettleDelta`；失败分支仍调 `RestoreDelta`；两者都失败时日志写明「可能重复累加」 |
| `worker/clicks_test.go` | 成功路径断言调用了 `SettleDelta` 且未调用 `RestoreDelta`；失败路径相反 |
| README | at-least-once 段落补一句「基线可能重复累加（上限一批），明细可能重复插入，都不影响跳转」 |

**验收**：跳转 100 次后人工制造 `AddClickCount` 失败（临时 `ALTER TABLE links ... ` 不可行，改用单元测试注入）→ 断言键里增量仍在；正常路径断言 `clicks:cnt:*` 最终清空且 `link_click_totals` 两个口径一致。

### M3-2 缓存击穿防护（singleflight）

**问题**：`Resolve` 在缓存 miss 时会回源 PG。热点短链刚发布（或被大规模转发）时，同一短码的 N 个并发请求会打 N 次 PG —— 负缓存只能挡住「已确认不存在」，挡不住「刚出现的热点」。

**落点**：`service/shortener.go` 的回源分支。用 `golang.org/x/sync/singleflight`（已在 go.mod 的 indirect 块里、模块缓存里也有；提升为 direct 不需要联网，`go mod tidy` 即可）。

**备选**：手写 ~30 行进程内 singleflight（不引依赖，但少人维护）。默认选前者。

**注意**
- key 用短码；**不要**调 `singleflight.Forget` —— 缓存写失败时让 TTL 自愈即可
- singleflight 会共享「失败」结果给所有等待者：负缓存（`putMissing`）负责兜住 404，不会形成击穿循环
- 回源自身有 PG op 超时（3s），所以一个慢请求不会无限拖住同短码的其他请求
- 单测：50 个 goroutine 并发打同一个 miss 短码，断言 fake repo 只被调用 1 次（`atomic.Int64` 计数 + `sync.WaitGroup`，注意用 `wg.Go`）

**可选**：`/healthz` 加 `pg_fallbacks` 计数（回源次数），这正是击穿的可观测口径。

### M3-3 PG 备份与恢复（README 目前完全没有）

**落点**：`docker-compose.yml` 加一个 `backup` profile 服务 + README「备份与恢复演练」小节。

```yaml
  backup:
    image: postgres:18.6-alpine        # 与服务端同 tag：pg_dump 版本必须 ≥ 服务端主版本
    profiles: ["ops"]
    environment:
      PGPASSWORD: ${POSTGRES_PASSWORD:?POSTGRES_PASSWORD is required}
    volumes:
      - ./deploy/backup:/backup
    entrypoint: ["/bin/sh", "-c"]
    command:
      - |
        while true; do
          pg_dump -h postgres -U ashen -d ashen -Fc -f /backup/ashen-$$(date +%Y%m%d-%H%M%S).dump
          find /backup -name 'ashen-*.dump' -mtime +14 -delete
          sleep 86400
        done
    depends_on:
      postgres: { condition: service_healthy }
```

**恢复演练（必须真的跑一次，否则等于没有备份）**

```powershell
docker compose --profile ops up -d backup
Start-Sleep -Seconds 20
docker compose exec postgres createdb -U ashen restore_check
docker compose exec postgres pg_restore -U ashen -d restore_check /backup/<最新的>.dump
docker compose exec postgres psql -U ashen -d restore_check -c "select count(*) from links; select * from link_click_totals limit 5;"
docker compose exec postgres dropdb -U ashen restore_check
```

**验收**：演练一次「dump → 恢复到临时库 → 两个口径与主库一致 → 删临时库」，并把命令写进 README。

---

## 7. M4 — 产品功能（小件）

### M4-1 链接标签

- 迁移 `000003_links_tags`：`ALTER TABLE links ADD COLUMN tags text[] NOT NULL DEFAULT '{}'` + `CREATE INDEX links_tags_gin ON links USING gin (tags)`（down 严格互逆：drop index + drop column）
- 契约：`linkDTO.tags []string`（JSON tag 用 `json:"tags,omitempty"`）；创建/修改入参 `tags`（≤10 个、每个 ≤32 字符、去重、保留原大小写但按小写比较）
- 列表筛选 `GET /api/links?tag=ops`：SQL 用 `tags @> ARRAY[$n]` 走 GIN
- 前端：`types.ts` + 列表/详情展示 + 输入框（逗号分隔，与现有 `Input.vue` 风格一致）
- 验收：打两个标签 → 按其中之一筛选能命中；`SET enable_seqscan=off; EXPLAIN ...` 看到走 `links_tags_gin`

### M4-2 点击明细页

- 迁移 `000004_click_events_page_index`：`CREATE INDEX click_events_link_time_id_idx ON click_events (link_id, occurred_at DESC, id DESC)`
  —— 现有索引是 `(link_id, occurred_at DESC)`，**没有 id**，而 keyset 分页需要一个稳定的兜底键（同一毫秒内的多条会漏行/重复）
- 新接口 `GET /api/links/{code}/clicks?limit=&cursor=&days=&device=`（鉴权与详情一致：无权限 404）
- 落点：`domain.ClickRepository` 加 `ListByLink(ctx, q ClickListQuery) ([]ClickEvent, ClickCursor, error)`；`store/postgres/click.go` 实现 `(occurred_at, id) < ($2, $3)` 的 keyset；游标编码复用 `service` 里 base64+`|` 的做法（`service` 里那份是 `RFC3339Nano|uuid`，这里换成 `RFC3339Nano|int64`）
- **PII 取舍**：不返回原始 IP，返回掩码（IPv4 抹掉最后一段，IPv6 只留前 4 组）；理由：明细页是给人看的，原始 IP 只在风控/排障时才需要，而落库时已经有了
- 前端：详情页加「最近点击」表 + 「加载更多」
- 验收：跳转 3 次 → 3 行时间倒序；`limit=2` 拿到游标并能翻到第 2 页；掩码 IP 看不到完整地址

### M4-3 二维码

- 方案 A（推荐，零后端改动）：前端 `qrcode`（npm，~10KB）把 `short_url` 画成 canvas/SVG，详情页加「下载二维码」
- 方案 B（有需要再做）：`GET /api/links/{code}/qr.svg` 后端生成（`skip2/go-qrcode`），好处是能被邮件/印刷品直接引用
- 验收：手机扫码打开短链；在 DESIGN.md 的暖奶油面板上用深墨前景，对比度足够（不要用珊瑚色当二维码前景）

---

## 8. M5 — 大件（先拍板，见 §9）

### M5-1 短链密码保护

- 迁移 `000005_links_password`：`ALTER TABLE links ADD COLUMN password_hash text NOT NULL DEFAULT ''`
- 哈希：bcrypt cost 12（`service/auth.go` 的 `BcryptCost`），与匿名管理密钥的 SHA-256 **明确区分**（前者抗爆破，后者是高熵随机数）
- 跳转：`GET /{code}` 命中带密码的链接 → 渲染密码页（扩展 `handler/html.go`，沿用 DESIGN.md token）；`POST /{code}` 校验 → 下发 HttpOnly + SameSite=Lax + Secure 的短期签名 cookie（`HMAC-SHA256(code|exp)`，密钥从 `JWT_SECRET` 派生而非直接共用）；校验通过后 302
- **密码页与校验请求都不计入点击**（只统计真正的跳转）
- nginx：短码正则 location 已经是「全方法转发」，不用改；后端要多注册一条 `POST /{code}`
- 限流：`POST /{code}` 按 IP 加一条（防口令爆破，比照 login 的 20 次/10 分钟）
- 验收：设密码 → 未解锁时 200 密码页且 `click_events` 不增 → 错误口令 401/422 → 正确口令拿到 cookie 后 302 且计数 +1

### M5-2 GeoIP 国家维度

- `click_events.country` 字段与统计的 `TopN` 框架都已就位，缺的是解析与入库
- 依赖：mmdb 文件 + `oschwald/maxminddb-golang`；**许可证要选**：GeoLite2 需账号 + 署名，DB-IP Lite 是 CC-BY（README 必须注明数据来源与署名）
- 解析放在 worker（跳转路径不能碰文件 IO）；**没有 mmdb 文件时降级为 country 全空**，不能启动失败
- 统计：`StatsAggregate` 加 `Countries`，前端加一个分布列表（复用 `DistributionList.vue`）
- 验收：换一个已知 IP 的 UA/Referer 跳转 → 统计出现对应国家；删掉 mmdb 文件重启 → 一切照旧、country 为空且日志只有一条 warn

### M5-3 自定义域名 / 分域

- README 说「分域只需改 nginx」，实际还差数据模型：`domains(id, domain, owner_id, verified_at)` + `links.domain_id`
- 缓存键升级 `link:v2:{host}:{code}`（正好用上键里的 `v1` 版本位；这也是为什么当初坚持给所有键带版本前缀）
- nginx：多 `server_name` + 证书（certbot/ACME）；`PUBLIC_BASE_URL` 退化为「默认域名」，`short_url` 按链接所属域名拼
- CORS 白名单要跟着域名走（现在只允许 `PUBLIC_BASE_URL` 一个来源）
- 验收：两个域名各自解析自己的短码；同 code 在两个域名下互不串味（缓存键 + DB 唯一约束都要带上域名）

### M5-4 团队 / 多租户

- `orgs` + `memberships(role)` + `links.org_id`；所有查询从 `owner_id = $1` 变成「owner 或 org 成员」
- 最难的不是表结构，是语义：`X-Manage-Key` 的认领、软删除的权限、统计的可见范围都要重新定
- 建议：等有真实多人协作需求再做（这是本文件里唯一建议「先别做」的大件）

---

## 9. 迁移与契约变更台账

### 9.1 迁移编号（顺序即实施顺序）

| 迁移 | 内容 | 里程碑 | 兼容性 |
| --- | --- | --- | --- |
| `000002_click_event_uid` | `event_uid` + partial unique index | M2-1 | 加可空列 + 新索引，不破坏 |
| `000003_links_tags` | `tags text[]` + GIN | M4-1 | 不破坏 |
| `000004_click_events_page_index` | `(link_id, occurred_at DESC, id DESC)` | M4-2 | 不破坏（只加索引） |
| `000005_links_password` | `password_hash` | M5-1 | 不破坏 |
| `000006_domains` | `domains` + `links.domain_id` | M5-3 | 加可空列，历史行落默认域名 |
| `000007_orgs` | `orgs`/`memberships`/`links.org_id` | M5-4 | **破坏性**：需要回填 + 双写窗口 |

**约定**：up/down 严格互逆；上线顺序永远是「先迁移、后发版」；`click_events` 是大表 —— 加列在 PG18 不重写表，但**建索引要考虑并发**：如果将来数据量大到需要 `CREATE INDEX CONCURRENTLY`，必须先实测 golang-migrate 的 postgres 驱动是否把单个迁移文件包在事务里（它在事务里会直接报错）；被包住的话就把该迁移拆成独立文件并确认驱动的行为，或把建索引挪出迁移（手工步骤 + README 记录）。

### 9.2 契约 / 配置变更汇总

| 变更 | 类型 | 影响面 |
| --- | --- | --- |
| `linkDTO.click_count` 语义改为「基线 + 待同步增量」 | 语义（字段名不变） | M2-2；README 口径说明要改 |
| `linkDTO.tags` | 新增字段 | M4-1；`types.ts` 同步 |
| `GET /api/links/{code}/clicks` | 新接口 | M4-2 |
| `POST /{code}` | 新方法 | M5-1 |
| `linkDTO.password_protected bool` | 新增字段 | M5-1 |
| `/healthz` 的 `pg_fallbacks`（可选） | 新增字段 | M3-2 |
| 无新环境变量（除可选的 `POSTGRES_TEST_DSN` 供 store 集成测试） | — | — |

---

## 10. 规范与不变量（实施时必须遵守）

- 沿用 `PLAN.md` §8.1 的强制清单；本计划相关的重点：`errors_as_type`、`sync_waitgroup_go`（singleflight / 并发测试）、`atomic_types`（计数器）、`slices_*`/`maps_*`、`min_max`、`new_expression`、`json_omitzero`（`tags` / `password_protected`）、`http_servemux_patterns`（新增路由）、`range_over_int`、`testing_t_context`（新增测试）、`time_tick_gc`（备份循环用 `time.Tick` 而非刻意 Stop）。
- 每编辑一个 Go 文件前，先跑 `run-tool.ps1 list --file-path <文件>` 并读完输出（use-modern-go skill 的要求）；出现新 guideline 先 `explain` 再决定。
- 仓库不变量（**违反会直接造成线上故障**）：
  - 短码字符集与长度 3–32 三处一致（`shortcode` 常量 / nginx 正则 / DB CHECK）
  - **任何新的后端顶级路径都要进 `pkg/shortcode/reserved.go`**，否则能被注册成短码。例：`/metrics` 落在 nginx 短码正则 `^/[A-Za-z0-9_-]{3,32}$` 的范围内（`metrics` 正好 7 位），不进保留字表就会「有人注册了 metrics，然后他的短链永远打不开 + 指标被公开」
  - 前端顶级路由仍是「四处同步」（nginx `location =` / `reserved.go` / `shortcode_test.go` / `vite.config.ts` 的 `SPA_ROUTES`）
  - `internal/domain` 不 import 任何第三方库（singleflight 只能出现在 store/service 层）
  - 每批次一个 commit + 门禁 green

---

## 11. 每个里程碑的验收清单

**全局 DoD（沿用 `PLAN.md` §12）**：`gofmt -l` 无输出、`go vet ./...`、`go test -race ./...`、`pnpm typecheck && pnpm lint && pnpm build`；M1 之后再加一条「CI 全绿」。

| 里程碑 | 可执行验收 |
| --- | --- |
| M0 | 三条容器级验收的命令都跑过，README 的验收记录表更新 |
| M1 | CI 三个 job 绿；故意破坏 fmt/smoke 能变红；handler/httpx 新测试全绿 |
| M2 | `count(*) = count(distinct event_uid)`；重复投递不改明细条数；列表 `click_count` 与详情 `total_clicks` 相等 |
| M3 | 注入 `AddClickCount` 失败后增量仍在；50 并发 miss 只回源 1 次；备份能恢复到临时库并核对两个口径 |
| M4 | 标签筛选走 GIN；明细分页可翻页且 IP 已掩码；扫码能打开 |
| M5 | 每项自己的验收（见 §8 各小节） |

---

## 12. 风险登记（本计划新增）

| 风险 | 影响 | 对策 |
| --- | --- | --- |
| `event_uid` 唯一索引在大表上建得慢 | 迁移窗口内写入放大 | 数据量大时用 `CREATE INDEX CONCURRENTLY`（先确认驱动事务行为，见 §9.1） |
| 补偿式计数导致重复累加 | 基线 > 明细，对账时困惑 | README 写明口径；`link_click_totals` 视图做人工对账；重复上限是「一批」 |
| singleflight 引入跨请求共享状态 | 一个慢回源拖住同短码的其他请求 | 回源有 PG op 超时；失败结果共享但被负缓存兜住 |
| CI smoke 依赖 Docker Hub 拉镜像 | 网络抖动导致红 | 只在 main + 手动触发；失败时 dump `docker compose logs` |
| mmdb 许可证 | 合规 | README 署名数据来源；或改用 CC-BY 的 DB-IP Lite |
| 密码保护引入新的爆破面 | 短链被暴力猜口令 | 按 IP 限流（比照 login）+ 密码页不计点击 + 口令 bcrypt |

---

## 13. 不做清单（明确排除，避免膨胀）

| 不做 | 原因 |
| --- | --- |
| 抓取目标页标题 | SSRF 风险（`PLAN.md` §1.2 已定） |
| Prometheus + Grafana 全套 | 只做零依赖 `/metrics` 文本端点，且默认**不对外**（不进 nginx location） |
| A/B 分流 / 短链轮换 | 需要 `link_targets` 表 + 「目标页归属」的新语义，收益不明 |
| 短码回收（已删除的 code 重新可用） | 品牌与安全上都更安全的是不回收：旧二维码/印刷品会指到新链接。要回收得把唯一约束改成 partial（`WHERE status <> 3`）并接受历史明细与新链接共享 `short_code` —— 默认**不做** |
| 前端引入 Pinia / ECharts | 现有 composable + 手写 SVG 够用 |
| 自建对象存储 / 短链快照页 | 与「短链」的核心价值无关 |

---

## 14. 建议的开工顺序

1. **M0**：今天在你本机补跑三条容器级验收（30 分钟，命令见 §3）
2. **M1-1 CI** → **M1-2 handler/httpx 测试** → **M1-3 README 验收记录**
3. **M2-1 `event_uid`** → **M2-2 列表增量**
4. **M3-1 补偿式计数** → **M3-2 singleflight** → **M3-3 备份**
5. **M4-1 标签** → **M4-2 明细页** → **M4-3 二维码**
6. **M5** 按 §15 的拍板结果排期

每个里程碑收尾：跑全局 DoD + 更新 README 验收记录 + 一个 commit（本会话约定）。

---

## 15. 需要你拍板的点（不影响 M1–M4 开工）

1. **CI 的 smoke job 频率**：只在 main + 手动（默认，推荐），还是每个 PR 都跑（反馈慢 3–5 分钟）？
2. **`/metrics` 要不要做**：默认先不做；要做必须①只在内网可达②把 `metrics` 加进保留字表（理由见 §10）
3. **GeoIP 用哪份数据**：GeoLite2（需账号 + 署名）还是 DB-IP Lite（CC-BY）？
4. **短码回收**：默认不回收（见 §13），若你要回收，我按 partial unique index 方案做并补迁移与测试
5. **明细页的 IP**：默认只返回掩码；若你要完整 IP（排障场景），我加一个「仅所有者可见」的开关并写进 README 的数据可见性说明
6. **标签是否要预设 + 颜色**：默认自由文本（不加 UI 复杂度）
