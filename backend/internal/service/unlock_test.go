package service

import (
	"errors"
	"testing"
	"time"

	"ashen-courier/internal/domain"
)

// unlockTestSecret 与其它用例的密钥刻意不同：解锁子密钥由它派生，
// 换了 secret 签出来的凭据必须通不过。
const unlockTestSecret = "test-secret-0123456789"

// TestLinkUnlockerRoundTrip 覆盖签发 / 校验的正反两条路。
func TestLinkUnlockerRoundTrip(t *testing.T) {
	t.Parallel()

	unlocker := NewLinkUnlocker(unlockTestSecret, 30*time.Minute)
	now := time.Now()
	token := unlocker.Issue("abc123", now)

	if err := unlocker.Verify("abc123", token, now); err != nil {
		t.Fatalf("刚签发的凭据应当有效：%v", err)
	}
	if err := unlocker.Verify("abc123", token, now.Add(29*time.Minute)); err != nil {
		t.Fatalf("TTL 内应当仍然有效：%v", err)
	}

	tests := []struct {
		name  string
		code  string
		token string
		at    time.Time
	}{
		{"过期", "abc123", token, now.Add(31 * time.Minute)},
		{"换了短码", "other1", token, now},
		{"空凭据", "abc123", "", now},
		{"不是 base64", "abc123", "not-base64!!", now},
		{"解码后没有分隔符", "abc123", "YWJj", now},
		{"过期时间不是数字", "abc123", "eHh4fHh4", now},
		{"签名被截断", "abc123", token[:len(token)-4], now},
		{"另一个 secret 签的", "abc123", NewLinkUnlocker("another-secret-9876543210", 30*time.Minute).Issue("abc123", now), now},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := unlocker.Verify(tt.code, tt.token, tt.at)
			if err == nil {
				t.Fatal("无效凭据必须被拒绝")
			}
			// 全部失败都要归为 ErrUnauthorized：handler 据此重新渲染口令页，
			// 而不是给用户一页错误码
			if !errors.Is(err, domain.ErrUnauthorized) {
				t.Fatalf("失败必须归为 ErrUnauthorized，实际 %v", err)
			}
		})
	}
}

// TestLinkUnlockerSeparatesCodes 单独钉住「A 链的凭据在 B 链上无效」——
// cookie 是所有受保护链接共用的，这条语义一旦丢了，一个浏览器的解锁会横向打通所有链接。
func TestLinkUnlockerSeparatesCodes(t *testing.T) {
	t.Parallel()

	unlocker := NewLinkUnlocker(unlockTestSecret, time.Minute)
	now := time.Now()

	a := unlocker.Issue("codeaaa", now)
	b := unlocker.Issue("codebbb", now)
	if a == b {
		t.Fatal("不同短码的凭据不该相同")
	}
	if err := unlocker.Verify("codebbb", a, now); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("A 的凭据用在 B 上必须被拒，实际 %v", err)
	}
}
