package handler

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"ashen-courier/internal/domain"
	"ashen-courier/internal/httpx"
)

// 本文件守的是 `/healthz` 的**字段可见性边界**（2026-09-22 审计 A3）。
//
// 背景：`/healthz` 是匿名可达的探针（README 的 API 表里鉴权列是「—」），
// 它原本把**完整诊断**一起回出去 —— 版本指纹、进程存活时长、Stream 积压水位、
// 回源次数、丢弃计数、是否内嵌 worker、限流是否降级。而这与 `/metrics` 是同一批数字，
// 后者在 nginx 里被显式 404、README 还专门用一节论证「不对外」。
// 同一批数字必须同一套可见性，所以诊断挪到 `/healthz/details`。
//
// 这个边界最容易静默失效的方向是**反向的**：往 HealthReport 上新增一个诊断字段，
// 顺手在 handler 里把 report 整个写出去 —— 于是新字段立刻公开，而没有任何测试会红
// （HealthReport 的字段都带 omitzero，多一个少一个都不影响既有断言）。
// 这就是下面那条「键集合必须恰好等于三项」而不是「不含某几个字段」的原因：
// 白名单式断言能挡住**将来**新增的字段，黑名单不能。

// fatHealthProbe 返回一份**每个诊断字段都非零**的报告。
//
// 「都非零」是本用例成立的前提：HealthReport 的诊断字段都带 `omitzero`，
// 零值会被 JSON 丢掉 —— 若用 okProbe（只填 status/postgres/redis），
// 那么「公开响应里不该有 version」会因为 version 本来就是空串而必然通过，
// 是个永远绿的守卫。注意 publicStatus / publicPostgres 这两个期望值刻意与下面不同，
// 一旦漏了 .Public()，键集合断言会先红。
type fatHealthProbe struct{}

const (
	fatProbeVersion = "v9.9.9-leak-canary"
	fatProbeUptime  = int64(18)
)

func (fatHealthProbe) Report(context.Context) httpx.HealthReport {
	return httpx.HealthReport{
		Status:            "degraded",
		Version:           fatProbeVersion,
		Postgres:          "error",
		Redis:             "ok",
		WorkerEnabled:     true,
		DroppedClicks:     11,
		FailedClicks:      12,
		QueueLen:          13,
		StreamLen:         14,
		StreamPending:     15,
		PGFallbacks:       16,
		RateLimitDegraded: 17,
		UptimeSeconds:     fatProbeUptime,
		RateLimitByNative: true,
		RateLimitDisabled: true,
		ConsumedClicks:    19,
		WorkerErrors:      20,
		Errors:            []string{"postgres 不可用"},
	}
}

// TestHealthzExposesOnlyLivenessSubset 钉住公开面只含存活/就绪所需的三项。
func TestHealthzExposesOnlyLivenessSubset(t *testing.T) {
	t.Parallel()

	router := newTestRouterWithProbe(t, map[string]*domain.Link{}, newStubUsers(), fatHealthProbe{})

	t.Run("/healthz 的键集合恰好是 status/postgres/redis", func(t *testing.T) {
		t.Parallel()

		code, body := getJSON(t, router, "/healthz")
		if code != http.StatusServiceUnavailable {
			t.Fatalf("状态码 %d，期望 503 —— 探针报 degraded 时编排系统必须能感知（Retry-After 随之）", code)
		}

		var keys map[string]any
		if err := json.Unmarshal([]byte(body), &keys); err != nil {
			t.Fatalf("响应不是合法 JSON：%v（%q）", err, body)
		}

		// 白名单：任何**将来**新增的诊断字段都会在这里被拦下。
		allowed := map[string]bool{"status": true, "postgres": true, "redis": true}
		for k := range keys {
			if !allowed[k] {
				t.Errorf("公开的 /healthz 里出现了 %q。\n"+
					"    这个端点匿名可达，诊断字段（版本指纹、存活时长、积压水位、回源次数…）"+
					"只该出现在 /healthz/details（与 /metrics 同策略：nginx 显式 404）。\n"+
					"    加字段的正确做法：加到 httpx.HealthReport（details 与 /metrics 会自动带上），"+
					"而**不是**加到 httpx.PublicHealthReport。\n"+
					"    响应：%s", k, body)
			}
		}
		for k := range allowed {
			if _, ok := keys[k]; !ok {
				t.Errorf("公开的 /healthz 少了 %q：编排系统靠它判断「是哪个依赖不可用」，"+
					"否则 503 只是一个无法行动的信号。响应：%s", k, body)
			}
		}

		// 取值也要是探针原话（degraded / error / ok），别在截取时写死了 "ok"
		var got httpx.PublicHealthReport
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("公开响应解不成 PublicHealthReport：%v", err)
		}
		if got.Status != "degraded" || got.Postgres != "error" || got.Redis != "ok" {
			t.Errorf("公开响应取值不对：%+v，期望 degraded/error/ok（应当是探针的结论原样透出）", got)
		}
	})

	t.Run("/healthz/details 带回完整诊断，且 degraded 时仍回 200", func(t *testing.T) {
		t.Parallel()

		code, body := getJSON(t, router, "/healthz/details")
		if code != http.StatusOK {
			t.Fatalf("状态码 %d，期望 200：判断依赖好坏由 /healthz 的状态码承担；"+
				"本端点是诊断快照，degraded 那一刻的数字最该被交出去（与 /metrics 同理由）", code)
		}

		var full httpx.HealthReport
		if err := json.Unmarshal([]byte(body), &full); err != nil {
			t.Fatalf("诊断响应解不成 HealthReport：%v（%q）", err, body)
		}

		// 逐字段断言而不是只查「有没有这几个词」：失败信息要能直接指出是哪个字段没带上。
		// 覆盖每一类：身份（version）、时序（uptime）、积压（queue/stream/pending）、
		// 观测计数（pg_fallbacks / dropped / failed / rate_limit_degraded）、
		// 拓扑（worker_enabled / rate_limit_native_increx / rate_limit_disabled）、
		// 内嵌 worker（consumed / worker_errors）、故障说明（errors）。
		if full.Version != fatProbeVersion {
			t.Errorf("version = %q，期望 %q", full.Version, fatProbeVersion)
		}
		if full.UptimeSeconds != fatProbeUptime {
			t.Errorf("uptime_seconds = %d，期望 %d", full.UptimeSeconds, fatProbeUptime)
		}
		if full.QueueLen != 13 || full.StreamLen != 14 || full.StreamPending != 15 {
			t.Errorf("积压字段不对：queue=%d stream=%d pending=%d，期望 13/14/15",
				full.QueueLen, full.StreamLen, full.StreamPending)
		}
		if full.PGFallbacks != 16 || full.DroppedClicks != 11 || full.FailedClicks != 12 {
			t.Errorf("观测计数不对：pg_fallbacks=%d dropped=%d failed=%d，期望 16/11/12",
				full.PGFallbacks, full.DroppedClicks, full.FailedClicks)
		}
		if full.RateLimitDegraded != 17 {
			t.Errorf("rate_limit_degraded = %d，期望 17", full.RateLimitDegraded)
		}
		if !full.WorkerEnabled || !full.RateLimitByNative || !full.RateLimitDisabled {
			t.Errorf("拓扑布尔不对：worker_enabled=%v native=%v disabled=%v，期望全 true",
				full.WorkerEnabled, full.RateLimitByNative, full.RateLimitDisabled)
		}
		if full.ConsumedClicks != 19 || full.WorkerErrors != 20 {
			t.Errorf("内嵌 worker 字段不对：consumed=%d errors=%d，期望 19/20",
				full.ConsumedClicks, full.WorkerErrors)
		}
		if len(full.Errors) != 1 || full.Errors[0] != "postgres 不可用" {
			t.Errorf("errors = %v，期望 [\"postgres 不可用\"]（诊断端点要给出可行动的故障说明）", full.Errors)
		}
	})

	t.Run("探针为 nil 时公开面仍然只回三项", func(t *testing.T) {
		t.Parallel()

		// 这个分支绕过了 Report，是另一条独立的写出路径 —— 单独钉一次，
		// 免得将来只改了一处。
		router := newTestRouterWithProbe(t, map[string]*domain.Link{}, newStubUsers(), nil)

		code, body := getJSON(t, router, "/healthz")
		if code != http.StatusOK {
			t.Fatalf("状态码 %d，期望 200（探针缺席时不该判不健康）", code)
		}

		var keys map[string]any
		if err := json.Unmarshal([]byte(body), &keys); err != nil {
			t.Fatalf("响应不是合法 JSON：%v（%q）", err, body)
		}
		if len(keys) != 3 {
			t.Errorf("键集合 = %v，期望恰好 status/postgres/redis 三项", keys)
		}
		for _, k := range []string{"status", "postgres", "redis"} {
			if _, ok := keys[k]; !ok {
				t.Errorf("少了 %q：%v", k, keys)
			}
		}
	})
}

// getJSON 发一次 GET 并回（状态码, 响应体）。
func getJSON(t *testing.T, router http.Handler, path string) (int, string) {
	t.Helper()

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
	return rr.Code, rr.Body.String()
}
