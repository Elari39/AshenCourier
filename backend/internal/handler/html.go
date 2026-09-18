package handler

import (
	"bytes"
	"html/template"
	"log/slog"
	"net/http"

	"ashen-courier/internal/httpx"
)

// errorPageData 是短码失效页的渲染数据。
type errorPageData struct {
	// Code 是 HTTP 状态码。
	Code int
	// Title 是衬线大标题。
	Title string
	// Message 是一句解释。
	Message string
	// RequestID 便于用户报障。
	RequestID string
}

// errorPageTmpl 是短码失效页模板。
//
// 风格遵循 DESIGN.md：暖奶油画布 #faf9f5、深墨色正文 #141413、
// 珊瑚主色 #cc785c 只用在 CTA 上、衬线大标题、圆角 md(8px)。
// 刻意不引外部 CSS/字体 —— 这个页面在网络异常时也要能秒开。
var errorPageTmpl = template.Must(template.New("error-page").Parse(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>{{.Code}} · {{.Title}} · AshenCourier</title>
<style>
  :root {
    --canvas: #faf9f5; --ink: #141413; --body: #3d3d3a;
    --muted: #6c6a64; --hairline: #e6dfd8; --primary: #cc785c;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center;
    background: var(--canvas); color: var(--ink);
    font-family: Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    padding: 32px;
  }
  main { max-width: 560px; width: 100%; }
  .code {
    font-size: 13px; font-weight: 500; letter-spacing: 1.5px; text-transform: uppercase;
    color: var(--muted); margin: 0 0 16px;
  }
  h1 {
    font-family: "Cormorant Garamond", Tiempos Headline, Garamond, "Times New Roman", serif;
    font-weight: 400; font-size: 48px; line-height: 1.1; letter-spacing: -1px; margin: 0 0 16px;
  }
  p { font-size: 16px; line-height: 1.55; color: var(--body); margin: 0 0 32px; }
  a.home {
    display: inline-block; height: 40px; line-height: 40px; padding: 0 20px;
    background: var(--primary); color: #fff; border-radius: 8px;
    font-size: 14px; font-weight: 500; text-decoration: none;
  }
  .rid {
    margin: 32px 0 0; padding-top: 16px; border-top: 1px solid var(--hairline);
    font-family: "JetBrains Mono", ui-monospace, monospace; font-size: 12px; color: var(--muted);
  }
</style>
</head>
<body>
<main>
  <p class="code">{{.Code}}</p>
  <h1>{{.Title}}</h1>
  <p>{{.Message}}</p>
  <a class="home" href="/">去创建一条新的短链</a>
  {{if .RequestID}}<p class="rid">request_id: {{.RequestID}}</p>{{end}}
</main>
</body>
</html>
`))

// writeErrorPage 渲染短码失效页。
//
// 用 html/template 而不是手拼字符串：Title/Message 将来若掺入用户输入，
// 模板的自动转义能直接挡住 XSS。渲染失败则退回纯文本。
func writeErrorPage(w http.ResponseWriter, r *http.Request, data errorPageData) {
	data.RequestID = httpx.RequestIDFrom(r.Context())

	// 先渲到内存：模板出错时还来得及退回纯文本，不会写出半个页面
	var buf bytes.Buffer
	if err := errorPageTmpl.Execute(&buf, data); err != nil {
		slog.Error("渲染短码失效页失败", "err", err, "status", data.Code)
		http.Error(w, data.Message, data.Code)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(data.Code)
	_, _ = w.Write(buf.Bytes())
}
