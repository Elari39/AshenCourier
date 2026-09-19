package handler

import (
	"bytes"
	"html/template"
	"log/slog"
	"net/http"

	"ashen-courier/internal/httpx"
)

// pageCSS 是短码失效页与口令页共用的样式。
//
// 风格遵循 DESIGN.md：暖奶油画布 #faf9f5、深墨色正文 #141413、
// 珊瑚主色 #cc785c 只用在 CTA 上、衬线大标题、圆角 md(8px)。
// 刻意不引外部 CSS/字体 —— 这两张页面都在跳转链路上，网络异常时也要能秒开。
//
// 抽成一份而不是各写一遍：口令页与失效页是同一个入口（GET / POST /{code}）的两个分支，
// 样式一旦漂移，用户会以为跳到了别的站点。
const pageCSS = `  :root {
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
  .error {
    margin: -16px 0 24px; padding: 10px 14px; border-radius: 8px;
    background: #fbeee9; color: #8a3b21; font-size: 14px;
  }
  form { margin: 0 0 8px; }
  label { display: block; font-size: 13px; font-weight: 500; color: var(--muted); margin: 0 0 8px; }
  input[type="password"] {
    width: 100%; height: 44px; padding: 0 12px; margin: 0 0 16px;
    border: 1px solid var(--hairline); border-radius: 8px;
    background: #fff; color: var(--ink); font-size: 15px; font-family: inherit;
  }
  input[type="password"]:focus { outline: 2px solid var(--primary); outline-offset: 1px; }
  button {
    width: 100%; height: 44px; border: 0; border-radius: 8px;
    background: var(--primary); color: #fff;
    font-size: 15px; font-weight: 500; font-family: inherit; cursor: pointer;
  }
  .rid {
    margin: 32px 0 0; padding-top: 16px; border-top: 1px solid var(--hairline);
    font-family: "JetBrains Mono", ui-monospace, monospace; font-size: 12px; color: var(--muted);
  }
`

// pageHead 拼出两张页面共用的文档头。
//
// title 是**模板源码片段**而不是渲染后的文本（失效页的标题里有 {{.Code}}），
// 因此调用方传的是 "{{.Code}} · ..." 这样的字面量。
func pageHead(title string) string {
	return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>` + title + `</title>
<style>
` + pageCSS + `</style>
</head>
`
}

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

// passwordPageData 是短链口令页的渲染数据。
type passwordPageData struct {
	// Code 是短码，用于表单的 action。
	Code string
	// Title 是衬线大标题。
	Title string
	// Message 是一句解释。
	Message string
	// Error 非空表示上一次提交失败，页面顶部用珊瑚色提示。
	Error string
	// RequestID 便于用户报障。
	RequestID string
}

// errorPageTmpl 是短码失效页模板。
var errorPageTmpl = template.Must(template.New("error-page").Parse(
	pageHead("{{.Code}} · {{.Title}} · AshenCourier") + `<body>
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

// passwordPageTmpl 是短链口令页模板。
//
// 表单是**原生 POST**，不引一行 JS：这张页面在跳转链路上，用户可能是从聊天工具里点开的，
// 少一个失败面就少一类「点了没反应」。method 用 post 而不是 get —— 口令绝不能进 URL
// （会被浏览器历史、Referer、访问日志一起记下来）。
var passwordPageTmpl = template.Must(template.New("password-page").Parse(
	pageHead("需要口令 · AshenCourier") + `<body>
<main>
  <p class="code">Protected</p>
  <h1>{{.Title}}</h1>
  <p>{{.Message}}</p>
  {{if .Error}}<p class="error">{{.Error}}</p>{{end}}
  <form method="post" action="/{{.Code}}">
    <label for="password">访问口令</label>
    <input id="password" name="password" type="password" required autofocus
           autocomplete="current-password" spellcheck="false">
    <button type="submit">解锁并跳转</button>
  </form>
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

// writePasswordPage 渲染短链口令页。
//
// status 由调用方给定：未解锁时 200（这是一张正常内容页，不是「请求失败」），
// 口令错误时 401。两者都带 no-store —— 页面状态取决于本次请求是否已解锁，
// 不能被任何中间层或浏览器缓存复用。
func writePasswordPage(w http.ResponseWriter, r *http.Request, code, errMessage string, status int) {
	data := passwordPageData{
		Code:      code,
		Title:     "这条短链需要口令",
		Message:   "输入所有者设置的口令即可继续跳转。",
		Error:     errMessage,
		RequestID: httpx.RequestIDFrom(r.Context()),
	}

	var buf bytes.Buffer
	if err := passwordPageTmpl.Execute(&buf, data); err != nil {
		slog.Error("渲染短链口令页失败", "err", err, "code", code)
		http.Error(w, data.Message, status)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}
