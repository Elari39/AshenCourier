package domain

import (
	"context"
	"strings"
	"time"
	"uuid"
)

// User 是账号实体。
type User struct {
	// ID 由 Go 侧 uuid.NewV7() 生成。
	ID uuid.UUID
	// Email 原文保留大小写，唯一性由 lower(email) 索引保证。
	Email string
	// PasswordHash 是 bcrypt 摘要，绝不出现在任何 DTO 里。
	PasswordHash string
	// DisplayName 为空时前端回落到邮箱前缀。
	DisplayName string
	// CreatedAt / UpdatedAt 由数据库维护。
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NormalizeEmail 统一做 trim + 小写，用于查询与唯一性比较。
// 注意：DB 里存的仍是用户原始输入，只是建了 lower(email) 唯一索引。
func NormalizeEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// IsValidEmail 做最小可用性检查：非空、含单个 '@'、两侧都非空、总长受限。
// 刻意不写完整 RFC 5322 正则 —— MVP 阶段靠"注册后能不能收到信"来兜底更实际。
func IsValidEmail(email string) bool {
	if len(email) < 3 || len(email) > 254 {
		return false
	}
	at := strings.IndexByte(email, '@')
	if at <= 0 || at == len(email)-1 {
		return false
	}
	if strings.ContainsAny(email, " \t\r\n") {
		return false
	}
	// '@' 之后必须有点号，且点号不在首尾
	domainPart := email[at+1:]
	dot := strings.IndexByte(domainPart, '.')
	return dot > 0 && dot < len(domainPart)-1
}

// UserRepository 是账号仓储接口，实现在 internal/store/postgres。
type UserRepository interface {
	// Create 插入用户；邮箱唯一约束冲突时返回带 Field="email" 的 *ConflictError。
	Create(ctx context.Context, u *User) error
	// GetByEmail 按不区分大小写的邮箱查询；不存在返回 *NotFoundError。
	GetByEmail(ctx context.Context, email string) (*User, error)
	// GetByID 按主键查询；不存在返回 *NotFoundError。
	GetByID(ctx context.Context, id uuid.UUID) (*User, error)
}
