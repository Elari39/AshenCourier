package postgres

import (
	"errors"
	"strings"
	"testing"
	"uuid"

	"ashen-courier/internal/domain"
)

// 分域（自定义域名）的集成测试。
//
// 这里只测「换成真 PG 才会暴露」的东西：
//   - `domain_id IS NOT DISTINCT FROM $2` 的真实语义（NULL 与 NULL 必须算相等；
//     而 `= $2` 在 NULL 上恒为 NULL，症状是**默认域名的历史短链全部查不到**）
//   - 两处 CHECK / 唯一 / 外键约束（域名形态、域名唯一、域下还有短链时不许删域）
//
// 域过滤的「顺序」与「缓存」语义在 internal/service 的单测里覆盖，不在这里重复。

// registerDomain 落库一条域名。
//
// 刻意不走仓储：DomainRepository 只暴露 ListAll（域表是运维写、应用读的配置表），
// 没有 Create。测试直接写库反而更接近真实的登记方式。
func registerDomain(t *testing.T, db *DB, name string, ownerID *uuid.UUID) domain.Domain {
	t.Helper()

	d := domain.Domain{ID: uuid.NewV7(), Name: name, OwnerID: ownerID}
	_, err := db.pool.Exec(t.Context(),
		"INSERT INTO domains (id, domain, owner_id) VALUES ($1, $2, $3)",
		toPgUUID(d.ID), d.Name, uuidParam(d.OwnerID))
	if err != nil {
		t.Fatalf("登记域名 %q: %v", name, err)
	}
	return d
}

// TestGetByCodeInDomain 是这一批最关键的一条断言：**域不匹配必须查不到**。
func TestGetByCodeInDomain(t *testing.T) {
	t.Parallel()

	db := testDB(t)
	ctx := t.Context()

	domA := registerDomain(t, db, "a-"+testSuffix(t)+".example", nil)
	domB := registerDomain(t, db, "b-"+testSuffix(t)+".example", nil)

	// 默认域名的短链：domain_id 为 NULL，落库走的是「不传这一列」的老路径
	defCode := testCode(t, "def")
	createLink(t, db, defCode, nil)

	// 挂在自定义域上的短链
	domCode := testCode(t, "dom")
	createLink(t, db, domCode, func(l *domain.Link) { l.DomainID = &domA.ID })

	// 默认域名的短链：用 nil 查得到
	got, err := db.Links().GetByCodeInDomain(ctx, defCode, nil)
	if err != nil {
		t.Fatalf("默认域名查询 %q 应当成功：%v", defCode, err)
	}
	if got.DomainID != nil {
		t.Errorf("默认域名短链的 DomainID = %v，期望 nil", got.DomainID)
	}

	// **同一条默认域名的短链，带着某个域 ID 去查必须查不到。**
	// 这是 `IS NOT DISTINCT FROM` 与「NULL 匹配任意值」的分界线：
	// 写成宽松匹配的实现在这里会把默认域名的链接喂给任意域。
	for _, d := range []domain.Domain{domA, domB} {
		if _, err := db.Links().GetByCodeInDomain(ctx, defCode, &d.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("带域 %s 查默认域名的短链 %q 应当 NotFound，实际 %v", d.Name, defCode, err)
		}
	}

	// 自定义域名的短链：在所属域查得到，其它域（含默认域）查不到
	got, err = db.Links().GetByCodeInDomain(ctx, domCode, &domA.ID)
	if err != nil {
		t.Fatalf("在所属域查询 %q 应当成功：%v", domCode, err)
	}
	if got.DomainID == nil || *got.DomainID != domA.ID {
		t.Errorf("查询结果的 DomainID = %v，期望 %v", got.DomainID, domA.ID)
	}
	for _, d := range []*uuid.UUID{nil, &domB.ID} {
		if _, err := db.Links().GetByCodeInDomain(ctx, domCode, d); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("短码 %q 在另一个域上应当 NotFound（域隔离），实际 %v", domCode, err)
		}
	}

	// 管理端按短码直查（不带域）仍然找得到 —— 短码在这一批里仍是全局唯一的
	if _, err := db.Links().GetByCode(ctx, domCode); err != nil {
		t.Errorf("管理端按短码直查应当成功（短码全局唯一）：%v", err)
	}
}

// TestShortCodeStillGloballyUnique 钉住 N6-1 的**边界**。
//
// 这一批刻意不动 links.short_code 的全局唯一约束：所有按短码的既有查询
// （管理接口、口令校验、计数回刷）因此完全不受影响，风险被限制在「读路径多一个 Host」。
//
// 代价是「两个域名各自解析同一个短码」还做不到 —— 那需要把唯一键换成
// (domain_id, short_code)，并连带改短码生成重试、保留字校验与每一处 GetByCode。
// 那条断言属于下一批，届时**这条用例应当被反写**（从「必须冲突」变成「必须共存」）。
func TestShortCodeStillGloballyUnique(t *testing.T) {
	t.Parallel()

	db := testDB(t)

	domA := registerDomain(t, db, "uniq-"+testSuffix(t)+".example", nil)
	code := testCode(t, "same")

	createLink(t, db, code, nil)
	err := db.Links().Create(t.Context(), &domain.Link{
		ID:        uuid.NewV7(),
		ShortCode: code,
		TargetURL: "https://example.com/other",
		Status:    domain.LinkStatusActive,
		DomainID:  &domA.ID,
	})
	if err == nil {
		t.Fatal("短码在 N6-1 仍然全局唯一：同码不同域应当冲突。若这条失败，说明唯一约束已改成 (domain_id, short_code) —— 请同步反写本用例与相关查询")
	}
	var conflict *domain.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("期望 *domain.ConflictError，实际 %v", err)
	}
}

// TestDomainConstraints 覆盖表上的三处约束。它们的共同点是「绕过应用层直接写库时兜底」，
// 所以只能在 store 层用真 SQL 验证。
func TestDomainConstraints(t *testing.T) {
	t.Parallel()

	db := testDB(t)
	ctx := t.Context()

	owner := createUser(t, db)
	name := "constraints-" + testSuffix(t) + ".example"

	registered := registerDomain(t, db, name, &owner.ID)

	t.Run("域名唯一", func(t *testing.T) {
		_, err := db.pool.Exec(ctx,
			"INSERT INTO domains (id, domain) VALUES ($1, $2)", toPgUUID(uuid.NewV7()), name)
		if err == nil {
			t.Fatal("重复登记同一个域名应当报唯一约束冲突")
		}
		if !strings.Contains(err.Error(), "domains_domain_key") {
			t.Errorf("期望撞上 domains_domain_key，实际 %v", err)
		}
	})

	t.Run("域名形态必须已归一化", func(t *testing.T) {
		// 这两类写法一旦进库，按 Host 查询就永远命中不了，
		// 而症状是「域名明明登记了、短链却 404」——所以约束必须在库里兜住
		for _, bad := range []string{"UPPER.example", "https://x.example", "-lead.example", "trail-.example", "a..b"} {
			_, err := db.pool.Exec(ctx,
				"INSERT INTO domains (id, domain) VALUES ($1, $2)", toPgUUID(uuid.NewV7()), bad)
			if err == nil {
				t.Errorf("域名 %q 不该通过 domains_domain_shape", bad)
			}
		}
	})

	t.Run("域下还有短链时不许删域", func(t *testing.T) {
		createLink(t, db, testCode(t, "restrict"), func(l *domain.Link) { l.DomainID = &registered.ID })

		if _, err := db.pool.Exec(ctx, "DELETE FROM domains WHERE id = $1", toPgUUID(registered.ID)); err == nil {
			t.Fatal("域下还有短链时删除应当被外键拦下：否则这些短链会静默掉回默认域并在默认域上被解析")
		}
	})
}

// TestListAllDomains 覆盖快照的数据来源：全量、带上 owner、按域名排序。
func TestListAllDomains(t *testing.T) {
	t.Parallel()

	db := testDB(t)
	ctx := t.Context()

	owner := createUser(t, db)
	suffix := testSuffix(t)
	// 后登记的先查出来，用来证明「排序由 SQL 决定」而不是插入顺序
	second := registerDomain(t, db, "zz-"+suffix+".example", nil)
	first := registerDomain(t, db, "aa-"+suffix+".example", &owner.ID)

	list, err := db.Domains().ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	byID := map[uuid.UUID]domain.Domain{}
	for _, d := range list {
		byID[d.ID] = d
	}
	for _, want := range []domain.Domain{first, second} {
		got, ok := byID[want.ID]
		if !ok {
			t.Errorf("ListAll 结果里缺少域名 %q（共 %d 条）", want.Name, len(list))
			continue
		}
		if got.Name != want.Name {
			t.Errorf("域名 %v 的 Name = %q，期望 %q", want.ID, got.Name, want.Name)
		}
		if got.CreatedAt.IsZero() {
			t.Errorf("域名 %q 的 CreatedAt 未填", want.Name)
		}
	}

	if got := byID[first.ID]; got.OwnerID == nil || *got.OwnerID != owner.ID {
		t.Errorf("带 owner 的域名解出来 OwnerID = %v，期望 %v", got.OwnerID, owner.ID)
	}
	if got := byID[second.ID]; got.OwnerID != nil {
		t.Errorf("无 owner 的域名解出来 OwnerID = %v，期望 nil", got.OwnerID)
	}

	// 排序是 ListAll 的契约（快照按它建表，失败信息里域名有序更好读）
	aa := indexOfDomain(list, first.Name)
	zz := indexOfDomain(list, second.Name)
	if aa < 0 || zz < 0 {
		t.Fatalf("两个测试域名都应出现在结果里：%q=%d, %q=%d", first.Name, aa, second.Name, zz)
	}
	if aa > zz {
		t.Errorf("ListAll 应当按域名升序：%q 排在第 %d 位，%q 排在第 %d 位", first.Name, aa, second.Name, zz)
	}
}

// indexOfDomain 返回某域名在列表中的下标；不存在返回 -1。
func indexOfDomain(list []domain.Domain, name string) int {
	for i, d := range list {
		if d.Name == name {
			return i
		}
	}
	return -1
}
