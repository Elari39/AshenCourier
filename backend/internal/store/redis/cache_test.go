package redis

import (
	"testing"

	"ashen-courier/internal/domain"
)

// TestCachedLinkWirePasswordProtected 守住缓存线格式的向后兼容。
//
// 加了 password_protected 之后，**发版前写进 Redis 的老条目**必须照常解出来
// （上线顺序是「先迁移、后发版」，发版那一刻库里也确实没有带口令的行），
// 而不是解码失败让整条缓存变成 miss。
func TestCachedLinkWirePasswordProtected(t *testing.T) {
	t.Parallel()

	const id = "01890000-0000-7000-8000-000000000001"

	t.Run("往返：带口令的条目仍然带口令", func(t *testing.T) {
		t.Parallel()

		raw, err := marshal(cachedLinkWire{
			ID:                id,
			ShortCode:         "abc123",
			TargetURL:         "https://example.com",
			Status:            int16(domain.LinkStatusActive),
			PasswordProtected: true,
		})
		if err != nil {
			t.Fatalf("编码: %v", err)
		}

		var wire cachedLinkWire
		if err := unmarshal(raw, &wire); err != nil {
			t.Fatalf("解码: %v", err)
		}
		entry, err := wire.toDomain()
		if err != nil {
			t.Fatalf("转领域: %v", err)
		}
		if !entry.PasswordProtected {
			t.Error("带口令的条目解出来必须仍带口令，否则会被直接放行")
		}
	})

	t.Run("老格式：没有该字段 ⇒ 无口令", func(t *testing.T) {
		t.Parallel()

		const legacy = `{"id":"01890000-0000-7000-8000-000000000001","code":"abc123","target":"https://example.com","status":1}`

		var wire cachedLinkWire
		if err := unmarshal(legacy, &wire); err != nil {
			t.Fatalf("加字段是兼容变更，老条目必须能解码：%v", err)
		}
		entry, err := wire.toDomain()
		if err != nil {
			t.Fatalf("转领域: %v", err)
		}
		if entry.PasswordProtected {
			t.Error("老条目应当解出「无口令」—— 它与发版前的事实一致")
		}
	})
}
