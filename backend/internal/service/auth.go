// Package service 是应用服务层：编排领域对象与仓储，承载业务规则。
//
// 依赖方向：service → domain（接口）+ 少量标准库/鉴权库，不依赖任何 store 实现。
// 这样 store 可以被换成假实现在单测里替换。
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"ashen-courier/internal/domain"
)

// 鉴权相关常量。
const (
	// BcryptCost 是密码哈希成本。12 约 250ms/次，足以让离线爆破不划算。
	BcryptCost = 12
	// MinPasswordLength 是最短密码长度（按字节计，ASCII 场景等价于字符数）。
	MinPasswordLength = 8
	// MaxPasswordLength 是 bcrypt 的硬上限。超过 72 字节 bcrypt 会静默截断，
	// 必须显式拒绝，否则「长度 80 的密码」与「前 72 字节相同的密码」等价。
	MaxPasswordLength = 72
	// MaxDisplayNameLength 是昵称长度上限。
	MaxDisplayNameLength = 64
	// tokenIssuer 是 JWT 的 iss 声明。
	tokenIssuer = "ashen-courier"
	// tokenAudience 是 JWT 的 aud 声明。
	tokenAudience = "ashen-courier-web"
)

// Auth 负责注册、登录与令牌签发/校验。
type Auth struct {
	users  domain.UserRepository
	secret []byte
	expiry time.Duration
}

// NewAuth 构造鉴权服务。
func NewAuth(users domain.UserRepository, secret string, expiry time.Duration) *Auth {
	return &Auth{users: users, secret: []byte(secret), expiry: expiry}
}

// Session 是一次注册/登录的结果。
type Session struct {
	// User 是当前用户。
	User *domain.User
	// Token 是 HS256 签名的 JWT。
	Token string
	// ExpiresAt 是令牌过期时刻。
	ExpiresAt time.Time
}

// RegisterInput 是注册入参。
type RegisterInput struct {
	// Email 是登录邮箱。
	Email string
	// Password 是明文口令（仅在本次调用内存在，不落任何日志）。
	Password string
	// DisplayName 是可选昵称。
	DisplayName string
}

// Register 创建账号并直接签发令牌。
func (a *Auth) Register(ctx context.Context, in RegisterInput) (*Session, error) {
	email := strings.TrimSpace(in.Email)
	if !domain.IsValidEmail(email) {
		return nil, domain.Invalid("email", "请输入合法的邮箱地址")
	}
	if err := ValidatePassword(in.Password); err != nil {
		return nil, err
	}
	displayName := strings.TrimSpace(in.DisplayName)
	if len([]rune(displayName)) > MaxDisplayNameLength {
		return nil, domain.Invalid("display_name", fmt.Sprintf("昵称不能超过 %d 个字符", MaxDisplayNameLength))
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), BcryptCost)
	if err != nil {
		return nil, fmt.Errorf("service.auth: hash password: %w", err)
	}

	user := &domain.User{
		ID:           uuid.NewV7(),
		Email:        email,
		PasswordHash: string(hash),
		DisplayName:  displayName,
	}
	if err := a.users.Create(ctx, user); err != nil {
		return nil, err
	}
	return a.issue(user)
}

// Login 校验口令并签发令牌。
//
// 失败分三类，每一类的下游动作都不同，所以刻意用不同的错误表达：
//
//	邮箱格式不合法 / 口令为空 → *InvalidInputError（422 字段级，与 Register 同一套口径）
//	邮箱不存在 或 口令不对     → domain.ErrInvalidCredentials（401，文案「邮箱或密码不正确」）
//	依赖不可用                → 原样上抛（503）
//
// 「邮箱不存在」与「口令不对」必须归为同一个错误：分开就等于把登录页做成账号枚举器。
// 口令比对在邮箱不存在时也走一次 bcrypt（dummyCompare），抹平时间差。
//
// 前置的字段校验（邮箱格式、口令非空）在**任何**数据库与 bcrypt 动作之前返回，
// 所以它不会引入新的侧信道 —— 这两条判定与「账号是否存在」无关。
// 反过来说，这里刻意**不**校验口令强度（长度/复杂度）：登录不是注册，
// 策略是注册侧的事；将来若上调 MinPasswordLength，在登录侧也校验会让
// 老用户直接登不进来。
func (a *Auth) Login(ctx context.Context, email, password string) (*Session, error) {
	normalized := domain.NormalizeEmail(email)
	if !domain.IsValidEmail(normalized) {
		return nil, domain.Invalid("email", "请输入合法的邮箱地址")
	}
	if strings.TrimSpace(password) == "" {
		return nil, domain.Invalid("password", "请输入密码")
	}

	user, err := a.users.GetByEmail(ctx, normalized)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// dummyCompare 消耗与真实比对相当的时间，抵御计时侧信道
			dummyCompare(password)
			return nil, fmt.Errorf("service.auth: login %q: %w", normalized, domain.ErrInvalidCredentials)
		}
		return nil, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, fmt.Errorf("service.auth: login %q: %w", normalized, domain.ErrInvalidCredentials)
	}
	return a.issue(user)
}

// ParseToken 校验令牌并返回其中的用户 ID，不查库。
// 供中间件在每个请求上使用 —— 这是热路径，不能多打一次数据库。
func (a *Auth) ParseToken(token string) (uuid.UUID, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return uuid.Nil(), fmt.Errorf("service.auth: parse token: %w", domain.ErrUnauthorized)
	}

	claims := &jwt.RegisteredClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) {
		return a.secret, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithAudience(tokenAudience),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !parsed.Valid {
		return uuid.Nil(), fmt.Errorf("service.auth: parse token: %w", domain.ErrUnauthorized)
	}

	id, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil(), fmt.Errorf("service.auth: parse token subject %q: %w", claims.Subject, domain.ErrUnauthorized)
	}
	return id, nil
}

// User 按 ID 取用户，供 /api/auth/me 使用。
func (a *Auth) User(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	return a.users.GetByID(ctx, id)
}

// issue 签发 HS256 令牌。
func (a *Auth) issue(user *domain.User) (*Session, error) {
	now := time.Now()
	expiresAt := now.Add(a.expiry)

	claims := jwt.RegisteredClaims{
		Subject:   user.ID.String(),
		Issuer:    tokenIssuer,
		Audience:  jwt.ClaimStrings{tokenAudience},
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now.Add(-time.Minute)), // 容忍轻微时钟漂移
		ExpiresAt: jwt.NewNumericDate(expiresAt),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(a.secret)
	if err != nil {
		return nil, fmt.Errorf("service.auth: sign token: %w", err)
	}
	return &Session{User: user, Token: signed, ExpiresAt: expiresAt}, nil
}

// ValidatePassword 校验口令强度。返回 domain.InvalidInputError，handler 会映射成 422。
func ValidatePassword(password string) error {
	if len(password) < MinPasswordLength {
		return domain.Invalid("password", fmt.Sprintf("密码至少 %d 位", MinPasswordLength))
	}
	if len(password) > MaxPasswordLength {
		return domain.Invalid("password", fmt.Sprintf("密码不能超过 %d 字节", MaxPasswordLength))
	}
	if strings.TrimSpace(password) == "" {
		return domain.Invalid("password", "密码不能全是空白字符")
	}
	return nil
}

// dummyHash 是启动后算一次就固定下来的假摘要，用于抹平登录失败路径的耗时差异。
//
// 成本 12 的一次 bcrypt 在开发机上约 250ms，放在包级变量里会导致 init
// 阶段白等一次 —— 因此用 sync.OnceValue 惰性计算、只算一次。
var dummyHash = sync.OnceValue(func() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("ashen-courier-dummy-password"), BcryptCost)
	if err != nil {
		return nil
	}
	return h
})

// dummyCompare 在执行一次无意义的 bcrypt 比对，用于「邮箱不存在」分支。
func dummyCompare(password string) {
	if h := dummyHash(); h != nil {
		_ = bcrypt.CompareHashAndPassword(h, []byte(password))
	}
}
