package redis

import (
	"encoding/json/v2"
	"fmt"
	"uuid"
)

// marshal / unmarshal 是本包访问 JSON 的唯一入口。
//
// 用 encoding/json/v2 而不是 v1：v2 的默认更安全 —— 拒绝非法 UTF-8、
// 拒绝重复键、nil slice/map 编码为空数组/对象，缓存内容不是外部契约，
// 可以直接吃 v2 的严格默认值。
func marshal(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("store.redis: json marshal: %w", err)
	}
	return string(b), nil
}

func unmarshal(data string, out any) error {
	if err := json.Unmarshal([]byte(data), out); err != nil {
		return fmt.Errorf("store.redis: json unmarshal: %w", err)
	}
	return nil
}

// parseUUID 解析缓存里的 UUID 字符串。
func parseUUID(raw string) (uuid.UUID, error) {
	u, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil(), fmt.Errorf("store.redis: parse uuid %q: %w", raw, err)
	}
	return u, nil
}

// formatUUID 把可空 UUID 编码成字符串；nil 得到空串（缓存线格式里空串 = 默认域名）。
//
// 传指针而不是值：`domain_id` 为 NULL 表示「默认域名」，这在分域模型里是一个
// 有意义的状态，不是「缺失」。用零 UUID 去表示它会让 nil 与
// uuid.Nil() 两种写法混在代码里，而它们在这里的含义并不相同。
func formatUUID(u *uuid.UUID) string {
	if u == nil {
		return ""
	}
	return u.String()
}

// parseOptionalUUID 解析可选 UUID；空串得到 nil（= 默认域名）。
func parseOptionalUUID(raw string) (*uuid.UUID, error) {
	if raw == "" {
		return nil, nil
	}
	u, err := parseUUID(raw)
	if err != nil {
		return nil, err
	}
	return &u, nil
}
