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
