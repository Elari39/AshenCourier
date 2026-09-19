package redis

import (
	"strings"
	"testing"
	"uuid"
)

// TestKeyConstruction 钉住键的构造。
//
// 这些前缀是跨进程契约：worker 回刷时按 clicks:cnt:{code} 取值、api 写增量时按
// 同一个键 INCR，一旦某处改了前缀而另一次没改，症状是「点击计数永远回刷不到 PG」
// —— 而两边的单测都还是绿的（各自自洽）。这里把字面量写死，改动必须是有意的。
//
// 链接缓存为什么是 v2 且必须带域：v1 只按短码拼键，而分域之后
// 「同一个短码」在不同域下是两条不同的短链，负缓存更会因此把「不属于这个域」
// 误记成「不存在」——那是短链在自己域上 404 的成因（详见 domain.LinkRef）。
//
// 另外守住负缓存键里的 "miss:" 分段：短码字符集含 '-'，若把域与短码直接相接，
// 短码 "a-b" 与「域 a、短码 b」会撞车。
func TestKeyConstruction(t *testing.T) {
	t.Parallel()

	domA := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	domB := uuid.MustParse("22222222-2222-4222-8222-222222222222")

	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "默认域的正向键", got: LinkKey(nil, "abc1234"), want: "link:v2:-:abc1234"},
		{name: "默认域的负缓存键", got: MissKey(nil, "abc1234"), want: "link:v2:miss:-:abc1234"},
		{
			name: "自定义域的正向键",
			got:  LinkKey(&domA, "abc1234"),
			want: "link:v2:11111111-1111-4111-8111-111111111111:abc1234",
		},
		{
			name: "自定义域的负缓存键",
			got:  MissKey(&domA, "abc1234"),
			want: "link:v2:miss:11111111-1111-4111-8111-111111111111:abc1234",
		},
		{name: "计数增量键", got: ClickCounterKey("abc1234"), want: "clicks:cnt:abc1234"},
		{name: "dirty 集合键", got: dirtySetKey, want: "clicks:dirty"},
		{name: "Stream 键", got: streamKey, want: "clicks:stream"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.got != tt.want {
				t.Errorf("键 = %q，期望 %q", tt.got, tt.want)
			}
		})
	}

	// 1) 域必须真的参与拼键：同码不同域不能共用条目
	if LinkKey(&domA, "abc1234") == LinkKey(&domB, "abc1234") {
		t.Fatal("两个域下同一个短码的正向键撞车了：分域会读到别人的链接")
	}
	if MissKey(&domA, "abc1234") == MissKey(nil, "abc1234") {
		t.Fatal("自定义域与默认域的负缓存键撞车了：跨域探测会污染负缓存")
	}
	if MissKey(nil, "abc1234") == MissKey(nil, "abc1235") {
		t.Fatal("不同短码的负缓存键撞车了")
	}

	// 2) 短码含 '-' 时正负缓存键不能撞车
	if LinkKey(nil, "-abc") == MissKey(nil, "abc") {
		t.Fatal("短码 \"-abc\" 的正向键与 \"abc\" 的负缓存键撞车了")
	}

	// 3) 域与短码之间必须有分隔符，否则「域 + 短码」的拼接有歧义
	if strings.Contains(defaultDomainKey, ":") {
		t.Fatalf("默认域占位符 %q 不能含冒号：它会让键的分段产生歧义", defaultDomainKey)
	}
	if LinkKey(nil, "xabc") == LinkKey(nil, "abc") {
		t.Fatal("不同短码的正向键撞车了")
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
