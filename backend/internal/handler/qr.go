package handler

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	qrcode "github.com/skip2/go-qrcode"

	"ashen-courier/internal/domain"
	"ashen-courier/internal/httpx"
	"ashen-courier/internal/pkg/shortcode"
	"ashen-courier/internal/service"
)

// SVG 的尺寸与配色常量。
const (
	// qrPixelSize 是 SVG 的 width/height 属性。坐标本身用模块数（viewBox），
	// 所以这个值只决定「默认显示多大」——`<img width=64>` 或印刷放大都不会糊。
	qrPixelSize = 256
	// qrForeground 是深墨（--color-ink），与前端画布上的二维码同一个前景色。
	qrForeground = "#141413"
	// qrBackground 用**纯白**而不是屏幕上的暖奶油底（--color-canvas）：
	// 屏幕上的奶油底是为了和 DESIGN.md 的面板一致，而这个端点服务的是
	// 「被邮件模板 / 印刷品引用」的场景 —— 白底在复印、贴纸与弱光下对比度更稳。
	qrBackground = "#ffffff"
)

// qrCacheSeconds 是二维码响应的缓存时长。
//
// 图的内容只依赖 short_url（所属域 + 短码），与目标地址、标题、点击量都无关，
// 所以改这些字段不需要换图；而域名改名、短链被删这类事件等 5 分钟也完全可以接受。
const qrCacheSeconds = 300

// qrHandler 处理 GET /api/links/{code}/qr.svg（PLAN-NEXT §19.1 / 批次 N8）。
//
// 这是全站唯一返回图片的接口，为「二维码要被别人引用」而存在：
// 前端画 canvas 只能解决「在详情页看到并下载」，而邮件模板、印刷品、
// 第三方系统需要的是一个稳定的图片 URL。
type qrHandler struct {
	shortener *service.Shortener
}

// serve 处理一次二维码请求。
//
// 鉴权取舍（照 §19.1 实现，不要自行收紧）：
//   - **公开可读，不校验所有者**：二维码的内容就是 short_url 本身，
//     而 GET /{code} 本来就对所有人开放 —— 拿到短链的人本来就能跳转，
//     能画出二维码不构成新的信息泄露。要求凭据会让这个端点的唯一用途（被别处引用）无法实现。
//   - 只要求「短链存在且未被软删除」，与 claim 的判定一致。
//     **不**要求 Redirectable：印好的二维码不该因为链接临时过期/停用就取不到图，
//     而扫码时该 410 的仍然 410。
func (h *qrHandler) serve(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	// 与 GET /{code} 同源的形态校验：保留字与非法形态在这里同样不该被当成短码
	if !shortcode.IsValidShape(code) || shortcode.IsReserved(code) {
		writeLinkNotFound(w, r)
		return
	}

	link, err := h.shortener.Get(r.Context(), code)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeLinkNotFound(w, r)
			return
		}
		httpx.WriteDomainError(w, r, err)
		return
	}
	if link.Status == domain.LinkStatusDeleted {
		writeLinkNotFound(w, r)
		return
	}

	svg, err := renderQRSVG(h.shortener.ShortURL(r.Context(), link), qrcode.Medium)
	if err != nil {
		slog.Error("生成二维码失败", "err", err, "code", code)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal", "服务器内部错误", "")
		return
	}

	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	// 响应体里只有模块几何，没有任何用户可控字节（见 renderQRSVG）；
	// nosniff 是这条结论的兜底：万一将来有人往 SVG 里塞内容，
	// 浏览器也不会按别的类型去解释它。
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", qrCacheSeconds))
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", "qr-"+code+".svg"))
	w.WriteHeader(http.StatusOK)

	if _, err := io.WriteString(w, svg); err != nil {
		// 状态码已经写出去了，客户端提前断开只能记一条日志 —— 这不是服务端故障
		slog.Debug("写出二维码响应中断", "err", err, "code", code)
	}
}

// renderQRSVG 把内容编码成二维码，再渲染成 SVG 字符串。
//
// 为什么编码用库、渲染自己写：Reed-Solomon 纠错、掩码选择、版本与格式信息
// 全是规范驱动的一堆位运算（见 skip2/go-qrcode 的 ~3000 行版本表）——
// 手写一遍的收益只是「少一个依赖」，代价是任何一个位算错都会产出一个
// **看起来很像、扫不出来**的图形，而这类 bug 肉眼与正确输出没有区别。
// 库只提供 Bitmap()、不产 SVG，于是序列化这几十行留在这里，由单测钉死。
//
// ⚠️ Bitmap() 返回的**已经包含 4 模块静默区**（`qrCodeVersion.quietZoneSize()` 恒返回 4，
// 且 `buildRegularSymbol` 在 `DisableBorder` 为 false 时会把它算进符号尺寸）。
// 再补一圈就会得到 8 模块白边：不是错，但白边过宽会让二维码在版面上显得很小。
// qr_test.go 里的静默区断言就是钉这一条。
//
// 输出里不含任何用户可控字节：content 只参与编码，不进 SVG 文本或属性。
// 于是既没有 XML 转义问题，也没有「往 SVG 里注入脚本」的面 —— 这是刻意的，
// 不要为了「让二维码里带个说明」而往 <title> 里塞短链（真要塞就得先转义，
// 并同步改掉 TestRenderQRSVGContainsNoUserBytes）。
func renderQRSVG(content string, level qrcode.RecoveryLevel) (string, error) {
	symbol, err := qrcode.New(content, level)
	if err != nil {
		return "", fmt.Errorf("handler.qr: 编码二维码: %w", err)
	}

	bitmap := symbol.Bitmap()
	if len(bitmap) == 0 || len(bitmap) != len(bitmap[0]) {
		return "", fmt.Errorf("handler.qr: 二维码位图形状异常（%d 行）", len(bitmap))
	}
	side := len(bitmap)

	var b strings.Builder
	// 深色模块大约占一半，每个矩形最多 ~20 字节，先按量级预留，避免多次扩容
	b.Grow(side*side*10 + 512)

	// viewBox 用模块坐标：每个模块正好 1 个单位，模块边界永远落在整数上，
	// 缩放渲染时不会因为小数坐标出现灰边。
	fmt.Fprintf(&b,
		`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" `+
			`shape-rendering="crispEdges" role="img" aria-label="短链二维码">`,
		qrPixelSize, qrPixelSize, side, side)
	// 背景必须显式画出来：SVG 默认透明，透明底贴到深色页面上必然扫不出来
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="%s"/>`, side, side, qrBackground)

	// 同一行里连续的深色模块合并成一条子路径。
	// 一个模块一个 <rect> 在高版本（100+ 模块）下会写出上万个元素，
	// 文件大一个数量级，抓取和解析都跟着慢。
	b.WriteString(`<path fill="`)
	b.WriteString(qrForeground)
	b.WriteString(`" d="`)
	for y, row := range bitmap {
		for x := 0; x < side; {
			if !row[x] {
				x++
				continue
			}
			start := x
			for x < side && row[x] {
				x++
			}
			// M x y → 横移到 run 的末尾 → 下移 1 → 回到起点 → 闭合
			fmt.Fprintf(&b, "M%d %dh%dv1h-%dz", start, y, x-start, x-start)
		}
	}
	b.WriteString(`"/></svg>`)

	return b.String(), nil
}
