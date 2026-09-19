package service

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"ashen-courier/internal/domain"
)

// testLinkHash 只算一次 bcrypt 摘要。
//
// cost 12 在 -race 下要几秒一次：本包的多个用例都需要一个「已设口令」的摘要，
// 每个都重算一遍会让这个包的单测时间被 bcrypt 支配。
var (
	testLinkHashOnce sync.Once
	testLinkHashVal  string
	testLinkHashErr  error
)

func testLinkHash(t *testing.T) string {
	t.Helper()

	testLinkHashOnce.Do(func() { testLinkHashVal, testLinkHashErr = HashLinkPassword("smoke-pass-9f3a") })
	if testLinkHashErr != nil {
		t.Fatalf("准备摘要: %v", testLinkHashErr)
	}
	return testLinkHashVal
}

// TestHashAndCheckLinkPassword 守住「明文绝不落库」与 bcrypt 的属性。
func TestHashAndCheckLinkPassword(t *testing.T) {
	t.Parallel()

	hash := testLinkHash(t)
	plain := "smoke-pass-9f3a"
	if hash == plain {
		t.Fatal("摘要不能等于明文")
	}
	if !strings.HasPrefix(hash, "$2") {
		t.Errorf("应当是 bcrypt 摘要（$2a$ / $2b$ 前缀），实际 %q", hash)
	}
	if cost, costErr := bcrypt.Cost([]byte(hash)); costErr != nil || cost != BcryptCost {
		t.Errorf("cost = %d（err=%v），期望 %d", cost, costErr, BcryptCost)
	}

	if !CheckLinkPassword(hash, plain) {
		t.Error("正确口令应当通过")
	}
	if CheckLinkPassword(hash, "wrong") {
		t.Error("错误口令不该通过")
	}
	if CheckLinkPassword("", plain) {
		t.Error("空摘要（没设口令）永远不该通过 —— 否则等于把「有口令」判成「无口令」")
	}
}

// TestNormalizeLinkPassword 钉住「空串 = 不设口令」与「强度只走一条规则」。
func TestNormalizeLinkPassword(t *testing.T) {
	t.Parallel()

	t.Run("空串 = 不设口令", func(t *testing.T) {
		got, err := normalizeLinkPassword("")
		if err != nil {
			t.Fatalf("空串不该报错：%v", err)
		}
		if got != "" {
			t.Errorf("空串应当原样返回（不设口令），实际 %q", got)
		}
	})

	t.Run("过短 / 过长都按字段 password 拒绝", func(t *testing.T) {
		for _, plain := range []string{
			strings.Repeat("a", MinPasswordLength-1),
			strings.Repeat("a", MaxPasswordLength+1),
		} {
			_, err := normalizeLinkPassword(plain)
			if err == nil {
				t.Fatalf("长度 %d 的口令应当被拒绝（bcrypt 超过 72 字节会静默截断）", len(plain))
			}
			invalid, ok := errors.AsType[*domain.InvalidInputError](err)
			if !ok {
				t.Fatalf("应当是 *domain.InvalidInputError，实际 %v", err)
			}
			if invalid.Field != "password" {
				t.Errorf("字段 = %q，期望 password", invalid.Field)
			}
		}
	})

	t.Run("合法口令只留摘要", func(t *testing.T) {
		const plain = "smoke-pass-9f3a"
		got, err := normalizeLinkPassword(plain)
		if err != nil {
			t.Fatalf("合法口令不该报错：%v", err)
		}
		if got == plain || !strings.HasPrefix(got, "$2") {
			t.Errorf("落库的必须是 bcrypt 摘要，实际 %q", got)
		}
	})
}
