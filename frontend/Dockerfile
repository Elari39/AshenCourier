# syntax=docker/dockerfile:1.7
# ============================================================
# AshenCourier 前端镜像：Vite 构建 → nginx 托管静态产物
#
# ⚠️ 构建上下文是「仓库根目录」，不是 frontend/ 本身。
#    因为镜像既要 frontend/ 的源码，也要 deploy/nginx/nginx.conf。
#    单独构建请用：
#      docker build -f frontend/Dockerfile -t ashen-courier-frontend .
#    （compose 里已经这么配好了，直接 docker compose up 即可）
# ============================================================

# ---------- 阶段 1：构建 ----------
FROM node:22-alpine AS build
WORKDIR /app

# corepack 会按 package.json 的 packageManager 字段锁定 pnpm 版本；
# 关掉下载确认提示，否则 CI 里会因为等待交互而卡住
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN corepack enable

# 先只拷依赖清单与 .npmrc：源码改动不会让依赖层失效，
# 同时让容器内的 pnpm 用与本地同一个 registry（镜像源在 .npmrc 里实测选过）
COPY frontend/package.json frontend/pnpm-lock.yaml frontend/.npmrc ./
RUN --mount=type=cache,id=pnpm-store,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile

# 再拷源码并构建
COPY frontend/ ./

# 短链基址（构建期注入）：只在落地页演示区用到，留空则回退 window.location.origin。
# 声明在 build 之前而不是 stage 开头 —— ARG 取值变化会让**后续所有层**失效，
# 靠后声明能保住上面那条 pnpm install 的缓存（改域名不该重装一遍依赖）。
ARG VITE_SHORT_BASE_URL=""
ENV VITE_SHORT_BASE_URL=$VITE_SHORT_BASE_URL
RUN pnpm build

# ---------- 阶段 2：运行时 ----------
# 钉到具体 minor（与 postgres:18.6-alpine / redis:8.10-alpine 一个规矩）：
# `nginx:alpine` 会随 minor 滚动，nginx 的配置指令在不同版本上行为会有差异
# （比如 IPv6 监听脚本、默认的 server_tokens / MIME 表），滚动 tag 会让
# 「昨天能起、今天起不来」找不到原因。1.31-alpine 与本机 nginx:alpine
# 当前指向的是同一个镜像（digest 相同），所以这次钉版本不改变现状。
FROM nginx:1.31-alpine AS runtime

# 用仓库里的 nginx.conf 整体覆盖默认配置：
# 反代 /api、短码正则 → backend，/assets 优先于短码正则，其余走 SPA fallback
COPY deploy/nginx/nginx.conf /etc/nginx/nginx.conf
# 安全响应头单独一份，由 nginx.conf 在**每个自带 add_header 的层级** include
# （nginx 的 add_header 是替换而不是合并，理由见该文件顶部）。
# 路径写死成 /etc/nginx/security-headers.conf —— 与 nginx.conf 里的 include 一致。
COPY deploy/nginx/security-headers.conf /etc/nginx/security-headers.conf

COPY --from=build /app/dist /usr/share/nginx/html

EXPOSE 80

HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1/ || exit 1

CMD ["nginx", "-g", "daemon off;"]
