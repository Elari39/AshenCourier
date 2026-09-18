package httpx

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// errShutdownTimeout 是优雅关闭超时的取消原因，用于区分
// 「上游把进程关掉了」与「关闭本身超时」。
var errShutdownTimeout = errors.New("graceful shutdown timeout")

// ServerConfig 是 HTTP 服务的启动参数。
type ServerConfig struct {
	// Addr 是监听地址。
	Addr string
	// Handler 是已装配好中间件的处理器。
	Handler http.Handler
	// Logger 是结构化日志器。
	Logger *slog.Logger
	// ShutdownTimeout 是优雅关闭的最长等待时间。
	ShutdownTimeout time.Duration

	// ReadHeaderTimeout 防 Slowloris；ReadTimeout / WriteTimeout 兜底。
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	// IdleTimeout 是 keep-alive 空闲上限。
	IdleTimeout time.Duration
}

// Server 包装 http.Server，提供带超时的优雅关闭。
type Server struct {
	http            *http.Server
	logger          *slog.Logger
	shutdownTimeout time.Duration
}

// NewServer 构造 HTTP 服务。
func NewServer(cfg ServerConfig) *Server {
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           cfg.Handler,
		ReadHeaderTimeout: cmp.Or(cfg.ReadHeaderTimeout, 10*time.Second),
		ReadTimeout:       cmp.Or(cfg.ReadTimeout, 30*time.Second),
		WriteTimeout:      cmp.Or(cfg.WriteTimeout, 30*time.Second),
		IdleTimeout:       cmp.Or(cfg.IdleTimeout, 90*time.Second),
		ErrorLog:          slog.NewLogLogger(cfg.Logger.Handler(), slog.LevelWarn),
	}
	return &Server{
		http:            srv,
		logger:          cfg.Logger,
		shutdownTimeout: cmp.Or(cfg.ShutdownTimeout, 15*time.Second),
	}
}

// Run 启动服务并阻塞，直到 ctx 取消后完成优雅关闭。
//
// ctx 取消时通过 context.AfterFunc 触发关闭：
// 关闭用的 context 从 ctx 派生但去掉取消（context.WithoutCancel），
// 否则「ctx 已取消」会让 Shutdown 立刻失败。
func (s *Server) Run(ctx context.Context) error {
	serveErr := make(chan error, 1)
	go func() {
		s.logger.Info("HTTP 服务已启动", "addr", s.http.Addr)
		if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- fmt.Errorf("httpx: listen and serve: %w", err)
			return
		}
		serveErr <- nil
	}()

	stop := context.AfterFunc(ctx, func() {
		s.logger.Info("收到退出信号，开始优雅关闭", "timeout", s.shutdownTimeout)

		shutdownCtx, cancel := context.WithTimeoutCause(
			context.WithoutCancel(ctx), s.shutdownTimeout, errShutdownTimeout)
		defer cancel()

		if err := s.http.Shutdown(shutdownCtx); err != nil {
			// 超时：强制断开剩余连接，避免进程卡死
			s.logger.Error("优雅关闭超时，强制关闭连接", "err", err, "cause", context.Cause(shutdownCtx))
			_ = s.http.Close()
		}
	})
	defer stop()

	return <-serveErr
}
