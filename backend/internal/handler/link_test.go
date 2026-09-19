package handler

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"uuid"

	"ashen-courier/internal/domain"
	"ashen-courier/internal/httpx"
	"ashen-courier/internal/service"
)

// ---- 测试替身：手写，不引 mock 库（与 service / worker 的既有测试一致）----

// stubLinkRepo 只实现被测代码真正走到的方法。
//
// 内嵌 domain.LinkRepository 是刻意的：没实现的方法保留 nil 接口值，
// 一旦被测路径意外走到那里就会立刻 panic（而不是悄悄返回零值把断言蒙混过去）。
type stubLinkRepo struct {
	domain.LinkRepository
	getByCode   func(ctx context.Context, code string) (*domain.Link, error)
	listByOwner func(ctx context.Context, filter domain.LinkFilter) ([]domain.Link, domain.LinkCursor, error)
}

func (r *stubLinkRepo) GetByCode(ctx context.Context, code string) (*domain.Link, error) {
	return r.getByCode(ctx, code)
}

func (r *stubLinkRepo) ListByOwner(ctx context.Context, filter domain.LinkFilter) ([]domain.Link, domain.LinkCursor, error) {
	return r.listByOwner(ctx, filter)
}

// stubDeltas 是 domain.ClickDeltaBatchReader 的替身：记录被问过的短码，并可注入读失败。
type stubDeltas struct {
	byCode map[string]int64
	err    error
	asked  [][]string
}

func (d *stubDeltas) PendingDeltas(_ context.Context, codes []string) (map[string]int64, error) {
	d.asked = append(d.asked, codes)
	if d.err != nil {
		return nil, d.err
	}
	return d.byCode, nil
}

// stubCache 是「永远不会命中」的缓存：测试关心的是仓储与鉴权路径，
// 缓存写失败/未命中都不该影响它们的结论（与生产里 Redis 故障降级为直查 PG 一致）。
type stubCache struct {
	misses  int
	puts    int
	missing int
	evicted int
}

func (c *stubCache) Get(_ context.Context, code string) (*domain.CachedLink, error) {
	c.misses++
	return nil, fmt.Errorf("stub cache: %q: %w", code, domain.ErrCacheMiss)
}

func (c *stubCache) Put(context.Context, *domain.CachedLink, time.Duration) error {
	c.puts++
	return nil
}

func (c *stubCache) PutMissing(context.Context, string, time.Duration) error {
	c.missing++
	return nil
}

func (c *stubCache) Evict(context.Context, ...string) error {
	c.evicted++
	return nil
}

// stubClicks 是 domain.ClickRepository 的占位实现：路由表用例不打聚合，
// 内嵌 nil 接口保证「不小心走到聚合」时立刻 panic 而不是静默返回零值。
type stubClicks struct {
	domain.ClickRepository
}

// stubDelta 让统计路径读到一个确定的 0 增量。
type stubDelta struct {
	domain.ClickDeltaReader
}

func (stubDelta) PendingDelta(context.Context, string) (int64, error) { return 0, nil }

// okProbe 让 /healthz 稳定返回 200，免得路由表用例依赖真实依赖探针。
type okProbe struct{}

func (okProbe) Report(context.Context) httpx.HealthReport {
	return httpx.HealthReport{Status: "ok", Postgres: "ok", Redis: "ok"}
}

// stubUsers 供 service.NewAuth 使用。路由表用例刻意都不带有效令牌
// （带令牌的鉴权语义由 service/auth 的单测覆盖），因此这些方法不会被调用。
type stubUsers struct {
	domain.UserRepository
}

// newTestShortener 造一个只依赖仓储与空缓存的 Shortener。
func newTestShortener(repo domain.LinkRepository) *service.Shortener {
	return service.NewShortener(repo, &stubCache{}, nil, service.ShortenerConfig{
		BaseURL:     "https://s.example",
		CacheTTL:    time.Minute,
		NegativeTTL: time.Minute,
	})
}

// errCode 解析统一错误体里的 code；不是合法 JSON 直接判失败。
func errCode(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()

	var body httpx.ErrorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON：%v（%q）", err, rr.Body.String())
	}
	return body.Error.Code
}

// patchRequest 构造一个 PATCH 请求并带上 code 路径参数。
func patchRequest(t *testing.T, code string) *http.Request {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/api/links/"+code, nil)
	r.SetPathValue("code", code)
	return r
}

// TestBuildPatch 守住「状态码语义」的集中地：跨字段冲突、删除只能走 DELETE、
// 空 patch、以及「不许把已过期链接改回 active」。
func TestBuildPatch(t *testing.T) {
	t.Parallel()

	expired := time.Now().Add(-time.Hour).UTC()
	future := time.Now().Add(time.Hour).UTC()

	tests := []struct {
		name       string
		link       domain.Link
		req        updateLinkRequest
		wantOK     bool
		wantStatus int
		wantCode   string
		// wantStatusPatch 仅在 wantOK 时校验：补丁里的 status 是否被设成该值。
		wantStatusPatch *domain.LinkStatus
		// wantPassword 仅在 wantOK 时校验：补丁里的口令字段。handler 只透传，
		// 摘要化发生在 service 层，所以这里看到的就是请求里那个字符串。
		wantPassword *string
		// wantPatchEmpty 只在「在任何字段被写入之前就拒绝」的用例上为 true。
		// expired_link 不适用：那时 patch.Status 已经赋值，但 handler 在 ok=false 时
		// 直接返回、不会调用 Update —— 拒绝的补丁不允许被使用，而不是不允许非空。
		wantPatchEmpty bool
	}{
		{
			name:       "expires_at 与 clear_expires 同时传：422 invalid_expires_at",
			link:       domain.Link{ShortCode: "abc1234"},
			req:        updateLinkRequest{ExpiresAt: &future, ClearExpires: true},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "invalid_expires_at",
			// 两个字段都不该被写进补丁：冲突检查在赋值之前
			wantPatchEmpty: true,
		},
		{
			name:       "status=deleted 被拦：删除只能走 DELETE",
			link:       domain.Link{ShortCode: "abc1234"},
			req:        updateLinkRequest{Status: new("deleted")},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "invalid_status",
			// Status 没被赋值（删除分支在赋值之前返回）
			wantPatchEmpty: true,
		},
		{
			name:           "空 patch：422 empty_patch",
			link:           domain.Link{ShortCode: "abc1234"},
			req:            updateLinkRequest{},
			wantStatus:     http.StatusUnprocessableEntity,
			wantCode:       "empty_patch",
			wantPatchEmpty: true,
		},
		{
			name:       "已过期链接改回 active：422 expired_link",
			link:       domain.Link{ShortCode: "abc1234", ExpiresAt: &expired},
			req:        updateLinkRequest{Status: new("active")},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "expired_link",
		},
		{
			// 同时给了未来的过期时间：链接确实会被续期，放行
			name:            "已过期链接 + 新的 expires_at：放行",
			link:            domain.Link{ShortCode: "abc1234", ExpiresAt: &expired},
			req:             updateLinkRequest{Status: new("active"), ExpiresAt: &future},
			wantOK:          true,
			wantStatusPatch: new(domain.LinkStatusActive),
		},
		{
			// clear_expires 把过期时间清空，等于永久有效 → 也放行
			name:            "已过期链接 + clear_expires：放行",
			link:            domain.Link{ShortCode: "abc1234", ExpiresAt: &expired},
			req:             updateLinkRequest{Status: new("active"), ClearExpires: true},
			wantOK:          true,
			wantStatusPatch: new(domain.LinkStatusActive),
		},
		{
			// 没动 status 就不该触发过期校验：改标题是允许的
			name:   "已过期链接只改标题：放行",
			link:   domain.Link{ShortCode: "abc1234", ExpiresAt: &expired},
			req:    updateLinkRequest{Title: new("新标题")},
			wantOK: true,
		},
		{
			name:       "password 与 clear_password 同时传：422 invalid_password",
			link:       domain.Link{ShortCode: "abc1234"},
			req:        updateLinkRequest{Password: new("new-pass-1234"), ClearPassword: true},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "invalid_password",
			// 冲突检查在赋值之前：两个字段都不该进补丁
			wantPatchEmpty: true,
		},
		{
			name:       "password 传空串：422 invalid_password（清除请用 clear_password）",
			link:       domain.Link{ShortCode: "abc1234"},
			req:        updateLinkRequest{Password: new("")},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "invalid_password",
			// 空串不接受，避免「空串 = 清除」与「不传 = 不动」两种都能清空的写法
			wantPatchEmpty: true,
		},
		{
			name:         "设置口令：透传给 service（摘要化在那一层）",
			link:         domain.Link{ShortCode: "abc1234"},
			req:          updateLinkRequest{Password: new("new-pass-1234")},
			wantOK:       true,
			wantPassword: new("new-pass-1234"),
		},
		{
			name:         "clear_password：补丁里是空串（由 service 原样落库）",
			link:         domain.Link{ShortCode: "abc1234", PasswordHash: "$2a$12$placeholder", PasswordProtected: true},
			req:          updateLinkRequest{ClearPassword: true},
			wantOK:       true,
			wantPassword: new(""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rr := httptest.NewRecorder()
			h := &linkHandler{}
			link := tt.link

			patch, ok := h.buildPatch(rr, patchRequest(t, link.ShortCode), &link, tt.req)

			if ok != tt.wantOK {
				t.Fatalf("buildPatch ok=%v，期望 %v（响应 %d %s）",
					ok, tt.wantOK, rr.Code, rr.Body.String())
			}
			if !tt.wantOK {
				if rr.Code != tt.wantStatus {
					t.Errorf("状态码 %d，期望 %d", rr.Code, tt.wantStatus)
				}
				if got := errCode(t, rr); got != tt.wantCode {
					t.Errorf("错误码 %q，期望 %q", got, tt.wantCode)
				}
				// 被拒绝的补丁不允许被使用：handler 在 ok=false 时直接返回、不落库。
				// 只有「在任何字段赋值之前就拒绝」的用例才额外要求补丁为空。
				if tt.wantPatchEmpty && !patch.IsEmpty() {
					t.Errorf("补丁在早期拒绝路径上仍非空：%+v", patch)
				}
				return
			}
			if patch.IsEmpty() {
				t.Error("通过的补丁不该为空")
			}
			if tt.wantStatusPatch != nil {
				if patch.Status == nil || *patch.Status != *tt.wantStatusPatch {
					t.Errorf("patch.Status=%v，期望 %v", patch.Status, *tt.wantStatusPatch)
				}
			}
			if tt.wantPassword != nil {
				if patch.PasswordHash == nil || *patch.PasswordHash != *tt.wantPassword {
					t.Errorf("patch.PasswordHash=%v，期望 %q", patch.PasswordHash, *tt.wantPassword)
				}
			}
		})
	}
}

// TestLoadAuthorized 守住「404 不泄露存在性」与「403/404 的分工」——
// 这两条是审计里反复咬人的地方。
func TestLoadAuthorized(t *testing.T) {
	t.Parallel()

	ownerID := uuid.NewV7()
	otherID := uuid.NewV7()
	deleted := domain.Link{ShortCode: "del1234", Status: domain.LinkStatusDeleted, OwnerID: &ownerID}
	owned := domain.Link{ShortCode: "own1234", Status: domain.LinkStatusActive, OwnerID: &ownerID}
	anonymous := domain.Link{ShortCode: "anon123", Status: domain.LinkStatusActive}
	// 持钥的匿名链接：manage_key 明文是 "test-manage-key"，库里只存摘要
	keyed := domain.Link{
		ShortCode: "key1234",
		Status:    domain.LinkStatusActive,
		KeyHash:   service.HashManageKey("test-manage-key"),
	}

	tests := []struct {
		name       string
		link       *domain.Link
		repoErr    error
		actor      service.Actor
		manageKey  string
		forDetail  bool
		wantOK     bool
		wantStatus int
		wantCode   string
	}{
		{
			name:       "无权限 + forDetail：404（不泄露资源存在性）",
			link:       &owned,
			actor:      service.Actor{UserID: &otherID},
			forDetail:  true,
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "无权限 + 非 detail：403",
			link:       &owned,
			actor:      service.Actor{UserID: &otherID},
			forDetail:  false,
			wantStatus: http.StatusForbidden,
			wantCode:   "forbidden",
		},
		{
			name:       "已删除的短链：404（哪怕本人有权限）",
			link:       &deleted,
			actor:      service.Actor{UserID: &ownerID},
			forDetail:  false,
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "仓储报不存在：404",
			repoErr:    domain.NotFound("link", "abc1234"),
			actor:      service.Actor{},
			forDetail:  false,
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:      "所有者本人：放行",
			link:      &owned,
			actor:     service.Actor{UserID: &ownerID},
			forDetail: true,
			wantOK:    true,
		},
		{
			// 匿名链接没有 owner_id，只能靠 manage_key 通过 —— 认领接口依赖这条
			name:       "匿名链接 + 空密钥：403（非 detail）",
			link:       &anonymous,
			actor:      service.Actor{},
			forDetail:  false,
			wantStatus: http.StatusForbidden,
			wantCode:   "forbidden",
		},
		{
			name:      "匿名链接 + 正确 manage_key：放行",
			link:      &keyed,
			manageKey: "test-manage-key",
			forDetail: true,
			wantOK:    true,
		},
		{
			name:       "匿名链接 + 错误 manage_key：404（detail 不泄露存在性）",
			link:       &keyed,
			manageKey:  "wrong-manage-key",
			forDetail:  true,
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code := "abc1234"
			if tt.link != nil {
				code = tt.link.ShortCode
			}
			repo := &stubLinkRepo{getByCode: func(context.Context, string) (*domain.Link, error) {
				if tt.repoErr != nil {
					return nil, tt.repoErr
				}
				return tt.link, nil
			}}
			shortener := newTestShortener(repo)

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/links/"+code, nil)
			r.SetPathValue("code", code)
			if tt.manageKey != "" {
				r.Header.Set(manageKeyHeader, tt.manageKey)
			}
			r = r.WithContext(withActor(r, tt.actor))

			rr := httptest.NewRecorder()
			link, ok := loadAuthorized(rr, r, shortener, tt.forDetail)

			if ok != tt.wantOK {
				t.Fatalf("loadAuthorized ok=%v，期望 %v（响应 %d %s）",
					ok, tt.wantOK, rr.Code, rr.Body.String())
			}
			if tt.wantOK {
				if link == nil {
					t.Fatal("放行时必须返回链接")
				}
				return
			}
			if rr.Code != tt.wantStatus {
				t.Errorf("状态码 %d，期望 %d", rr.Code, tt.wantStatus)
			}
			if got := errCode(t, rr); got != tt.wantCode {
				t.Errorf("错误码 %q，期望 %q", got, tt.wantCode)
			}
		})
	}
}

// withActor 把测试构造的身份塞进请求：生产路径上由 optionalAuth / requireAuth
// 中间件写 context，这里直接调用同一套私有 helper，避免重复实现鉴权上下文。
func withActor(r *http.Request, actor service.Actor) context.Context {
	if actor.UserID != nil {
		return withUserID(r.Context(), *actor.UserID)
	}
	return r.Context()
}
