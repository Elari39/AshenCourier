package httpx

import (
	"strings"
	"testing"
)

// 本文件守的是「公开面 / 内部面」的边界，它是**配置契约而不是代码契约**。
//
// 项目里有一批端点只该在内网可达：`/metrics`（Prometheus 抓取）与
// `/healthz/details`（完整诊断快照）。它们的可见性不由路由决定 —— 后端既不鉴权也不限流，
// 全靠 nginx 里那一行 `location = … { return 404; }` 兜底。
//
// 由此产生两个**方向相反**的静默失效，两条都必须用配置断言堵住：
//
//  1. 少写一行 404 ⇒ 端点掉进 `location /` 的 SPA fallback，以 **200 + index.html** 返回。
//     「软 200」比 403 更危险：扫描器会认为端点存在，而人肉核对时看到的是一张首页，
//     很容易以为「没事」。与 batch 2 那 10 条幽灵 SPA 路由是同一类病。
//  2. 反过来多写一行 404、把 `/healthz` 本身也挡掉 ⇒ 编排系统的存活探针永久失败。
//     所以本用例同时做一条**反向断言**：`/healthz` 必须仍在 proxy，且不是 404。
//
// 与 realip_test.go / nginx_headers_test.go 同样的处理：跨出 backend 模块去读仓库根的
// deploy/，仓库没完整检出时透明 SKIP（跳过要说出来，不能静默绿）。

// nginxNotFoundBlocks 是「必须在 nginx 里显式 404」的内部端点清单。
//
// 加新端点时两处都要改，而两边漏改的后果不对称：
//   - 只加清单不加路由 → 公网多一个 404（无害）
//   - 只加路由不加清单 → **静默泄露**（与 /metrics 同一批数据的诊断端点变成匿名可读）
//
// 所以清单是这份文件里的「单一记录点」，改 router.go 的公共端点时必须回来加一行。
var nginxNotFoundBlocks = []string{
	"/metrics",
	"/healthz/details",
}

// TestNginxBlocksInternalDiagnostics 断言内部诊断端点被 nginx 显式挡在公网之外。
func TestNginxBlocksInternalDiagnostics(t *testing.T) {
	t.Parallel()

	conf, ok := nginxConf(t)
	if !ok {
		return
	}
	blocks := parseBlocks(conf)

	// 下限探针：解析出空集合是最容易发生的失效模式 —— 空 == 空 照样通过。
	// 真实配置里有 http / events / 两个 map / server / 十几个 location。
	const minBlocks = 8
	if len(blocks) < minBlocks {
		t.Fatalf("只解析出 %d 个块（下限 %d）：解析逻辑或配置结构变了，先修解析再信任本用例",
			len(blocks), minBlocks)
	}

	for _, path := range nginxNotFoundBlocks {
		want := "location = " + path
		got := exactBlocks(blocks, want)
		if len(got) != 1 {
			t.Errorf("`%s` 应当**恰好一条**，实际 %d 条。\n"+
				"    它是内部端点，公网必须显式 404：少了它就会掉进 `location /` 的 SPA fallback，"+
				"以 **200 + index.html** 返回 —— 软 200 比 404 更糟，扫描器会认为端点存在。",
				want, len(got))
			continue
		}
		if !hasDirective(got[0], "return 404") {
			t.Errorf("`%s` 里没有 `return 404`，块内容：%v\n"+
				"    用 return 404 而不是 deny：对外要表达的语义是「这里什么都没有」；"+
				"403 等于告诉扫描器「这个路径存在，只是不给你看」，反而指了路。",
				want, got[0].code)
		}
	}

	// ---- 反向断言：别把公开的存活探针一起挡掉 ----
	// 少了这一段，一个「顺手一刀切」的改动（把所有健康端点都 return 404）不会被发现，
	// 而它会让编排系统的探针永久失败、容器永远是 unhealthy。
	live := exactBlocks(blocks, "location = /healthz")
	if len(live) != 1 {
		t.Fatalf("`location = /healthz` 应当**恰好一条**，实际 %d 条。\n"+
			"    它是编排系统唯一能用的存活/就绪探针（backend 镜像的 HEALTHCHECK 走 "+
			"`api -healthcheck`，即请求本机 /healthz 看状态码）。", len(live))
	}
	if hasDirective(live[0], "return 404") {
		t.Errorf("`location = /healthz` 被写成了 404：探针会永久失败、容器一直是 unhealthy。\n"+
			"    收窄信息面靠的是**收窄响应体**（见 httpx.PublicHealthReport），"+
			"而不是把这个端点本身关掉。块内容：%v", live[0].code)
	}
	if !hasDirective(live[0], "proxy_pass") {
		t.Errorf("`location = /healthz` 没有 proxy_pass：它应当转发到后端，由应用判断依赖状态"+
			"（依赖异常时后端回 503 + Retry-After）。块内容：%v", live[0].code)
	}
}

// exactBlocks 按块头**精确**相等取块。
//
// 不能复用 findBlocks：它用 HasPrefix，而 `location = /healthz` 恰好是
// `location = /healthz/details` 的前缀 —— 用它会把两个端点混成一对，
// 于是「/healthz 被误挡成 404」这条断言会去检查错的那个块。
// （守卫自己算错比漏测更难发现，所以这里单独写一个精确匹配。）
func exactBlocks(blocks []nginxBlock, header string) []nginxBlock {
	var out []nginxBlock
	for _, b := range blocks {
		if strings.TrimSpace(b.header) == header {
			out = append(out, b)
		}
	}
	return out
}
