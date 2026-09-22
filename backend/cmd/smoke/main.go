// Command smoke 是 AshenCourier 的端到端冒烟测试。
//
// 为什么用 Go 而不是 shell 脚本：本机 Git Bash 缺 coreutils，
// 而 Go 程序跨平台且能做真正的 JSON 断言。
//
// 覆盖：健康检查 → 匿名创建 → 302 跳转 → 统计收敛 → 鉴权边界 →
// 修改/删除 → 保留字与开放重定向防护 → 访问口令 → 注册/登录 → 认领 → 分页 →
// 二维码 → 限流。
//
// 用法：
//
//	go run ./cmd/smoke -base http://localhost:8080
//	go run ./cmd/smoke -base http://localhost:8080 -v      # 打印每次交互
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

func main() {
	os.Exit(run())
}

// resp 是一次 HTTP 交互的原始结果。
type resp struct {
	status int
	header http.Header
	body   []byte
}

// decode 把响应体解析成 v。
func (r *resp) decode(v any) error {
	if len(r.body) == 0 {
		return errors.New("响应体为空")
	}
	return json.Unmarshal(r.body, v)
}

// suite 是冒烟测试的上下文。
type suite struct {
	base    string
	client  *http.Client
	verbose bool
	// expectSPA 为 true 时额外断言站点经 nginx 托管 SPA。
	// 直连后端时顶级路由是 404（被保留字挡掉），只有走 nginx 才应该是 HTML。
	expectSPA bool

	// ctx 在 runAll 里注入，check 用它在同一个总超时下执行每一步
	ctx context.Context

	checks   int
	failures int
}

func run() int {
	base := flag.String("base", "http://localhost:8080", "服务基址")
	timeout := flag.Duration("timeout", 60*time.Second, "整体超时")
	verbose := flag.Bool("v", false, "打印每次请求的细节")
	expectSPA := flag.Bool("expect-spa", false,
		"额外断言 SPA 顶级路由（/login 等）经 nginx 返回 HTML —— 走 nginx 时应当打开")
	flag.Parse()

	s := &suite{
		base:      strings.TrimRight(*base, "/"),
		verbose:   *verbose,
		expectSPA: *expectSPA,
		client: &http.Client{
			// 跳转接口要断言 302 本身，不能被 client 自动跟随
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Timeout: 15 * time.Second,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	fmt.Printf("AshenCourier 冒烟测试 → %s\n\n", s.base)

	if err := s.runAll(ctx); err != nil {
		s.fail("整体流程", "%v", err)
	}

	fmt.Printf("\n结果：%d 项检查，%d 项失败\n", s.checks, s.failures)
	if s.failures > 0 {
		return 1
	}
	fmt.Println("全部通过 ✅")
	return 0
}

// check 执行一步断言。
func (s *suite) check(name string, fn func(ctx context.Context) error) {
	s.checks++
	err := fn(s.ctx)
	if err != nil {
		s.failures++
		fmt.Printf("  ✗ %s\n    %v\n", name, err)
		return
	}
	fmt.Printf("  ✓ %s\n", name)
}

// fail 记录一次失败。
func (s *suite) fail(name, format string, args ...any) {
	s.failures++
	fmt.Printf("  ✗ %s\n    %s\n", name, fmt.Sprintf(format, args...))
}

// do 发一个请求。body 非 nil 时序列化成 JSON，headers 会被附加到请求上。
func (s *suite) do(ctx context.Context, method, path string, body any, headers map[string]string) (*resp, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("序列化请求体: %w", err)
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, s.base+path, reader)
	if err != nil {
		return nil, fmt.Errorf("构造请求: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	raw, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer raw.Body.Close()

	data, err := io.ReadAll(io.LimitReader(raw.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("读取响应体: %w", err)
	}

	out := &resp{status: raw.StatusCode, header: raw.Header, body: data}
	if s.verbose {
		fmt.Printf("      [%s %s] → %d %s\n", method, path, out.status, truncate(string(data), 200))
	}
	return out, nil
}

// postForm 发一个 application/x-www-form-urlencoded 的 POST。
//
// 口令页是**原生表单提交**而不是 JSON，这条路径必须按浏览器的形态测；
// 用 s.do 会带上 application/json，后端就读不到 password 字段了。
func (s *suite) postForm(ctx context.Context, path string, form url.Values) (*resp, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("构造请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	raw, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", path, err)
	}
	defer raw.Body.Close()

	data, err := io.ReadAll(io.LimitReader(raw.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("读取响应体: %w", err)
	}

	out := &resp{status: raw.StatusCode, header: raw.Header, body: data}
	if s.verbose {
		fmt.Printf("      [POST %s form] → %d %s\n", path, out.status, truncate(string(data), 200))
	}
	return out, nil
}

// unlockCookieName 必须与 handler 里的解锁 cookie 名一致。
//
// 刻意硬编码而不是 import internal/handler：冒烟工具是黑盒端到端，
// 引了内部包就等于把「实现改了测试跟着改」的通道打开。
const unlockCookieName = "ac_unlock"

// unlockCookieValue 从响应的 Set-Cookie 里取出解锁凭据的 name=value 片段。
// 交给 http.Response.Cookies() 解析，免得手撕 SameSite / Secure 这些属性。
func unlockCookieValue(r *resp) string {
	for _, c := range (&http.Response{Header: r.header}).Cookies() {
		if c.Name == unlockCookieName {
			return c.Name + "=" + c.Value
		}
	}
	return ""
}

// ---- 各步骤 ----

func (s *suite) runAll(ctx context.Context) error {
	s.ctx = ctx

	// ---------- 1. 健康检查 ----------
	var health struct {
		Status   string `json:"status"`
		Postgres string `json:"postgres"`
		Redis    string `json:"redis"`
	}

	s.check("GET /healthz 返回 ok 且 PG / Redis 均可用", func(ctx context.Context) error {
		got, err := s.do(ctx, http.MethodGet, "/healthz", nil, nil)
		if err != nil {
			return err
		}
		if got.status != http.StatusOK {
			return fmt.Errorf("期望 200，实际 %d：%s", got.status, got.body)
		}
		if err := got.decode(&health); err != nil {
			return err
		}
		if health.Status != "ok" {
			return fmt.Errorf("status=%q，期望 ok", health.Status)
		}
		if health.Postgres != "ok" || health.Redis != "ok" {
			return fmt.Errorf("依赖异常：postgres=%q redis=%q", health.Postgres, health.Redis)
		}
		return nil
	})
	if s.failures > 0 {
		// 服务没起来，后面的步骤没有意义
		return nil
	}

	// ---------- 1b. SPA 顶级路由（仅经 nginx 时才有意义）----------
	// 这条检查专门盯住一个真实踩过的坑：nginx 的正则 location 优先级高于
	// 普通前缀 location /，所以 /login、/dashboard 这类「看起来像短码的单段路径」
	// 会被短码正则截走，转发到后端拿 404 —— 直连后端时反而是正常的 404，看不出问题。
	if s.expectSPA {
		s.check("SPA 顶级路由经 nginx 返回 HTML（没被短码正则吃掉）", func(ctx context.Context) error {
			for _, path := range []string{"/", "/login", "/register", "/dashboard"} {
				got, err := s.do(ctx, http.MethodGet, path, nil, nil)
				if err != nil {
					return err
				}
				if got.status != http.StatusOK {
					return fmt.Errorf("GET %s 返回 %d，期望 200 —— 检查 nginx 是否漏了 `location = %s`", path, got.status, path)
				}
				if ct := got.header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
					return fmt.Errorf("GET %s 的 Content-Type=%q，期望 text/html", path, ct)
				}
			}
			return nil
		})
	}

	// ---------- 1c. 缓存头与安全响应头（仅经 nginx 时才有意义）----------
	// 这两组都是「坏了也不报错」的类型：页面照样渲染、接口照样 200，
	// 只有响应头不见了（安全退化）、或者浏览器把旧外壳缓存起来（部署后白屏）。
	// 所以它们必须是 HTTP 断言 —— 光看代码或跑单测都发现不了。
	if s.expectSPA {
		s.check("SPA 外壳带 Cache-Control: no-cache（含深链与 /index.html 本身）", func(ctx context.Context) error {
			// 四条路径覆盖不同入口：根、单段 SPA 路由、深链（/links/{code} 这种最常被分享的形态）、
			// 以及 index.html 本身。它们最终都由 `location = /index.html` 送出（try_files 的
			// 最后一个参数会触发内部重定向），少覆盖一种就可能漏掉一处缓存头。
			for _, path := range []string{"/", "/login", "/links/smoke-probe", "/index.html"} {
				got, err := s.do(ctx, http.MethodGet, path, nil, nil)
				if err != nil {
					return err
				}
				if got.status != http.StatusOK {
					return fmt.Errorf("GET %s 返回 %d，期望 200", path, got.status)
				}
				cc := headerValue(got, "Cache-Control")
				if !strings.Contains(cc, "no-cache") {
					return fmt.Errorf("GET %s 的 Cache-Control=%q，期望含 no-cache\n"+
						"    （没有它就只有 Last-Modified，浏览器按启发式规则缓存旧外壳，"+
						"而 /assets/ 没有 fallback ⇒ 部署后白屏）", path, cc)
				}
			}
			return nil
		})

		s.check("安全响应头在 200 / 404 / 401 三种响应上都在（always 生效）", func(ctx context.Context) error {
			cases := []struct {
				path string
				want int
			}{
				{"/", http.StatusOK},
				{"/zzzzzzz", http.StatusNotFound},         // 短链失效页：应用自己发的 404
				{"/api/auth/me", http.StatusUnauthorized}, // 无凭据：401
			}
			for _, c := range cases {
				got, err := s.do(ctx, http.MethodGet, c.path, nil, nil)
				if err != nil {
					return err
				}
				if got.status != c.want {
					return fmt.Errorf("GET %s 返回 %d，期望 %d", c.path, got.status, c.want)
				}
				if err := assertSecurityHeaders(got, c.path); err != nil {
					return err
				}
			}
			return nil
		})

		s.check("内容哈希资源既有 immutable 缓存头、也没有丢掉安全头", func(ctx context.Context) error {
			// 这条专门盯 nginx 的 add_header **不继承**那个坑：`location ^~ /assets/`
			// 为了缓存头自己写了一条 add_header，于是 server 层那批安全头在那里会整批消失。
			// 资源路径从首页里现取，避免把哈希文件名写死在断言里。
			index, err := s.do(ctx, http.MethodGet, "/", nil, nil)
			if err != nil {
				return err
			}
			asset := assetPathRE.FindString(string(index.body))
			if asset == "" {
				return errors.New("首页里找不到 /assets/*.js 引用，无法验证静态资源的响应头")
			}

			got, err := s.do(ctx, http.MethodGet, asset, nil, nil)
			if err != nil {
				return err
			}
			if got.status != http.StatusOK {
				return fmt.Errorf("GET %s 返回 %d，期望 200", asset, got.status)
			}
			if cc := headerValue(got, "Cache-Control"); !strings.Contains(cc, "immutable") {
				return fmt.Errorf("GET %s 的 Cache-Control=%q，期望含 immutable", asset, cc)
			}
			return assertSecurityHeaders(got, asset)
		})
	}

	// ---------- 1d. /api 命名空间的错误体契约 ----------
	// 这两条盯的是「契约」而不是功能：ServeMux 会绕过应用的 WriteError 自己回
	// 一句纯文本（未注册路径 404、方法不对 405），于是 README 承诺的
	// 「失败响应统一为 {"error":{…}}」在 /api 下会破功 —— 而前端的错误处理
	// 正是按 error.code 分支的，拿到 text/plain 只会在解析处失败。
	// 它们不消耗任何配额，但必须排在所有创建动作之前：契约一旦不成立，
	// 后面那些断言的可信度也无从谈起。
	s.check("/api 下未注册路径回统一 JSON 404（不是 net/http 的纯文本）", func(ctx context.Context) error {
		got, err := s.do(ctx, http.MethodGet, "/api/does-not-exist", nil, nil)
		if err != nil {
			return err
		}
		if got.status != http.StatusNotFound {
			return fmt.Errorf("期望 404，实际 %d", got.status)
		}
		if ct := got.header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			return fmt.Errorf("Content-Type=%q，期望 application/json", ct)
		}
		body, err := decodeErrorBody(got)
		if err != nil {
			return err
		}
		if body.Code != "not_found" {
			return fmt.Errorf("error.code=%q，期望 not_found", body.Code)
		}
		return nil
	})

	s.check("/api 下方法不对回统一 JSON 405，且 Allow 头保留", func(ctx context.Context) error {
		// 用 PUT：它不在任何路由的方法集里（与 go test 的 TestRouterMethodAwareness 同一理由）。
		// Allow 必须活着 —— 它是客户端唯一能知道「该用哪个方法」的地方，
		// 也正是「改注册一条 /api/ 兜底模式」那种改法会顺手弄丢的东西。
		got, err := s.do(ctx, http.MethodPut, "/api/links", nil, nil)
		if err != nil {
			return err
		}
		if got.status != http.StatusMethodNotAllowed {
			return fmt.Errorf("期望 405，实际 %d", got.status)
		}
		if allow := got.header.Get("Allow"); !strings.Contains(allow, "GET") || !strings.Contains(allow, "POST") {
			return fmt.Errorf("Allow=%q，期望同时含 GET 与 POST", allow)
		}
		body, err := decodeErrorBody(got)
		if err != nil {
			return err
		}
		if body.Code != "method_not_allowed" {
			return fmt.Errorf("error.code=%q，期望 method_not_allowed", body.Code)
		}
		return nil
	})

	// ---------- 2. 匿名创建 ----------
	var created struct {
		Link struct {
			ID         string `json:"id"`
			ShortCode  string `json:"short_code"`
			ShortURL   string `json:"short_url"`
			TargetURL  string `json:"target_url"`
			Title      string `json:"title"`
			Status     string `json:"status"`
			ClickCount int64  `json:"click_count"`
			Anonymous  bool   `json:"anonymous"`
		} `json:"link"`
		ManageKey string `json:"manage_key"`
	}

	const targetURL = "https://example.com/smoke/very/long/path?x=1&y=2"

	s.check("POST /api/links 匿名创建返回 201 与一次性 manage_key", func(ctx context.Context) error {
		got, err := s.do(ctx, http.MethodPost, "/api/links", map[string]any{
			"target_url": targetURL,
			"title":      "冒烟测试",
		}, nil)
		if err != nil {
			return err
		}
		if got.status != http.StatusCreated {
			return fmt.Errorf("期望 201，实际 %d：%s", got.status, got.body)
		}
		if err := got.decode(&created); err != nil {
			return err
		}
		if created.ManageKey == "" {
			return errors.New("匿名创建必须返回 manage_key")
		}
		if created.Link.ShortCode == "" {
			return errors.New("响应缺少 short_code")
		}
		if !created.Link.Anonymous {
			return errors.New("anonymous 应为 true")
		}
		if created.Link.Status != "active" {
			return fmt.Errorf("status=%q，期望 active", created.Link.Status)
		}
		if !strings.HasPrefix(created.Link.ShortURL, s.base+"/") {
			return fmt.Errorf("short_url=%q 未以 %s/ 开头", created.Link.ShortURL, s.base)
		}
		return nil
	})

	code := created.Link.ShortCode
	manageKey := created.ManageKey
	keyHeader := map[string]string{"X-Manage-Key": manageKey}
	if code == "" {
		return errors.New("创建失败，无法继续后续步骤")
	}

	// ---------- 3. 跳转 ----------
	s.check("GET /{code} 返回 302 且 Location 等于目标地址", func(ctx context.Context) error {
		got, err := s.do(ctx, http.MethodGet, "/"+code, nil, nil)
		if err != nil {
			return err
		}
		if got.status != http.StatusFound {
			return fmt.Errorf("期望 302，实际 %d", got.status)
		}
		if loc := got.header.Get("Location"); loc != targetURL {
			return fmt.Errorf("Location=%q，期望 %q", loc, targetURL)
		}
		return nil
	})

	// ---------- 4. 统计收敛 ----------
	const clicks = 5
	s.check(fmt.Sprintf("连续跳转 %d 次后统计在 10s 内收敛", clicks), func(ctx context.Context) error {
		agents := []string{
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36",
			"Mozilla/5.0 (iPhone; CPU iPhone OS 17_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Mobile/15E148 Safari/604.1",
			"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Safari/605.1.15",
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36 Edg/128.0.0.0",
			"Mozilla/5.0 (X11; Linux x86_64; rv:129.0) Gecko/20100101 Firefox/129.0",
		}
		for i := range clicks {
			got, err := s.do(ctx, http.MethodGet, "/"+code, nil, map[string]string{
				"User-Agent": agents[i%len(agents)],
				"Referer":    "https://twitter.com/some/status",
			})
			if err != nil {
				return err
			}
			if got.status != http.StatusFound {
				return fmt.Errorf("第 %d 次跳转返回 %d", i+1, got.status)
			}
		}

		// 两个口径都要到位：
		//   total_clicks  = PG 基线 + Redis 待同步增量（跳转后立刻可见）
		//   window_clicks = click_events 明细条数（必须等 worker 落库，验证的是消费链路）
		deadline := time.Now().Add(15 * time.Second)
		var lastTotal, lastWindow int64
		for time.Now().Before(deadline) {
			stats, err := s.fetchStats(ctx, code, keyHeader)
			if err == nil {
				lastTotal, lastWindow = stats.TotalClicks, stats.WindowClicks
				if lastTotal >= clicks && lastWindow >= clicks {
					if len(stats.Daily) != 7 {
						return fmt.Errorf("daily 长度 %d，期望 7（补齐空缺日期）", len(stats.Daily))
					}
					if !hasDevice(stats, "desktop") && !hasDevice(stats, "mobile") {
						return fmt.Errorf("设备分布未识别出 desktop/mobile：%+v", stats.Devices)
					}
					if len(stats.Browsers) == 0 {
						return errors.New("浏览器分布为空，UA 解析可能未生效")
					}
					return nil
				}
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(300 * time.Millisecond):
			}
		}
		return fmt.Errorf("15s 内 total_clicks=%d、window_clicks=%d，期望都 ≥ %d",
			lastTotal, lastWindow, clicks)
	})

	// ---------- 5. 鉴权边界 ----------
	s.check("无密钥读详情返回 404（不泄露资源是否存在）", func(ctx context.Context) error {
		got, err := s.do(ctx, http.MethodGet, "/api/links/"+code, nil, nil)
		if err != nil {
			return err
		}
		if got.status != http.StatusNotFound {
			return fmt.Errorf("期望 404，实际 %d", got.status)
		}
		return nil
	})

	s.check("带 X-Manage-Key 读详情返回 200", func(ctx context.Context) error {
		got, err := s.do(ctx, http.MethodGet, "/api/links/"+code, nil, keyHeader)
		if err != nil {
			return err
		}
		if got.status != http.StatusOK {
			return fmt.Errorf("期望 200，实际 %d：%s", got.status, got.body)
		}
		return nil
	})

	s.check("PATCH 改标题后缓存被主动失效（立刻可读到新值）", func(ctx context.Context) error {
		got, err := s.do(ctx, http.MethodPatch, "/api/links/"+code, map[string]any{
			"title": "改过的标题",
		}, keyHeader)
		if err != nil {
			return err
		}
		if got.status != http.StatusOK {
			return fmt.Errorf("PATCH 期望 200，实际 %d：%s", got.status, got.body)
		}

		// 立刻回读：若缓存没失效，这里会拿到旧值
		got, err = s.do(ctx, http.MethodGet, "/api/links/"+code, nil, keyHeader)
		if err != nil {
			return err
		}
		var body struct {
			Title string `json:"title"`
		}
		if err := got.decode(&body); err != nil {
			return err
		}
		if body.Title != "改过的标题" {
			return fmt.Errorf("title=%q，期望「改过的标题」（缓存可能未失效）", body.Title)
		}
		return nil
	})

	// ---------- 6. 输入防护 ----------
	s.check("javascript: 目标被拒（开放重定向防护）", func(ctx context.Context) error {
		got, err := s.do(ctx, http.MethodPost, "/api/links", map[string]any{
			"target_url": "javascript:alert(1)",
		}, nil)
		if err != nil {
			return err
		}
		if got.status != http.StatusUnprocessableEntity {
			return fmt.Errorf("期望 422，实际 %d：%s", got.status, got.body)
		}
		return nil
	})

	customCode := "smoke-" + randomSuffix(6)
	// 这条链接后面要用作「访问口令」的载体：口令用例一律走 PATCH，
	// 不再新建链接 —— 创建接口是 10 次/分钟/IP，本套用例前面已经用掉 8 次，
	// 再建几条会把最后的限流用例变成偶发红。
	var customKey string
	s.check("自定义短码可创建", func(ctx context.Context) error {
		got, err := s.do(ctx, http.MethodPost, "/api/links", map[string]any{
			"target_url":  "https://example.com/custom",
			"custom_code": customCode,
		}, nil)
		if err != nil {
			return err
		}
		if got.status != http.StatusCreated {
			return fmt.Errorf("期望 201，实际 %d：%s", got.status, got.body)
		}
		var body struct {
			ManageKey string `json:"manage_key"`
		}
		if err := got.decode(&body); err != nil {
			return err
		}
		if body.ManageKey == "" {
			return errors.New("匿名创建必须返回 manage_key（口令用例要用它改这条链接）")
		}
		customKey = body.ManageKey
		return nil
	})

	s.check("重复的自定义短码返回 409", func(ctx context.Context) error {
		got, err := s.do(ctx, http.MethodPost, "/api/links", map[string]any{
			"target_url":  "https://example.com/custom-2",
			"custom_code": customCode,
		}, nil)
		if err != nil {
			return err
		}
		if got.status != http.StatusConflict {
			return fmt.Errorf("期望 409，实际 %d：%s", got.status, got.body)
		}
		return nil
	})

	s.check("保留字（login）被拒绝，前端路由不会被短码吃掉", func(ctx context.Context) error {
		got, err := s.do(ctx, http.MethodPost, "/api/links", map[string]any{
			"target_url":  "https://example.com/hijack",
			"custom_code": "login",
		}, nil)
		if err != nil {
			return err
		}
		if got.status != http.StatusUnprocessableEntity {
			return fmt.Errorf("期望 422，实际 %d：%s", got.status, got.body)
		}
		return nil
	})

	s.check("GET /login 不会被当成短码跳转", func(ctx context.Context) error {
		got, err := s.do(ctx, http.MethodGet, "/login", nil, nil)
		if err != nil {
			return err
		}
		// 直连后端时是 404；经 nginx 时是 SPA 首页 200。两者都不是跳转。
		if got.status == http.StatusFound || got.status == http.StatusGone {
			return fmt.Errorf("期望 404 或 200，实际 %d（被短码路由吃掉了）", got.status)
		}
		return nil
	})

	// ---------- 6b. 访问口令（M5-1）----------
	// 载体是上面那条 customCode 链接：口令一律通过 PATCH 设置，不新建链接
	// （创建接口 10 次/分钟/IP，本套用例前面已经用掉 8 次）。
	customKeyHeader := map[string]string{"X-Manage-Key": customKey}
	const customPassword = "smoke-pass-9f3a"

	s.check("PATCH 设置口令后：跳转返回 200 口令页，且不计点击", func(ctx context.Context) error {
		got, err := s.do(ctx, http.MethodPatch, "/api/links/"+customCode, map[string]any{
			"password": customPassword,
		}, customKeyHeader)
		if err != nil {
			return err
		}
		if got.status != http.StatusOK {
			return fmt.Errorf("设置口令期望 200，实际 %d：%s", got.status, got.body)
		}
		var body struct {
			PasswordProtected bool `json:"password_protected"`
		}
		if err := got.decode(&body); err != nil {
			return err
		}
		if !body.PasswordProtected {
			return errors.New("设置口令后 password_protected 应为 true")
		}

		// 未解锁：200 + HTML 的口令页，不是 302
		page, err := s.do(ctx, http.MethodGet, "/"+customCode, nil, nil)
		if err != nil {
			return err
		}
		if page.status != http.StatusOK {
			return fmt.Errorf("未解锁期望 200 口令页，实际 %d", page.status)
		}
		if ct := page.header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			return fmt.Errorf("口令页 Content-Type=%q，期望 text/html", ct)
		}
		if !bytes.Contains(page.body, []byte("<form")) {
			return errors.New("口令页里应当有口令表单")
		}

		// 口令页不是跳转：计数必须还是 0（用 stats 的 total_clicks，
		// 它含 Redis 待同步增量；详情接口的 click_count 只有 PG 基线）
		stats, err := s.fetchStats(ctx, customCode, customKeyHeader)
		if err != nil {
			return err
		}
		if stats.TotalClicks != 0 {
			return fmt.Errorf("口令页不该计点击，total_clicks=%d，期望 0", stats.TotalClicks)
		}
		return nil
	})

	s.check("口令错误：401 且不计点击", func(ctx context.Context) error {
		got, err := s.postForm(ctx, "/"+customCode, url.Values{"password": {"wrong-guess"}})
		if err != nil {
			return err
		}
		if got.status != http.StatusUnauthorized {
			return fmt.Errorf("错误口令期望 401，实际 %d：%s", got.status, got.body)
		}
		if ct := got.header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			return fmt.Errorf("口令错误也应当回到口令页，Content-Type=%q", ct)
		}

		stats, err := s.fetchStats(ctx, customCode, customKeyHeader)
		if err != nil {
			return err
		}
		if stats.TotalClicks != 0 {
			return fmt.Errorf("口令错误也不该计点击，total_clicks=%d，期望 0", stats.TotalClicks)
		}
		return nil
	})

	s.check("口令正确：303 → 带 cookie 的 GET 302，且恰好只多一次点击", func(ctx context.Context) error {
		got, err := s.postForm(ctx, "/"+customCode, url.Values{"password": {customPassword}})
		if err != nil {
			return err
		}
		if got.status != http.StatusSeeOther {
			return fmt.Errorf("口令正确期望 303（让浏览器改用 GET），实际 %d：%s", got.status, got.body)
		}
		if loc := got.header.Get("Location"); loc != "/"+customCode {
			return fmt.Errorf("303 的 Location=%q，期望 /%s", loc, customCode)
		}

		cookie := unlockCookieValue(got)
		if cookie == "" {
			return errors.New("303 响应缺少解锁 cookie")
		}

		jump, err := s.do(ctx, http.MethodGet, "/"+customCode, nil, map[string]string{"Cookie": cookie})
		if err != nil {
			return err
		}
		if jump.status != http.StatusFound {
			return fmt.Errorf("带 cookie 跳转期望 302，实际 %d", jump.status)
		}

		// 只多一次：解锁那次不计点击，计点击的是随后这个 GET。
		//
		// 这里要**轮询**而不是读一次：点击是先投进有界队列、再由写入协程 INCR 进 Redis 的，
		// 慢 runner 上「GET 的响应」与「INCR 落地」之间可能差几毫秒。读一次就断言会让这条用例
		// 偶发变红（与上面「统计收敛」那条同一个套路）。
		deadline := time.Now().Add(5 * time.Second)
		var lastTotal int64
		for time.Now().Before(deadline) {
			stats, err := s.fetchStats(ctx, customCode, customKeyHeader)
			if err != nil {
				return err
			}
			lastTotal = stats.TotalClicks
			if lastTotal == 1 {
				return nil
			}
			if lastTotal > 1 {
				return fmt.Errorf("解锁后 total_clicks=%d，期望恰好 1（解锁那次不该计点击）", lastTotal)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
		return fmt.Errorf("5s 内 total_clicks 仍是 %d，期望 1", lastTotal)
	})

	// ---------- 7. 注册 / 登录 / 归属 ----------
	emailA := fmt.Sprintf("smoke-a-%s@example.com", randomSuffix(8))
	passwordA := "smoke-pass-9f3a"
	var tokenA string

	s.check("POST /api/auth/register 返回 201 与 token", func(ctx context.Context) error {
		got, err := s.do(ctx, http.MethodPost, "/api/auth/register", map[string]any{
			"email":        emailA,
			"password":     passwordA,
			"display_name": "冒烟甲",
		}, nil)
		if err != nil {
			return err
		}
		if got.status != http.StatusCreated {
			return fmt.Errorf("期望 201，实际 %d：%s", got.status, got.body)
		}
		var body struct {
			User struct {
				ID string `json:"id"`
			} `json:"user"`
			Token string `json:"token"`
		}
		if err := got.decode(&body); err != nil {
			return err
		}
		if body.Token == "" {
			return errors.New("响应缺少 token")
		}
		if body.User.ID == "" {
			return errors.New("响应缺少 user.id")
		}
		tokenA = body.Token
		return nil
	})

	s.check("重复邮箱注册返回 409", func(ctx context.Context) error {
		got, err := s.do(ctx, http.MethodPost, "/api/auth/register", map[string]any{
			"email": emailA, "password": passwordA,
		}, nil)
		if err != nil {
			return err
		}
		if got.status != http.StatusConflict {
			return fmt.Errorf("期望 409，实际 %d：%s", got.status, got.body)
		}
		return nil
	})

	s.check("错误口令登录返回 401 invalid_credentials，文案是「邮箱或密码不正确」", func(ctx context.Context) error {
		got, err := s.do(ctx, http.MethodPost, "/api/auth/login", map[string]any{
			"email": emailA, "password": "wrong-password",
		}, nil)
		if err != nil {
			return err
		}
		if got.status != http.StatusUnauthorized {
			return fmt.Errorf("期望 401，实际 %d", got.status)
		}
		body, err := decodeErrorBody(got)
		if err != nil {
			return err
		}
		if body.Code != "invalid_credentials" {
			return fmt.Errorf("error.code=%q，期望 invalid_credentials —— 与「未认证」的 unauthorized "+
				"分开，前端才知道该改输入还是该回登录页", body.Code)
		}
		if body.Message != "邮箱或密码不正确" {
			return fmt.Errorf("message=%q，期望「邮箱或密码不正确」——"+
				"原先是「登录状态无效，请重新登录」，会把改密码的人引去清 cookie", body.Message)
		}
		return nil
	})

	s.check("登录的字段级校验与注册同口径（422 + field）", func(ctx context.Context) error {
		cases := []struct {
			name  string
			body  map[string]any
			field string
		}{
			{"邮箱格式不合法", map[string]any{"email": "bad", "password": passwordA}, "email"},
			{"口令为空", map[string]any{"email": emailA, "password": ""}, "password"},
		}
		for _, c := range cases {
			got, err := s.do(ctx, http.MethodPost, "/api/auth/login", c.body, nil)
			if err != nil {
				return err
			}
			if got.status != http.StatusUnprocessableEntity {
				return fmt.Errorf("%s：期望 422（字段级校验），实际 %d：%s", c.name, got.status, truncate(string(got.body), 160))
			}
			body, err := decodeErrorBody(got)
			if err != nil {
				return err
			}
			if body.Field != c.field {
				return fmt.Errorf("%s：error.field=%q，期望 %q（前端靠它把提示挂到输入框）", c.name, body.Field, c.field)
			}
		}
		return nil
	})

	s.check("正确口令登录返回 200，且 /api/auth/me 可用", func(ctx context.Context) error {
		got, err := s.do(ctx, http.MethodPost, "/api/auth/login", map[string]any{
			"email": emailA, "password": passwordA,
		}, nil)
		if err != nil {
			return err
		}
		if got.status != http.StatusOK {
			return fmt.Errorf("登录期望 200，实际 %d：%s", got.status, got.body)
		}

		me, err := s.do(ctx, http.MethodGet, "/api/auth/me", nil, bearer(tokenA))
		if err != nil {
			return err
		}
		if me.status != http.StatusOK {
			return fmt.Errorf("/api/auth/me 期望 200，实际 %d", me.status)
		}
		return nil
	})

	s.check("无 token 访问 /api/auth/me 返回 401 unauthorized（与凭据错误分开）", func(ctx context.Context) error {
		got, err := s.do(ctx, http.MethodGet, "/api/auth/me", nil, nil)
		if err != nil {
			return err
		}
		if got.status != http.StatusUnauthorized {
			return fmt.Errorf("期望 401，实际 %d", got.status)
		}
		body, err := decodeErrorBody(got)
		if err != nil {
			return err
		}
		if body.Code != "unauthorized" {
			return fmt.Errorf("error.code=%q，期望 unauthorized —— 前端据此清空本地会话并送回登录页", body.Code)
		}
		return nil
	})

	var ownedCodes []string
	s.check("登录用户创建的链接归属账号（anonymous=false）", func(ctx context.Context) error {
		for i := range 3 {
			got, err := s.do(ctx, http.MethodPost, "/api/links", map[string]any{
				"target_url": fmt.Sprintf("https://example.com/owned/%d", i),
				"title":      fmt.Sprintf("账号链接 %d", i),
			}, bearer(tokenA))
			if err != nil {
				return err
			}
			if got.status != http.StatusCreated {
				return fmt.Errorf("第 %d 次创建返回 %d：%s", i+1, got.status, got.body)
			}
			var body struct {
				Link struct {
					ShortCode string `json:"short_code"`
					Anonymous bool   `json:"anonymous"`
				} `json:"link"`
				ManageKey string `json:"manage_key"`
			}
			if err := got.decode(&body); err != nil {
				return err
			}
			if body.Link.Anonymous {
				return errors.New("登录创建的链接 anonymous 应为 false")
			}
			if body.ManageKey != "" {
				return errors.New("登录创建不应返回 manage_key")
			}
			ownedCodes = append(ownedCodes, body.Link.ShortCode)
		}
		return nil
	})

	s.check("GET /api/links?limit=2 返回 2 条并给出 next_cursor", func(ctx context.Context) error {
		got, err := s.do(ctx, http.MethodGet, "/api/links?limit=2", nil, bearer(tokenA))
		if err != nil {
			return err
		}
		if got.status != http.StatusOK {
			return fmt.Errorf("期望 200，实际 %d：%s", got.status, got.body)
		}
		var body struct {
			Links []struct {
				ShortCode string `json:"short_code"`
			} `json:"links"`
			NextCursor string `json:"next_cursor"`
		}
		if err := got.decode(&body); err != nil {
			return err
		}
		if len(body.Links) != 2 {
			return fmt.Errorf("返回 %d 条，期望 2 条", len(body.Links))
		}
		if body.NextCursor == "" {
			return errors.New("还有下一页但 next_cursor 为空")
		}
		return nil
	})

	s.check("非所有者访问他人链接详情返回 404", func(ctx context.Context) error {
		other, err := s.register(ctx, "smoke-c")
		if err != nil {
			return err
		}
		if len(ownedCodes) == 0 {
			return errors.New("没有可用的受测链接")
		}
		got, err := s.do(ctx, http.MethodGet, "/api/links/"+ownedCodes[0], nil, bearer(other))
		if err != nil {
			return err
		}
		if got.status != http.StatusNotFound {
			return fmt.Errorf("期望 404，实际 %d", got.status)
		}
		return nil
	})

	// ---------- 8. 认领 ----------
	var tokenB string
	s.check("登录用户持 manage_key 认领匿名链接后，匿名归属变为账号", func(ctx context.Context) error {
		token, err := s.register(ctx, "smoke-b")
		if err != nil {
			return err
		}
		tokenB = token
		// 认领前：B 无权限
		before, err := s.do(ctx, http.MethodGet, "/api/links/"+code, nil, bearer(tokenB))
		if err != nil {
			return err
		}
		if before.status != http.StatusNotFound {
			return fmt.Errorf("认领前期望 404，实际 %d", before.status)
		}

		claimed, err := s.do(ctx, http.MethodPost, "/api/links/"+code+"/claim", nil, mergeHeaders(bearer(tokenB), keyHeader))
		if err != nil {
			return err
		}
		if claimed.status != http.StatusOK {
			return fmt.Errorf("认领期望 200，实际 %d：%s", claimed.status, claimed.body)
		}

		// 认领后：B 有权限，且不再 anonymous
		after, err := s.do(ctx, http.MethodGet, "/api/links/"+code, nil, bearer(tokenB))
		if err != nil {
			return err
		}
		if after.status != http.StatusOK {
			return fmt.Errorf("认领后期望 200，实际 %d", after.status)
		}
		var body struct {
			Anonymous bool `json:"anonymous"`
		}
		if err := after.decode(&body); err != nil {
			return err
		}
		if body.Anonymous {
			return errors.New("认领后 anonymous 应为 false")
		}
		// 管理密钥应当已作废
		old, err := s.do(ctx, http.MethodGet, "/api/links/"+code, nil, keyHeader)
		if err != nil {
			return err
		}
		if old.status != http.StatusNotFound {
			return fmt.Errorf("认领后旧 manage_key 应失效（期望 404），实际 %d", old.status)
		}
		return nil
	})

	// ---------- 9. 二维码（公开可读，N8 / M4-3 方案 B）----------
	//
	// 这个端点刻意不要求凭据：二维码的内容就是 short_url 本身，而 GET /{code}
	// 本来就公开。这条断言同时钉住「公开」这件事 —— 将来谁给它挂上鉴权，这里会红。
	s.check("GET /api/links/{code}/qr.svg 公开可读、是 SVG、且不含用户可控字节", func(ctx context.Context) error {
		got, err := s.do(ctx, http.MethodGet, "/api/links/"+code+"/qr.svg", nil, nil)
		if err != nil {
			return err
		}
		if got.status != http.StatusOK {
			return fmt.Errorf("期望 200（不带任何凭据），实际 %d：%s", got.status, got.body)
		}
		if ct := got.header.Get("Content-Type"); !strings.HasPrefix(ct, "image/svg+xml") {
			return fmt.Errorf("Content-Type 期望 image/svg+xml，实际 %q", ct)
		}
		if cc := got.header.Get("Cache-Control"); !strings.Contains(cc, "max-age=300") {
			return fmt.Errorf("Cache-Control 期望含 max-age=300，实际 %q", cc)
		}

		body := string(got.body)
		if !strings.HasPrefix(body, "<svg ") || !strings.Contains(body, "<path ") {
			return fmt.Errorf("响应体不是预期的 SVG 形状：%.80s", body)
		}
		// 二维码把 short_url 编成了**模块几何**：文本里既不该出现短码，
		// 也不该出现目标地址。这条同时钉住「没有 XML 注入面」——
		// 响应体里根本不进任何用户可控字节。
		if strings.Contains(body, code) || strings.Contains(body, targetURL) {
			return errors.New("SVG 响应体里出现了用户可控字节（短码或目标地址）")
		}
		return nil
	})

	// ---------- 10. 删除 ----------
	s.check("DELETE 后详情 404、跳转 404、二维码 404", func(ctx context.Context) error {
		del, err := s.do(ctx, http.MethodDelete, "/api/links/"+code, nil, bearer(tokenB))
		if err != nil {
			return err
		}
		if del.status != http.StatusNoContent {
			return fmt.Errorf("期望 204，实际 %d：%s", del.status, del.body)
		}

		detail, err := s.do(ctx, http.MethodGet, "/api/links/"+code, nil, bearer(tokenB))
		if err != nil {
			return err
		}
		if detail.status != http.StatusNotFound {
			return fmt.Errorf("删除后详情期望 404，实际 %d", detail.status)
		}

		jump, err := s.do(ctx, http.MethodGet, "/"+code, nil, nil)
		if err != nil {
			return err
		}
		if jump.status != http.StatusNotFound {
			return fmt.Errorf("删除后跳转期望 404，实际 %d", jump.status)
		}

		// 已软删除的短链不该再出图：二维码印出去之后，取图这件事本身
		// 仍应以「链接还在不在」为准（而不是以「曾经存在过」为准）。
		qr, err := s.do(ctx, http.MethodGet, "/api/links/"+code+"/qr.svg", nil, nil)
		if err != nil {
			return err
		}
		if qr.status != http.StatusNotFound {
			return fmt.Errorf("删除后二维码期望 404，实际 %d", qr.status)
		}
		return nil
	})

	// ---------- 11. 限流（放在最后：会消耗掉本 IP 的创建配额）----------
	s.check("创建接口按 IP 限流，连续请求最终返回 429 + Retry-After", func(ctx context.Context) error {
		for i := range 15 {
			got, err := s.do(ctx, http.MethodPost, "/api/links", map[string]any{
				"target_url": fmt.Sprintf("https://example.com/ratelimit/%d", i),
			}, nil)
			if err != nil {
				return err
			}
			if got.status == http.StatusTooManyRequests {
				if got.header.Get("Retry-After") == "" {
					return errors.New("429 响应缺少 Retry-After 头")
				}
				return nil
			}
			if got.status != http.StatusCreated {
				return fmt.Errorf("第 %d 次请求返回意外状态 %d：%s", i+1, got.status, got.body)
			}
		}
		return errors.New("连续 15 次创建都没有触发限流")
	})

	return nil
}

// statsBody 是统计响应的形状。
type statsBody struct {
	TotalClicks  int64 `json:"total_clicks"`
	WindowClicks int64 `json:"window_clicks"`
	Days         int   `json:"days"`
	Daily        []struct {
		Date   string `json:"date"`
		Clicks int64  `json:"clicks"`
	} `json:"daily"`
	Devices []struct {
		Device string `json:"device"`
		Clicks int64  `json:"clicks"`
	} `json:"devices"`
	Browsers []struct {
		Browser string `json:"browser"`
		Clicks  int64  `json:"clicks"`
	} `json:"browsers"`
}

// fetchStats 拉一次统计。
func (s *suite) fetchStats(ctx context.Context, code string, headers map[string]string) (*statsBody, error) {
	got, err := s.do(ctx, http.MethodGet, "/api/links/"+code+"/stats?days=7", nil, headers)
	if err != nil {
		return nil, err
	}
	if got.status != http.StatusOK {
		return nil, fmt.Errorf("stats 返回 %d：%s", got.status, got.body)
	}
	var body statsBody
	if err := got.decode(&body); err != nil {
		return nil, err
	}
	if body.Days != 7 {
		return nil, fmt.Errorf("days=%d，期望 7", body.Days)
	}
	return &body, nil
}

// hasDevice 判断设备分布里是否存在指定取值。
func hasDevice(stats *statsBody, want string) bool {
	for _, d := range stats.Devices {
		if d.Device == want && d.Clicks > 0 {
			return true
		}
	}
	return false
}

// register 注册一个随机邮箱的账号并返回 token。
func (s *suite) register(ctx context.Context, prefix string) (string, error) {
	email := fmt.Sprintf("%s-%s@example.com", prefix, randomSuffix(8))
	got, err := s.do(ctx, http.MethodPost, "/api/auth/register", map[string]any{
		"email":    email,
		"password": "smoke-pass-9f3a",
	}, nil)
	if err != nil {
		return "", err
	}
	if got.status != http.StatusCreated {
		return "", fmt.Errorf("注册 %s 返回 %d：%s", email, got.status, got.body)
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := got.decode(&body); err != nil {
		return "", err
	}
	return body.Token, nil
}

// bearer 构造 Authorization 头。
func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// mergeHeaders 合并两组请求头。
func mergeHeaders(sets ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, set := range sets {
		for k, v := range set {
			out[k] = v
		}
	}
	return out
}

// randomSuffix 生成指定长度的十六进制随机串，避免冒烟测试之间互相撞车。
func randomSuffix(n int) string {
	buf := make([]byte, (n+1)/2)
	if _, err := rand.Read(buf); err != nil {
		// 退化到时间戳：冒烟测试不该因为熵源问题直接失败
		return fmt.Sprintf("%x", time.Now().UnixNano())[:n]
	}
	return hex.EncodeToString(buf)[:n]
}

// truncate 截断过长的响应体，便于阅读。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// assetPathRE 从首页 HTML 里取出一条内容哈希资源路径，用来验静态资源的响应头。
// 不写死文件名：哈希随每次构建变化，写死会让这条断言在下次部署后失效。
var assetPathRE = regexp.MustCompile(`/assets/[A-Za-z0-9._-]+\.js`)

// requiredSecurityHeaders 是经 nginx 访问时**必须**出现的响应头。
// 键是头名，值是它必须包含的片段（空串表示只查头名在场）。
//
// 这里只钉「在不在」与最关键的取值，不复述 security-headers.conf 里的整串 CSP ——
// 冒烟工具是黑盒端到端，太细的断言会让改一行 nginx 配置就要改测试。
var requiredSecurityHeaders = map[string]string{
	"Strict-Transport-Security": "max-age=31536000",
	"X-Frame-Options":           "DENY",
	"X-Content-Type-Options":    "nosniff",
	"Referrer-Policy":           "strict-origin-when-cross-origin",
	"Content-Security-Policy":   "frame-ancestors 'none'",
}

// errorBody 是统一错误体里被断言的那几个字段。
type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field"`
}

// decodeErrorBody 解析统一错误体。
//
// 刻意在这里硬编码结构、不 import internal/httpx：冒烟工具是黑盒端到端，
// 它该独立表达「契约长什么样」；引了内部结构就等于把「实现改了、断言跟着改」
// 的通道打开（同一理由见文件顶部关于 unlockCookieName 的注释）。
func decodeErrorBody(r *resp) (errorBody, error) {
	var wrapper struct {
		Error errorBody `json:"error"`
	}
	if err := r.decode(&wrapper); err != nil {
		return errorBody{}, fmt.Errorf("响应不是统一错误体（%q）：%w", truncate(string(r.body), 160), err)
	}
	if wrapper.Error.Code == "" {
		return errorBody{}, fmt.Errorf("错误体里缺 code：%q", truncate(string(r.body), 160))
	}
	return wrapper.Error, nil
}

// assertSecurityHeaders 断言一次响应上安全头齐全。
//
// 取值一律走 headerValue（把所有同名头拼起来）而不是 Header.Get：
// Get 只看**第一条**，而 nginx 允许同一个头出现多次 —— `/assets/` 上的 Cache-Control
// 就是两条（`expires 1y` 加一条 `max-age=…`，`add_header` 再加一条 `public, immutable`），
// 只看第一条会得到「没有 immutable」这个**假**结论（本轮第一次跑就是这么红的）。
func assertSecurityHeaders(r *resp, where string) error {
	for name, want := range requiredSecurityHeaders {
		got := headerValue(r, name)
		if got == "" {
			return fmt.Errorf("%s 的响应缺 %s（nginx 那条 add_header 是否漏了 always？"+
				"少了 always 时非 2xx 响应就不带这些头）", where, name)
		}
		if want != "" && !strings.Contains(got, want) {
			return fmt.Errorf("%s 的 %s=%q，期望含 %q", where, name, got, want)
		}
	}
	return nil
}

// headerValue 把同名响应头的所有取值拼成一条（逗号分隔，与浏览器看到的等价）。
func headerValue(r *resp, name string) string {
	return strings.Join(r.header.Values(name), ", ")
}
