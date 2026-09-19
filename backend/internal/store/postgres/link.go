package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"ashen-courier/internal/domain"
	"uuid"
)

// LinkStore 实现 domain.LinkRepository，
// 与 DB 共用同一个连接池，拆成独立类型是为了避免不同实体的同名方法互相覆盖。
type LinkStore struct {
	db *DB
}

// linkColumns 是 SELECT / RETURNING 里统一的列顺序，必须与 linkRow 的字段一一对应。
//
// created_ip 用 host(...) 取：inet 的文本形式会带上掩码长度（单个地址读出来是
// "203.0.113.7/32"），而这一列存的始终是单个地址。click_events.ip 早就做了同样的
// 处理（见 click.go 的 clickEventColumns），这里对齐它 —— 是集成测试第一次跑就发现的。
const linkColumns = `id, short_code, target_url, title, owner_id, key_hash, password_hash, status,
       click_count, expires_at, tags, coalesce(host(created_ip), ''), created_at, updated_at, domain_id`

// linkRow 是 links 表的一行。
type linkRow struct {
	id        pgtype.UUID
	shortCode string
	targetURL string
	title     string
	ownerID   pgtype.UUID
	keyHash   []byte
	// passwordHash 是访问口令的 bcrypt 摘要；空串 = 不设口令（列是 NOT NULL DEFAULT ''）。
	passwordHash string
	status       int16
	clickCount   int64
	expiresAt    pgtype.Timestamptz
	tags         []string
	createdIP    string
	createdAt    time.Time
	updatedAt    time.Time
	// domainID 为 NULL 表示默认域名（见 domain.Link.DomainID）。
	domainID pgtype.UUID
}

// dest 返回交给 rows.Scan 的扫描目标，顺序与 linkColumns 完全一致。
func (r *linkRow) dest() []any {
	return []any{
		&r.id, &r.shortCode, &r.targetURL, &r.title, &r.ownerID, &r.keyHash, &r.passwordHash,
		&r.status, &r.clickCount, &r.expiresAt, &r.tags, &r.createdIP, &r.createdAt, &r.updatedAt,
		&r.domainID,
	}
}

// toDomain 把行数据转成领域实体。
func (r *linkRow) toDomain() *domain.Link {
	l := &domain.Link{
		ID:        fromPgUUID(r.id),
		ShortCode: r.shortCode,
		TargetURL: r.targetURL,
		Title:     r.title,
		OwnerID:   uuidPtr(r.ownerID),
		KeyHash:   r.keyHash,
		// 库读路径同时填两个字段：PasswordProtected 是「缓存路径也要能回答」的冗余标志，
		// 见 domain.Link.PasswordProtected 的注释。
		PasswordHash:      r.passwordHash,
		PasswordProtected: r.passwordHash != "",
		Status:            domain.LinkStatus(r.status),
		ClickCount:        r.clickCount,
		Tags:              r.tags,
		CreatedIP:         r.createdIP,
		CreatedAt:         r.createdAt,
		UpdatedAt:         r.updatedAt,
		DomainID:          uuidPtr(r.domainID),
	}
	if r.expiresAt.Valid {
		t := r.expiresAt.Time
		l.ExpiresAt = &t
	}
	return l
}

// tagsParam 保证写库的 []string 非 nil。
//
// 列是 NOT NULL DEFAULT '{}'，而 pgx 会把 nil 切片编码成 SQL NULL ——
// 直接传 nil 会撞上 NOT NULL 约束（不是回落到默认值，那只有「不给这一列」时才发生）。
func tagsParam(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}

// Create 插入一条短链。ID 由调用方（service 层）生成，不依赖数据库默认值。
func (s *LinkStore) Create(ctx context.Context, link *domain.Link) error {
	const q = `
INSERT INTO links (id, short_code, target_url, title, owner_id, key_hash, password_hash,
                   status, click_count, expires_at, tags, created_ip, created_at, updated_at, domain_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NULLIF($12, '')::inet, now(), now(), $13)
RETURNING created_at, updated_at`

	opCtx, cancel := s.db.opCtx(ctx)
	defer cancel()

	err := s.db.pool.QueryRow(opCtx, q,
		toPgUUID(link.ID),
		link.ShortCode,
		link.TargetURL,
		link.Title,
		uuidParam(link.OwnerID),
		link.KeyHash,
		link.PasswordHash,
		int16(link.Status),
		link.ClickCount,
		link.ExpiresAt,
		tagsParam(link.Tags),
		link.CreatedIP,
		// nil = 默认域名（列可空，NULL 即语义本身）
		uuidParam(link.DomainID),
	).Scan(&link.CreatedAt, &link.UpdatedAt)
	if err != nil {
		return fmt.Errorf("store.postgres: create link %q: %w", link.ShortCode, mapWriteError(err, "short_code", link.ShortCode))
	}
	if link.Tags == nil {
		// 让内存里的实体与库里一致（列是 NOT NULL DEFAULT '{}'）
		link.Tags = []string{}
	}
	return nil
}

// GetByCode 按短码精确查询（大小写敏感）。软删除的行也会返回，
// 由调用方（domain.Link.Redirectable）决定是 404 还是 410。
//
// 不带域：短码全局唯一，管理端靠它唯一定位。
func (s *LinkStore) GetByCode(ctx context.Context, code string) (*domain.Link, error) {
	const q = `SELECT ` + linkColumns + ` FROM links WHERE short_code = $1`
	return s.queryLink(ctx, q, code)
}

// GetByCodeInDomain 在指定域内按短码查询；domainID 为 nil 表示默认域名。
//
// SQL 用 `IS NOT DISTINCT FROM` 而不是 `=`：默认域名的行 domain_id 是 NULL，
// 而 `domain_id = NULL` 恒为 NULL（不是 true），写成等号会让所有历史短链
// 在跳转路径上全部查不到 —— 这是本批最容易踩空的一处。
func (s *LinkStore) GetByCodeInDomain(ctx context.Context, code string, domainID *uuid.UUID) (*domain.Link, error) {
	const q = `SELECT ` + linkColumns + ` FROM links
WHERE short_code = $1 AND domain_id IS NOT DISTINCT FROM $2`
	return s.queryLink(ctx, q, code, uuidParam(domainID))
}

// queryLink 执行「查一行凭据 / 短链」的查询并转成领域实体。
func (s *LinkStore) queryLink(ctx context.Context, q string, args ...any) (*domain.Link, error) {
	opCtx, cancel := s.db.opCtx(ctx)
	defer cancel()

	var r linkRow
	if err := s.db.pool.QueryRow(opCtx, q, args...).Scan(r.dest()...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			code, _ := args[0].(string)
			return nil, domain.NotFound("link", code)
		}
		return nil, fmt.Errorf("store.postgres: get link: %w", storageError(err))
	}
	return r.toDomain(), nil
}

// Update 按短码做部分更新，返回更新后的实体。
func (s *LinkStore) Update(ctx context.Context, code string, patch domain.LinkPatch) (*domain.Link, error) {
	const q = `
UPDATE links SET
    target_url = COALESCE($2, target_url),
    title      = COALESCE($3, title),
    status     = COALESCE($4, status),
    expires_at = CASE WHEN $5 THEN NULL ELSE COALESCE($6, expires_at) END,
    tags       = COALESCE($7, tags),
    password_hash = COALESCE($8, password_hash),
    updated_at = now()
WHERE short_code = $1
RETURNING ` + linkColumns

	// status 需要 *int16；用 Go 1.26+ 的 new(表达式) 直接构造，不写多余的临时变量
	var statusArg *int16
	if patch.Status != nil {
		statusArg = new(int16(*patch.Status))
	}

	// tags 用「指针指向的空切片」表示清空：nil 指针必须原样传成 SQL NULL，
	// 否则 COALESCE 会把「不想动」误当成「清空」。
	var tagsArg *[]string
	if patch.Tags != nil {
		tagsArg = new(tagsParam(*patch.Tags))
	}

	// password_hash 同理：nil → SQL NULL → 保持原值；指向空串 → 写空串 → 清除口令。
	// patch.PasswordHash 已经在 service 层被替换成 bcrypt 摘要，这里只负责落库。

	opCtx, cancel := s.db.opCtx(ctx)
	defer cancel()

	var r linkRow
	err := s.db.pool.QueryRow(opCtx, q,
		code, patch.TargetURL, patch.Title, statusArg, patch.ClearExpires, patch.ExpiresAt, tagsArg,
		patch.PasswordHash,
	).Scan(r.dest()...)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.NotFound("link", code)
		}
		return nil, fmt.Errorf("store.postgres: update link %q: %w", code, mapWriteError(err, "short_code", code))
	}
	return r.toDomain(), nil
}

// SoftDelete 把状态改为 deleted。对已删除的行是幂等的（仍然返回成功）。
func (s *LinkStore) SoftDelete(ctx context.Context, code string) error {
	const q = `
UPDATE links
SET status = $2, updated_at = now()
WHERE short_code = $1
RETURNING id`

	opCtx, cancel := s.db.opCtx(ctx)
	defer cancel()

	var id pgtype.UUID
	err := s.db.pool.QueryRow(opCtx, q, code, int16(domain.LinkStatusDeleted)).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.NotFound("link", code)
		}
		return fmt.Errorf("store.postgres: soft delete link %q: %w", code, storageError(err))
	}
	return nil
}

// ListByOwner 用 keyset 分页列出归属某用户的链接。
// 多取一条用于判断「是否还有下一页」，返回值里的游标即下一页起点。
func (s *LinkStore) ListByOwner(ctx context.Context, filter domain.LinkFilter) ([]domain.Link, domain.LinkCursor, error) {
	args := []any{uuidParam(&filter.OwnerID)}
	where := []string{"owner_id = $1", "status <> " + strconv.Itoa(int(domain.LinkStatusDeleted))}

	if q := strings.TrimSpace(filter.Query); q != "" {
		args = append(args, "%"+escapeLike(q)+"%")
		n := strconv.Itoa(len(args))
		where = append(where, "(short_code ILIKE $"+n+" OR target_url ILIKE $"+n+" OR title ILIKE $"+n+")")
	}
	if tag := strings.TrimSpace(filter.Tag); tag != "" {
		// 数组包含：走 links_tags_gin。参数用 []string 让 pgx 编码成 text[]，
		// 与列类型一致（传裸字符串会被当成 text，PG 会报类型不匹配）。
		args = append(args, []string{tag})
		where = append(where, "tags @> $"+strconv.Itoa(len(args)))
	}
	if filter.Cursor.Valid {
		args = append(args, filter.Cursor.CreatedAt, toPgUUID(filter.Cursor.ID))
		where = append(where, "(created_at, id) < ($"+strconv.Itoa(len(args)-1)+", $"+strconv.Itoa(len(args))+")")
	}

	// 多取 1 条用于探测下一页
	args = append(args, filter.Limit+1)
	sql := `SELECT ` + linkColumns + ` FROM links WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY created_at DESC, id DESC LIMIT $` + strconv.Itoa(len(args))

	opCtx, cancel := s.db.opCtx(ctx)
	defer cancel()

	rows, err := s.db.pool.Query(opCtx, sql, args...)
	if err != nil {
		return nil, domain.LinkCursor{}, fmt.Errorf("store.postgres: list links: %w", storageError(err))
	}
	defer rows.Close()

	links := make([]domain.Link, 0, filter.Limit)
	for rows.Next() {
		var r linkRow
		if err := rows.Scan(r.dest()...); err != nil {
			return nil, domain.LinkCursor{}, fmt.Errorf("store.postgres: scan link row: %w", storageError(err))
		}
		links = append(links, *r.toDomain())
	}
	if err := rows.Err(); err != nil {
		return nil, domain.LinkCursor{}, fmt.Errorf("store.postgres: iterate link rows: %w", storageError(err))
	}

	var next domain.LinkCursor
	if len(links) > filter.Limit {
		links = links[:filter.Limit]
		last := links[len(links)-1]
		next = domain.LinkCursor{CreatedAt: last.CreatedAt, ID: last.ID, Valid: true}
	}
	return links, next, nil
}

// Claim 把匿名链接挂到指定账号下，并清空管理密钥。
// 已被他人认领 / 不存在 / 已删除都会返回领域错误。
func (s *LinkStore) Claim(ctx context.Context, code string, ownerID uuid.UUID) (*domain.Link, error) {
	const q = `
UPDATE links
SET owner_id = $2, key_hash = NULL, updated_at = now()
WHERE short_code = $1 AND owner_id IS NULL AND status <> $3
RETURNING ` + linkColumns

	opCtx, cancel := s.db.opCtx(ctx)
	defer cancel()

	var r linkRow
	err := s.db.pool.QueryRow(opCtx, q, code, toPgUUID(ownerID), int16(domain.LinkStatusDeleted)).Scan(r.dest()...)
	if err == nil {
		return r.toDomain(), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("store.postgres: claim link %q: %w", code, storageError(err))
	}

	// 没更新到行：区分「不存在」与「已被占用」，后者返回 409
	existing, getErr := s.GetByCode(ctx, code)
	if getErr != nil {
		return nil, getErr
	}
	if existing.Status == domain.LinkStatusDeleted {
		return nil, domain.NotFound("link", code)
	}
	return nil, domain.Conflict("owner_id", code)
}

// AddClickCount 把 Redis 侧的计数增量累加进 PG 基线，返回累加后的值。
func (s *LinkStore) AddClickCount(ctx context.Context, code string, delta int64) (int64, error) {
	const q = `
UPDATE links
SET click_count = click_count + $2, updated_at = now()
WHERE short_code = $1
RETURNING click_count`

	opCtx, cancel := s.db.opCtx(ctx)
	defer cancel()

	var total int64
	if err := s.db.pool.QueryRow(opCtx, q, code, delta).Scan(&total); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, domain.NotFound("link", code)
		}
		return 0, fmt.Errorf("store.postgres: add click count %q: %w", code, storageError(err))
	}
	return total, nil
}

// ExpireDue 把已过期但仍为 active 的链接批量置为 disabled，返回被处理的短链引用。
// 一次只处理 limit 条，避免单条 SQL 长时间持锁。
//
// 返回 LinkRef 而不是光秃秃的短码：调用方紧接着要做缓存失效，而缓存键是
// 「域 + 短码」。只给短码的话，挂在自定义域上的过期短链的缓存条目就删不掉，
// 表现为「已过期但仍在跳转」直到 TTL 过期。
func (s *LinkStore) ExpireDue(ctx context.Context, now time.Time, limit int) ([]domain.LinkRef, error) {
	const q = `
UPDATE links SET status = $3, updated_at = now()
WHERE short_code IN (
    SELECT short_code FROM links
    WHERE expires_at IS NOT NULL AND expires_at < $1 AND status = $2
    ORDER BY expires_at
    LIMIT $4
)
RETURNING short_code, domain_id`

	opCtx, cancel := s.db.opCtx(ctx)
	defer cancel()

	rows, err := s.db.pool.Query(opCtx, q, now, int16(domain.LinkStatusActive), int16(domain.LinkStatusDisabled), limit)
	if err != nil {
		return nil, fmt.Errorf("store.postgres: expire due links: %w", storageError(err))
	}
	defer rows.Close()

	var refs []domain.LinkRef
	for rows.Next() {
		var (
			code     string
			domainID pgtype.UUID
		)
		if err := rows.Scan(&code, &domainID); err != nil {
			return nil, fmt.Errorf("store.postgres: scan expired link: %w", storageError(err))
		}
		refs = append(refs, domain.LinkRef{Code: code, DomainID: uuidPtr(domainID)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store.postgres: iterate expired links: %w", storageError(err))
	}
	return refs, nil
}

// escapeLike 转义 LIKE / ILIKE 的通配符，避免用户输入的 % 与 _ 变成通配。
// PostgreSQL 的 ILIKE 默认转义符就是反斜杠，无需额外 ESCAPE 子句。
func escapeLike(s string) string {
	var sb strings.Builder
	sb.Grow(len(s) + 8)
	for i := range len(s) {
		switch s[i] {
		case '%', '_', '\\':
			sb.WriteByte('\\')
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}
