package httpx

import (
	"net/http"
	"strings"
)

// apiPrefix 是「统一 JSON 错误体」这条契约适用的路径前缀。
const apiPrefix = "/api"

// APIErrorContract 把 /api 命名空间里由 net/http 内建产生的 404 / 405 改写成统一错误体。
//
// 起因：ServeMux 有两处会绕过本包的 WriteError、自己回一句纯文本 ——
//
//	路径完全没注册     → 404 "404 page not found"
//	路径注册了但方法不对 → 405 "Method Not Allowed"
//
// 于是 README 承诺的「失败响应统一为 {"error":{…}}」在这个命名空间里不成立。
// 这不是纸面问题：前端 `src/api/client.ts` 按 `error.code` 分支，拿到 text/plain
// 会在解析处失败，报出的错与真实原因无关（退化成「请求失败（HTTP 404）」，
// 而不是「这个接口不存在」）。
//
// 为什么不是「注册一条 mux.Handle("/api/", …) 兜底」：不带方法前缀的模式匹配
// **任意方法**，于是 `PUT /api/links` 不再是 405 而是 404 —— 把「方法不对」
// 降级成「路径不存在」，而 405 携带的 Allow 头正是客户端唯一能知道
// 「该用哪个方法」的地方。router_test.go 的 TestRouterMethodAwareness 盯着这一点。
// 所以这里在**响应侧**改写，路由侧一条不动：状态码与 Allow 原样保留。
//
// 判定依据是「本包自己从不产生的组合」：
//
//	状态码 ∈ {404, 405}  且  Content-Type == text/plain; charset=utf-8
//
// 本包的 WriteJSON 一律写 application/json，短码失效页写 text/html，
// 因此不会误伤自己的 404。这层对 net/http 实现细节的耦合由用例钉住：
// Go 哪天换了写法，TestRouterAPIErrorContract 会红，而不是静默退回去。
//
// 作用域刻意只限 /api：非接口路径上的 404/405 面向浏览器，保持 net/http
// 的默认表现更合理，也避免把改动扩散到 SPA 外壳与短码跳转路径上。
func APIErrorContract(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAPIPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(&apiErrorWriter{ResponseWriter: w, req: r}, r)
	})
}

// isAPIPath 判断路径是否落在 /api 命名空间里。
// 显式排除 `/apifoo` 这类同前缀但不同段的名字，避免前缀匹配误命中。
func isAPIPath(path string) bool {
	return path == apiPrefix || strings.HasPrefix(path, apiPrefix+"/")
}

// apiErrorWriter 在响应写出前判定一次：这是不是 ServeMux 内建的那句纯文本错误。
// 命中就换成本包的 JSON 错误体，并吞掉随后那句纯文本。
type apiErrorWriter struct {
	http.ResponseWriter
	req *http.Request
	// decided 表示已经处理过第一次写出（WriteHeader，或被隐式 200 的 Write）。
	decided bool
	// swallow 表示已经改写过，后续的 WriteHeader / Write 一律丢弃。
	swallow bool
}

// WriteHeader 实现 http.ResponseWriter。
func (w *apiErrorWriter) WriteHeader(status int) {
	if w.decided {
		if !w.swallow {
			w.ResponseWriter.WriteHeader(status)
		}
		return
	}
	w.decided = true

	if !w.isBareMuxError(status) {
		w.ResponseWriter.WriteHeader(status)
		return
	}

	code, message := "not_found", "接口不存在"
	if status == http.StatusMethodNotAllowed {
		code, message = "method_not_allowed", "该接口不支持此请求方法"
	}
	w.swallow = true
	// 必须在底层 WriteHeader 之前写出 JSON：头一旦提交就改不动了。
	// Allow 头由 ServeMux 提前写好，这里刻意不碰 —— 它是 405 的全部价值所在。
	WriteError(w.ResponseWriter, w.req, status, code, message, "")
}

// Write 实现 http.ResponseWriter。
func (w *apiErrorWriter) Write(b []byte) (int, error) {
	if w.swallow {
		// 已经换成 JSON 了，那句纯文本丢掉；返回 len(b) 免得调用方以为写失败
		return len(b), nil
	}
	w.decided = true // 隐式 200：此后不可能再是内建错误，不必再判定
	return w.ResponseWriter.Write(b)
}

// Flush 透传，免得 http.ResponseController 在本层拿到 ErrNotSupported。
func (w *apiErrorWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap 让 http.ResponseController 能拿到底层 writer。
func (w *apiErrorWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// isBareMuxError 判定「这次写出是 net/http 内建的纯文本错误」。
//
// http.Error 会**先**把 Content-Type 设成 text/plain、再调 WriteHeader，
// 所以判定时头已经就位。这也正是它能与本包自己的 404 区分开的原因：
// WriteJSON 在 WriteHeader 之前就把 Content-Type 设成了 application/json。
func (w *apiErrorWriter) isBareMuxError(status int) bool {
	if status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
		return false
	}
	return w.Header().Get("Content-Type") == "text/plain; charset=utf-8"
}
