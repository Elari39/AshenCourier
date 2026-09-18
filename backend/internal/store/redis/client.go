// Package redis 封装本项目用到的四类 Redis 用法：缓存、计数增量、Stream、限流。
//
// 统一约定：
//   - 所有键都带版本前缀（v1），方便将来换数据结构时平滑迁移
//   - 所有调用都必须带 context；本包会为每次操作套上 op 超时，
//     并用 context.WithTimeoutCause 记录「超时」这个原因，便于日志归因
//   - Redis 故障不阻塞跳转：调用方拿到错误后自行降级（见 service 层）
package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// 键前缀与固定键。短码字符集是 [0-9A-Za-z_-]，不含 ':'，因此拼键无歧义。
const (
	// linkKeyPrefix 是短码正向缓存：link:v1:{code}
	linkKeyPrefix = "link:v1:"
	// missKeyPrefix 是短码负缓存：link:v1:miss:{code}
	//
	// 注意：不用 PLAN.md 草案里的 "link:v1:-{code}"。短码字符集含 '-'，
	// 那么短码 "-abc" 的正向键 link:v1:-abc 会与短码 "abc" 的负缓存键撞车。
	// 由于短码不含 ':'，插一层 "miss:" 段即可彻底消除歧义。
	missKeyPrefix = "link:v1:miss:"
	// clickCounterPrefix 是计数增量：clicks:cnt:{code}
	clickCounterPrefix = "clicks:cnt:"
	// dirtySetKey 是需要回刷计数的短码集合
	dirtySetKey = "clicks:dirty"
	// streamKey 是点击事件流
	streamKey = "clicks:stream"
	// consumerGroup 是 Stream 的消费组名
	consumerGroup = "clicks"
	// streamMaxLen 是 Stream 的近似长度上限，防止消费跟不上导致 OOM
	streamMaxLen = 100000
)

// errOpTimeout 是「Redis 操作超时」这个取消原因。
var errOpTimeout = errors.New("redis operation timeout")

// Client 是 Redis 客户端包装，同时承载缓存 / 计数 / Stream / 限流四组方法。
type Client struct {
	rdb     *goredis.Client
	timeout time.Duration
	// increx 表示服务端是否支持 Redis 8.8+ 的原生窗口计数命令 INCREX。
	increx bool
}

// Options 是 Open 的入参。
type Options struct {
	// Addr 形如 "localhost:6379"。
	Addr string
	// Password 为空表示无口令。
	Password string
	// DB 是逻辑库编号。
	DB int
	// Timeout 是单次命令超时。
	Timeout time.Duration
	// PoolSize 是连接池上限。
	PoolSize int
}

// Open 建立连接、探测能力并做一次 Ping 验证。
func Open(ctx context.Context, opt Options) (*Client, error) {
	if opt.Timeout <= 0 {
		opt.Timeout = 200 * time.Millisecond
	}
	if opt.PoolSize <= 0 {
		opt.PoolSize = 32
	}

	rdb := goredis.NewClient(&goredis.Options{
		Addr:            opt.Addr,
		Password:        opt.Password,
		DB:              opt.DB,
		DialTimeout:     5 * time.Second,
		ReadTimeout:     opt.Timeout,
		WriteTimeout:    opt.Timeout,
		PoolSize:        opt.PoolSize,
		MinIdleConns:    2,
		ConnMaxIdleTime: 5 * time.Minute,
	})

	c := &Client{rdb: rdb, timeout: opt.Timeout}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("store.redis: ping %s: %w", opt.Addr, err)
	}

	c.increx = c.detectIncrex(ctx)
	return c, nil
}

// Close 关闭客户端。
func (c *Client) Close() error {
	if err := c.rdb.Close(); err != nil {
		return fmt.Errorf("store.redis: close: %w", err)
	}
	return nil
}

// Ping 做一次连通性探测，供 /healthz 使用。
func (c *Client) Ping(ctx context.Context) error {
	opCtx, cancel := c.opCtx(ctx)
	defer cancel()

	if err := c.rdb.Ping(opCtx).Err(); err != nil {
		return fmt.Errorf("store.redis: ping: %w", err)
	}
	return nil
}

// Raw 暴露底层客户端，仅供 worker 做少量高级操作（如 XAUTOCLAIM 的定制参数）。
func (c *Client) Raw() *goredis.Client { return c.rdb }

// SupportsINCREX 表示服务端是否原生支持 INCREX。
func (c *Client) SupportsINCREX() bool { return c.increx }

// opCtx 为单次 Redis 调用套上超时，并记录取消原因。
// 这样日志里能区分「Redis 慢」与「上游把请求取消了」。
func (c *Client) opCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(parent, c.timeout, errOpTimeout)
}

// detectIncrex 探测服务端是否支持 INCREX（Redis 8.8+）。
// 探测失败一律按「不支持」处理，回落到 Lua 脚本 —— 限流绝不能因为探测失败而失效。
func (c *Client) detectIncrex(ctx context.Context) bool {
	opCtx, cancel := c.opCtx(ctx)
	defer cancel()

	info, err := c.rdb.Do(opCtx, "COMMAND", "INFO", "INCREX").Slice()
	if err != nil || len(info) == 0 || info[0] == nil {
		return false
	}
	return true
}

// 键构造器：集中在一处，避免各处手拼字符串写错前缀。

// LinkKey 返回短码正向缓存键。
func LinkKey(code string) string { return linkKeyPrefix + code }

// MissKey 返回短码负缓存键。
func MissKey(code string) string { return missKeyPrefix + code }

// ClickCounterKey 返回计数增量键。
func ClickCounterKey(code string) string { return clickCounterPrefix + code }

// isNil 判断错误是否为「键不存在」（redis.Nil）。
// Redis 的「不存在」是正常分支而不是故障，调用方需要区分对待。
func isNil(err error) bool { return errors.Is(err, goredis.Nil) }

// anySlice 把 []string 展开为 go-redis 变参方法所需的 []any。
func anySlice(ss []string) []any {
	out := make([]any, 0, len(ss))
	for _, s := range ss {
		out = append(out, s)
	}
	return out
}
