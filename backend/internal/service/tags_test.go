package service

import (
	"errors"
	"strings"
	"testing"

	"ashen-courier/internal/domain"
)

// TestNormalizeTags 守住标签的三条规则：折叠小写、去重、限制数量与长度。
//
// 折叠小写不是审美选择：筛选走 `tags @> ARRAY[$1]`（数组包含大小写敏感），
// 保留大小写就会出现「存了 Ops、按 ops 筛不到」。
func TestNormalizeTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      []string
		want    []string
		wantErr bool
	}{
		{name: "空输入", in: nil, want: nil},
		{name: "只有空白项", in: []string{"", "   "}, want: []string{}},
		{name: "去空白并折叠小写", in: []string{"  Ops ", "Dev"}, want: []string{"ops", "dev"}},
		{name: "按小写去重且保留首次出现顺序", in: []string{"Ops", "ops", "DEV", "dev"}, want: []string{"ops", "dev"}},
		{name: "混合空项与重复项", in: []string{"", "ops", "  ", "ops"}, want: []string{"ops"}},
		{name: "正好 10 个", in: tenTags(), want: tenTags()},
		{name: "11 个超限", in: append(tenTags(), "extra"), wantErr: true},
		{name: "单个标签 32 字符（按 rune 算）", in: []string{strings.Repeat("a", 32)}, want: []string{strings.Repeat("a", 32)}},
		{name: "单个标签 33 字符超限", in: []string{strings.Repeat("a", 33)}, wantErr: true},
		{name: "中文标签按字符数而非字节数", in: []string{strings.Repeat("标", 32)}, want: []string{strings.Repeat("标", 32)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := normalizeTags(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("期望报错，实际得到 %v", got)
				}
				if _, ok := errors.AsType[*domain.InvalidInputError](err); !ok {
					t.Fatalf("错误类型 = %T，期望 *domain.InvalidInputError（要能被映射成 422）", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("不该报错：%v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("结果 = %v，期望 %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("结果 = %v，期望 %v", got, tt.want)
				}
			}
		})
	}
}

// TestNormalizeTagFilter 守住「筛选用与写入侧同一套规则」，
// 否则会出现「存进去能筛到、手打筛不到」的不一致。
func TestNormalizeTagFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{in: "Ops", want: "ops"},
		{in: "  DEV  ", want: "dev"},
		{in: "", want: ""},
		{in: "   ", want: ""},
	}

	for _, tt := range tests {
		if got := normalizeTagFilter(tt.in); got != tt.want {
			t.Errorf("normalizeTagFilter(%q) = %q，期望 %q", tt.in, got, tt.want)
		}
	}
}

// tenTags 返回正好 10 个合法标签。
func tenTags() []string {
	out := make([]string, 0, MaxTags)
	for i := range MaxTags {
		out = append(out, "tag"+string(rune('a'+i)))
	}
	return out
}
