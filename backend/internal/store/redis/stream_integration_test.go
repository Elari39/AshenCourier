package redis

import (
	"context"
	"strconv"
	"testing"
	"time"
	"uuid"

	goredis "github.com/redis/go-redis/v9"

	"ashen-courier/internal/domain"
)

// 本文件是 ReapDeadLetters（毒消息清理）的**真 Redis** 集成测试，
// 门控与 counter_integration_test.go 一致：未设置 REDIS_TEST_ADDR 时整体 SKIP。
//
// 为什么必须真跑：这段逻辑的核心是 XPENDING 的**游标翻页**（nextStreamID）。
// 游标推不动就是死循环，推过头就漏消息 —— 两种错在纯 Go 单测里都看不出来，
// 因为 PEL 是 Redis 内部状态，fake 根本模拟不出 delivery count。
//
// 本测试会用 XCLAIM 反复认领同一条消息来把重投次数抬上去（这正是 AutoClaim
// 在真实世界里做的事），所以断言的是「Redis 自己记的次数」，不是我们伪造的。

// seedPending 往 Stream 里投 count 条消息，并把它们的重投次数抬到 attempts。
//
// 返回消息 ID 列表；调用方负责清理（把它们 ACK 掉）。
func seedPending(t *testing.T, c *Client, count int, attempts int) []string {
	t.Helper()

	if err := c.EnsureGroup(t.Context()); err != nil {
		t.Fatalf("创建消费组失败：%v", err)
	}
	// 先排空 PEL：这几个用例都用 PendingCount 做断言，那是**整个消费组**的计数，
	// 上一次运行（尤其是失败中断的那次）留下的条目会把它带偏。
	// 用 maxRetries=1 —— PEL 里的消息重投次数必然 ≥ 1，于是等于「全部丢弃」。
	for range 20 {
		drained, err := c.ReapDeadLetters(t.Context(), 1, 1000)
		if err != nil {
			t.Fatalf("排空 PEL 失败：%v", err)
		}
		if len(drained) == 0 {
			break
		}
	}

	// 用远过去的时间戳生成 ID，避免与上一次运行留下的条目混在一起
	base := time.Now().Add(-time.Hour).UnixMilli()
	ids := make([]string, 0, count)
	for i := range count {
		id, err := c.rdb.XAdd(t.Context(), &goredis.XAddArgs{
			Stream: streamKey,
			ID:     strconv.FormatInt(base, 10) + "-" + strconv.Itoa(i),
			Values: []any{
				fieldCode, "it-dead-letter",
				fieldLinkID, uuid.New().String(),
				fieldOccurred, "1712345678901000000",
			},
		}).Result()
		if err != nil {
			t.Fatalf("投递第 %d 条消息失败：%v", i, err)
		}
		ids = append(ids, id)
	}

	// 第一次投递：XREADGROUP 把它们放进 PEL（次数 = 1）
	if _, err := c.rdb.XReadGroup(t.Context(), &goredis.XReadGroupArgs{
		Group:    consumerGroup,
		Consumer: "it-seeder",
		Streams:  []string{streamKey, ">"},
		Count:    int64(count),
	}).Result(); err != nil && !isNil(err) {
		t.Fatalf("首次投递失败：%v", err)
	}

	// 剩下的次数靠 XCLAIM 抬：每次认领都会让 delivery count +1
	for range attempts - 1 {
		if _, err := c.rdb.XClaim(t.Context(), &goredis.XClaimArgs{
			Stream:   streamKey,
			Group:    consumerGroup,
			Consumer: "it-seeder",
			MinIdle:  0,
			Messages: ids,
		}).Result(); err != nil {
			t.Fatalf("XCLAIM 抬升重投次数失败：%v", err)
		}
	}

	// ⚠️ 清理里**不能**用 t.Context()：它在 Cleanup 函数运行之前就已被取消，
	// 于是 ACK 静默失败、这几条消息留在 PEL 里，把下一个用例的 PendingCount 断言带偏。
	// （这个坑就是本文件第一版里 TestReapDeadLettersPaginates 变红的原因。）
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := c.Ack(ctx, ids...); err != nil {
			t.Errorf("清理测试消息失败（会污染下一个用例）：%v", err)
		}
	})
	return ids
}

// TestReapDeadLettersReapsOverRetried 是主路径：重投次数达到上限的消息被丢弃，
// 并且被丢弃之后真的离开了 PEL（PendingCount 降下来）。
func TestReapDeadLettersReapsOverRetried(t *testing.T) {
	// 与 counter 的用例共用同一个 Stream，不能并行
	c := openTestClient(t)

	const n = 5
	ids := seedPending(t, c, n, 3)

	dead, err := c.ReapDeadLetters(t.Context(), 3, 100)
	if err != nil {
		t.Fatalf("ReapDeadLetters 失败：%v", err)
	}
	if len(dead) != n {
		t.Fatalf("丢弃了 %d 条，期望 %d 条", len(dead), n)
	}

	// 被丢弃的消息必须真的不在 PEL 里了
	pending, err := c.PendingCount(t.Context())
	if err != nil {
		t.Fatalf("读取 pending 数失败：%v", err)
	}
	if pending != 0 {
		t.Errorf("清理后 PEL 仍有 %d 条（期望 0）", pending)
	}
	_ = ids
}

// TestReapDeadLettersRespectsMaxRetries 守住「不误杀」：
// 重投次数没到上限的消息必须留在 PEL 里 —— 一次长时间的 PG 故障不该把正常消息丢掉。
func TestReapDeadLettersRespectsMaxRetries(t *testing.T) {
	c := openTestClient(t)

	seedPending(t, c, 3, 2) // 只投了 2 次

	dead, err := c.ReapDeadLetters(t.Context(), 5, 100)
	if err != nil {
		t.Fatalf("ReapDeadLetters 失败：%v", err)
	}
	if len(dead) != 0 {
		t.Fatalf("重投次数未达上限却被丢弃了 %v", dead)
	}

	pending, err := c.PendingCount(t.Context())
	if err != nil {
		t.Fatalf("读取 pending 数失败：%v", err)
	}
	if pending != 3 {
		t.Errorf("PEL = %d，期望仍是 3（未达上限的消息必须留着）", pending)
	}
}

// TestReapDeadLettersPaginates 守住游标翻页：单轮上限小于毒消息总数时，
// 每轮只丢上限那么多，**且每一轮都要有进展**（推不动游标会死循环，这个用例会直接超时）。
func TestReapDeadLettersPaginates(t *testing.T) {
	c := openTestClient(t)

	const n = 5
	seedPending(t, c, n, 4)

	first, err := c.ReapDeadLetters(t.Context(), 4, 2)
	if err != nil {
		t.Fatalf("第一轮清理失败：%v", err)
	}
	if len(first) != 2 {
		t.Fatalf("第一轮丢弃 %d 条，期望 2（单轮上限）", len(first))
	}

	second, err := c.ReapDeadLetters(t.Context(), 4, 10)
	if err != nil {
		t.Fatalf("第二轮清理失败：%v", err)
	}
	if len(second) != n-2 {
		t.Fatalf("第二轮丢弃 %d 条，期望 %d", len(second), n-2)
	}

	pending, err := c.PendingCount(t.Context())
	if err != nil {
		t.Fatalf("读取 pending 数失败：%v", err)
	}
	if pending != 0 {
		t.Errorf("两轮之后 PEL 仍有 %d 条（期望 0）", pending)
	}
}

// TestNextStreamID 是纯函数用例，钉住游标推进的形状。
// 注意「-」是闭区间：推进必须落在 last 之后，否则同一批条目会被无限返回。
func TestNextStreamID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		in     string
		want   string
		wantOK bool
	}{
		{name: "普通", in: "1712345678901-0", want: "1712345678901-1", wantOK: true},
		{name: "序号进位", in: "1712345678901-9", want: "1712345678901-10", wantOK: true},
		{name: "缺序号", in: "1712345678901", wantOK: false},
		{name: "序号非数字", in: "1712345678901-abc", wantOK: false},
		{name: "空串", in: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := nextStreamID(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v，期望 %v（got=%q）", ok, tt.wantOK, got)
			}
			if ok && got != tt.want {
				t.Errorf("nextStreamID(%q) = %q，期望 %q", tt.in, got, tt.want)
			}
		})
	}
}

// 编译期确认本包仍满足 domain 的端口（新增了 ReapDeadLetters 之后）。
var _ domain.ClickStream = (*Client)(nil)
