package redis

import (
	"os"
	"testing"
	"time"
)

// 本文件是 counter.go 里那段 Lua 结算脚本的**真 Redis** 集成测试。
//
// 为什么必须真跑：settleDeltaScript 的语义（减到 0 才删键、有残留就不摘 dirty）
// 全部在 Redis 里执行，纯 Go 单测只能断言脚本源码的字符串形状 —— 那种断言
// 「改坏了也未必红」。键与集合的真实状态才是要守的东西。
//
// 门控沿用仓库既有约定（对照 POSTGRES_TEST_DSN / GEOIP_TEST_DB）：
// 未设置 REDIS_TEST_ADDR 时整体 SKIP，不设置它的人不该平白变红。
// 口令用 REDIS_TEST_PASSWORD（compose 的生产形态强制 requirepass，dev 形态没有）。
//
// 本机（compose 的 dev 形态映射了 6379）：
//
//	REDIS_TEST_ADDR=localhost:6379 go test -run TestSettleDelta -v ./internal/store/redis/
//
// CI 由 backend job 的 redis service 提供（见 .github/workflows/ci.yml）。
const (
	redisTestAddrEnv     = "REDIS_TEST_ADDR"
	redisTestPasswordEnv = "REDIS_TEST_PASSWORD"
)

// openTestClient 建一个连真 Redis 的客户端；未配置时返回 nil，调用方 SKIP。
func openTestClient(t *testing.T) *Client {
	t.Helper()

	addr := os.Getenv(redisTestAddrEnv)
	if addr == "" {
		t.Skipf("未设置 %s，跳过真 Redis 集成测试", redisTestAddrEnv)
	}

	c, err := Open(t.Context(), Options{
		Addr:     addr,
		Password: os.Getenv(redisTestPasswordEnv),
		Timeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("连接测试 Redis %s 失败：%v", addr, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// isDirty 直接查集合，而不是走 DirtyCodes —— 后者是 SRANDMEMBER 取样，
// 用来断言「在或不在」会偶发性地假绿。
func isDirty(t *testing.T, c *Client, code string) bool {
	t.Helper()

	ok, err := c.rdb.SIsMember(t.Context(), dirtySetKey, code).Result()
	if err != nil {
		t.Fatalf("查询 dirty 集合失败：%v", err)
	}
	return ok
}

// TestSettleDeltaClearsCounterAndDirty 是正常路径：这批增量结算完应当
// 「键已删除 + dirty 已摘掉」，与 README 里「回刷后 clicks:cnt:* 清空」一致。
func TestSettleDeltaClearsCounterAndDirty(t *testing.T) {
	t.Parallel()

	c := openTestClient(t)
	code := "it-settle-clean"

	// 布置：5 次点击
	if err := c.rdb.IncrBy(t.Context(), ClickCounterKey(code), 5).Err(); err != nil {
		t.Fatalf("布置计数键失败：%v", err)
	}
	if err := c.MarkDirty(t.Context(), code); err != nil {
		t.Fatalf("布置 dirty 失败：%v", err)
	}
	t.Cleanup(func() {
		_ = c.rdb.Del(t.Context(), ClickCounterKey(code)).Err()
		_ = c.rdb.SRem(t.Context(), dirtySetKey, code).Err()
	})

	delta, err := c.TakeDelta(t.Context(), code)
	if err != nil {
		t.Fatalf("TakeDelta 失败：%v", err)
	}
	if delta != 5 {
		t.Fatalf("TakeDelta = %d，期望 5", delta)
	}

	if err := c.SettleDelta(t.Context(), code, delta); err != nil {
		t.Fatalf("SettleDelta 失败：%v", err)
	}

	if n, _ := c.rdb.Exists(t.Context(), ClickCounterKey(code)).Result(); n != 0 {
		t.Errorf("结算后计数键仍存在（应当被删掉，否则会留下无 TTL 的 0 值键）")
	}
	if isDirty(t, c, code) {
		t.Errorf("结算后 dirty 仍含该短码（下一轮会白扫一次）")
	}
}

// TestSettleDeltaKeepsDirtyWhenResidualRemains 钉住本次修掉的缺陷。
//
// 场景：TakeDelta 读到 5 之后、SettleDelta 执行之前，又来了 2 次点击
// （INCR 到 7 且重新 SADD dirty）。脚本只知道 5，减完还剩 2。
//
// 修之前：脚本无条件 SREM，于是这 2 条既不在 dirty 里、又留在键里没人扫 ——
// 基线落后 2 条，键永不删除，只有等下一次点击重新 SADD 才能自愈。
// 修之后：有残留就不摘 dirty，下一轮 TakeDelta 读到 2 并继续回刷。
func TestSettleDeltaKeepsDirtyWhenResidualRemains(t *testing.T) {
	t.Parallel()

	c := openTestClient(t)
	code := "it-settle-residual"

	if err := c.rdb.IncrBy(t.Context(), ClickCounterKey(code), 5).Err(); err != nil {
		t.Fatalf("布置计数键失败：%v", err)
	}
	if err := c.MarkDirty(t.Context(), code); err != nil {
		t.Fatalf("布置 dirty 失败：%v", err)
	}
	t.Cleanup(func() {
		_ = c.rdb.Del(t.Context(), ClickCounterKey(code)).Err()
		_ = c.rdb.SRem(t.Context(), dirtySetKey, code).Err()
	})

	delta, err := c.TakeDelta(t.Context(), code)
	if err != nil {
		t.Fatalf("TakeDelta 失败：%v", err)
	}

	// 模拟窗口内到达的新点击（跳转路径做的是同一件事：INCR + SADD）
	if err := c.rdb.IncrBy(t.Context(), ClickCounterKey(code), 2).Err(); err != nil {
		t.Fatalf("模拟新点击失败：%v", err)
	}
	if err := c.MarkDirty(t.Context(), code); err != nil {
		t.Fatalf("模拟新点击登记 dirty 失败：%v", err)
	}

	if err := c.SettleDelta(t.Context(), code, delta); err != nil {
		t.Fatalf("SettleDelta 失败：%v", err)
	}

	// 1) 残留必须还在键里（没有被连带删掉）
	left, err := c.PendingDelta(t.Context(), code)
	if err != nil {
		t.Fatalf("读取残留失败：%v", err)
	}
	if left != 2 {
		t.Errorf("残留 = %d，期望 2（新点击不能被这批结算吃掉）", left)
	}

	// 2) dirty 必须还在 —— 这一条正是缺陷本身
	if !isDirty(t, c, code) {
		t.Errorf("有残留时 dirty 不该被摘掉：残留会永远等不到下一次回刷")
	}

	// 3) 下一轮必须能把残留继续回刷干净
	next, err := c.TakeDelta(t.Context(), code)
	if err != nil {
		t.Fatalf("第二轮 TakeDelta 失败：%v", err)
	}
	if err := c.SettleDelta(t.Context(), code, next); err != nil {
		t.Fatalf("第二轮 SettleDelta 失败：%v", err)
	}
	if n, _ := c.rdb.Exists(t.Context(), ClickCounterKey(code)).Result(); n != 0 {
		t.Errorf("第二轮结算后计数键仍存在")
	}
	if isDirty(t, c, code) {
		t.Errorf("第二轮结算后 dirty 仍含该短码")
	}
}

// TestSettleDeltaZeroClearsDirty 守住 delta=0 这一支：键里没有增量，
// 但仍然要把 dirty 摘掉 —— 否则该短码会每轮被白扫一次，永远出不了集合。
func TestSettleDeltaZeroClearsDirty(t *testing.T) {
	t.Parallel()

	c := openTestClient(t)
	code := "it-settle-zero"

	if err := c.MarkDirty(t.Context(), code); err != nil {
		t.Fatalf("布置 dirty 失败：%v", err)
	}
	t.Cleanup(func() {
		_ = c.rdb.Del(t.Context(), ClickCounterKey(code)).Err()
		_ = c.rdb.SRem(t.Context(), dirtySetKey, code).Err()
	})

	if err := c.SettleDelta(t.Context(), code, 0); err != nil {
		t.Fatalf("SettleDelta(0) 失败：%v", err)
	}
	if isDirty(t, c, code) {
		t.Errorf("delta=0 时 dirty 应当被摘掉")
	}

	// 负 delta 会被夹到 0，行为一致（防御上层算错时把键减成负数）
	if err := c.MarkDirty(t.Context(), code); err != nil {
		t.Fatalf("再次布置 dirty 失败：%v", err)
	}
	if err := c.SettleDelta(t.Context(), code, -3); err != nil {
		t.Fatalf("SettleDelta(-3) 失败：%v", err)
	}
	if isDirty(t, c, code) {
		t.Errorf("负 delta 时 dirty 同样应当被摘掉")
	}
}
