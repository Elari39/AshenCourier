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
> **还剩什么**（M5 前置条件、可选项、自动化缺口）见文末 **§16**；待拍板项的当前状态见 **§15**。

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
| **N1** | store 层迁移与 SQL 集成测试进 CI（§17.1，即自动化缺口 16.3-1） | ✅ | `da6731d` | 带 `POSTGRES_TEST_DSN` 时 **15** 个用例全绿（PG 18.6 容器）；不带 DSN 时 11 个集成用例 SKIP、4 个单测仍绿。变异验证两条都按预期变红：删掉 `ON CONFLICT ... WHERE event_uid IS NOT NULL` → `42P10 there is no unique or exclusion constraint matching`；把 keyset 的 `(occurred_at, id) <` 退化成 `occurred_at <` → `got=[12 11 10 9 8 6 5 4 3 2] want=[12 11 10 9 8 7 6 5 4 3 2 1]`（并列时间上漏掉第 7 与第 1 条）。第一次跑还发现 `links.created_ip` 读出来带 `/32` 掩码长度，已与 `click_events.ip` 一样改用 `host()` |
| **N2** | 短链密码保护（§17.2，即 M5-1） | ✅ | `566aa2b` | 容器级 ①–⑥：未解锁 **200 密码页**且 `total_clicks` 0、错误口令 **401** 且仍 0、正确口令 **303 + `ac_unlock`**、带 cookie 的 GET **302** 且 `total_clicks`=**1**（不是 2）、`clear_password` 后立刻 302；库里只有 `$2a$12$…` 摘要（与明文比较为 `f`）。迁移 000005 往返两轮无报错；冒烟 **27 / 27**；无头 Chrome 里创建表单的口令输入、详情页「受口令保护」徽章、编辑面板的「清除口令」、口令页、输错提示、输对**真的落到目标地址**，逐条通过 |
| **N3** | 前端纯函数单测（vitest）+ 消掉 `splitTags` 重复（16.3-3） | ✅ | `9071d62` | `vitest run` **31 个用例全绿**（`format.ts` 24 / `tags.ts` 7，约 0.4s）；`splitTags` 从 `ShortenForm.vue` 与 `LinkDetailView.vue` 提到 `src/utils/tags.ts`（原先是逐字相同的两份）；CI 的 `frontend` job 补一行 `pnpm test`。变异验证两条：`truncateMiddle` 的 `head + tail + 1` 退化成 `head + tail` → 红；`tags` 的 `filter(length > 0)` 改成 `> 1` → **第一次没抓住**（用例里没有一个单字符标签），补一条后才红 |
| **N4** | 浏览器级验收入库并进 CI（16.3-2） | ✅ | `0464754` | 固化为 `frontend/e2e/`（4 个文件，**零 npm 依赖**：只用 Node 内置 `fetch` / `WebSocket` 直连 CDP）**19 项全绿**；挂进 CI 的 `smoke` job 并**排在 `cmd/smoke` 之前**（创建接口是 10 次/分/IP 的硬配额，而 smoke 的最后一项会故意打满它）—— 本机实测这个顺序：e2e **19/19** → 紧接 smoke **27/27**。变异验证：去掉二维码的行内尺寸清理 → **4 条版式断言全红、而像素断言仍全绿**，正是第 6 条坑的复现 |
| **N5** | M5-2 GeoIP 国家维度（数据源见偏离第 9 条） | ✅ | `021277c` | 新增 `internal/store/geoip`（`Resolver` + `OpenOrDefault` + 无库时 noop 降级）、config 的 `GEOIP_DB_PATH`、worker 填 `country`、统计多一个 `countries` 分布 + 前端复用 `DistributionList`。容器级：投两条公网 IP 的点击 → `8.8.8.8` = **US**、`114.114.114.114` = **CN**，走完 Stream → mmdb → PG → 统计全链路；两条降级路径（留空 = 一条 `INFO` / 文件打不开 = 一条 `WARN`）都只 warn、照常落库为 NULL、`/healthz` 仍 200。集成测试新增 `TestAggregateCountriesExcludesEmpty`（变异：去掉「排除空国家」谓词 → 结果多出 `{unknown 1}` 桶，红）。**零迁移**（`click_events.country` 000001 就有） |
| **N6-1** | 自定义域名：数据模型 + 按域缓存键 + `Host` 定位（M5-3 前半） | ✅ | `0456154` | 迁移 `000006`（`domains` 表 + `links.domain_id` 可空）；新增 `internal/pkg/hostname`（去端口 / 去尾点 / 转小写）；跳转按 `Host` 定位域，缓存键升到 `link:v2:{域}:{code}`（域为 `-` = 默认域名）。容器级 **24/24**：跨域双向 404 隔离、`Host: A.LOCAL:8080` 也命中、真 Redis 里键确实分域。变异验证（缓存键不分域 + `sameDomain` 恒 true）→ **8/24 红**，还原后回到 24/24。**没动** `links.short_code` 的全局唯一约束 —— 那是 N6-2，见 §18 |

> ✅ N1 / N2 已于 2026-09-19 推送，CI 三个 job 全绿（run `35424447615`）：
> `backend` 54s —— 含真 PG service 上的 store 集成测试（`internal/store/postgres` **1.271s**，
> 跳过的话只有零点几秒，所以是真的跑了）；`frontend` 41s；`smoke` 85s —— **27 / 27**，
> 三条口令用例逐条 ✓。上表两行里除 CI 结论外的数字都是本机实测（真 PG 18.6 容器 + 无头 Chrome）。

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

8. **`geoip` 放在 `internal/store/geoip` 而不是计划里的 `internal/pkg/geoip`**（§8 M5-2）。
   仓库自己的分层是「`internal/pkg/` = 零第三方依赖的纯工具」（「`domain` 不 import 第三方」是同一条约束的
   延伸），而这个包带着 `oschwald/maxminddb-golang/v2`；按「外部数据源的实现放 `store/`」的惯例挪了过去，
   顺带把两个入口（api 内嵌 worker / 独立 worker）的降级逻辑收成一处 `OpenOrDefault`。
9. **N5 的容器级验收用的是本机已有的 `GeoLite2-Country.mmdb`，不是计划里写的 DB-IP Lite**。
   `download.db-ip.com` 的直连在本机被拒（Python `urlopen` 403；curl 带浏览器 UA 同样被拒），
   而两种库**格式与字段一致**（都是 MaxMind DB、都读 `country.iso_code`），所以解码路径的验证价值不变。
   因此 README 的署名段同时写明两种数据源的要求，且仓库**不附带**任何 mmdb（`.gitignore` 挡住）。
10. **N6-1 修掉一处计划里没有的缺陷：跨域探测会污染负缓存**（§8 M5-3 只写了「缓存键升级」）。
    「短码全局唯一」的模型下有一条隐蔽路径：在 A 域访问一个属于 B 域的短码 → 回源查不到 →
    `PutMissing` 写下 `link:v1:miss:{code}`；此后**在 B 域的正常访问会命中这条负缓存而 404** ——
    负缓存不按域分，等于让「探错域名」变成一次拒绝服务。修法是键升级时**把域编进键**
    （`link:v2:{域}:{code}`），而不是只把 `v1` 改成 `v2`；`Evict` 的入参也从 `code` 变成
    `LinkRef{Code, DomainID}` —— 不知道域就删不掉正确的条目（worker 的 `ExpireDue` 跟着改成返回 `[]LinkRef`）。
    做法：先用一条**红测试**把这个路径钉住（`TestCrossDomainProbeDoesNotPoisonNegativeCache`），再改实现。
11. **`createLinkRequest` 原本没有 `domain` 字段**（N6-1 实施中发现）。
    service 层的 `CreateInput.Domain` 一直在，但 HTTP 入参结构体里没有这个成员；而解码器**拒绝未知字段**
    （README 的 API 说明里写着这条），所以带 `domain` 的请求会直接 422 `invalid_json` —— 看起来像
    「参数名写错了」，实际是这个能力从接口层根本到不了。补字段 + 接线，并加 `TestCreateAcceptsDomainField`
    断言「不能是 `invalid_json`，且未登记域名是字段级 422 `invalid_domain`」，而不是被静默忽略后创建成功。

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
| **M5** | 大件（先拍板） | 密码保护、GeoIP、自定义域名、多租户 | 按需 | §9 拍板；**M5-1 已细化并立项为下一批次 N2（§17.2）** |

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

> **已细化为 §17.2（下一批次 N2）**：迁移 SQL、状态码语义、cookie 属性、缓存策略、限流规则、
> 验收命令与测试清单都在那里。本节保留原始设计意图，不改写。

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

> **实施状态**：代码侧前半已完成 —— **N6-1 / `0456154`**（数据模型 + `Host` 定位 + 按域缓存键），
> 结果与实测见 §0 的表。后半（短码唯一约束改成按域）是 **§18 的待拍板项**。
> 本节以下保留**原始设计意图**不改写；其中一处实施时改了：缓存键的域那一段用的是
> **域 ID（UUID）而不是 `host`**（`link:v2:{域 UUID}:{code}`，默认域名是 `-`）——
> 用 host 会让「同一个域的多种写法」（大小写、带端口、尾点）各自留一份缓存条目，
> 而域名改名还要整体失效缓存；域 ID 没有这些问题，且 `Host` 的归一化仍然要做（用于查域）。

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
| `000005_links_password` | `password_hash` | **N2**（§17.2）✅ | 不破坏：`NOT NULL DEFAULT ''` 加列，无索引；down 严格互逆（已实测往返两轮） |
| `000006_domains` | `domains` + `links.domain_id` | **N6-1**（§0）✅ | 不破坏：加可空列，历史行自动落默认域名；down 严格互逆（已实测 down/up） |
| `000007_links_domain_short_code` | 唯一约束从 `short_code` 放宽到 `(domain_id, short_code) NULLS NOT DISTINCT` | **N6-2**（§18，**待拍板**） | 写入侧不破坏（放宽约束）；但**管理端按短码定位的语义会破**（§18.2），所以它不是「加个索引」那么简单。⚠️ down 有个真实的坑：从按域唯一退回全局唯一时，若库里已存在跨域同码的行，`ADD CONSTRAINT ... UNIQUE (short_code)` 会直接失败 —— down 必须先检测并报错，而不是静默半途 |
| `000008_orgs` | `orgs`/`memberships`/`links.org_id` | M5-4 | **破坏性**：需要回填 + 双写窗口 |

**约定**：up/down 严格互逆；上线顺序永远是「先迁移、后发版」；`click_events` 是大表 —— 加列在 PG18 不重写表，但**建索引要考虑并发**：如果将来数据量大到需要 `CREATE INDEX CONCURRENTLY`，必须先实测 golang-migrate 的 postgres 驱动是否把单个迁移文件包在事务里（它在事务里会直接报错）；被包住的话就把该迁移拆成独立文件并确认驱动的行为，或把建索引挪出迁移（手工步骤 + README 记录）。

### 9.2 契约 / 配置变更汇总

| 变更 | 类型 | 影响面 |
| --- | --- | --- |
| `linkDTO.click_count` 语义改为「基线 + 待同步增量」 | 语义（字段名不变） | M2-2；README 口径说明要改 |
| `linkDTO.tags` | 新增字段 | M4-1；`types.ts` 同步 |
| `GET /api/links/{code}/clicks` | 新接口 | M4-2 |
| `POST /{code}` | 新方法 | **N2**（§17.2）；成功 303 → `/{code}`，失败 401 |
| `linkDTO.password_protected bool` | 新增字段 | **N2**（§17.2）；`types.ts` 同步 |
| `GET /{code}` 命中带口令且未解锁时返回 200 密码页 | 语义（不再是 302） | **N2**（§17.2）；冒烟与 README 同步 |
| `/healthz` 的 `pg_fallbacks`（可选） | 新增字段 | M3-2 |
| 无新环境变量（除可选的 `POSTGRES_TEST_DSN` 供 store 集成测试） | — | — |
| `POSTGRES_TEST_DSN` | 新增（**仅测试**，不是运行时配置） | **N1**（§17.1）；未设置时集成测试整体跳过 |
| `POST /api/links` 入参 `domain` | 新增字段 | **N6-1**；必须已在 `domains` 表登记，否则 422 `invalid_domain`；不传 = 默认域名 |
| `GET /{code}` 按请求 `Host` 定位域 | 语义 | **N6-1**；未登记的主机名按默认域名处理（不是 404） |
| `linkDTO.domain` + `short_url` 按所属域拼 | 新增字段 + 语义 | **N6-1**；`types.ts` 同步 |
| 缓存键 `link:v1:{code}` → `link:v2:{域}:{code}` | **键格式（跨进程契约）** | **N6-1**；api 写、worker 失效都要带域（`Evict` 收 `LinkRef`）。旧 `link:v1:*` 条目按 TTL 自愈，不发版清理 —— 但**跳转路径不再读它们**，所以上线后第一批请求会全部回源一次 |
| 统计响应 `countries` | 新增字段 | **N5**；`types.ts` 同步；未部署库文件时是空数组（前端整块隐藏） |
| `GEOIP_DB_PATH` | 新增环境变量 | **N5**；留空 = 一条 `INFO`，文件打不开 = 一条 `WARN`，两者都降级为 country 全空 |
| `GEOIP_TEST_DB` | 新增（**仅测试**） | **N5**；未设置时唯一会真查库的用例 SKIP |

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
| M5 | 每项自己的验收（见 §8 各小节）；**M5-1 见 §17.2** |
| **N1** | `POSTGRES_TEST_DSN` 下集成测试全绿；不带 DSN 时整包跳过且不红；CI `backend` job 含 postgres service |
| **N2** | 密码页不计点击；错误口令 401 不计点击；正确口令 303 → 带 cookie 的 GET 302 且计数恰好 +1；库里只有 bcrypt 摘要 |

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

## 15. 需要你拍板的点（M0–M4 已完成，逐条附「当前状态」）

下面每条的默认值**都已经实现并生效**，但没有你的明确确认 —— 也就是说
「默认」与「拍板」目前的差距只在纸面上，你要改哪条我就照改。

1. **CI 的 smoke job 频率**：只在 main + 手动（默认，推荐），还是每个 PR 都跑（反馈慢 3–5 分钟）？
   → 当前状态：已按默认实现（`.github/workflows/ci.yml` 的 `if:` 只放 main 与 `workflow_dispatch`）。
2. **`/metrics` 要不要做**：默认先不做；要做必须①只在内网可达②把 `metrics` 加进保留字表（理由见 §10）
   → 当前状态：未做。
3. **GeoIP 用哪份数据**：GeoLite2（需账号 + 署名）还是 DB-IP Lite（CC-BY）？
   → 当前状态：**已实现（N5 / `021277c`），两条路都支持** —— 代码只认「MaxMind DB 格式 + `country.iso_code`」，
   两个库的格式与字段一致，所以这从来不是非此即彼的选择。README 的署名段**同时**写明两种数据源的要求
   （DB-IP 走 CC BY 4.0 需署名；GeoLite2 按其许可同样需署名），仓库不附带任何 mmdb。
   本机验收实际用的是 `GeoLite2-Country.mmdb`（偏离第 9 条：DB-IP 直连被拒）。
4. **短码回收**：默认不回收（见 §13），若你要回收，我按 partial unique index 方案做并补迁移与测试
   → 当前状态：未做（不回收）。
5. **明细页的 IP**：默认只返回掩码；若你要完整 IP（排障场景），我加一个「仅所有者可见」的开关并写进 README 的数据可见性说明
   → 当前状态：已按默认实现 —— 只回掩码网段（IPv4 `/24`、IPv6 `/64`，见 `internal/pkg/ipmask`），
   原始 IP 只留在 `click_events.ip` 里；「仅所有者可见完整 IP」的开关未做。
6. **标签是否要预设 + 颜色**：默认自由文本（不加 UI 复杂度）
   → 当前状态：已按默认实现（自由文本、统一小写存储，无预设与颜色）。

---

## 16. 未完成清单（2026-09-19 盘点）

> 这一节回答「还剩什么」。基线是 `d76db54`（M0–M4 十二个批次全部完成并推送、CI 全绿）。
> 结论：**M0–M4 没有遗留** —— 剩下的全部是「按计划推迟的 M5」「计划里标注可选的」，
> 以及三条我在实施中发现的自动化缺口（计划里没写，属于我的判断）。

### 16.1 M5 的开工前置条件（内容与验收见 §8）

| 批次 | 迁移 | 前置条件 | 状态（2026-09-19 夜） |
| --- | --- | --- | --- |
| **M5-1 密码保护** | `000005_links_password` | **无** —— 四个大件里唯一可以立刻开工的 | ✅ **N2 / `566aa2b`** |
| **M5-2 GeoIP** | 无（`click_events.country` 已预留） | 数据源（§15 第 3 条） | ✅ **N5 / `021277c`** —— 两种库皆可，不是非此即彼 |
| **M5-3 自定义域名** | `000006_domains` | 真上线需要域名与证书（certbot / ACME），但**本机验收不需要**：`curl -H 'Host: a.local'` 即可 | 代码侧前半 ✅ **N6-1 / `0456154`**；后半（短码唯一约束按域）**待拍板 §18** |
| **M5-4 多租户** | `000008_orgs`（**破坏性**，需回填 + 双写窗口） | 计划里唯一建议「先别做」（§8 结论），等真实多人协作需求 | ⏸ 不做 |

### 16.2 计划里标注「可选 / 有需要再做」的

| 项 | 现状 | 什么时候值得做 |
| --- | --- | --- |
| M4-3 **方案 B**：后端 `GET /api/links/{code}/qr.svg`（`skip2/go-qrcode`） | 只做了方案 A（前端 `qrcode` 画 canvas + 下载 1024×1024 PNG，零后端改动） | 需要「邮件 / 印刷品里直接引用一个图片 URL」时 |
| **`/metrics` 零依赖文本端点** | 未做（§13 明确列为不做） | 要做必须①仅内网可达②把 `metrics` 加进 `pkg/shortcode/reserved.go` |
| **`POSTGRES_TEST_DSN` 的 store 集成测试** | **已立项为 §17.1（下一批次 N1）** | 越早越好 —— 迁移与 SQL 目前只有人工验收守着 |

### 16.3 三条自动化缺口（实施中的判断，不是原计划内容）

1. **store 层 SQL 没有自动化测试**：`internal/store/postgres` 只测了错误归类与 `opCtx`
   超时，迁移与查询语句本身（含 M4-2 新加的 keyset 分页 SQL）靠容器级验收 + `EXPLAIN`
   人工守住。补法：CI 的 smoke job 里已经有真 PG，加一个 `POSTGRES_TEST_DSN` 门控的
   集成测试，把「keyset 两页无重叠无缺口」「`tags @> ARRAY[...]` 走 GIN」这类断言搬进自动。
   → **已实施（§17.1 / N1 / `da6731d`）**：11 个集成用例 + 9 条断言清单 + CI 改法。实现时修正一处：不复用 smoke 的容器栈，
   而是给 `backend` job 加 `services.postgres`（compose 的 postgres 刻意不映射宿主端口，宿主的 `go test` 够不到它）。
2. **浏览器级验收不在 CI 里**：无头 Chrome + jsQR 那套脚本在 `.workbuddy/tmp/`（已
   gitignore，只在本机跑），所以「画布不溢出容器」这类版式断言**只有我在本地执行**。
   代价刚付过一次：二维码画布撑破容器（`qrcode` 写的行内 `320px` 盖过 Tailwind 类），
   旧验收只解码 `toDataURL()` 的像素 —— 那永远是 320×320，**版式坏了也全绿**，
   是有人在真浏览器里看了一眼才暴露的（见 README「六条踩过的坑」第 6 条）。
   补法：把 Playwright / Puppeteer 引进 CI（约 +1–2 分钟），或至少把版式断言固化成
   随仓库入库的可重复脚本。
   → **已实施（N4 / `0464754`）**：选了后一条 —— `frontend/e2e/` 入库（`cdp.mjs` / `harness.mjs` /
   `detail-page.mjs` / `password-gate.mjs` / `browser-check.mjs`），**零 npm 依赖**（Node 内置 `fetch` +
   `WebSocket` 直连 CDP，自己探 `CHROME_BIN`），CI 里用 runner 自带的 `google-chrome`，挂在 `smoke` job 上。
   没引 Playwright 是因为它要下浏览器（约 +1–2 分钟）而这里要守的是「版式没歪」这种几何断言，
   用不着它的选择器与追踪能力。踩到的坑：Chrome 的临时 profile 有几千个文件，
   本机沙箱的批量删除守卫会把整个进程带走（连汇总都来不及打印）—— 改成不删、交系统临时目录回收。
3. **前端零单测**：只有 typecheck / lint / build 三道门，没有 vitest 用例。
   `splitTags`、`describeClient`、`formatDateTimeSeconds`、`truncateMiddle` 都是纯函数，
   测起来成本很低。
   → **已实施（N3 / `9071d62`）**：vitest 接进 `frontend`，31 个用例；顺带把在这两个组件里
   **逐字重复**的 `splitTags` 提到 `src/utils/tags.ts`（行为逐字保持，只在 `ShortenForm.vue` 里
   把一次 `splitTags(tags.value)` 复用成局部变量，避免一次提交里调两遍）。
   有价值的一处：变异验证**第一次是失败的** —— 把 `tags` 的 `filter(length > 0)` 改成 `> 1`，
   用例全绿，因为我的用例里没有一个单字符标签。这说明「写了测试」与「测试能抓住这个改动」是两件事。

### 16.4 建议的下一步（已选定，2026-09-19）

> **结论**：同时细化并实施 **M5-1 短链密码保护**（= §17.2 / N2 / `566aa2b`）与
> **16.3-1 store 集成测试**（= §17.1 / N1 / `da6731d`），顺序 **N1 → N2** —— 两批均**已完成**，
> 完整规格见 **§17**，剩下什么见 **§17.3**。
> C 里的「先拍板 §15」不阻塞这两项 —— 第 3 条（GeoIP 数据源）只挡 M5-2。

- **A. M5-1 短链密码保护** —— 唯一不需要先拍板、又有真实产品价值的大件。
  落点：迁移 `000005` + 密码页（扩展 `handler/html.go`）+ `POST /{code}` + HMAC cookie
  + 一条限流规则 + 「密码页不计点击」的验收。
- **B. 补自动化缺口** —— 优先 16.3-1（CI 里已有 PG 可用），其次 16.3-2。
- **C. 先拍板 §15** —— 尤其第 3 条（GeoIP 数据源）与第 1 条（smoke 频率）。

### 16.5 索引：想了解什么看哪一节

| 想了解 | 看哪 |
| --- | --- |
| 已完成到哪一步（12 个批次 + 实测验收 + commit） | §0 |
| **N1 / N2 的完整规格（落点 / SQL / 验收 / commit 边界）** | §17（两个批次均已落地） |
| **N3–N6-1 的批次记录（含 11 条偏离）** | **§0** |
| **N6-2（短码唯一约束按域）的决策点 —— 待拍板** | **§18** |
| M5 各批次的落点、契约、验收标准 | §8 |
| 明确不做的事及理由 | §13 |
| 需要拍板的六个点与当前状态 | §15 |
| 迁移与契约变更台账（000002–000006 已实施；000007–000008 未实施） | §9 |
| 实测验收记录（①–⑪）、设计取舍与踩过的坑 | `README.md` 的「验收记录」「已知限制」「六条踩过的坑」 |

---

## 17. 下一批次规格（N1 / N2）

> 本节把 §16.4 的「方向」升级为「可执行规格」：**不改写** §8 与 §16.3 的原始记录，只补足落点文件、
> SQL、状态码语义、验收命令与提交边界。
> 两个批次各自独立可回滚 —— N2 只复用 N1 的测试脚手架，不依赖 N1 的代码。
> 顺序：**N1 → N2**。理由：N1 给 N2 的迁移 000005 提供自动回归网，且不依赖任何未拍板项。

### 17.1 批次 N1：store 层迁移与 SQL 集成测试进 CI（自动化缺口 16.3-1）

**问题**：`internal/store/postgres` 目前只测了错误归类与 `opCtx` 超时；迁移文件本身、以及
M4-1/M4-2 新加的 SQL（`tags @> ARRAY[...]`、`(occurred_at, id) < (...)` 的 keyset）至今
只靠容器级验收与人工 `EXPLAIN` 守着。本批次把这类断言搬进自动。

#### 17.1.1 落点

| 文件 | 动作 | 内容 |
| --- | --- | --- |
| `backend/internal/store/postgres/integration_test.go` | 新建 | `TestMain` + DSN 门控 + 建库护栏 + 迁移执行器 |
| `backend/internal/store/postgres/link_integration_test.go` | 新建 | links 表与列表查询 |
| `backend/internal/store/postgres/click_integration_test.go` | 新建 | click_events 与聚合查询 |
| `.github/workflows/ci.yml` | 修改 | `backend` job 加 `services.postgres` + `POSTGRES_TEST_DSN` |
| `README.md` | 修改 | 环境变量表补 `POSTGRES_TEST_DSN`；「本地开发」补怎么在本机跑 |

#### 17.1.2 脚手架（四个硬约束）

1. **门控**：`POSTGRES_TEST_DSN` 为空 ⇒ 每个集成测试 `t.Skip("未设置 POSTGRES_TEST_DSN")`。
   **不**在 `TestMain` 里直接失败 —— 否则所有不关心它的开发者与 PR 都会平白变红。
2. **建库护栏**：连接前解析 DSN，**要求库名以 `_test` 结尾**，否则 `t.Fatal`。因为下一步是破坏性的。
3. **破坏性准备**：`DROP SCHEMA public CASCADE` + `CREATE SCHEMA public`，然后按文件名排序执行
   `backend/migrations/*.up.sql`（路径用 `filepath.Join("..", "..", "..", "migrations")` —— 包目录是
   `backend/internal/store/postgres`，向上三层才回到 `backend`；`go test` 的 cwd
   就是包目录）。这一步**顺带把迁移文件本身纳入了回归**，正是 16.3-1 想要的。
4. **隔离与并行**：连接池 `MaxConns: 8`；用例之间靠数据隔离（每个用例自己生成 owner uuid 与短码），
   因此可以继续 `t.Parallel()` —— 不需要 `TRUNCATE` 这种会互相打架的清理。

#### 17.1.3 断言清单（本节即验收标准）

| # | 用例 | 断言 |
| --- | --- | --- |
| 1 | `TestMigrationsApply` | 全部 up 文件按序成功；`links` / `click_events` / 视图 `link_click_totals` 存在；`click_events_event_uid_key`（partial unique）、`links_tags_gin`、`click_events_link_time_id_idx` 存在；**旧索引 `click_events_link_time_idx` 已不在**（守住 §0 偏离第 6 条） |
| 2 | `TestLinkRoundTrip` | `Create` → `GetByCode` 往返一致（tags / created_ip / expires_at / status）；不存在 ⇒ `*domain.NotFoundError` |
| 3 | `TestLinkUpdateSemantics` | 只传 title 时其它列不动；`ClearExpires` 置 NULL；`Tags` 传空切片 = 清空、`nil` = 不动 |
| 4 | `TestListByOwnerKeysetPagination` | 同一 owner 造 5 条 → `limit=2` 翻三页：**无重叠、无缺口**、时间倒序、末页游标 `Valid=false` |
| 5 | `TestListByOwnerTagFilterUsesGinIndex` | 造 200 条（含 `tags=['ops']` 与不含）→ `ANALYZE links` → 过滤只命中含标签的行；`SET LOCAL enable_seqscan = off` 后 `EXPLAIN` 文本里出现 `links_tags_gin` |
| 6 | `TestInsertBatchDedupByEventUID` | 同一批（含相同 `event_uid`）插两次 ⇒ 条数不变；`event_uid` 为空串的行**不去重**（插两行）；不同 uid 正常插入 |
| 7 | `TestListByLinkKeysetPagination` | 造 12 条明细，**其中 3 条 `occurred_at` 完全相同**（正是迁移 000004 要修的洞）→ `limit=5` 翻三页无重叠无缺口、时间倒序；`Device` 过滤含 `unknown` 桶口径；`Since` 过滤生效 |
| 8 | `TestAggregateDayBoundaryIsUTC` | 一条 23:30Z、一条次日 00:30Z ⇒ `Daily` 恰好 2 天（守住 `AT TIME ZONE 'UTC'`）；空 device 归 `unknown` |
| 9 | `TestAddClickCountAndExpireDue` | `AddClickCount` 累加并返回总值、不存在 ⇒ `*NotFoundError`；`ExpireDue` 只动「active 且已过期」、`limit` 生效、返回短码列表 |

> 第 5 条是全批次唯一的「计划形状」断言。用 200 行 + `ANALYZE` 消除计划器在十行小表上选 seqscan 的抖动，
> 让它成为**确定性断言**而不是偶发红 —— 人工验收（§0 容器级 ⑨）当时是 `SET enable_seqscan=off` 直接看的，
> 自动化必须把样本量补上才算复现。
>
> 集成测试第一次跑就发现一处真实不一致：`links.created_ip` 用 `::text` 读出来会带掩码长度
> （`203.0.113.7/32`），而这一列存的始终是单个地址；已与 `click_events.ip` 一样改用 `host()`。

#### 17.1.4 CI 改动（只动 `backend` job）

```yaml
    services:
      postgres:
        image: postgres:18.6-alpine            # 与服务端同 tag
        env:
          POSTGRES_USER: ashen
          POSTGRES_PASSWORD: ashen
          POSTGRES_DB: ashen_test
        ports: ["5432:5432"]
        options: >-
          --health-cmd "pg_isready -U ashen -d ashen_test"
          --health-interval 5s --health-timeout 3s --health-retries 10
    env:
      POSTGRES_TEST_DSN: postgres://ashen:ashen@localhost:5432/ashen_test?sslmode=disable
```

- 服务容器**不占** PR 关键路径：`backend` job 仍是一分钟量级（PG 冷启动约 5s）。
- `smoke` job 不动（它本来就有真容器栈）。
- **为什么不用 compose 的 postgres**：它刻意不映射宿主端口（`docker-compose.yml` 只 `expose`），
  宿主机上的 `go test` 够不到它；而 Actions 的 `services:` 天生带健康检查与端口映射。

#### 17.1.5 验收（提交前必须实测并留档）

```powershell
cd F:/WorkSpace/Coding/Go/AshenCourier
docker compose -f docker-compose.dev.yml up -d postgres
docker compose -f docker-compose.dev.yml exec postgres createdb -U ashen ashen_test   # 首次
cd backend
$env:POSTGRES_TEST_DSN = 'postgres://ashen:ashen@localhost:5432/ashen_test?sslmode=disable'
go test -race -count=1 ./internal/store/postgres/ -v
Remove-Item Env:POSTGRES_TEST_DSN
go test -race -count=1 ./...     # 不带 DSN：集成测试必须整体跳过且全绿
go vet ./...
gofmt -l .                       # 必须无输出
```

**变异验证（防假绿）**：临时把迁移 000004 的索引名改错、或删掉 `InsertBatch` 的
`ON CONFLICT ... WHERE event_uid IS NOT NULL` 谓词 ⇒ 对应用例必须变红；改回 ⇒ 绿。

**提交边界**：`test(store): 迁移与 SQL 集成测试，CI backend job 加 postgres service（16.3-1）`
（**已落地**：`da6731d`）

---

### 17.2 批次 N2：短链密码保护（M5-1 的落地版）

§8 的 M5-1 给了设计意图；本节补的是**会写进代码的那些决定**：迁移 SQL、状态码、cookie 属性、
缓存策略、限流规则、验收命令与测试清单。

#### 17.2.1 落点

| 文件 | 动作 |
| --- | --- |
| `backend/migrations/000005_links_password.{up,down}.sql` | 新建 |
| `backend/internal/domain/link.go`、`domain/cache.go` | 修改（字段 + 不变量） |
| `backend/internal/store/postgres/link.go` | 修改（列清单四处同步 + Update 的 COALESCE） |
| `backend/internal/store/redis/cache.go` | 修改（线格式加一个布尔） |
| `backend/internal/service/unlock.go`（新）、`service/linkpassword.go`（新）、`service/shortener.go` | 签发/校验 + 口令哈希 + `VerifyPassword` |
| `backend/internal/handler/{html,redirect,router,dto,link}.go` | 密码页 + `POST /{code}` + 契约字段 |
| `backend/internal/config/config.go`、`backend/cmd/api/main.go` | 解锁 TTL（内置默认）+ 装配 |
| `backend/cmd/smoke/main.go` | 三条新用例（24 → 27） |
| `frontend/src/api/types.ts`、`components/ShortenForm.vue`、`views/LinkDetailView.vue` | 契约 + 创建表单 + 徽章/设置清除 |
| `README.md` | API 表 + 已知限制 + 验收记录 |

#### 17.2.2 迁移 000005

```sql
-- up
ALTER TABLE links ADD COLUMN password_hash text NOT NULL DEFAULT '';
COMMENT ON COLUMN links.password_hash IS
    '访问口令的 bcrypt 摘要（cost 12）；空串 = 不设口令。跳转路径只读缓存里的「有没有口令」布尔，摘要只在 POST /{code} 上校验一次';

-- down（严格互逆）
ALTER TABLE links DROP COLUMN password_hash;
```

不加索引（没有按口令查询的场景）。`NOT NULL DEFAULT ''` 与 `tags` 同一个理由：可空会让每个查询都要
写 `coalesce`，而 `COALESCE($n, password_hash)` 这种「不动就保持原值」的更新语义在 NULL 下会变得微妙。

#### 17.2.3 契约与配置变更

| 变更 | 类型 | 说明 |
| --- | --- | --- |
| `POST /{code}` | 新方法 | 表单体 `password`；成功 303 → `/{code}`，失败 401 |
| `GET /{code}` | 语义扩展 | 命中带口令且未解锁的链接 ⇒ **200 + 密码页**（不再是 302） |
| `linkDTO.password_protected bool` | 新增字段 | `json:"password_protected,omitzero"`（沿用 `dto.go` 的 bool 规则）；**永不输出 hash** |
| 创建/修改入参 `password` / `clear_password` | 新增字段 | 创建时可选；PATCH 可设置或清除 |
| `POSTGRES_TEST_DSN` | 新增（**仅测试**） | 未设置时集成测试整体跳过；不是运行时配置 |
| 环境变量 | **零新增** | 解锁 TTL 是内置默认值（`DefaultLinkUnlockTTL = 30m`），与缓存 TTL 等同类 |

#### 17.2.4 关键语义（照此实现，不要再自行发挥）

```text
GET /{code}
  ├ 形态/保留字校验（不变）→ Resolve（缓存优先，miss 才回源）
  ├ Redirectable 失败 → 404 / 410（不变；**先于**口令闸门 —— 死链永不显示口令页）
  ├ 无口令 或 解锁 cookie 有效 → 302 + 记一次点击（不变）
  └ 有口令 且 cookie 无效/缺失 → 200 密码页（no-store + noindex，**不计点击**）

POST /{code}               ← 新增；nginx 的短码 location 不限方法，无需改 nginx
  ├ 形态/保留字校验 → VerifyPassword（库读 + Redirectable + bcrypt 比对）
  ├ 口令错误 → 401 + 重新渲染密码页（**不计点击**）
  └ 口令正确 → Set-Cookie + 303 See Other → Location: /{code}（**不计点击**）
                          └ 浏览器随后 GET → 302，这一次才是「真正的跳转」，只记 1 次
```

- **为什么是 303 而不是 302**：POST 返回 302 时浏览器可能用 POST 重放 `Location`；303 明确「换个 GET 去取」。
  于是「解锁」天然不计点击，计点击的是随后那个 GET —— 一次解锁恰好 +1，不是 +2。
- **Cookie**：名 `ac_unlock`，值 = 签名凭据（**code 装进签名体内**），`Path=/`、`HttpOnly`、
  `SameSite=Lax`、`MaxAge = TTL`，`Secure` **仅当 `PUBLIC_BASE_URL` 是 https** —— 写死 `Secure`
  会让 `http://localhost:8080` 与局域网 IP 永远解锁不了。
- **密钥域分离**：解锁凭据的 HMAC 子密钥由 `JWT_SECRET` 派生（`HMAC-SHA256(secret, "ashen-courier/link-unlock/v1")`），
  不直接共用 —— 同一条密钥签两类凭据会互相放大泄露面。比较用 `hmac.Equal`（常数时间）。
- **缓存里不放摘要**：`CachedLink` 只多一个 `PasswordProtected bool`。跳转路径只需要「有没有口令」，
  而 Redis 转储泄露不该 enable 离线爆破；比对只在被限流的 `POST /{code}` 上做一次库读。
- **不升 `link:v1:` 缓存键版本**：上线顺序是「先迁移、后发版」，发版那一刻库里不存在任何带口令的行，
  旧缓存条目解出 `PasswordProtected=false` **恰好是正确的**（不存在漏判窗口）；而线格式只是加字段，
  不是改名，注解里禁止的是后者。
- **限流**：scope `unlock`、维度 `ByIP`、20 次 / 10 分钟（复用 login 的取值）。scope 不同 ⇒
  Redis 键与 login / redirect 完全隔离，不会互相吃配额。
- **不加 CSRF token**：这个 POST 不改变任何服务端状态、不依赖会话（只证明「知道口令」），
  CSRF 的收益仅仅是「帮受害者解锁一条链接」。理由写进代码注释，免得后人当成漏项。
- **口令强度**：复用 `ValidatePassword`（8–72 字节）与 `BcryptCost = 12`，不另造第二套规则；
  72 是 bcrypt 的截断上限，必须显式拒绝。

#### 17.2.5 边界情况

| 场景 | 期望 |
| --- | --- |
| 有口令 + 已删除 / 停用 / 过期 | 仍是 404 / 410（Redirectable 先于口令闸门） |
| Redis 挂 | 回源 PG（既有降级路径），口令页照常 |
| `POST /{code}` 遇到无口令链接 | 303 → GET → 302，正常计一次点击 |
| 畸形 / 截断 / 篡改 cookie | 静默视为未解锁（不报错页、不 500） |
| 超长请求体 | `http.MaxBytesReader`（表单很小，1 KiB 足够）→ 413 |
| 口令错误 ×N | `unlock` scope 429 + `Retry-After`；`RATE_LIMIT_DISABLED` 对它同样生效 |
| 限流器故障 | 沿用既有降级：放行 + warn |
| 详情接口 | 只有 `password_protected` 布尔，绝不输出 hash |
| `password` 与 `clear_password` 同时传 | 422 `invalid_password`（比照 `expires_at` / `clear_expires`） |
| `password` 传空串 | 422（清除请用 `clear_password`），避免「空串表示清除」与「不传表示不动」的歧义 |

#### 17.2.6 验收（可执行）

```powershell
cd F:/WorkSpace/Coding/Go/AshenCourier
docker compose up -d --build ; docker compose ps      # 5 个 healthy，migrate 到 000005

# ① 建带口令的短链 + 摘要确实落库（只有摘要、没有明文）
$c = curl.exe -s -XPOST localhost:8080/api/links -H 'content-type: application/json' -d '{"target_url":"https://example.com/locked","password":"smoke-pass-9f3a"}' | ConvertFrom-Json
$code = $c.link.short_code ; $key = $c.manage_key
$c.link.password_protected                            # True
docker compose exec postgres psql -U ashen -d ashen -c "select left(password_hash,7), length(password_hash), password_hash = 'smoke-pass-9f3a' from links where short_code='$code'"
#   期望 bcrypt cost 12 前缀 / 长度 60 / 与明文比较为 f

# ② 未解锁：200 密码页、不是跳转、不计点击
curl.exe -si "localhost:8080/$code" | Select-Object -First 3
curl.exe -s -H "X-Manage-Key: $key" "localhost:8080/api/links/$code/stats?days=1" | ConvertFrom-Json | Select-Object total_clicks
#   期望 200 + text/html；total_clicks = 0（注意：用 stats 的 total_clicks，不是详情 click_count）

# ③ 错误口令 → 401，计数仍 0
curl.exe -si -XPOST "localhost:8080/$code" -d 'password=wrong-guess' | Select-Object -First 1

# ④ 正确口令 → 303 + Set-Cookie（HttpOnly / SameSite=Lax / Path=/）
curl.exe -si -XPOST "localhost:8080/$code" -d 'password=smoke-pass-9f3a' | Select-Object -First 6

# ⑤ 带 cookie 的 GET → 302，且计数恰好 +1
curl.exe -si -b "ac_unlock=<上一步的值>" "localhost:8080/$code" | Select-Object -First 3
curl.exe -s -H "X-Manage-Key: $key" "localhost:8080/api/links/$code/stats?days=1" | ConvertFrom-Json | Select-Object total_clicks
docker compose exec postgres psql -U ashen -d ashen -c "select count(*) from click_events where short_code='$code'"

# ⑥ 清除口令后立刻可跳转（证明 Update 主动失效了缓存）
curl.exe -s -XPATCH -H "X-Manage-Key: $key" -H 'content-type: application/json' -d '{"clear_password":true}' "localhost:8080/api/links/$code" | Out-Null
curl.exe -si "localhost:8080/$code" | Select-Object -First 1        # 302

# ⑦ 迁移往返：down 1 / up 各两次无报错，之后新跳转仍正常

# ⑧ 端到端冒烟（含三条新用例）
cd backend ; go run ./cmd/smoke -base http://localhost:8080 -expect-spa     # 期望 27/27
```

浏览器侧（无头 Chrome，沿用 `.workbuddy/tmp/browser-check.mjs`）：创建表单填口令 → 详情页出现
「受口令保护」徽章 → 打开短链看到口令页 → 输错一次看到错误提示 → 输对跳转。

#### 17.2.7 测试清单与变异验证

| 文件 | 用例 |
| --- | --- |
| `service/unlock_test.go`（新） | 签发/校验往返；过期、换 code、篡改签名、空串/畸形 token ⇒ `ErrUnauthorized`；**换一个 secret 签的 token 必须不通过** |
| `service/linkpassword_test.go`（新） | 摘要前缀是 bcrypt cost 12 且 ≠ 明文；比对正/误；7 字节与 73 字节都 422 |
| `service/shortener_test.go`（改） | Create 带口令 ⇒ `HasPassword()`；Update 设置/清除；`VerifyPassword` 四条分支（无口令/正确/错误/已删除） |
| `handler/password_test.go`（新） | 未解锁 GET ⇒ 200 + HTML + 无 `Set-Cookie`；错误口令 ⇒ 401；正确 ⇒ 303 + cookie 属性齐全；`SecureCookies=false` 时不带 `Secure` |
| `handler/link_test.go`、`handler/router_test.go`（改） | `buildPatch` 的两条 422；路由表 12 → 13 条（新增 `POST /{code}`） |
| `store/redis`（改） | 线格式往返带 `password_protected`；**旧线格式（没有该字段）解出来是 false** |
| 17.1 的集成测试（改） | `password_hash` 的 Create/Update 往返与清除 |

**冒烟怎么接**：新增的三条用例挂在**已经建好的** `customCode` 链接上（用 `PATCH` 设口令），
不新建链接 —— 创建接口是 10 次/分钟/IP，而现有 24 项里已经用掉 8 次，再建几条会把最后的限流
用例变成偶发红。「密码页不计点击」的断言用 `stats` 的 `total_clicks`（基线 + 待同步增量），
**不是**详情接口的 `click_count` —— 后者只有 PG 基线，worker 没刷之前恒为 0，断言会假通过。

**测试耗时**：bcrypt cost 12 在 `-race` 下每次要数秒。测试里必须**共用一份摘要**（`sync.Once`）
并且只保留「对 / 错」两次真实比对，否则 handler 包的单测会从 20s 变成 55s（实测）。

**变异验证**：删掉口令闸门 ⇒ 冒烟 ① 必红；把 401 与 303 写反 ⇒ ③ 必红；把 cookie 的 `Secure`
写死为 `true` ⇒ 本机 ⑤ 必红。

**提交边界**：`feat(links): 短链密码保护（迁移 000005 + 密码页 + POST /{code} + HMAC cookie）（M5-1）`
（**已落地**：`566aa2b`）

---

### 17.3 这三个批次做完之后还剩什么（2026-09-19 夜 更新）

N1 / N2 已完成（`da6731d` / `566aa2b`），16.3 的三条缺口也全部补齐：

- ~~**16.3-2 浏览器级验收进 CI**~~ → **已完成（N4 / `0464754`）**：`frontend/e2e/` 入库并挂上 `smoke` job，
  19 项（含二维码的 4 条版式断言）每次推 main 都真跑。**代价说清楚**：挂 `smoke` 意味着 PR 不跑浏览器验收
  —— 这是「`docker compose up -d --build` 要几分钟」的必然结果，不是漏项。
- ~~**16.3-3 前端零单测**~~ → **已完成（N3 / `9071d62`）**：vitest 31 个用例进 CI。
- **M5 只剩两件**：
  - **M5-2 GeoIP** → **已完成（N5 / `021277c`）**，数据源按 `DB-IP Lite` / `GeoLite2` 二者皆可（偏离第 9 条）。
  - **M5-3 自定义域名** → **代码侧完成前半（N6-1 / `0456154`）**：数据模型 + `Host` 定位 + 按域缓存键都就位；
    后半「短码唯一约束改成按域」是 **§18 的待拍板项**。真上线另需在 nginx 加 `server_name` 与证书（配置工作，不改代码）。
  - **M5-4 多租户** → 仍是计划里唯一建议「先别做」的大件（§8 结论），等真实多人协作需求。
- **§15 的六个待拍板项**：现在只剩第 3 条（GeoIP 数据源）在事实上被定成了「两种库都支持」，
  其余五条都已按默认实现且写进 README。
- ~~还欠一次 CI 验证~~：已完成 —— run `35424447615` 三个 job 全绿，`backend` 里
  `internal/store/postgres` 跑了 **1.271s**（真 PG，不是跳过），`smoke` **27 / 27**。
  ⚠️ **但 N3–N6-1 这四个批次还没有经过一次 CI 验证**（都只在本机跑过门禁）——
  推上去之后要确认三件事：`frontend` job 的 `pnpm test` 能过（新增了 vitest 依赖）、
  `smoke` job 里 `frontend/e2e/` 真能在 runner 自带的 Chrome 上跑起来（本机用 `CHROME_BIN` 探测）、
  以及 `backend` job 的集成测试在加了 `000006` 之后仍全绿。

---

---

## 18. N6-2 的决策点：短码唯一约束要不要改成按域（**待拍板**）

**N6-1 已经完成的部分**：`domains` 表、`links.domain_id`、`Host` 归一化与按域定位、
按域分开的缓存键、`short_url` 按所属域拼。**唯一没动的是 `links.short_code` 的全局唯一约束** ——
也就是说，现在**同一个短码在两个域下不能共存**（第二条会被 `ConflictError` 挡掉）。

### 18.1 要改的话，改的是什么

```sql
-- 000007（示意，尚未实施）
ALTER TABLE links DROP CONSTRAINT links_short_code_key;
CREATE UNIQUE INDEX links_domain_short_code_key
    ON links (domain_id, short_code) NULLS NOT DISTINCT;   -- ⚠️ 这四个字不能省
```

**`NULLS NOT DISTINCT` 不能省，这不是风格问题**（本机 PG 18 实测）：

| 写法 | 插两条 `(NULL, 'sale')` | 结果 |
| --- | --- | --- |
| `UNIQUE (domain_id, short_code)` | 两条**都插进去了** | 默认域名（`domain_id IS NULL`）下可以出现**任意多个同码短链** —— 比不改还糟 |
| `UNIQUE NULLS NOT DISTINCT (domain_id, short_code)` | 第二条被拒：`duplicate key value violates unique constraint` | 默认域名也真的按域唯一 |

原因是 SQL 的唯一约束把每个 NULL 视作互不相等，而**默认域名的行恰好全是 NULL** ——
也就是说不加那四个字，这次迁移保护的恰好是「自定义域名」，放过的是「默认域名」，
而默认域名是绝大多数短链所在的地方。

### 18.2 真正的代价不在 SQL，在管理端

现在**所有管理端接口都按短码定位**（README 的 API 表）：

| 接口 / 键 | 定位方式 |
| --- | --- |
| `GET` / `PATCH` / `DELETE` `/api/links/{code}` | 短码 |
| `GET /api/links/{code}/stats` | 短码 |
| `GET /api/links/{code}/clicks` | 短码 |
| `POST /api/links/{code}/claim` | 短码 |
| `clicks:cnt:{code}`（待同步增量） | 短码 |

短码一旦只在域内唯一，`/api/links/{code}` 就**有歧义**（同码两条，返回哪条？）。三条出路：

1. 给这些接口加 `?domain=`（默认域可省略）；
2. 路径改成 `/api/domains/{domain}/links/{code}`；
3. 让管理端仍按全局唯一定位 —— 但那等于要求短码事实上全局唯一，也就是**不做这件事**。

无论 1 还是 2，都是**破坏性 API 变更**：`frontend/src/api/` 全部要跟着改，
`cmd/smoke` 与 `frontend/e2e/` 里每一处 URL 也要改，`TestCreateAcceptsDomainField` 这类用例的前提会变。

### 18.3 三个选项

| 选项 | 得到什么 | 付出什么 |
| --- | --- | --- |
| **A. 不做（我的建议）** | 零风险。当前语义已经在**三处**写死并注释：`LinkRepository.GetByCode` 的注释、README 的 `links` 数据模型行、N6-1 的验收断言（跨域双向 404） | 「`a.com/sale` 与 `b.com/sale` 指向不同目标」这个能力没有 |
| **B. 做，管理端加 `?domain=`** | 路径形状不变，默认域可省略 ⇒ 老 URL 不破 | 「有时必须传、有时不用」；参数漏传会静默落到默认域（而结果看起来是成功的）；12 处调用点 + 前端 + 冒烟 + 浏览器验收都要动 |
| **C. 做，路径改成 `/api/domains/{domain}/links/{code}`** | 语义最干净（资源层级与数据模型一致） | 所有管理端 URL 都变；默认域名在路径里怎么表达需要额外约定（`/api/links/{code}` 与 `/api/domains/_/links/{code}` 并存？） |

**我建议 A**，理由不是「改动大」而是**收益不成立**：
「同一个短码在两个域下指向不同目标」听起来像个需求，但它真正的场景通常是
「同一个目标页在多个域下各有一个短码」—— 那个用两个短码就能表达，不需要共享同一个 code。
而共享 code 会带来一条**长期的语义负担**：管理端、计数键、缓存的「短码」到底指哪一条，
此后每一处都要停下来想一想。这也正是 N6-1 把 `GetByCode` 与 `GetByCodeInDomain`
**拆成两个方法**的原因（管理端按短码、跳转必须按域）—— 做了 N6-2 之后，这两个方法的区别就没有意义了。

### 18.4 如果你要 B 或 C，我的实施边界

单开一批（`N6-2`），边界如下：

- 迁移 `000007`：`UNIQUE NULLS NOT DISTINCT (domain_id, short_code)`（PG 15+ 特性，本项目 PG 18 实测可用）
- 短码生成的冲突重试改成「**域内**冲突才重试」；**保留字校验不变** —— 保留字是跨域的，
  `api` 在任何域下都不能当短码（否则 `a.com/api` 与 `b.com/api` 会一个 404 一个跳转）
- 管理端所有按短码定位的调用点改成 `(域, 短码)` 二元组（`Update` / `SoftDelete` / `Claim` / `Stats` / 明细）
- 验收：「同 code 在两个域下各建一条 → 各自跳自己的目标；管理端按 `?domain=` 分别拿到两条」，
  并且**补一条反向断言**：同一个域内重复短码仍然必须被拒（否则这次迁移就白做了）
- 变异验证：把 `NULLS NOT DISTINCT` 去掉 → 「默认域名下同码被拒」这条断言必须变红
