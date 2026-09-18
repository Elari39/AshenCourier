package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestClientIP 守住「只信 nginx 注入的 X-Real-IP，绝不解析 X-Forwarded-For」。
//
// X-Forwarded-For 是客户端可以随便伪造的头：一旦按它取 IP，
// 限流键与点击明细里的 IP 就都能被攻击者任意指定（刷量、嫁祸、绕过配额）。
func TestClientIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		trustProxy bool
		headers    map[string]string
		remoteAddr string
		want       string
	}{
		{
			name:       "TRUST_PROXY=true：取 X-Real-IP",
			trustProxy: true,
			headers:    map[string]string{"X-Real-IP": "203.0.113.7"},
			remoteAddr: "172.20.0.5:54321",
			want:       "203.0.113.7",
		},
		{
			name:       "TRUST_PROXY=true：伪造的 X-Forwarded-For 必须被忽略",
			trustProxy: true,
			headers: map[string]string{
				"X-Forwarded-For": "1.2.3.4",
				"X-Real-IP":       "203.0.113.7",
			},
			remoteAddr: "172.20.0.5:54321",
			want:       "203.0.113.7",
		},
		{
			name:       "TRUST_PROXY=true 但只有 X-Forwarded-For：回落到 RemoteAddr",
			trustProxy: true,
			headers:    map[string]string{"X-Forwarded-For": "1.2.3.4"},
			remoteAddr: "172.20.0.5:54321",
			want:       "172.20.0.5",
		},
		{
			name:       "TRUST_PROXY=true 但 X-Real-IP 不是合法 IP：回落到 RemoteAddr",
			trustProxy: true,
			headers:    map[string]string{"X-Real-IP": "not-an-ip"},
			remoteAddr: "172.20.0.5:54321",
			want:       "172.20.0.5",
		},
		{
			name:       "TRUST_PROXY=true：X-Real-IP 两侧空白被裁剪",
			trustProxy: true,
			headers:    map[string]string{"X-Real-IP": "  203.0.113.7  "},
			remoteAddr: "172.20.0.5:54321",
			want:       "203.0.113.7",
		},
		{
			name:       "TRUST_PROXY=false：哪怕带了 X-Real-IP 也只认 RemoteAddr",
			trustProxy: false,
			headers:    map[string]string{"X-Real-IP": "203.0.113.7"},
			remoteAddr: "172.20.0.5:54321",
			want:       "172.20.0.5",
		},
		{
			name:       "RemoteAddr 没有端口：按整串解析",
			trustProxy: false,
			remoteAddr: "203.0.113.7",
			want:       "203.0.113.7",
		},
		{
			name:       "IPv6 RemoteAddr：规范化后返回",
			trustProxy: false,
			remoteAddr: "[2001:db8::1]:443",
			want:       "2001:db8::1",
		},
		{
			name:       "非法 RemoteAddr：返回空串（而不是把垃圾写进明细）",
			trustProxy: false,
			remoteAddr: "garbage",
			want:       "",
		},
		{
			name:       "IPv6 的 X-Real-IP：规范化后返回",
			trustProxy: true,
			headers:    map[string]string{"X-Real-IP": "2001:0db8:0000:0000:0000:0000:0000:0001"},
			remoteAddr: "172.20.0.5:54321",
			want:       "2001:db8::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remoteAddr
			for k, v := range tt.headers {
				r.Header.Set(k, v)
			}

			if got := ClientIP(r, tt.trustProxy); got != tt.want {
				t.Errorf("ClientIP=%q，期望 %q", got, tt.want)
			}
		})
	}
}

// TestStatusRecorder 守住访问日志的状态码与字节数统计。
//
// 关键点：初始 status 就是 200，所以「是否已经写过头」必须用独立的 wrote 标志，
// 否则「先 Write（隐式 200）再 WriteHeader(500)」会被记成 500，而客户端实际收到 200。
func TestStatusRecorder(t *testing.T) {
	t.Parallel()

	t.Run("隐式 200", func(t *testing.T) {
		t.Parallel()

		rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
		if _, err := rec.Write([]byte("hi")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if rec.status != http.StatusOK || !rec.wrote {
			t.Errorf("status=%d wrote=%v，期望 200 / true", rec.status, rec.wrote)
		}
		if rec.bytes != 2 {
			t.Errorf("bytes=%d，期望 2", rec.bytes)
		}
	})

	t.Run("重复 WriteHeader 只记第一次", func(t *testing.T) {
		t.Parallel()

		rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
		rec.WriteHeader(http.StatusCreated)
		rec.WriteHeader(http.StatusInternalServerError)
		if rec.status != http.StatusCreated {
			t.Errorf("status=%d，期望 201（第二次 WriteHeader 必须被忽略）", rec.status)
		}
	})

	t.Run("先 Write 再 WriteHeader(500)：仍记 200", func(t *testing.T) {
		t.Parallel()

		rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
		if _, err := rec.Write([]byte("ok")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		rec.WriteHeader(http.StatusInternalServerError)
		if rec.status != http.StatusOK {
			t.Errorf("status=%d，期望 200（隐式 200 之后的状态码不能被改写）", rec.status)
		}
	})

	t.Run("多次 Write 累加字节", func(t *testing.T) {
		t.Parallel()

		rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
		for _, part := range []string{"abc", "de", "f"} {
			if _, err := rec.Write([]byte(part)); err != nil {
				t.Fatalf("Write: %v", err)
			}
		}
		if rec.bytes != 6 {
			t.Errorf("bytes=%d，期望 6", rec.bytes)
		}
	})
}

// TestRetryAfterHeader 守住 Retry-After 的秒数换算：向上取整、下限 1。
// 下限 1 不是洁癖：`Retry-After: 0` 意味着「立刻重试」，等于没退避。
func TestRetryAfterHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   time.Duration
		want string
	}{
		{1200 * time.Millisecond, "2"},
		{0, "1"},
		{-time.Second, "1"},
		{500 * time.Millisecond, "1"},
		{time.Second, "1"},
		{1500 * time.Millisecond, "2"},
		{3 * time.Second, "3"},
	}

	for _, tt := range tests {
		if got := RetryAfterHeader(tt.in); got != tt.want {
			t.Errorf("RetryAfterHeader(%v)=%q，期望 %q", tt.in, got, tt.want)
		}
	}
}

// TestSanitizeRequestID 守住请求 ID 的白名单：控制字符与超长一律丢弃。
//
// 上游传来的 X-Request-Id 会进日志与响应头；不校验就能往日志里注入换行、
// 伪造日志行，或者用一个超长值把日志撑爆。
func TestSanitizeRequestID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "合法值原样返回", in: "01J8Z0M6WQ3F5T9K", want: "01J8Z0M6WQ3F5T9K"},
		{name: "两侧空白被裁剪", in: "  abc123  ", want: "abc123"},
		{name: "空串", in: "", want: ""},
		{name: "只有空白", in: "   ", want: ""},
		{name: "含换行（日志注入）", in: "abc\ndef", want: ""},
		{name: "含制表符", in: "abc\tdef", want: ""},
		{name: "含空格（非打印区间内）", in: "abc def", want: ""},
		{name: "含非 ASCII（中文）", in: "请求-id", want: ""},
		{name: "超长（129 个合法字符）", in: strings.Repeat("a", 129), want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := sanitizeRequestID(tt.in); got != tt.want {
				t.Errorf("sanitizeRequestID(%q)=%q，期望 %q", tt.in, got, tt.want)
			}
		})
	}

	t.Run("128 字节的合法值仍然接受", func(t *testing.T) {
		t.Parallel()

		in := strings.Repeat("a", 128)
		if got := sanitizeRequestID(in); got != in {
			t.Errorf("128 字节应当被接受，实际得到 %d 字节", len(got))
		}
	})
}
