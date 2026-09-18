package httpx

import (
	"encoding/json/v2"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"ashen-courier/internal/domain"
)

// TestMain 把默认 slog 换成丢弃器：WriteDomainError 在 5xx 分支会打 error 日志，
// 不换掉的话测试输出会被这些噪音淹没。
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// decodeErrorBody 解析统一错误体，顺带证明错误响应是合法 JSON。
func decodeErrorBody(t *testing.T, rr *httptest.ResponseRecorder) ErrorBody {
	t.Helper()

	var body ErrorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("错误响应不是合法 JSON：%v（%q）", err, rr.Body.String())
	}
	return body
}

// TestWriteDomainError 守住 F2：多个哨兵同时成立时，「可重试」必须压过「业务冲突」。
func TestWriteDomainError(t *testing.T) {
	t.Parallel()

	conflictErr := domain.Conflict("short_code", "abc1234")

	tests := []struct {
		name           string
		err            error
		wantStatus     int
		wantCode       string
		wantRetryAfter string
	}{
		{
			// service.Create 在自动短码连续冲突后返回的正是这个组合：
			// 用户没有提供短码，所以不能说「该短链已被占用，请换一个」。
			name:           "自动短码耗尽：可重试压过业务冲突",
			err:            errors.Join(domain.ErrUnavailable, conflictErr),
			wantStatus:     http.StatusServiceUnavailable,
			wantCode:       "unavailable",
			wantRetryAfter: "2",
		},
		{
			name:       "纯冲突仍是 409",
			err:        conflictErr,
			wantStatus: http.StatusConflict,
			wantCode:   "conflict",
		},
		{
			name:       "字段级校验是 422",
			err:        domain.Invalid("target_url", "仅支持 http/https 链接"),
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "invalid_url",
		},
		{
			name:       "脏数据仍是 500",
			err:        domain.ErrInternal,
			wantStatus: http.StatusInternalServerError,
			wantCode:   "internal",
		},
		{
			name:           "依赖不可用是 503 + Retry-After（PG 故障路径）",
			err:            domain.ErrUnavailable,
			wantStatus:     http.StatusServiceUnavailable,
			wantCode:       "unavailable",
			wantRetryAfter: "2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/api/links/abc1234", nil)
			rr := httptest.NewRecorder()

			WriteDomainError(rr, req, tc.err)

			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d（body=%s）", rr.Code, tc.wantStatus, rr.Body.String())
			}
			if got := rr.Header().Get("Retry-After"); got != tc.wantRetryAfter {
				t.Fatalf("Retry-After = %q, want %q", got, tc.wantRetryAfter)
			}
			if body := decodeErrorBody(t, rr); body.Error.Code != tc.wantCode {
				t.Fatalf("error.code = %q, want %q", body.Error.Code, tc.wantCode)
			}
		})
	}
}

// decodeProbe 与 createLinkRequest 同形，只保留一个字段用于观察解码结果。
type decodeProbe struct {
	TargetURL string `json:"target_url"`
}

// TestDecodeJSON 覆盖请求体解码的四条分支：正常、未知字段、重复键/类型错误、超长。
func TestDecodeJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantOK     bool
		wantStatus int
		wantCode   string
	}{
		{
			name:   "已知字段正常解析",
			body:   `{"target_url":"https://example.com/x"}`,
			wantOK: true,
		},
		{
			name:       "未知字段被拒（字段名拼错不再是静默忽略）",
			body:       `{"target_url":"https://example.com/x","titel":"标题"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_json",
		},
		{
			name:       "重复键被拒",
			body:       `{"target_url":"a","target_url":"b"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_json",
		},
		{
			name:       "类型不匹配被拒",
			body:       `{"target_url":123}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_json",
		},
		{
			name:       "畸形 JSON 被拒",
			body:       `{`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_json",
		},
		{
			name:       "超过上限的请求体是 413",
			body:       `{"target_url":"` + strings.Repeat("a", maxBodyBytes) + `"}`,
			wantStatus: http.StatusRequestEntityTooLarge,
			wantCode:   "body_too_large",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodPost, "/api/links", strings.NewReader(tc.body))
			rr := httptest.NewRecorder()

			got, ok := DecodeJSON[decodeProbe](rr, req)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v（status=%d body=%s）", ok, tc.wantOK, rr.Code, rr.Body.String())
			}
			if tc.wantOK {
				if got.TargetURL == "" {
					t.Fatal("解析成功却没有拿到字段值")
				}
				return
			}
			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d（body=%s）", rr.Code, tc.wantStatus, rr.Body.String())
			}
			if body := decodeErrorBody(t, rr); body.Error.Code != tc.wantCode {
				t.Fatalf("error.code = %q, want %q", body.Error.Code, tc.wantCode)
			}
		})
	}
}
