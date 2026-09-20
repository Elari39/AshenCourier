package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"uuid"

	qrcode "github.com/skip2/go-qrcode"

	"ashen-courier/internal/domain"
)

// qrRun 是 SVG 里的一条子路径重建成矩形后的形状（模块坐标，1 单位 = 1 模块）。
type qrRun struct {
	X     int
	Y     int
	Width int
}

// qrRunPattern 对应 renderQRSVG 写出的 "M<x> <y>h<w>v1h-<w>z"。
var qrRunPattern = regexp.MustCompile(`M(\d+) (\d+)h(\d+)v1h-(\d+)z`)

// TestRenderQRSVGMatchesBitmap 把 SVG 重新解析成模块矩阵，与编码结果的位图**逐格**比对。
//
// 这是本批次最强的一条断言：它同时覆盖了路径语法（h/v/h-/z 的走向）、
// 行内合并（多个连续模块被合成一条子路径）与坐标原点（左上角是 0,0）。
// 只要 SVG 画错了任何一个模块，扫码结果就会偏，而肉眼看不出差别。
func TestRenderQRSVGMatchesBitmap(t *testing.T) {
	t.Parallel()

	const content = "https://s.example/abc1234"

	svg, err := renderQRSVG(content, qrcode.Medium)
	if err != nil {
		t.Fatalf("renderQRSVG: %v", err)
	}

	side := qrViewBoxSide(t, svg)

	// 期望值来自编码库本身：这里测的是「渲染是否忠实」，不是「编码是否正确」
	symbol, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		t.Fatalf("qrcode.New: %v", err)
	}
	want := symbol.Bitmap()
	if len(want) != side {
		t.Fatalf("viewBox 边长 %d，位图 %d —— 渲染尺寸与编码结果不一致", side, len(want))
	}

	got := makeGrid(side)
	for _, run := range parseQRRuns(t, svg) {
		for x := run.X; x < run.X+run.Width; x++ {
			if x >= side {
				t.Fatalf("子路径越界：x=%d 超过边长 %d", x, side)
			}
			got[run.Y][x] = true
		}
	}

	for y := range want {
		for x := range want[y] {
			if got[y][x] != want[y][x] {
				t.Fatalf("模块 (%d,%d) 渲染为 %v，位图是 %v", x, y, got[y][x], want[y][x])
			}
		}
	}
}

// TestRenderQRSVGQuietZone 钉住静默区：**恰好 4 模块**，不多不少。
//
// 这一条防的是「再补一圈白边」这个很自然的改动 —— 编码库的 Bitmap() 已经带了
// quietZoneSize=4 的边框（见 renderQRSVG 的注释），手工再加一圈会得到 8 模块，
// 二维码在版面上会显得很小。变异验证：把 renderQRSVG 改成再补一圈 → 本用例变红。
func TestRenderQRSVGQuietZone(t *testing.T) {
	t.Parallel()

	const content = "https://s.example/abc1234"

	svg, err := renderQRSVG(content, qrcode.Medium)
	if err != nil {
		t.Fatalf("renderQRSVG: %v", err)
	}
	side := qrViewBoxSide(t, svg)

	grid := makeGrid(side)
	for _, run := range parseQRRuns(t, svg) {
		for x := run.X; x < run.X+run.Width; x++ {
			grid[run.Y][x] = true
		}
	}

	// 外侧 4 圈必须全空（背景 rect 只负责底色，不参与这里的判定）
	const quiet = 4
	for i := range quiet {
		for j := range side {
			if grid[i][j] || grid[side-1-i][j] {
				t.Fatalf("第 %d 行/倒数第 %d 行不是静默区：静默区应当恰好 %d 模块", i, i, quiet)
			}
			if grid[j][i] || grid[j][side-1-i] {
				t.Fatalf("第 %d 列/倒数第 %d 列不是静默区：静默区应当恰好 %d 模块", i, i, quiet)
			}
		}
	}
	// 第 5 圈必须已经有内容：三条定位图案的左上角正好落在 (4,4)
	if !grid[quiet][quiet] {
		t.Fatalf("模块 (%d,%d) 应当是定位图案的第一格", quiet, quiet)
	}
}

// TestRenderQRSVGContainsNoUserBytes 钉住「响应体里没有任何用户可控字节」。
//
// 这是刻意的设计（见 renderQRSVG 的注释）：内容只参与编码，不进 SVG 的文本或属性，
// 于是既没有 XML 转义问题，也没有往 SVG 里注入脚本的面。
// 若将来有人往 <title> 里塞短链，这条用例会红 —— 那时必须同时加上转义。
func TestRenderQRSVGContainsNoUserBytes(t *testing.T) {
	t.Parallel()

	const code = "abc1234"
	const content = "https://s.example/" + code

	svg, err := renderQRSVG(content, qrcode.Medium)
	if err != nil {
		t.Fatalf("renderQRSVG: %v", err)
	}
	if strings.Contains(svg, content) || strings.Contains(svg, code) || strings.Contains(svg, "s.example") {
		t.Fatalf("SVG 里出现了用户可控的内容：%s", svg)
	}
}

// TestRenderQRSVGShape 守住 SVG 的骨架：尺寸属性、透明底与背景矩形。
func TestRenderQRSVGShape(t *testing.T) {
	t.Parallel()

	svg, err := renderQRSVG("https://s.example/abc1234", qrcode.Medium)
	if err != nil {
		t.Fatalf("renderQRSVG: %v", err)
	}

	side := qrViewBoxSide(t, svg)

	if !strings.HasPrefix(svg, "<svg ") {
		t.Fatalf("响应不是以 <svg 开头：%q", firstN(svg, 40))
	}
	if !strings.HasSuffix(svg, "</svg>") {
		t.Fatalf("响应没有正确闭合：%q", lastN(svg, 40))
	}
	// 背景必须显式画出来：SVG 默认透明，透明底贴到深色页面上扫不出来
	wantBG := fmt.Sprintf(`<rect width="%d" height="%d" fill="%s"/>`, side, side, qrBackground)
	if !strings.Contains(svg, wantBG) {
		t.Fatalf("缺少背景矩形：%q", wantBG)
	}
	if !strings.Contains(svg, fmt.Sprintf(`width="%d" height="%d"`, qrPixelSize, qrPixelSize)) {
		t.Fatalf("缺少 %d×%d 的显示尺寸", qrPixelSize, qrPixelSize)
	}
	if !strings.Contains(svg, `fill="`+qrForeground+`"`) {
		t.Fatalf("缺少前景色 %s", qrForeground)
	}
}

// TestQRRoute 覆盖 GET /api/links/{code}/qr.svg 的状态码语义与响应头。
func TestQRRoute(t *testing.T) {
	t.Parallel()

	const code = "qrroute"
	owned := uuid.NewV7()
	deleted := "qrdeleted"

	links := map[string]*domain.Link{
		code: {
			ID:        uuid.NewV7(),
			ShortCode: code,
			TargetURL: "https://example.com/qr",
			Status:    domain.LinkStatusActive,
			// 归属某个账号：下面的用例会证明「不校验所有者」是刻意的
			OwnerID: &owned,
		},
		deleted: {
			ID:        uuid.NewV7(),
			ShortCode: deleted,
			TargetURL: "https://example.com/gone",
			Status:    domain.LinkStatusDeleted,
		},
	}
	router := newTestRouter(t, links)

	tests := []struct {
		name       string
		path       string
		wantStatus int
		why        string
	}{
		{
			name: "有主的活动短链也公开可读", path: "/api/links/" + code + "/qr.svg",
			wantStatus: http.StatusOK,
			why:        "二维码的内容就是 short_url，拿到短链的人本来就能跳转；要求凭据会让「被邮件/印刷品引用」无法实现",
		},
		{
			name: "不存在的短码", path: "/api/links/qrnosuch/qr.svg",
			wantStatus: http.StatusNotFound,
			why:        "不给不存在的短码生成图（避免被当成免费的短码枚举工具）",
		},
		{
			name: "已软删除的短链", path: "/api/links/" + deleted + "/qr.svg",
			wantStatus: http.StatusNotFound,
			why:        "与 claim 的判定一致：存在性只看「未被软删除」",
		},
		{
			name: "保留字", path: "/api/links/login/qr.svg",
			wantStatus: http.StatusNotFound,
			why:        "保留字在任何接口上都不该被当成短码",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rr := httptest.NewRecorder()
			// 刻意不带任何凭据：这是「公开可读」的一部分
			router.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, nil))

			if rr.Code != tt.wantStatus {
				t.Fatalf("GET %s → %d，期望 %d（%s）：%s",
					tt.path, rr.Code, tt.wantStatus, tt.why, rr.Body.String())
			}
			if tt.wantStatus != http.StatusOK {
				return
			}

			if got := rr.Header().Get("Content-Type"); got != "image/svg+xml; charset=utf-8" {
				t.Errorf("Content-Type = %q", got)
			}
			if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q", got)
			}
			if got := rr.Header().Get("Cache-Control"); got != "public, max-age=300" {
				t.Errorf("Cache-Control = %q", got)
			}
			if !strings.Contains(rr.Body.String(), "<svg ") {
				t.Errorf("响应体不是 SVG：%q", firstN(rr.Body.String(), 40))
			}
			qrViewBoxSide(t, rr.Body.String())
		})
	}
}

// qrViewBoxSide 从 SVG 里解析 viewBox 的边长，并顺手断言宽高属性与之一致所需的形状。
func qrViewBoxSide(t *testing.T, svg string) int {
	t.Helper()

	m := regexp.MustCompile(`viewBox="0 0 (\d+) (\d+)"`).FindStringSubmatch(svg)
	if m == nil {
		t.Fatalf("SVG 里没有 viewBox：%q", firstN(svg, 80))
	}
	side, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("viewBox 边长不是整数：%q", m[1])
	}
	if m[1] != m[2] {
		t.Fatalf("viewBox 不是正方形：%v × %v", m[1], m[2])
	}
	// 边长 = 符号尺寸 + 两侧静默区，符号尺寸是 21 + 4k，静默区 8 ⇒ 边长 ≡ 1 (mod 4)
	if side%4 != 1 {
		t.Fatalf("viewBox 边长 %d 不是 21+4k+8 的形状", side)
	}
	return side
}

// parseQRRuns 解析 path 里的矩形子路径，并断言 h 与 h- 的宽度一致（走向必须闭合）。
func parseQRRuns(t *testing.T, svg string) []qrRun {
	t.Helper()

	matches := qrRunPattern.FindAllStringSubmatch(svg, -1)
	if len(matches) == 0 {
		t.Fatalf("SVG 里没有任何深色模块：%q", firstN(svg, 120))
	}

	runs := make([]qrRun, 0, len(matches))
	for _, m := range matches {
		x, _ := strconv.Atoi(m[1])
		y, _ := strconv.Atoi(m[2])
		w, _ := strconv.Atoi(m[3])
		back, _ := strconv.Atoi(m[4])
		if w != back {
			t.Fatalf("子路径不闭合：h%d 与 h-%d 不一致（M%d %d）", w, back, x, y)
		}
		runs = append(runs, qrRun{X: x, Y: y, Width: w})
	}
	return runs
}

// makeGrid 建一个 side×side 的模块矩阵。
func makeGrid(side int) [][]bool {
	grid := make([][]bool, side)
	for i := range grid {
		grid[i] = make([]bool, side)
	}
	return grid
}

// firstN / lastN 用于把断言失败时的巨量 SVG 截成能读的一段。
func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func lastN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
