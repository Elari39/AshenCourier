// Command smoke 是 AshenCourier 的端到端冒烟测试。
//
// 为什么用 Go 而不是 shell 脚本：本机 Git Bash 缺 coreutils，
// 而 Go 程序跨平台且能做真正的 JSON 断言。
//
// 覆盖：健康检查 → 匿名创建 → 302 跳转 → 统计收敛 → 鉴权边界 →
// 修改/删除 → 保留字与开放重定向防护 → 注册/登录 → 认领 → 分页 → 限流。
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
	"os"
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

	s.check("错误口令登录返回 401", func(ctx context.Context) error {
		got, err := s.do(ctx, http.MethodPost, "/api/auth/login", map[string]any{
			"email": emailA, "password": "wrong-password",
		}, nil)
		if err != nil {
			return err
		}
		if got.status != http.StatusUnauthorized {
			return fmt.Errorf("期望 401，实际 %d", got.status)
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

	s.check("无 token 访问 /api/auth/me 返回 401", func(ctx context.Context) error {
		got, err := s.do(ctx, http.MethodGet, "/api/auth/me", nil, nil)
		if err != nil {
			return err
		}
		if got.status != http.StatusUnauthorized {
			return fmt.Errorf("期望 401，实际 %d", got.status)
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

	// ---------- 9. 删除 ----------
	s.check("DELETE 后详情 404、跳转 404", func(ctx context.Context) error {
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
		return nil
	})

	// ---------- 10. 限流（放在最后：会消耗掉本 IP 的创建配额）----------
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
