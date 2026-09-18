package redis

import (
	"testing"
)

// TestKeyConstruction 钉住键的构造。
//
// 这些前缀是跨进程契约：worker 回刷时按 clicks:cnt:{code} 取值、api 写增量时按
// 同一个键 INCR，一旦某处改了前缀而另一次没改，症状是「点击计数永远回刷不到 PG」
// —— 而两边的单测都还是绿的（各自自洽）。这里把字面量写死，改动必须是有意的。
//
// 另外守住负缓存键里的 "miss:" 分段：短码字符集含 '-'，早先用 link:v1:-{code}
// 会让短码 "-abc" 的正向键与短码 "abc" 的负缓存键撞车。
func TestKeyConstruction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "正向缓存键", got: LinkKey("abc1234"), want: "link:v1:abc1234"},
		{name: "负缓存键", got: MissKey("abc1234"), want: "link:v1:miss:abc1234"},
		{name: "计数增量键", got: ClickCounterKey("abc1234"), want: "clicks:cnt:abc1234"},
		{name: "dirty 集合键", got: dirtySetKey, want: "clicks:dirty"},
		{name: "Stream 键", got: streamKey, want: "clicks:stream"},
		// 短码含 '-' 时正负缓存键不能撞车
		{name: "含连字符的正向键", got: LinkKey("-abc"), want: "link:v1:-abc"},
		{name: "含连字符的负缓存键", got: MissKey("abc"), want: "link:v1:miss:abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.got != tt.want {
				t.Errorf("键 = %q，期望 %q", tt.got, tt.want)
			}
		})
	}

	if LinkKey("-abc") == MissKey("abc") {
		t.Fatal("短码 \"-abc\" 的正向键与 \"abc\" 的负缓存键撞车了")
	}
}

// TestDeltaValue 守住 MGET 返回值的解析：go-redis 默认给 string，
// 但换编解码器时可能是 []byte 或 int64；解析不出来必须返回 false
// （调用方按 0 处理并记 debug），而不是把垃圾当成计数。
func TestDeltaValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		raw    any
		want   int64
		wantOK bool
	}{
		{name: "string", raw: "42", want: 42, wantOK: true},
		{name: "负数", raw: "-3", want: -3, wantOK: true},
		{name: "[]byte", raw: []byte("7"), want: 7, wantOK: true},
		{name: "int64", raw: int64(9), want: 9, wantOK: true},
		{name: "非数字", raw: "abc", wantOK: false},
		{name: "空串", raw: "", wantOK: false},
		{name: "nil", raw: nil, wantOK: false},
		{name: "未知类型", raw: 3.5, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := deltaValue(tt.raw)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v，期望 %v", ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("值 = %d，期望 %d", got, tt.want)
			}
		})
	}
}
