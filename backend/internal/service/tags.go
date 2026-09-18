package service

import (
	"fmt"
	"strings"

	"ashen-courier/internal/domain"
)

// 标签的边界。
const (
	// MaxTags 是单条短链的标签数上限。
	MaxTags = 10
	// MaxTagLength 是单个标签的字符数上限。
	MaxTagLength = 32
)

// normalizeTags 校验并规范化标签：去空白、丢空项、折叠为小写、按小写去重，
// 并保留首次出现的顺序。
//
// ⚠️ 统一转小写是与 PLAN-NEXT「保留原大小写」的一处有意偏离：
// 列表筛选走 `tags @> ARRAY[$1]`（GIN 索引加速），而**数组包含是大小写敏感的**。
// 若原样保留大小写，用户按 `ops` 就筛不到标了 `Ops` 的那条链接 ——
// 与「按小写比较」的契约直接矛盾。要么两者取其一，这里选了「比较一致」。
//
// 返回 nil 表示「没有标签」（空输入或全被过滤掉），调用方按空数组落库。
func normalizeTags(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		tag := strings.ToLower(strings.TrimSpace(item))
		if tag == "" {
			continue
		}
		if len([]rune(tag)) > MaxTagLength {
			return nil, domain.Invalid("tags", fmt.Sprintf("单个标签不能超过 %d 个字符", MaxTagLength))
		}
		if _, dup := seen[tag]; dup {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}

	if len(out) > MaxTags {
		return nil, domain.Invalid("tags", fmt.Sprintf("标签最多 %d 个", MaxTags))
	}
	return out, nil
}

// normalizeTagFilter 规范化筛选用的单个标签。
// 与写入侧同一套规则，否则会出现「存进去能筛到、手打筛不到」的不一致。
func normalizeTagFilter(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}
