package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"uuid"

	"golang.org/x/crypto/bcrypt"

	"ashen-courier/internal/domain"
)

// stubUserRepo 是内存版账号仓储：够用，且不把 store 拉进 service 的单测。
type stubUserRepo struct {
	byEmail map[string]*domain.User
	// queryErr 非 nil 时所有查询都失败，用来验证「依赖故障不会被吞成凭据错误」。
	queryErr error
	// queries 记录实际被查过的邮箱，用来断言「字段校验发生在任何查库动作之前」。
	queries []string
}

func newStubUserRepo(users ...*domain.User) *stubUserRepo {
	r := &stubUserRepo{byEmail: make(map[string]*domain.User, len(users))}
	for _, u := range users {
		r.byEmail[domain.NormalizeEmail(u.Email)] = u
	}
	return r
}

func (r *stubUserRepo) Create(_ context.Context, u *domain.User) error {
	key := domain.NormalizeEmail(u.Email)
	if _, ok := r.byEmail[key]; ok {
		return domain.Conflict("email", u.Email)
	}
	r.byEmail[key] = u
	return nil
}

func (r *stubUserRepo) GetByEmail(_ context.Context, email string) (*domain.User, error) {
	r.queries = append(r.queries, email)
	if r.queryErr != nil {
		return nil, r.queryErr
	}
	if u, ok := r.byEmail[email]; ok {
		return u, nil
	}
	return nil, domain.NotFound("user", email)
}

func (r *stubUserRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.User, error) {
	if r.queryErr != nil {
		return nil, r.queryErr
	}
	// 比字符串而不是比 UUID 值：不假设标准库 uuid.UUID 一定是可比较的数组类型
	for _, u := range r.byEmail {
		if u.ID.String() == id.String() {
			return u, nil
		}
	}
	return nil, domain.NotFound("user", id.String())
}

// newTestUser 造一个账号。用 MinCost 而不是生产的 BcryptCost：
// 单测关心的不是「哈希够不够慢」，12 会让每个用例白等 250ms。
func newTestUser(t *testing.T, email, password string) *domain.User {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("造摘要失败：%v", err)
	}
	return &domain.User{
		ID:           uuid.NewV7(),
		Email:        email,
		PasswordHash: string(hash),
		DisplayName:  "测试用户",
	}
}

func newTestAuth(users domain.UserRepository) *Auth {
	return NewAuth(users, "test-secret-0123456789", time.Hour)
}

// TestLoginRejectsMalformedFields 守住字段级校验：登录与注册要用**同一套口径**，
// 否则同一个「邮箱格式不对」在注册页是 422、在登录页是「凭据不正确」，
// 用户会以为是自己记错了密码。
//
// 另一条同样重要：这些判定必须在任何查库动作之前完成。
// 若先查库再判格式，「邮箱是否存在」就会以耗时的形式泄漏出去（枚举侧信道）。
func TestLoginRejectsMalformedFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		email    string
		password string
		wantCol  string
	}{
		{name: "邮箱无 @", email: "bad", password: "whatever123", wantCol: "email"},
		{name: "邮箱为空", email: "", password: "whatever123", wantCol: "email"},
		{name: "邮箱全是空白", email: "   ", password: "whatever123", wantCol: "email"},
		{name: "邮箱多个 @", email: "a@b@c.com", password: "whatever123", wantCol: "email"},
		{name: "邮箱无点号后缀", email: "a@localhost", password: "whatever123", wantCol: "email"},
		{name: "口令为空", email: "real@example.com", password: "", wantCol: "password"},
		{name: "口令全是空白", email: "real@example.com", password: "   ", wantCol: "password"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo := newStubUserRepo(newTestUser(t, "real@example.com", "correct-horse"))
			a := newTestAuth(repo)

			_, err := a.Login(t.Context(), tt.email, tt.password)
			if err == nil {
				t.Fatalf("期望字段级错误，实际成功了")
			}

			invalid, ok := errors.AsType[*domain.InvalidInputError](err)
			if !ok {
				t.Fatalf("错误类型 = %T，期望 *domain.InvalidInputError（要被映射成 422）：%v", err, err)
			}
			if invalid.Field != tt.wantCol {
				t.Errorf("field = %q，期望 %q（前端靠它把提示挂到对应的输入框）", invalid.Field, tt.wantCol)
			}
			if len(repo.queries) != 0 {
				t.Errorf("字段校验不该查库，但查了 %v —— 那会把「账号是否存在」变成耗时侧信道", repo.queries)
			}
		})
	}
}

// TestLoginCredentialFailuresShareOneError 守住防枚举：
// 「邮箱不存在」与「口令不对」必须归为同一个错误、同一句话。
//
// 同时钉住它**不是** domain.ErrUnauthorized：后者会被映射成
// 「登录状态无效，请重新登录」（送回登录页），而登录失败的下一步动作是
// 改输入 —— 前端只能靠错误码区分这两件事。
func TestLoginCredentialFailuresShareOneError(t *testing.T) {
	t.Parallel()

	repo := newStubUserRepo(newTestUser(t, "real@example.com", "correct-horse"))
	a := newTestAuth(repo)

	cases := []struct {
		name     string
		email    string
		password string
	}{
		{name: "邮箱不存在", email: "nobody@example.com", password: "correct-horse"},
		{name: "口令不对", email: "real@example.com", password: "wrong-horse"},
		{name: "邮箱大小写不同但仍存在、口令不对", email: "REAL@Example.COM", password: "wrong-horse"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := a.Login(t.Context(), tt.email, tt.password)
			if err == nil {
				t.Fatal("期望登录失败，实际成功了")
			}
			if !errors.Is(err, domain.ErrInvalidCredentials) {
				t.Fatalf("错误没归到 ErrInvalidCredentials：%v", err)
			}
			if errors.Is(err, domain.ErrUnauthorized) {
				t.Fatal("登录凭据错误不该同时命中 ErrUnauthorized —— 那会让前端把它当成会话过期去清空本地会话")
			}
			if _, ok := errors.AsType[*domain.InvalidInputError](err); ok {
				t.Fatalf("凭据错误不该是字段级错误（否则等于告诉攻击者邮箱是合法的）：%v", err)
			}
		})
	}
}

// TestLoginSucceedsWithNormalizedEmail 守住「查询用的是归一化后的邮箱」：
// DB 里的唯一索引建在 lower(email) 上，登录若拿原文去查，
// 用户把首字母大写就会登不进来（而注册是允许的）。
func TestLoginSucceedsWithNormalizedEmail(t *testing.T) {
	t.Parallel()

	repo := newStubUserRepo(newTestUser(t, "Real@Example.com", "correct-horse"))
	a := newTestAuth(repo)

	session, err := a.Login(t.Context(), "  REAL@example.COM  ", "correct-horse")
	if err != nil {
		t.Fatalf("大小写/空白差异不该导致登录失败：%v", err)
	}
	if session.Token == "" {
		t.Fatal("响应缺少 token")
	}
	if session.User == nil || session.User.Email != "Real@Example.com" {
		t.Fatalf("返回的用户不对：%+v", session.User)
	}

	want := domain.NormalizeEmail("  REAL@example.COM  ")
	if len(repo.queries) != 1 || repo.queries[0] != want {
		t.Errorf("查库用的是 %v，期望 %q（归一化后）", repo.queries, want)
	}
	if session.ExpiresAt.Before(time.Now()) {
		t.Errorf("令牌过期时间在过去：%v", session.ExpiresAt)
	}
}

// TestLoginPropagatesDependencyFailures 守住「依赖故障不能伪装成凭据错误」：
// PG 抖一下就让用户看到「邮箱或密码不正确」，会把人引到错误的方向去找问题。
func TestLoginPropagatesDependencyFailures(t *testing.T) {
	t.Parallel()

	cause := fmt.Errorf("stub: dial tcp: %w", domain.ErrUnavailable)
	repo := newStubUserRepo()
	repo.queryErr = cause
	a := newTestAuth(repo)

	_, err := a.Login(t.Context(), "real@example.com", "correct-horse")
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("依赖故障应原样上抛（503），实际：%v", err)
	}
	if errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatal("依赖故障被吞成了凭据错误")
	}
}

// TestLoginValidationIsNotRegisterPolicy 守住「登录不校验口令强度」。
//
// 这不是偷懒：强度策略是注册侧的事。若登录也校验，将来一旦上调
// MinPasswordLength，老用户会在登录页被自己的合法密码挡住 —— 他们连
// 「改密码」都进不去，因为改密码也要先登录。但「口令为空」仍要拒，
// 因为它不可能是任何账号的密码（注册侧最短 8 位）。
func TestLoginValidationIsNotRegisterPolicy(t *testing.T) {
	t.Parallel()

	short := "abc" // 短于 MinPasswordLength
	repo := newStubUserRepo(newTestUser(t, "legacy@example.com", short))
	a := newTestAuth(repo)

	// 先确认这条口令在注册侧确实是非法的
	if !errors.Is(ValidatePassword(short), domain.ErrInvalidInput) {
		t.Fatalf("用例前提不成立：%q 在注册侧应当被拒", short)
	}

	if _, err := a.Login(t.Context(), "legacy@example.com", short); err != nil {
		t.Fatalf("登录不该套用注册侧的长度策略：%v", err)
	}

	// 同理，超过 bcrypt 上限的口令在注册侧被拒，但登录侧不做长度拦截：
	// 没有账号可能拥有它，最终必然是「凭据不对」而不是 422。
	long := strings.Repeat("x", MaxPasswordLength+1)
	if _, err := a.Login(t.Context(), "legacy@example.com", long); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("超长口令应当是凭据错误而不是字段级错误，实际：%v", err)
	}
}
