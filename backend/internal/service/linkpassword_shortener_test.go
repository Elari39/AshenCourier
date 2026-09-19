package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"ashen-courier/internal/domain"
)

// TestCreateWithPassword 守住「明文不落库、摘要进实体」。
func TestCreateWithPassword(t *testing.T) {
	t.Parallel()

	repo := newLinkRepoFake()
	s := newShortenerForTest(repo, newMemCache())

	const plain = "smoke-pass-9f3a"
	got, err := s.Create(t.Context(), CreateInput{
		TargetURL: "https://example.com/locked",
		Password:  plain,
	})
	if err != nil {
		t.Fatalf("创建: %v", err)
	}
	if !got.Link.HasPassword() {
		t.Fatal("带口令创建的链接应当 HasPassword")
	}
	if got.Link.PasswordHash == plain {
		t.Fatal("实体里不能留明文")
	}
	if !CheckLinkPassword(got.Link.PasswordHash, plain) {
		t.Error("落库的摘要应当能用原口令校验通过")
	}

	stored := repo.links[got.Link.ShortCode]
	if stored == nil {
		t.Fatal("链接没有被落库")
	}
	if strings.Contains(stored.PasswordHash, plain) {
		t.Errorf("仓储里存的东西含有明文：%q", stored.PasswordHash)
	}
}

// TestCreateRejectsWeakPassword 守住强度校验发生在写库之前。
func TestCreateRejectsWeakPassword(t *testing.T) {
	t.Parallel()

	repo := newLinkRepoFake()
	s := newShortenerForTest(repo, newMemCache())

	if _, err := s.Create(t.Context(), CreateInput{
		TargetURL: "https://example.com/locked",
		Password:  "short",
	}); err == nil {
		t.Fatal("过短的口令必须被拒绝")
	}
	if len(repo.links) != 0 {
		t.Fatalf("校验失败时不该落库，实际写了 %d 条", len(repo.links))
	}
}

// TestUpdateLinkPassword 覆盖设置与清除两条路。
func TestUpdateLinkPassword(t *testing.T) {
	t.Parallel()

	repo := newLinkRepoFake()
	s := newShortenerForTest(repo, newMemCache())

	const code = "pw12345"
	repo.links[code] = &domain.Link{ShortCode: code, TargetURL: "https://example.com", Status: domain.LinkStatusActive}

	const plain = "new-pass-1234"
	updated, err := s.Update(t.Context(), code, domain.LinkPatch{PasswordHash: new(plain)})
	if err != nil {
		t.Fatalf("设置口令: %v", err)
	}
	if !updated.HasPassword() {
		t.Fatal("设置后应当 HasPassword")
	}
	if updated.PasswordHash == plain {
		t.Fatal("service 层必须把明文换成摘要后才交给仓储")
	}
	if !CheckLinkPassword(updated.PasswordHash, plain) {
		t.Fatalf("设置后应当能用该口令解锁：%+v", updated)
	}

	cleared, err := s.Update(t.Context(), code, domain.LinkPatch{PasswordHash: new("")})
	if err != nil {
		t.Fatalf("清除口令: %v", err)
	}
	if cleared.HasPassword() || cleared.PasswordHash != "" {
		t.Fatalf("清除后不该还有口令：%+v", cleared)
	}
}

// TestVerifyPassword 覆盖 POST /{code} 的五条分支。
func TestVerifyPassword(t *testing.T) {
	t.Parallel()

	const (
		code  = "pwverify"
		plain = "smoke-pass-9f3a"
	)

	hash := testLinkHash(t)

	repoFor := func(link *domain.Link) *linkRepoFake {
		repo := newLinkRepoFake()
		repo.links[code] = link
		return repo
	}
	locked := func() *domain.Link {
		return &domain.Link{
			ShortCode:         code,
			TargetURL:         "https://example.com",
			Status:            domain.LinkStatusActive,
			PasswordHash:      hash,
			PasswordProtected: true,
		}
	}

	t.Run("没设口令：直接放行", func(t *testing.T) {
		s := newShortenerForTest(repoFor(&domain.Link{
			ShortCode: code, TargetURL: "https://example.com", Status: domain.LinkStatusActive,
		}), newMemCache())
		if err := s.VerifyPassword(t.Context(), code, ""); err != nil {
			t.Fatalf("没设口令的链接无需解锁，实际 %v", err)
		}
	})

	t.Run("口令正确", func(t *testing.T) {
		s := newShortenerForTest(repoFor(locked()), newMemCache())
		if err := s.VerifyPassword(t.Context(), code, plain); err != nil {
			t.Fatalf("正确口令应当通过，实际 %v", err)
		}
	})

	t.Run("口令错误：ErrUnauthorized", func(t *testing.T) {
		s := newShortenerForTest(repoFor(locked()), newMemCache())
		if err := s.VerifyPassword(t.Context(), code, "wrong-guess"); !errors.Is(err, domain.ErrUnauthorized) {
			t.Fatalf("错误口令应当报 ErrUnauthorized，实际 %v", err)
		}
	})

	t.Run("已删除：ErrNotFound", func(t *testing.T) {
		deleted := locked()
		deleted.Status = domain.LinkStatusDeleted
		s := newShortenerForTest(repoFor(deleted), newMemCache())
		if err := s.VerifyPassword(t.Context(), code, plain); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("已删除的链接应当报 ErrNotFound（不泄露存在性），实际 %v", err)
		}
	})

	t.Run("已过期：ErrGone", func(t *testing.T) {
		expired := locked()
		past := time.Now().Add(-time.Hour)
		expired.ExpiresAt = &past
		s := newShortenerForTest(repoFor(expired), newMemCache())
		if err := s.VerifyPassword(t.Context(), code, plain); !errors.Is(err, domain.ErrGone) {
			t.Fatalf("已过期的链接应当报 ErrGone，实际 %v", err)
		}
	})
}
