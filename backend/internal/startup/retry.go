// Package startup 提供「启动期等待依赖就绪」的退避重试。
//
// 为什么需要它：api / worker 启动时各做一次依赖探测（postgres.Open 与 redis.Open
// 内部各有一次 Ping），失败就直接退出。在容器编排里这就是 **crash-loop** ——
// PG 还在初始化时（服务端报 SQLSTATE 57P03 「the database system is starting up」）
// 或刚从故障中恢复时，进程一次次起不来，只能靠 `restart: unless-stopped` 空转，
// 每轮都往日志里写一条 ERROR。
//
// 本机日志里实测到过三组这样的记录（`.workbuddy/log-*.txt`）：
// 2026-09-20 15:41、2026-09-21 01:47 各一组 `api 进程退出` + `worker 进程退出`。
//
// 退避重试把「让编排层重启整个进程」换成「进程内等一会儿再试」：
// 日志从 ERROR 降级成 WARN，收敛窗口也从「Docker 的 100ms→200ms→…→1min 翻倍退避」
// 变成可控的 30 秒。
//
// 与「fail fast」的关系：这不是把启动失败藏起来 —— 窗口用尽后仍然返回错误并让进程退出，
// 只是把「依赖暂时没起来」与「依赖根本连不上」这两种情况区分开了。
package startup

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// DefaultWindow 是等待依赖就绪的总时长上限。
//
// 取 30 秒的理由：它比 Docker 自身的重启退避（翻倍到 1 分钟）收敛得更快，
// 又不至于让「口令写错 / 端口填错」这类永久性失败拖很久才报出来 ——
// 那种情况下用户想尽快看到错误，而不是等 5 分钟。
const DefaultWindow = 30 * time.Second

// 退避区间。起步 250ms 是为了让「PG 只是抖了一下」这种情况几乎无感地过去；
// 上限 2 秒是为了在 30 秒窗口内还能留下十来次尝试，而不是退避到只能试两次。
const (
	minBackoff = 250 * time.Millisecond
	maxBackoff = 2 * time.Second
)

// Retry 反复尝试 fn，直到成功、ctx 被取消，或用尽 window。
//
// window <= 0 时取 DefaultWindow。
//
// 泛型而不是「只接受 func() error」：调用方拿到的正是依赖本身（*postgres.DB 等），
// 不用为了用重试而先声明一个外面的变量再往里赋值。
//
// 第一轮立即执行（不先等）；每一轮之前会记一条 WARN，方便排障时看出「启动慢是因为依赖没就绪」。
func Retry[T any](
	ctx context.Context,
	log *slog.Logger,
	what string,
	window time.Duration,
	fn func(context.Context) (T, error),
) (T, error) {
	var zero T

	if window <= 0 {
		window = DefaultWindow
	}
	startedAt := time.Now()
	deadline := startedAt.Add(window)
	delay := minBackoff

	for attempt := 1; ; attempt++ {
		value, err := fn(ctx)
		if err == nil {
			if attempt > 1 {
				log.Info("依赖已就绪，继续启动",
					"dependency", what, "attempts", attempt, "waited", time.Since(startedAt).String())
			}
			return value, nil
		}

		// ctx 被取消（收到 SIGTERM）：不再等下去，把这一轮的错误带出去
		if ctx.Err() != nil {
			return zero, err
		}

		// 剩余预算放不下下一次退避就放弃 —— 这样「窗口用尽」这件事有确定的上界，
		// 不会出现「等到了窗口之外才报错」。
		if time.Now().Add(delay).After(deadline) {
			return zero, fmt.Errorf(
				"startup: %s 在 %s 内未就绪（尝试 %d 次）: %w", what, window, attempt, err)
		}

		// 时长显式转成字符串：slog 对 time.Duration 的默认编码是**纳秒整数**，
		// 实测打出的是 "backoff":250000000 —— 运维得自己除以 1e6 才看得懂。
		log.Warn("依赖尚未就绪，稍后重试",
			"dependency", what, "attempt", attempt, "backoff", delay.String(), "err", err)

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, err
		case <-timer.C:
		}

		delay = nextBackoff(delay)
	}
}

// nextBackoff 给出下一次退避时长：翻倍，但封顶在 maxBackoff。
//
// 单独抽成一个纯函数是为了能被确定性地测到 —— 退避曲线的形状（起步 250ms、封顶 2s）
// 直接决定「30 秒窗口里还剩几次尝试」这个设计前提，而它没法靠测总耗时可靠地断言。
func nextBackoff(d time.Duration) time.Duration {
	return min(d*2, maxBackoff)
}
