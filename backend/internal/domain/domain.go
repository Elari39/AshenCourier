package domain

import (
	"context"
	"time"
	"uuid"
)

// Domain 是一条已登记的自定义域名。
//
// 「默认域名」不是一个 Domain 实例，而是一种**缺席**：`links.domain_id` 为 NULL
// 就表示该短链属于 PUBLIC_BASE_URL 指向的那个域名。这样历史行不需要回填，
// 也不需要造一条「默认域」的假记录。
type Domain struct {
	// ID 是主键，由登记方（运维脚本或后台）生成。
	ID uuid.UUID
	// Name 是纯主机名：小写、不含 scheme / 端口 / 路径。
	// 归一化在写入前完成（internal/pkg/hostname），库里另有一条 CHECK 兜底。
	Name string
	// OwnerID 为空表示没有归属账号（自建场景下的常见形态）。
	OwnerID *uuid.UUID
	// VerifiedAt 为空表示尚未验证归属。当前实现不校验 DNS TXT，
	// 由登记方保证只登记已指向本服务的域名。
	VerifiedAt *time.Time
	// CreatedAt 由数据库维护。
	CreatedAt time.Time
}

// DomainRepository 是自定义域名的仓储，实现在 internal/store/postgres。
//
// 刻意只暴露 ListAll 一个方法：域名表**极小且极少变动**（自建服务通常 1–3 行），
// 而最高频的用法是「每个跳转请求都要判一次 Host 属于哪个域」。
// 一次性读进内存做快照，比提供 ByName / ByID 让调用方反复查库更省，
// 也让 service 层不必关心「主机名匹配」这件事该在哪一层做。
type DomainRepository interface {
	// ListAll 返回全部已登记域名（顺序无要求）。
	// 表为空是完全正常的形态 —— 表示该部署只用默认域名。
	ListAll(ctx context.Context) ([]Domain, error)
}
