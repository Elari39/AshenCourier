package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"ashen-courier/internal/domain"
	"uuid"
)

// 本文件是 store 层的集成测试骨架。
//
// 为什么需要它：internal/store/postgres 原先只测了错误归类与 opCtx 超时，而迁移文件
// 本身、以及 links / click_events 上那些手写 SQL（M4-1 的 tags @> ARRAY[...]、
// M4-2 的 (occurred_at, id) < (...) keyset）一直只靠容器级验收 + 人工 EXPLAIN 守着。
//
// 三个硬约束（写成注释是因为它们都属于「删掉就会有人受伤」）：
//   - 未设置 POSTGRES_TEST_DSN 时**整体跳过**而不是失败：本机与不关心它的 PR 不该平白变红
//   - DSN 的库名必须以 _test 结尾：prepareTestDB 会执行 DROP SCHEMA public CASCADE
//   - 用例之间靠数据隔离（各自生成 owner 与短码），因此可以继续 t.Parallel()

const (
	// testDSNEnv 是集成测试的门控环境变量（PLAN-NEXT.md §9.2 已登记，运行时并不读它）。
	testDSNEnv = "POSTGRES_TEST_DSN"
	// testPrepareTimeout 是准备阶段的总预算：重置 schema + 跑完全部迁移。
	testPrepareTimeout = 60 * time.Second
	// testOpTimeout 比生产的 3s 宽松：共享 runner 上偶发慢一次不该让用例变红。
	testOpTimeout = 10 * time.Second
)

var (
	testDBOnce sync.Once
	testDBVal  *DB
	testDBErr  error
)

// TestMain 只负责在退出前关掉共享连接池；没有 DSN 时它什么都不做。
func TestMain(m *testing.M) {
	code := m.Run()
	if testDBVal != nil {
		testDBVal.Close()
	}
	os.Exit(code)
}

// testDB 返回集成测试共享的 DB；未配置 DSN 时跳过当前用例。
func testDB(t *testing.T) *DB {
	t.Helper()

	dsn := strings.TrimSpace(os.Getenv(testDSNEnv))
	if dsn == "" {
		t.Skipf("未设置 %s，跳过 store 集成测试", testDSNEnv)
	}

	testDBOnce.Do(func() { testDBVal, testDBErr = prepareTestDB(dsn) })
	if testDBErr != nil {
		t.Fatalf("准备测试数据库：%v", testDBErr)
	}
	return testDBVal
}

// prepareTestDB 重置 schema、按序执行全部 up 迁移，并返回共享连接池。
//
// 用「读文件 + 手工执行」而不是 golang-migrate 库：迁移文件本身就是要测的对象，
// 让测试直接跑它们才有意义；顺带也省掉了为测试再引一个依赖。
func prepareTestDB(dsn string) (*DB, error) {
	if err := guardTestDatabase(dsn); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), testPrepareTimeout)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("解析 %s: %w", testDSNEnv, err)
	}
	// 上限压到 8：用例是并发跑的，但没必要把 CI 上的 PG 打满
	cfg.MaxConns = 8

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("建立连接池: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("连不上测试数据库（%s 指向的 PG 活着吗？）: %w", testDSNEnv, err)
	}

	// 破坏性重置：每次运行都从「空库 + 全部迁移」这个确定状态开始 ——
	// 于是「迁移能不能在干净库上跑通」每轮都被验证一次。
	if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;"); err != nil {
		pool.Close()
		return nil, fmt.Errorf("重置 schema: %w", err)
	}
	if err := applyMigrations(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return &DB{pool: pool, timeout: testOpTimeout}, nil
}

// guardTestDatabase 拒绝任何库名不以 _test 结尾的 DSN。
//
// 这不是洁癖：prepareTestDB 会 DROP SCHEMA public CASCADE，一次手滑指向开发库就是
// 不可逆的数据丢失。库名后缀是唯一一处能便宜地挡住它的地方。
func guardTestDatabase(dsn string) error {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("解析 %s: %w", testDSNEnv, err)
	}
	if name := cfg.ConnConfig.Database; !strings.HasSuffix(name, "_test") {
		return fmt.Errorf("%s 的库名 %q 必须以 _test 结尾：本测试会执行 DROP SCHEMA public CASCADE", testDSNEnv, name)
	}
	return nil
}

// applyMigrations 按文件名顺序执行 backend/migrations 下的全部 up 文件。
//
// 一个文件里的多条语句一次发出去：pgx 在**没有参数**时走简单查询协议
// （conn.go 的注释就是 "Always use simple protocol when there are no arguments."），
// 带参数才会切到扩展协议并拒绝多语句。
func applyMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	// 包目录是 backend/internal/store/postgres，向上三层才回到 backend/
	paths, err := filepath.Glob(filepath.Join("..", "..", "..", "migrations", "*.up.sql"))
	if err != nil {
		return fmt.Errorf("查找迁移文件: %w", err)
	}
	if len(paths) == 0 {
		return errors.New("没有找到任何迁移文件（期望 backend/migrations/*.up.sql）")
	}
	slices.Sort(paths)

	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("读取迁移 %s: %w", filepath.Base(path), err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			return fmt.Errorf("执行迁移 %s: %w", filepath.Base(path), err)
		}
	}
	return nil
}

// testSuffix 生成 8 位随机十六进制串：让并发跑的用例之间在短码与邮箱上互不冲突。
func testSuffix(t *testing.T) string {
	t.Helper()

	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		t.Fatalf("生成随机后缀: %v", err)
	}
	return hex.EncodeToString(buf[:])
}

// testCode 生成只属于本用例的短码：要同时满足 ^[0-9A-Za-z_-]{3,32}$ 与
// 数据库的 links_code_shape CHECK。
func testCode(t *testing.T, prefix string) string {
	t.Helper()
	return prefix + "-" + testSuffix(t)
}

// createUser 落库一个账号。
//
// links.owner_id 有指向 users 的外键，所以「按 owner 列出我的链接」这类用例
// 不能凭空造一个 UUID 当 owner —— 那会直接撞上 links_owner_id_fkey。
func createUser(t *testing.T, db *DB) *domain.User {
	t.Helper()

	user := &domain.User{
		ID:           uuid.NewV7(),
		Email:        "it-" + testSuffix(t) + "@example.com",
		PasswordHash: "integration-test-placeholder",
		DisplayName:  "集成测试账号",
	}
	if err := db.Users().Create(t.Context(), user); err != nil {
		t.Fatalf("创建测试账号: %v", err)
	}
	return user
}

// createLink 落库一条测试短链；mutate 可以为 nil。
func createLink(t *testing.T, db *DB, code string, mutate func(*domain.Link)) *domain.Link {
	t.Helper()

	link := &domain.Link{
		ID:        uuid.NewV7(),
		ShortCode: code,
		TargetURL: "https://example.com/integration",
		Status:    domain.LinkStatusActive,
	}
	if mutate != nil {
		mutate(link)
	}
	if err := db.Links().Create(t.Context(), link); err != nil {
		t.Fatalf("创建测试短链 %q: %v", code, err)
	}
	return link
}

// countClicks 统计某短链的明细条数。
func countClicks(t *testing.T, db *DB, linkID uuid.UUID) int64 {
	t.Helper()

	var n int64
	if err := db.pool.QueryRow(t.Context(),
		"SELECT count(*) FROM click_events WHERE link_id = $1", toPgUUID(linkID)).Scan(&n); err != nil {
		t.Fatalf("统计明细条数: %v", err)
	}
	return n
}
