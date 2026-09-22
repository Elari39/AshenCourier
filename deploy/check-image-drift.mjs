#!/usr/bin/env node
// ============================================================
// 镜像 tag 漂移守卫：api 与 worker 必须来自**同一次构建**
// ============================================================
//
// 要守的问题（审计「已知缺口」第 2 条）：
//
//   backend/Dockerfile 一份，compose 里 backend 与 worker 两个服务都用它，
//   但 compose 会给**每个服务各建一个 tag**（ashencourier-backend、
//   ashencourier-worker）。于是 `docker compose build backend` 是完全合法的命令，
//   而它只重建 backend —— worker 会继续跑**旧二进制**。两种后果都不报错：
//
//     - `docker compose build backend && docker compose up -d` 之后，
//       worker 容器被重建了，但它用的是**没变**的旧 tag ⇒ 跑的还是老代码。
//       症状是「改了 worker 的行为，容器重启了、日志也重新打了，行为却没变」。
//     - 2026-09-21 实际踩到：worker 停在两天前的构建，而所有测试与 CI 都是绿的
//       （单测不碰镜像，e2e/smoke 只验行为）。
//
// 判据用**内容层**（RootFS.Layers）而不是构建时间：
//
//   1. 时间戳会骗人 —— backend 与 worker 是两次独立 build，冷缓存下 Created
//      会差几秒；而同一份源码热缓存时又可能完全相同。用它做判据必然要在
//      「误报」与「漏报」之间选一个。
//   2. 层是内容寻址的：COPY 进去的二进制一变，那一层的 diff_id 就变。因此
//      「两个 tag 的层序列逐项相等」与「两者是同一份源码构建的」等价 ——
//      与时间、缓存状态、构建顺序都无关。
//   3. 注意**不能**比 image id：compose 会给每个服务加自己的 label
//      （com.docker.compose.service=…），label 属于镜像 config ⇒ 两个 id
//      天生就不同（实测 backend/worker 的 id 一直不等，而层一直相等）。
//
// 顺带守第二条：**容器跑的是不是当前 tag**。
//   `docker compose build` 之后忘了 `up -d`，或只 up 了其中一个，容器会继续
//   跑旧镜像 —— 这时 tag 是新的、容器是旧的，CI 与 e2e 都可能看不出来。
//   判据是容器 `.Image` == 对应 tag 的 `.Id`（前者是容器实际启动的镜像 id）。
//
// 本守卫**探测不到**「两个都旧」（改了源码、两个 tag 都没重建）：那时两者
// 层仍然相等、容器也仍与 tag 相符，内部是自洽的。要覆盖它只能在构建流程里
// 比源码与产物的时间 —— 那属于另一件事，刻意不做（见 README 的已知限制）。
//
// 用法：
//   node deploy/check-image-drift.mjs
//   node deploy/check-image-drift.mjs --services backend,worker
//
// 零 npm 依赖；**刻意不经过 shell**（execFileSync 直接传参数数组）——
// 本机 Git Bash 缺 coreutils，且会吃掉 `{{.Field}}` 这类单字段模板的花括号，
// 走 shell 会把 `--format '{{.Created}}'` 变成字面量 `.Created`。
//
// 退出码：0 = 无漂移；1 = 有漂移或环境不对（缺 docker / compose / 服务）。

import { execFileSync } from "node:child_process";
import path from "node:path";

// 仓库根 = 本文件所在目录的上一级（脚本放在 deploy/ 下）
const REPO_ROOT = path.resolve(import.meta.dirname, "..");

// 必须来自同一次构建的服务对。它们是同一个 Dockerfile 的两个入口
// （Dockerfile 的 ENTRYPOINT 是 /app/api，worker 靠 compose 的 command 换入口）。
const DEFAULT_SERVICES = ["backend", "worker"];

function parseArgs(argv) {
  let services = DEFAULT_SERVICES;
  for (let i = 0; i < argv.length; i++) {
    if (argv[i] === "--services" && argv[i + 1]) {
      services = argv[i + 1]
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
      i++;
    } else if (argv[i] === "-h" || argv[i] === "--help") {
      console.log("用法: node deploy/check-image-drift.mjs [--services backend,worker]");
      process.exit(0);
    } else {
      console.error(`未知参数：${argv[i]}`);
      process.exit(2);
    }
  }
  if (services.length < 2) {
    console.error("至少要给两个服务才能比「是否同一次构建」");
    process.exit(2);
  }
  return { services };
}

// docker 调用一律不经 shell：参数数组直传。
// stdio 里带上 stderr 便于把 docker 自己的报错原样带给用户。
function docker(args, { allowFail = false } = {}) {
  try {
    return execFileSync("docker", args, {
      cwd: REPO_ROOT,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    }).trim();
  } catch (err) {
    if (allowFail) return null;
    const stderr = (err.stderr || "").toString().trim();
    throw new Error(`docker ${args.join(" ")} 失败：${stderr || err.message}`);
  }
}

// docker compose ps --format json：
// compose v2 每个容器输出一行 JSON；某些版本会输出一个 JSON 数组。两种都吃。
//
// ⚠️ 注意 `ps` 的 **Image 字段不能当 tag 用**：给的是「容器实际镜像的引用」。
// 一旦 tag 指向的镜像与容器跑的不是同一个，compose 就会把该字段解析成镜像 ID，
// 于是「容器 == tag」退化成恒真 —— 这正是本守卫第一次变异时踩到的假绿
// （把 worker 的 tag 指到 frontend 镜像，守卫居然报「无漂移」）。
// 服务对应的 tag 一律从 `compose config` 推导（见 serviceImage()）。
function composePs() {
  const out = docker(["compose", "ps", "-a", "--format", "json"], { allowFail: true });
  if (out === null) {
    throw new Error("`docker compose ps` 失败 —— 先确认 docker 已启动、且当前目录是仓库根");
  }
  const text = out.trim();
  if (!text) return [];
  if (text.startsWith("[")) return JSON.parse(text);
  return text
    .split("\n")
    .filter((l) => l.trim())
    .map((l) => JSON.parse(l));
}

// serviceImage 给出服务**在配置层面**对应的镜像名（也就是 tag）。
//
// 优先用 `services.<name>.image`（显式写了 image 的服务，如 postgres）；
// build 型服务没有 image 字段，此时按 compose 自己的推导规则拼 `<project>-<service>`
// （compose v2 用连字符分隔，实测就是 `ashencourier-backend`）。
// project 名取自 `compose config` 的顶层 name（目录名会影响它，所以不能写死）。
function serviceImage(config, service, project) {
  const explicit = config?.services?.[service]?.image;
  if (explicit) return explicit;
  return `${project}-${service}`;
}

// tmpl 是模板**体内**的表达式，例如 ".Id" / ".Created" / "json .RootFS.Layers"。
// 传体内表达式而不是完整 `{{…}}`：单字段模板在本机 Git Bash 里会被吃掉花括号，
// 而这里统一拼一次、且不经 shell，两边都稳。
function imageField(tag, tmpl) {
  return docker(["image", "inspect", tag, "--format", `{{${tmpl}}}`], { allowFail: true });
}

function containerImageId(containerId) {
  return docker(["inspect", "--format", "{{.Image}}", containerId], { allowFail: true });
}

function main() {
  const { services } = parseArgs(process.argv.slice(2));

  // compose 配置（含 project 名与显式 image）：service 对应的 tag 从这里推导，
  // 不从 `ps` 的 Image 字段取（理由见 composePs 的注释）。
  let config;
  try {
    config = JSON.parse(docker(["compose", "config", "--format", "json"]));
  } catch (err) {
    console.error(
      `读 compose 配置失败：${err.message}\n` +
        "  （compose 里的 ${DATABASE_URL:?…} 这类引用要求 .env 在场；先 `cp .env.example .env` 并填好必填口令）"
    );
    process.exit(2);
  }
  const project = config.name ?? path.basename(REPO_ROOT).toLowerCase();

  const ps = composePs();
  if (ps.length === 0) {
    console.error("compose 里没有任何容器 —— 先 `docker compose up -d --build`");
    process.exit(1);
  }
  // service -> 容器信息（同一服务可能有多个副本，取第一个即可）
  const byService = new Map();
  for (const c of ps) {
    const svc = c.Service ?? c.service;
    if (svc && !byService.has(svc)) byService.set(svc, c);
  }

  const missing = services.filter((s) => !byService.has(s));
  if (missing.length) {
    console.error(`compose 里找不到服务：${missing.join(", ")}（可用：${[...byService.keys()].join(", ")}）`);
    process.exit(1);
  }

  const failures = [];
  const info = new Map();

  for (const svc of services) {
    const c = byService.get(svc);
    const tag = serviceImage(config, svc, project);
    const id = imageField(tag, ".Id");
    const layers = imageField(tag, "json .RootFS.Layers");
    const created = imageField(tag, ".Created");
    if (id === null || layers === null) {
      failures.push(`服务 ${svc} 的镜像 tag \`${tag}\` 查不到 —— 它可能已被删掉，而容器还在（悬空容器）`);
      continue;
    }
    info.set(svc, { tag, id, created, layers: JSON.parse(layers), container: c });
    console.log(`  ${svc.padEnd(8)} tag=${tag}  created=${created}  层数=${JSON.parse(layers).length}`);
  }

  if (failures.length) {
    return report(failures);
  }

  // ---- 判据 1：所有服务的镜像内容层必须逐项相同 ----
  const [head, ...rest] = services;
  const base = info.get(head);
  for (const svc of rest) {
    const cur = info.get(svc);
    if (cur.layers.length !== base.layers.length || cur.layers.some((l, i) => l !== base.layers[i])) {
      const diffAt = base.layers.findIndex((l, i) => l !== cur.layers[i]);
      failures.push(
        `\`${cur.tag}\` 与 \`${base.tag}\` 的镜像内容层不同 —— 两者**不是同一次构建**的产物。\n` +
          `    首个不同的层：#${diffAt === -1 ? "(层数不同)" : diffAt}` +
          `（${base.tag} ${base.layers.length} 层 / ${cur.tag} ${cur.layers.length} 层）\n` +
          `    最常见的成因：只重建了一个服务 —— \`docker compose build backend\` 不会重建 worker。\n` +
          `    修法：\`docker compose build ${services.join(" ")} && docker compose up -d ${services.join(" ")}\``
      );
    }
  }

  // ---- 判据 2：容器跑的就是当前 tag（挡「build 了但没 up -d」） ----
  for (const svc of services) {
    const { tag, id, container } = info.get(svc);
    const cid = container.ID ?? container.Id;
    const running = containerImageId(cid);
    if (running === null) {
      failures.push(`服务 ${svc} 的容器 ${cid} 查不到 —— 它可能正在被替换`);
      continue;
    }
    if (running !== id) {
      failures.push(
        `服务 ${svc} 的容器跑的不是当前 tag：容器镜像 ${running.slice(0, 19)}… ≠ \`${tag}\` 的 ${id.slice(0, 19)}…\n` +
          `    成因：build 之后没 \`up -d\`（或只 up 了一部分），容器还挂在旧镜像上。\n` +
          `    修法：\`docker compose up -d ${svc}\``
      );
    }
  }

  return report(failures);
}

function report(failures) {
  if (failures.length === 0) {
    console.log("\n无漂移：以上服务来自同一次构建，且容器跑的就是当前 tag。");
    return 0;
  }
  console.error("\n镜像 tag 漂移：");
  for (const f of failures) console.error(`  ✗ ${f}`);
  console.error(
    "\n提示：两个后端 tag 必须同源 —— 重建时把服务名都列上：\n" +
      "  docker compose build backend worker && docker compose up -d backend worker\n" +
      "（CI 的 smoke job 跑的是 `docker compose up -d --build`，两个都会重建；\n" +
      "  本守卫在 CI 里是防 compose 文件被改成「只 build 其中一个」的回归网。）"
  );
  return 1;
}

process.exit(main());
