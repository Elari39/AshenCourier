package domain

import (
	"context"
	"fmt"
	"strings"
	"time"
	"uuid"
)

// LinkStatus 是短链的状态机。
//
// 用 int16 而不是 PG enum：MVP 阶段调整状态机不必写 ALTER TYPE，
// Go 侧一个命名类型就够表达，DB 侧只留 CHECK (status IN (1,2,3)) 兜底。
type LinkStatus int16

// 状态取值。数值一旦发布不可更改 —— 历史行里存的是数字。
const (
	// LinkStatusActive 表示可正常跳转。
	LinkStatusActive LinkStatus = 1
	// LinkStatusDisabled 表示被所有者主动停用，跳转返回 410。
	LinkStatusDisabled LinkStatus = 2
	// LinkStatusDeleted 表示软删除，跳转返回 404。
	LinkStatusDeleted LinkStatus = 3
)

// String 返回状态的 API 字符串表示。
func (s LinkStatus) String() string {
	switch s {
	case LinkStatusActive:
		return "active"
	case LinkStatusDisabled:
		return "disabled"
	case LinkStatusDeleted:
		return "deleted"
	default:
		return "unknown"
	}
}

// Valid 判断状态值是否落在合法枚举内。
func (s LinkStatus) Valid() bool {
	switch s {
	case LinkStatusActive, LinkStatusDisabled, LinkStatusDeleted:
		return true
	default:
		return false
	}
}

// ParseLinkStatus 解析 API 传入的状态字符串。
func ParseLinkStatus(raw string) (LinkStatus, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "active":
		return LinkStatusActive, nil
	case "disabled":
		return LinkStatusDisabled, nil
	case "deleted":
		return LinkStatusDeleted, nil
	default:
		return 0, Invalid("status", "只能是 active / disabled / deleted")
	}
}

// Link 是短链聚合根。
type Link struct {
	// ID 由 Go 侧 uuid.NewV7() 生成（时间有序，对 B-tree 索引友好）。
	ID uuid.UUID
	// ShortCode 是短码，大小写敏感，全局唯一。
	ShortCode string
	// TargetURL 是规范化后的目标地址，只允许 http / https。
	TargetURL string
	// Title 是用户手填的标题（MVP 不抓取目标页标题，避免 SSRF）。
	Title string
	// OwnerID 为空表示匿名创建。
	OwnerID *uuid.UUID
	// DomainID 是所属自定义域名（M5-3）。
	//
	// **nil 不是「没设置」，而是一个明确语义：默认域名**（PUBLIC_BASE_URL 指向的那个）。
	// 于是历史行不需要回填，读路径也不必为「老数据」单独开一条分支。
	DomainID *uuid.UUID
	// KeyHash 是匿名管理密钥的 SHA-256；被认领后清空。
	KeyHash []byte
	// PasswordHash 是访问口令的 bcrypt 摘要；空串表示不设口令。
	// 只有库读路径（store/postgres）会填它 —— 缓存里刻意不放摘要。
	PasswordHash string
	// PasswordProtected 表示「这条链接需要口令才能跳转」。
	//
	// 它与 PasswordHash 之间是一条**单向不变量**：PasswordHash 非空 ⇒ PasswordProtected 为 true。
	// 之所以要这个冗余布尔：跳转路径只读 Redis 缓存，而缓存里不放 bcrypt 摘要
	// （转储泄露不该 enable 离线爆破），因此缓存回填的实体只有这个标志位；
	// 库读路径一定同时填好两者。判空一律走 HasPassword()。
	PasswordProtected bool
	// Status 是状态机取值。
	Status LinkStatus
	// ClickCount 是 PG 侧基线计数，worker 把 Redis 增量刷进来。
	ClickCount int64
	// ExpiresAt 为空表示永久有效。
	ExpiresAt *time.Time
	// Tags 是标签，统一小写（筛选走数组包含，大小写敏感）。
	Tags []string
	// CreatedIP 记录创建者 IP，用于风控排查。
	CreatedIP string
	// CreatedAt / UpdatedAt 由数据库维护。
	CreatedAt time.Time
	UpdatedAt time.Time
}

// HasExpiry 判断是否设置了有效期。
func (l *Link) HasExpiry() bool { return l.ExpiresAt != nil }

// IsExpired 判断在给定时刻是否已过期。
func (l *Link) IsExpired(now time.Time) bool {
	return l.ExpiresAt != nil && !now.Before(*l.ExpiresAt)
}

// IsAnonymous 判断是否为匿名创建的链接。
func (l *Link) IsAnonymous() bool { return l.OwnerID == nil }

// HasPassword 判断这条链接是否需要口令才能跳转。
// 两个字段取或，理由见 PasswordProtected 的注释：库读路径填两个，缓存路径只填一个。
func (l *Link) HasPassword() bool { return l.PasswordProtected || l.PasswordHash != "" }

// Redirectable 判断该链接在给定时刻能否跳转；不能时返回对应的领域错误。
// 返回值是 errors.Is(err, ErrNotFound) / errors.Is(err, ErrGone) 可判定的包装错误。
func (l *Link) Redirectable(now time.Time) error {
	switch {
	case l.Status == LinkStatusDeleted:
		return fmt.Errorf("link %q: %w", l.ShortCode, ErrNotFound)
	case l.Status != LinkStatusActive:
		return fmt.Errorf("link %q status=%s: %w", l.ShortCode, l.Status, ErrGone)
	case l.IsExpired(now):
		return fmt.Errorf("link %q expired at %s: %w", l.ShortCode, *l.ExpiresAt, ErrGone)
	default:
		return nil
	}
}

// LinkPatch 描述一次部分更新；nil 字段表示「保持不变」。
//
// ExpiresAt 用「指针 + ClearExpires 标志」而不是双指针：可读性更好，也不容易误用。
// Tags 则用指针：指向空切片表示「清空标签」，nil 表示「不动」。
type LinkPatch struct {
	// TargetURL 非空时更新目标地址。
	TargetURL *string
	// Title 非空时更新标题。
	Title *string
	// Status 非空时更新状态。
	Status *LinkStatus
	// ExpiresAt 非空时设置为该时刻。
	ExpiresAt *time.Time
	// ClearExpires 为 true 时把 expires_at 置为 NULL（改为永久有效）。
	ClearExpires bool
	// Tags 指向新标签集合（空切片 = 清空）；nil 表示保持原样。
	Tags *[]string
	// PasswordHash 指向新的口令**摘要**（明文由 service 层摘要化后填入）；
	// nil 表示保持原样，指向空串表示清除口令。
	PasswordHash *string
}

// IsEmpty 判断这个 patch 是否什么都没改。
func (p LinkPatch) IsEmpty() bool {
	return p.TargetURL == nil && p.Title == nil && p.Status == nil &&
		p.ExpiresAt == nil && !p.ClearExpires && p.Tags == nil && p.PasswordHash == nil
}

// LinkFilter 是「我的链接」列表的查询条件。
type LinkFilter struct {
	// OwnerID 是列表归属的用户。
	OwnerID uuid.UUID
	// Query 是可选搜索词，匹配短码 / 目标地址 / 标题。
	Query string
	// Tag 是可选标签筛选（统一小写后走 tags @> ARRAY[...]）。
	Tag string
	// Limit 是本页条数。
	Limit int
	// Cursor 是上一页最后一条的位置；零值表示第一页。
	Cursor LinkCursor
}

// LinkCursor 是 keyset 分页游标。
//
// 用 (created_at, id) 复合位置而不是 offset：offset 在并发插入下会漏行/重复，
// 而 uuid v7 单调递增，配合 created_at DESC, id DESC 的排序天然稳定。
type LinkCursor struct {
	// CreatedAt 是上一页最后一条的创建时间。
	CreatedAt time.Time
	// ID 是上一页最后一条的 ID。
	ID uuid.UUID
	// Valid 为 false 表示这是第一页。
	Valid bool
}

// ClickCountWriter 把 Redis 侧的计数增量累加进 PG 基线，实现在 internal/store/postgres。
// 它是 LinkRepository 的一个窄切片：worker 只需要这一个方法，窄接口让 fake 不必
// 实现整个仓储。
type ClickCountWriter interface {
	// AddClickCount 把增量累加进 PG 基线，返回累加后的值；短码不存在返回 *NotFoundError。
	AddClickCount(ctx context.Context, code string, delta int64) (int64, error)
}

// ExpiredLinkSweeper 扫描并失效已到期的短链，实现在 internal/store/postgres。
type ExpiredLinkSweeper interface {
	// ExpireDue 把已过期但仍为 active 的链接置为 disabled，返回被处理的短链引用。
	// 带域是必需的：worker 紧接着要按「域 + 短码」失效缓存，只拿到短码就删不掉
	// 挂在自定义域上的那条（见 LinkRef）。
	ExpireDue(ctx context.Context, now time.Time, limit int) ([]LinkRef, error)
}

// LinkRepository 是短链仓储接口，实现在 internal/store/postgres。
type LinkRepository interface {
	// Create 插入一条短链；短码唯一约束冲突时返回 *ConflictError。
	Create(ctx context.Context, link *Link) error
	// GetByCode 按短码精确查询（大小写敏感）；不存在返回 *NotFoundError。
	//
	// **不带域**：短码在当前模型里是全局唯一的，所以管理端（详情 / 修改 / 删除 / 认领）
	// 只靠短码就能唯一定位。跳转与口令校验走 GetByCodeInDomain。
	GetByCode(ctx context.Context, code string) (*Link, error)
	// GetByCodeInDomain 在**指定域内**按短码精确查询；不存在返回 *NotFoundError。
	//
	// domainID 为 nil 表示默认域名（PUBLIC_BASE_URL 指向的那个）。
	// 跳转路径必须用它：分域之后「同一个短码」在不同域下是两条不同的短链，
	// 只按短码查会把 A 域的访问解析到 B 域的链接上 —— 那是把访问者送去错误目标，
	// 比 404 严重得多。
	//
	// 实现里写成 `domain_id IS NOT DISTINCT FROM $2`：NULL 与 NULL 在这里必须算相等，
	// 而 SQL 的 `=` 对 NULL 恒为 NULL（不是 true），写成 `= $2` 会让所有默认域名的
	// 历史短链全部查不到。
	GetByCodeInDomain(ctx context.Context, code string, domainID *uuid.UUID) (*Link, error)
	// Update 按短码做部分更新，返回更新后的实体；不存在返回 *NotFoundError。
	Update(ctx context.Context, code string, patch LinkPatch) (*Link, error)
	// SoftDelete 把状态改为 deleted；不存在返回 *NotFoundError。
	SoftDelete(ctx context.Context, code string) error
	// ListByOwner 按 owner 做 keyset 分页查询，同时返回下一页游标（无下一页时 Valid=false）。
	ListByOwner(ctx context.Context, filter LinkFilter) ([]Link, LinkCursor, error)
	// Claim 把匿名链接挂到指定账号下，并清空 key_hash。
	Claim(ctx context.Context, code string, ownerID uuid.UUID) (*Link, error)
	// AddClickCount 把 Redis 侧的计数增量累加进 PG 基线，返回累加后的值。
	AddClickCount(ctx context.Context, code string, delta int64) (int64, error)
	// ExpireDue 扫描并标记已过期但仍为 active 的链接，返回被处理的短链引用
	// （带所属域，见 LinkRef —— 调用方要按「域 + 短码」失效缓存）。
	ExpireDue(ctx context.Context, now time.Time, limit int) ([]LinkRef, error)
}
