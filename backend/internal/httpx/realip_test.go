package httpx

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// 「真实客户端 IP」这条链有两端：
//
//	nginx：用 set_real_ip_from + real_ip_header 把 $remote_addr 还原成访客地址，
//	       再 proxy_set_header X-Real-IP $remote_addr 注入给后端；
//	后端：ClientIP 只认 X-Real-IP（clientip_test.go 守着这一端）。
//
// 本用例守的是 **nginx 那一端**。它不能省，因为这条链断掉的症状是"静默"的：
//
// 本站实际部署在 Cloudflare 后面（README 的「CDN 前置部署」一节）。少了 restored-IP 配置时，
// $remote_addr 就是 CF 边缘节点的地址，于是
//   - 按 IP 的限流把同一边缘节点下的**所有访客算成一个 IP**（一个人刷满、所有人吃 429）；
//   - click_events.ip / links.created_ip / 访问日志记的都是 CF 节点；
//   - 启用 GeoIP 时，解析出的"国家"是 CF 机房所在国。
//
// 2026-09-22 实测确认过这个缺陷（明细里记到 `172.71.158.0/24`，落在 CF 的 172.64.0.0/13 内，
// 而访客并非从 CF 访问），所以这条断言钉的是**真实发生过的事**，不是假想。
//
// 与 routes_sync_test.go 同样的处理：跨出 backend 模块去读仓库根的 deploy/，
// 仓库没完整检出时透明 SKIP（跳过要说出来，不能静默绿）。
const (
	// minRealIPRanges 是 set_real_ip_from 的条数下限。
	// 取 10 而不是精确值：CF 会增删段，写死总数会让守法更新变麻烦；
	// 但只要有人把整段删掉、或只留一两条，这里就会红。
	minRealIPRanges = 10
	// cloudflareRangeUnderTest 是本次实测命中的那一段所属的 CF 网段。
	cloudflareRangeUnderTest = "172.64.0.0/13"
)

var (
	setRealIPFromRE = regexp.MustCompile(`(?m)^\s*set_real_ip_from\s+([0-9a-fA-F:./]+)\s*;`)
	realIPHeaderRE  = regexp.MustCompile(`(?m)^\s*real_ip_header\s+(\S+)\s*;`)
	proxyRealIPRE   = regexp.MustCompile(`(?m)^\s*proxy_set_header\s+X-Real-IP\s+\$remote_addr\s*;`)
)

// TestNginxRestoresRealClientIP 断言 nginx 会把真实访客地址还原出来。
func TestNginxRestoresRealClientIP(t *testing.T) {
	t.Parallel()

	// backend/internal/httpx → 上三级是仓库根
	path := filepath.Join("..", "..", "..", "deploy", "nginx", "nginx.conf")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("读不到 %s（需要完整仓库检出）：%v", path, err)
	}
	conf := string(body)

	// 1) 必须明确从 CF-Connecting-IP 取地址。
	//    不能退化成 X-Forwarded-For：那是客户端可伪造的头，取它等于让限流键与明细 IP 可被任意指定。
	m := realIPHeaderRE.FindStringSubmatch(conf)
	if m == nil {
		t.Fatalf("%s 里没有 `real_ip_header`：部署在 CDN 后面时 $remote_addr 会是边缘节点地址，"+
			"限流配额会被全网共享（详见本文件顶部注释）", path)
	}
	if got := strings.TrimSpace(m[1]); got != "CF-Connecting-IP" {
		t.Errorf("real_ip_header = %q，期望 CF-Connecting-IP —— X-Forwarded-For 是客户端可伪造的头，"+
			"用它取地址会让限流键与明细 IP 都能被攻击者指定", got)
	}

	// 2) 信任的地址段必须成规模地列出来。
	ranges := setRealIPFromRE.FindAllStringSubmatch(conf, -1)
	flat := make([]string, 0, len(ranges))
	for _, r := range ranges {
		flat = append(flat, strings.TrimSpace(r[1]))
	}
	if len(flat) < minRealIPRanges {
		t.Fatalf("只找到 %d 条 set_real_ip_from（下限 %d）：配置被删空或只留了几条，"+
			"`real_ip_header` 就再也不会生效。当前清单：%v", len(flat), minRealIPRanges, flat)
	}

	// 3) 实测命中的那一段必须在清单里 —— 它是本缺陷的证据，删掉它就等于把洞放回去。
	if !slices.Contains(flat, cloudflareRangeUnderTest) {
		t.Errorf("set_real_ip_from 缺少 %s：2026-09-22 实测到访客地址被记成该段内的 "+
			"CF 边缘地址（172.71.158.0/24），这一段必须在信任清单里", cloudflareRangeUnderTest)
	}

	// 4) 链的另一半：注入给后端的头名必须是 X-Real-IP，且值必须是改写后的 $remote_addr。
	//    写成 $realip_remote_addr（改写前的地址）会让整段 real_ip 配置白做。
	if !proxyRealIPRE.MatchString(conf) {
		t.Errorf("没找到 `proxy_set_header X-Real-IP $remote_addr;`：后端只认 X-Real-IP，" +
			"这里注入什么它就用什么；值写成 $realip_remote_addr 会让 real_ip 还原失效")
	}
}
