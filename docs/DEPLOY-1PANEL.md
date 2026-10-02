# 1Panel / Cloudflare 部署与回滚

目标：`https://shorten.miku831.fun` → Cloudflare → 1Panel OpenResty → frontend Nginx → Go API。此文档是配置交付；本轮没有登录或变更线上服务器。

## 生产环境变量

从 `.env.example` 创建生产专用环境文件，填写三个独立强密钥；不要复用本地审计口令。数据库密码包含 URL 保留字符时，连接串中的密码必须百分号编码。

```dotenv
PUBLIC_BASE_URL=https://shorten.miku831.fun
FRONTEND_BIND=127.0.0.1
FRONTEND_PORT=8080
APP_VERSION=<本次提交或发布版本>
```

保留 DATABASE_URL、REDIS_PASSWORD、JWT_SECRET 等其余必填项。PUBLIC_BASE_URL 同时用于后端返回地址、Secure cookie 和前端构建，修改后必须重新 build。生产使用明确的 `-f docker-compose.yml`，不要自动加载本机的 docker-compose.override.yml。

## 信任边界

1. Cloudflare 使用 **Full (strict)**，1Panel 配置有效源站证书。源站 80/443 入站只允许 Cloudflare 官方 IPv4/IPv6 网段；保留独立的受控运维入口。若证书续期依赖直接 HTTP 验证，改用 DNS 验证或明确安排受控续期流程。
2. 把 `deploy/1panel/cloudflare-realip.conf` 安装到 OpenResty 容器内 `/etc/nginx/ashen-cloudflare-realip.conf`。部署前与 https://www.cloudflare.com/ips/ 核对；该列表只属于最外层代理。
3. 将 `deploy/1panel/site.conf.example` 中的内容合入现有 HTTPS server。保留 1Panel 的证书配置，替换已有重复的 location /。外层从 CF-Connecting-IP 恢复地址，再覆盖 X-Real-IP、X-Forwarded-For、Host 和协议头。
4. 复制 `deploy/1panel/trusted-proxies.conf.example` 为生产文件；把两处示例地址替换为**应用 Nginx 实际看到的 1Panel 对端 IP**。挂载到 frontend 的 `/etc/nginx/trusted-proxies.conf:ro`。禁止写 `0.0.0.0/0`、全部 RFC1918 网段或整个共享网络。
5. 未挂载时默认不信任任何转发头，适合直连本地测试。配置错误会在访问日志中显现为网关 IP；不要为“修复 IP”扩大信任范围。

建议先 `nginx -t`（1Panel 中通常对应 OpenResty 的 nginx 命令），再 reload；应用容器也执行 `docker compose exec frontend nginx -t`。

## 两种连接方式

### OpenResty 运行在宿主或 host 网络

反代到 `http://127.0.0.1:8080`。应用保持回环端口绑定。Docker NAT 后应用看到的来源可能是网桥网关而非 127.0.0.1，必须以实际 `$realip_remote_addr` 为准配置单个可信地址。仅同宿主受信任进程能访问该入口。

根目录创建 `compose.production.yml`：

```yaml
services:
  frontend:
    volumes:
      - ./deploy/1panel/trusted-proxies.production.conf:/etc/nginx/trusted-proxies.conf:ro
```

运行命令使用 `docker compose --env-file .env.production -f docker-compose.yml -f compose.production.yml ...`。

### OpenResty 运行在普通 Docker 网络

容器内 127.0.0.1 指向 OpenResty 自己，不能反代到宿主回环。建立专用共享网络，只连接 OpenResty 和 frontend，指定 OpenResty 固定地址，并将这个单独地址加入信任文件。确认示例子网与现有网络不冲突。

```sh
docker network create --subnet 172.30.83.0/24 ashen-edge
docker network connect --ip 172.30.83.2 ashen-edge <实际OpenResty容器名>
```

将同样配置写入 1Panel 管理的 OpenResty 编排，确保容器重建后仍加入该网络。根目录生产覆盖文件：

```yaml
services:
  frontend:
    ports: !reset []
    volumes:
      - ./deploy/1panel/trusted-proxies.production.conf:/etc/nginx/trusted-proxies.conf:ro
    networks:
      default: {}
      edge:
        aliases: [ashen-frontend]
networks:
  edge:
    external: true
    name: ashen-edge
```

OpenResty 反代地址改为 `http://ashen-frontend:80`。`!reset` 需要支持该标签的 Compose v2；先用 `docker compose version` 和 `config --quiet` 验证。不要把 backend、数据库和 Redis 加入 edge 网络。

## 缓存与路由

- Cloudflare 不启用全站 Cache Everything。对 `/api/*`、短码跳转、口令页配置缓存绕过；不要缓存带 Set-Cookie 的响应。
- `/assets/*` 使用内容哈希和 immutable；HTML 外壳 no-cache；302、口令及失效页 no-store。
- `/metrics` 与 `/healthz/details` 仅内网访问，公网应是 404。公开 `/healthz` 只包含依赖状态。
- SPA 顶级路由、Vite 代理白名单和后端保留短码有同步测试。新增页面时一起更新。

## 升级到批次回刷

迁移 000007 增加 password_version，000008 增加 click_count_batches。旧计数基线原样保留，新版缓存前缀为 link:v3，旧条目自然过期。旧解锁 cookie 需要重新输入口令。

1. 保存旧镜像标签、版本和生产配置；完成 PostgreSQL 备份并验证可恢复。
2. 在外层开启维护响应，停止新流量。**先停止旧 API，让其退出并排空内存队列，旧 worker 继续运行至 clicks:dirty 为零且活动计数无残留，然后停止旧 worker。** 不可先停 worker 再等待它排空。
3. 检查 Stream 消费组 pending 和 lag，处理未落库事件。禁止删 Redis 数据卷或直接清空计数。
4. 从暂停流量后的数据库与 Redis 持久化数据再取一致备份。执行新迁移，构建并启动新 API、worker、frontend。禁止新旧 worker 混跑。
5. 检查容器健康、版本、匿名创建、口令修改后旧 cookie 失效、跳转、列表与统计，确认请求返回域名正确后解除维护。

查看计数状态（redis-cli 口令使用容器内环境，不在终端打印）：

```sh
docker compose exec redis sh -c 'REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli SCARD clicks:dirty'
docker compose exec redis sh -c 'REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli --scan --pattern "clicks:batch:v2:*"'
```

`clicks:epoch:v2:*` 是快照代际标记，确认后仍保留，不代表积压。批次去重表本轮不自动清理；监控其存储增长。

## 回滚

先维护模式停止新请求，停止新 API，保留新 worker 直到活动计数、dirty 和待确认批次全部排空，再停止 worker。记录备份后切换旧应用镜像；**保留新增数据库列和批次表**，不必执行 down 迁移。部署旧镜像时不要用旧迁移目录自动处理当前数据库版本。

回滚前清除已失效的旧 link:v2 缓存（只处理该缓存前缀，绝不清空 Redis），以免重新读取升级前的旧目标或口令状态。旧代码可能恢复历史缺陷；若无法排空新批次，应保持维护状态修复或恢复成对备份，不能直接强行启动旧 worker。恢复备份会丢失备份后的业务变更，必须明确恢复点。

## 本地验证

独立项目 `ashencourier-audit` 使用 18083（页面）、15483（PG）、16383（Redis），不要引用原项目数据卷。

```sh
docker compose --env-file <专用审计环境文件> -p ashencourier-audit -f docker-compose.yml -f deploy/compose.audit.yml up -d --build
node frontend/e2e/browser-check.mjs --base http://localhost:18083
node frontend/e2e/redesign-check.mjs http://localhost:18083
```

数据库集成测试使用库名以 `_test` 结尾的专用库，测试会重置该库 schema。Redis 测试设置 `REDIS_TEST_DB=15`，不得连接生产实例。代理模拟配置位于 `deploy/proxy-test/`，它使用回显上游验证信任边界，不是可上线的生产配置。
