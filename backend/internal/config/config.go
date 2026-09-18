// Package config 把环境变量装载成一份带默认值、并做过合法性校验的配置。
//
// 约定：
//   - 所有键都可通过环境变量覆盖，缺省值由 cmp.Or 提供，不使用 .env 解析库
//   - 只有真正无法给出安全默认值的项（数据库地址、Redis 口令、JWT 密钥）才拒绝启动
package config

import (
	"cmp"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// 默认值常量集中在此，便于对照 README 的环境变量表。
const (
	DefaultHTTPAddr        = ":8080"
	DefaultLogLevel        = "info"
	DefaultPGTimeout       = 3 * time.Second
	DefaultRedisTimeout    = 200 * time.Millisecond
	DefaultShutdownTimeout = 15 * time.Second
	DefaultCacheTTL        = time.Hour
	DefaultNegativeTTL     = 60 * time.Second
	DefaultLinkPageSize    = 20
	DefaultMaxLinkPageSize = 100
)

// insecureSecrets 是必须拒绝的「默认/占位」JWT 密钥，避免生产裸奔。
var insecureSecrets = []string{
	"", "secret", "changeme", "change-me", "change_me", "jwt-secret", "jwtsecret",
	"ashen", "ashen-courier", "test", "dev", "password", "your-secret-key",
}

// Config 是进程的全量配置。
type Config struct {
	// HTTPAddr 是 HTTP 监听地址，如 ":8080"。
	HTTPAddr string
	// DatabaseURL 是 PostgreSQL 连接串。
	DatabaseURL string
	// RedisAddr 是 Redis 地址，如 "redis:6379"。
	RedisAddr string
	// RedisPassword 是 Redis 口令，为空表示无口令（仅开发环境）。
	RedisPassword string
	// RedisDB 是 Redis 逻辑库编号。
	RedisDB int
	// JWTSecret 是 HS256 签名密钥。
	JWTSecret string
	// PublicBaseURL 是短链对外基址，用于拼 short_url，末尾不带 '/'。
	PublicBaseURL string
	// LogLevel 是 slog 级别。
	LogLevel slog.Level
	// WorkerEnabled 为 true 时，api 进程内嵌启动同一套 worker 循环。
	WorkerEnabled bool
	// TrustProxy 为 true 时从 X-Real-IP 取客户端 IP（生产由 nginx 注入）。
	TrustProxy bool

	// PGTimeout 是单次 PG 调用超时。
	PGTimeout time.Duration
	// RedisTimeout 是单次 Redis 调用超时。
	RedisTimeout time.Duration
	// ShutdownTimeout 是优雅关闭的最长等待时间。
	ShutdownTimeout time.Duration
	// CacheTTL 是短码正向缓存的基础 TTL。
	CacheTTL time.Duration
	// NegativeTTL 是不存在短码的负缓存 TTL。
	NegativeTTL time.Duration
	// LinkPageSize 是链接列表默认页大小。
	LinkPageSize int
	// MaxLinkPageSize 是链接列表页大小上限。
	MaxLinkPageSize int

	// JWTExpiry 是登录令牌有效期。
	JWTExpiry time.Duration

	// RateLimitCreatePerMin 是创建接口的每 IP 每分钟配额。
	RateLimitCreatePerMin int
	// RateLimitLoginPerWindow 是登录接口在 RateLimitLoginWindow 内的配额。
	RateLimitLoginPerWindow int
	// RateLimitLoginWindow 是登录接口的限流窗口。
	RateLimitLoginWindow time.Duration
	// RateLimitRedirectPerMin 是跳转接口的每 IP 每分钟宽松配额。
	RateLimitRedirectPerMin int
	// RateLimitStatsPerMin 是统计接口的每 IP×短码 每分钟配额。
	RateLimitStatsPerMin int
	// RateLimitDisabled 为 true 时启动即打开限流的应急开关（全量放行）。
	// 宁可短暂失去限流，也不让限流组件把整站挡在门外。
	RateLimitDisabled bool
}

// Role 标识进程角色，决定哪些配置项是必需的。
//
// worker 不签发令牌、也不拼对外短链，因此 api 必需的 JWT_SECRET /
// PUBLIC_BASE_URL 对它是可选的 —— 让独立的 worker 容器能少配两项。
type Role string

// 支持的进程角色。
const (
	// RoleAPI 是 HTTP 服务。
	RoleAPI Role = "api"
	// RoleWorker 是点击事件消费者。
	RoleWorker Role = "worker"
)

// Load 以 api 角色装载配置。
func Load() (*Config, error) { return LoadFor(RoleAPI) }

// LoadFor 从环境变量装载配置，并做启动前的强制校验。
// role 决定哪些项是必需的：RoleWorker 可省掉 JWT_SECRET 与 PUBLIC_BASE_URL。
func LoadFor(role Role) (*Config, error) {
	cfg := &Config{
		HTTPAddr:      cmp.Or(os.Getenv("HTTP_ADDR"), DefaultHTTPAddr),
		DatabaseURL:   strings.TrimSpace(os.Getenv("DATABASE_URL")),
		RedisAddr:     cmp.Or(os.Getenv("REDIS_ADDR"), "localhost:6379"),
		RedisPassword: os.Getenv("REDIS_PASSWORD"),
		JWTSecret:     strings.TrimSpace(os.Getenv("JWT_SECRET")),
		PublicBaseURL: strings.TrimRight(cmp.Or(os.Getenv("PUBLIC_BASE_URL"), "http://localhost:8080"), "/"),

		PGTimeout:       DefaultPGTimeout,
		RedisTimeout:    DefaultRedisTimeout,
		ShutdownTimeout: DefaultShutdownTimeout,
		CacheTTL:        DefaultCacheTTL,
		NegativeTTL:     DefaultNegativeTTL,
		LinkPageSize:    DefaultLinkPageSize,
		MaxLinkPageSize: DefaultMaxLinkPageSize,

		JWTExpiry: 7 * 24 * time.Hour,

		RateLimitCreatePerMin:   10,
		RateLimitLoginPerWindow: 20,
		RateLimitLoginWindow:    10 * time.Minute,
		RateLimitRedirectPerMin: 600,
		RateLimitStatsPerMin:    120,
	}

	var errs []error

	var err error
	if cfg.RedisDB, err = intEnv("REDIS_DB", 0); err != nil {
		errs = append(errs, err)
	}
	if cfg.WorkerEnabled, err = boolEnv("WORKER_ENABLED", false); err != nil {
		errs = append(errs, err)
	}
	if cfg.TrustProxy, err = boolEnv("TRUST_PROXY", true); err != nil {
		errs = append(errs, err)
	}
	if cfg.RateLimitDisabled, err = boolEnv("RATE_LIMIT_DISABLED", false); err != nil {
		errs = append(errs, err)
	}
	cfg.LogLevel = levelEnv("LOG_LEVEL")
	if err := validateDatabaseURL(cfg.DatabaseURL); err != nil {
		errs = append(errs, err)
	}
	// JWT_SECRET / PUBLIC_BASE_URL 是「只有 api 才需要」的两项：
	// worker 既不签发令牌，也不拼对外短链（见 Role 的注释）。
	// 判定写成「除 RoleWorker 之外一律要求」，这样将来新增角色时默认更严，不会误放行。
	if role != RoleWorker {
		if err := validateJWTSecret(cfg.JWTSecret); err != nil {
			errs = append(errs, err)
		}
		if err := validateBaseURL(cfg.PublicBaseURL); err != nil {
			errs = append(errs, err)
		}
	}
	if cfg.RedisAddr == "" {
		errs = append(errs, errors.New("config: REDIS_ADDR 不能为空"))
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("config: load: %w", errors.Join(errs...))
	}
	return cfg, nil
}

// validateDatabaseURL 要求给出可解析的 postgres 连接串。
func validateDatabaseURL(raw string) error {
	if raw == "" {
		return errors.New("config: DATABASE_URL 未设置")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("config: DATABASE_URL 无法解析: %w", err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return fmt.Errorf("config: DATABASE_URL 的 scheme 必须是 postgres/postgresql，实际为 %q", u.Scheme)
	}
	return nil
}

// validateJWTSecret 拒绝空值与常见占位值，并要求长度足够。
func validateJWTSecret(secret string) error {
	for _, bad := range insecureSecrets {
		if strings.EqualFold(secret, bad) {
			return errors.New("config: JWT_SECRET 未设置或仍是占位值，请用 `openssl rand -base64 32` 生成后写入 .env")
		}
	}
	if len(secret) < 16 {
		return fmt.Errorf("config: JWT_SECRET 过短（%d 字节），至少需要 16 字节", len(secret))
	}
	return nil
}

// validateBaseURL 要求基址是 http/https 的纯源（可带端口）。
func validateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("config: PUBLIC_BASE_URL 无法解析: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("config: PUBLIC_BASE_URL 必须是 http/https，实际为 %q", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("config: PUBLIC_BASE_URL 缺少主机名：%q", raw)
	}
	return nil
}

// intEnv 读取整型环境变量，未设置时返回 def。
func intEnv(key string, def int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("config: %s 不是合法整数: %w", key, err)
	}
	return v, nil
}

// boolEnv 读取布尔环境变量：未设置时返回 def。
//
// 除 strconv.ParseBool 认的 1/0/t/f/true/false 之外，额外接受 .env 与 compose
// 里更常见的 yes/on/no/off。只认 ParseBool 的话，照本函数注释写
// WORKER_ENABLED=yes 会直接让进程启动失败 —— 注释与实现必须一致。
func boolEnv(key string, def bool) (bool, error) {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if raw == "" {
		return def, nil
	}
	switch raw {
	case "yes", "on":
		return true, nil
	case "no", "off":
		return false, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("config: %s 不是合法布尔值（%q），可用 1/0/true/false/yes/no/on/off", key, raw)
	}
	return v, nil
}

// levelEnv 解析日志级别，未知级别回落 info 而不报错（日志级别不该阻塞启动）。
func levelEnv(key string) slog.Level {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch raw {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
