package startup

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// errNotReady 模拟「依赖已存在但还没准备好」这类启动期错误。
//
// 用 PG 的真实文案而不是随便一个哨兵：2026-09-20 日志里 api / worker 各自退出的
// 根因就是这个 SQLSTATE 57P03（`the database system is starting up`），
// 写明它是为了让读到这里的人知道这组用例在防什么。
var errNotReady = errors.New("the database system is starting up")

// captureLogger 返回写进 buf 的 JSON 日志器，用于断言等级与字段。
func captureLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, nil))
}

// TestRetrySucceedsOnFirstAttempt 钉住最常走的那条路径：依赖本来就绪时，
// 第一轮立即执行、不先等一个退避周期，也不留任何日志。
//
// 「不先等」不是小事：启动路径上每次探测都白等 250ms，会让「重启一次多久能服务」
// 变慢，而这一档恰恰是正常情况（占绝大多数启动次数）。
func TestRetrySucceedsOnFirstAttempt(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	calls := 0
	startedAt := time.Now()

	got, err := Retry(t.Context(), captureLogger(&buf), "postgres", DefaultWindow,
		func(context.Context) (int, error) {
			calls++
			return 42, nil
		})
	if err != nil {
		t.Fatalf("首次即成功却返回错误: %v", err)
	}
	if got != 42 {
		t.Errorf("返回值 = %d，期望 42", got)
	}
	if calls != 1 {
		t.Errorf("fn 调用 %d 次，期望 1 次", calls)
	}
	if elapsed := time.Since(startedAt); elapsed >= minBackoff {
		t.Errorf("首次尝试前等了 %s（>= minBackoff %s）：第一轮应当立即执行", elapsed, minBackoff)
	}
	if out := buf.String(); out != "" {
		t.Errorf("首次即成功不应产生日志，实际: %s", out)
	}
}

// TestRetryWaitsForSlowDependency 覆盖这组用例真正的目标场景：依赖前两次没就绪、之后就绪。
//
// window 传 0，顺带钉住「window <= 0 取 DefaultWindow」这条分支 —— 若它退化成
// 「直接用传入的 0」，第一次失败就会因为「剩余预算放不下退避」而立刻放弃，
// 本用例立刻变红。这正是把它写进用例而不是只留注释的原因。
func TestRetryWaitsForSlowDependency(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	calls := 0
	startedAt := time.Now()

	got, err := Retry(t.Context(), captureLogger(&buf), "postgres", 0,
		func(context.Context) (string, error) {
			calls++
			if calls < 3 {
				return "", errNotReady
			}
			return "ready", nil
		})
	if err != nil {
		t.Fatalf("第 3 次已就绪却仍返回错误: %v", err)
	}
	if got != "ready" {
		t.Errorf("返回值 = %q，期望 %q", got, "ready")
	}
	if calls != 3 {
		t.Errorf("fn 调用 %d 次，期望 3 次", calls)
	}
	// 这里只断言「确实等了」而不是精确时长：退避曲线的形状由 nextBackoff 的用例负责，
	// 混进时长断言只会让本用例在机器繁忙时随机变红。
	if elapsed := time.Since(startedAt); elapsed < 2*minBackoff {
		t.Errorf("只等了 %s，期望至少两个退避周期（%s）", elapsed, 2*minBackoff)
	}

	logs := buf.String()
	if !strings.Contains(logs, `"level":"WARN"`) || !strings.Contains(logs, `"dependency":"postgres"`) {
		t.Errorf("每轮失败应记一条带 dependency 的 WARN，实际日志: %s", logs)
	}
	// backoff 必须是 "250ms" 这样的字符串，而不是纳秒整数 250000000 ——
	// slog 对 time.Duration 的默认编码就是后者，运维看日志得自己除以 1e6。
	// 这条断言是照着容器里的真实输出加的（见 README 验收记录）。
	if !strings.Contains(logs, `"backoff":"250ms"`) {
		t.Errorf("WARN 里的 backoff 应是可读的时长字符串，实际日志: %s", logs)
	}
	if !strings.Contains(logs, "依赖已就绪") {
		t.Errorf("重试成功应记一条 INFO 说明等了多久，实际日志: %s", logs)
	}
}

// TestRetryGivesUpAfterWindow 钉住「退避重试不等于把启动失败藏起来」：
// 窗口用尽后仍然返回错误，且错误链里能 errors.Is 到根因。
//
// 错误链这条是有实际用途的：否则排障时只能从一堆 WARN 里翻，而最终那条 FATAL
// 只剩一句「未就绪」，看不出底层是拨号失败还是认证失败。
//
// 同时它也守着「重试有上界」——fn 在本用例里永远失败，若上界判断被写坏，用例会挂住而不是变绿。
func TestRetryGivesUpAfterWindow(t *testing.T) {
	t.Parallel()

	const window = 600 * time.Millisecond

	calls := 0
	startedAt := time.Now()

	got, err := Retry(t.Context(), slog.New(slog.DiscardHandler), "postgres", window,
		func(context.Context) (int, error) {
			calls++
			return 7, errNotReady
		})
	if err == nil {
		t.Fatal("窗口用尽后应返回错误，实际 nil")
	}
	if !errors.Is(err, errNotReady) {
		t.Errorf("错误链里应保留根因（errors.Is 到 errNotReady），实际: %v", err)
	}
	if !strings.Contains(err.Error(), "postgres") || !strings.Contains(err.Error(), window.String()) {
		t.Errorf("错误信息应说明是哪个依赖、窗口多长，实际: %v", err)
	}
	if got != 0 {
		t.Errorf("失败时应返回零值，实际 %d", got)
	}
	if calls < 2 {
		t.Errorf("fn 只调用 %d 次：窗口 %s 内应至少重试一次", calls, window)
	}
	if elapsed := time.Since(startedAt); elapsed > window+2*time.Second {
		t.Errorf("耗时 %s 超出窗口 %s 过多", elapsed, window)
	}
}

// TestRetryAbortsOnContextCancel 钉住「收到 SIGTERM 后不再傻等」。
//
// 关停时继续退避重试会拖住优雅关闭：进程明明已经被要求退出，却在后台又等一轮依赖。
// 这里把窗口设成 1 分钟，正是为了让「没有及时退出」这种情况无法靠运气蒙过去。
func TestRetryAbortsOnContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var buf bytes.Buffer
	calls := 0
	startedAt := time.Now()

	_, err := Retry(ctx, captureLogger(&buf), "redis", time.Minute,
		func(context.Context) (string, error) {
			calls++
			if calls == 1 {
				cancel() // 模拟第一次探测期间收到 SIGTERM
			}
			return "", errNotReady
		})
	if err == nil {
		t.Fatal("ctx 已取消却返回 nil 错误")
	}
	if !errors.Is(err, errNotReady) {
		t.Errorf("应把最后一轮探测的错误带出来，实际: %v", err)
	}
	if calls != 1 {
		t.Errorf("ctx 取消后不应再尝试，fn 调用 %d 次", calls)
	}
	if elapsed := time.Since(startedAt); elapsed > minBackoff {
		t.Errorf("ctx 取消后等了 %s 才返回", elapsed)
	}
	if strings.Contains(buf.String(), `"level":"WARN"`) {
		t.Errorf("ctx 已取消时不该再记「稍后重试」的 WARN，实际日志: %s", buf.String())
	}
}

// TestNextBackoffDoublesAndCaps 钉住退避曲线的形状，不需要任何 sleep。
//
// 为什么值得专门测：起步 250ms + 封顶 2s 决定了 30 秒窗口内还能剩几次尝试
// （容器里实测 **18 次**）。把封顶上丢掉会退化成「指数退避吃掉整个窗口」，
// 把翻倍丢掉则退化成「固定 250ms 高频重试」—— 两者都能靠这条断言挡下来。
func TestNextBackoffDoublesAndCaps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{name: "起步 250ms 翻倍到 500ms", in: 250 * time.Millisecond, want: 500 * time.Millisecond},
		{name: "500ms 翻倍到 1s", in: 500 * time.Millisecond, want: time.Second},
		{name: "1s 翻倍到 2s", in: time.Second, want: 2 * time.Second},
		{name: "到达封顶后不再增长", in: 2 * time.Second, want: 2 * time.Second},
		{name: "超过封顶也回落到封顶", in: 10 * time.Second, want: 2 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := nextBackoff(tt.in); got != tt.want {
				t.Errorf("nextBackoff(%s) = %s，期望 %s", tt.in, got, tt.want)
			}
		})
	}

	// 封顶必须是 maxBackoff 本身，而不是某个「差不多」的值：
	// 它同时是 WARN 日志里 backoff 字段的上界，运维按这个数估算「还要等多久」。
	if got := nextBackoff(maxBackoff); got != maxBackoff {
		t.Errorf("nextBackoff(maxBackoff) = %s，期望封顶值 %s", got, maxBackoff)
	}
	if minBackoff <= 0 || maxBackoff < minBackoff {
		t.Fatalf("退避区间不自洽：min=%s max=%s", minBackoff, maxBackoff)
	}
}
