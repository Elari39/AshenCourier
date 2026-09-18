package shortcode

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestReservedSetContents 断言保留字表的关键条目，防止误删导致前端路由被短码吃掉。
// 新增前端顶级路由时，请同步在下面的 wantContains 里补一行。
func TestReservedSetContents(t *testing.T) {
	t.Parallel()

	// 这些是最不能少的一组：少任何一个都会造成真实故障
	wantContains := []string{
		// 基础设施
		"api", "assets", "healthz", "static",
		"favicon.ico", "robots.txt", "sitemap.xml", "index.html",
		// 前端 SPA 顶级路由（与 frontend/src/router/index.ts 严格对应）
		"login", "register", "dashboard", "links",
	}

	all := ReservedCodes()
	for _, want := range wantContains {
		if !IsReserved(want) {
			t.Errorf("保留字表缺少 %q", want)
		}
		if slices.Index(all, want) < 0 {
			t.Errorf("ReservedCodes() 未包含 %q", want)
		}
	}

	// ReservedCodes 必须已排序且无重复
	for i := 1; i < len(all); i++ {
		if all[i-1] >= all[i] {
			t.Fatalf("ReservedCodes() 未按字典序排列或有重复：%q >= %q", all[i-1], all[i])
		}
	}

	if len(all) < 40 {
		t.Fatalf("保留字表条目过少（%d），疑似被误删", len(all))
	}
}

func TestIsReserved(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code string
		want bool
	}{
		{"精确命中", "dashboard", true},
		{"大写也命中", "Dashboard", true},
		{"全大写也命中", "API", true},
		{"带点的静态文件", "robots.txt", true},
		{"空串不算保留", "", false},
		{"普通短码", "go-blog", false},
		{"含保留字但不是保留字", "dashboard2", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := IsReserved(tc.code); got != tc.want {
				t.Fatalf("IsReserved(%q) = %v, want %v", tc.code, got, tc.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		code    string
		wantErr error
	}{
		{"合法", "go-blog", nil},
		{"合法含下划线", "my_link_1", nil},
		{"合法全大写", "ABC", nil},
		{"太短", "ab", ErrTooShort},
		{"空串", "", ErrTooShort},
		{"太长", strings.Repeat("a", MaxLength+1), ErrTooLong},
		{"刚好最长", strings.Repeat("a", MaxLength), nil},
		{"非法字符-点", "go.blog", ErrBadChar},
		{"非法字符-斜杠", "go/blog", ErrBadChar},
		{"非法字符-中文", "短码abc", ErrBadChar},
		{"保留字", "login", ErrReserved},
		{"保留字大写", "LOGIN", ErrReserved},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := Validate(tc.code)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Validate(%q) 错误 = %v, want %v", tc.code, err, tc.wantErr)
			}
		})
	}
}

func TestIsValidShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code string
		want bool
	}{
		{"abc", true},
		{"ABC-123_xyz", true},
		{"ab", false},
		{strings.Repeat("a", 33), false},
		{"go.blog", false},
		// 与 Validate 的关键差异：保留字在形态层是「合法」的
		{"login", true},
	}

	for _, tc := range tests {
		t.Run(tc.code, func(t *testing.T) {
			t.Parallel()

			if got := IsValidShape(tc.code); got != tc.want {
				t.Fatalf("IsValidShape(%q) = %v, want %v", tc.code, got, tc.want)
			}
		})
	}
}

func TestGenerate(t *testing.T) {
	t.Parallel()

	seen := make(map[string]struct{}, 512)
	for range 512 {
		code, err := Generate()
		if err != nil {
			t.Fatalf("Generate() 返回错误：%v", err)
		}
		if len(code) != DefaultLength {
			t.Fatalf("Generate() 长度 = %d, want %d", len(code), DefaultLength)
		}
		if !IsValidShape(code) {
			t.Fatalf("Generate()=%q 形态非法", code)
		}
		if IsReserved(code) {
			t.Fatalf("Generate()=%q 撞上保留字", code)
		}
		if _, dup := seen[code]; dup {
			t.Fatalf("512 次生成出现重复短码 %q", code)
		}
		seen[code] = struct{}{}
	}
}
