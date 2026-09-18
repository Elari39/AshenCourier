package base62

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		n    uint64
	}{
		{"零", 0},
		{"一位下界", 1},
		{"一位上界", 61},
		{"两位下界", 62},
		{"两位次值", 63},
		{"跨位边界", 62*62 - 1},
		{"较大值", 0x0123456789abcdef},
		{"uint64 上界", math.MaxUint64},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			enc := Encode(tc.n)
			got, err := Decode(enc)
			if err != nil {
				t.Fatalf("Decode(%q) 返回错误：%v", enc, err)
			}
			if got != tc.n {
				t.Fatalf("往返不一致：Encode(%d)=%q → Decode=%d", tc.n, enc, got)
			}
		})
	}
}

func TestEncodeShape(t *testing.T) {
	t.Parallel()

	// 固定映射：顺序必须是 0-9 → A-Z → a-z，改字母表会破坏既有短码
	got := []string{Encode(0), Encode(9), Encode(10), Encode(35), Encode(36), Encode(61), Encode(62)}
	want := []string{"0", "9", "A", "Z", "a", "z", "10"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Encode 字母表第 %d 项：got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDecodeErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		wantErr error
	}{
		{"空串", "", ErrEmpty},
		{"小写非法字符", "ab$c", ErrInvalidChar},
		{"空格", "ab c", ErrInvalidChar},
		{"URL 安全字符也不接受", "ab-c", ErrInvalidChar},
		{"溢出", strings.Repeat("z", 12), ErrOverflow},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := Decode(tc.in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Decode(%q) 错误 = %v, 期望 %v", tc.in, err, tc.wantErr)
			}
		})
	}
}

func TestRandom(t *testing.T) {
	t.Parallel()

	t.Run("非法长度", func(t *testing.T) {
		t.Parallel()

		for _, n := range []int{-1, 0} {
			if _, err := Random(n); !errors.Is(err, ErrInvalidLength) {
				t.Fatalf("Random(%d) 错误 = %v, 期望 ErrInvalidLength", n, err)
			}
		}
	})

	t.Run("长度与字符集", func(t *testing.T) {
		t.Parallel()

		const n = 7
		seen := make(map[string]struct{}, 256)
		for range 256 {
			s, err := Random(n)
			if err != nil {
				t.Fatalf("Random(%d) 返回错误：%v", n, err)
			}
			if len(s) != n {
				t.Fatalf("Random(%d) 长度 = %d, want %d", n, len(s), n)
			}
			for i := range len(s) {
				if index(s[i]) < 0 {
					t.Fatalf("Random(%d)=%q 含非法字符 %q", n, s, s[i])
				}
			}
			if _, dup := seen[s]; dup {
				t.Fatalf("Random(%d) 在 256 次采样中出现重复值 %q", n, s)
			}
			seen[s] = struct{}{}
		}
	})
}

func BenchmarkEncode(b *testing.B) {
	for b.Loop() {
		_ = Encode(0x0123456789abcdef)
	}
}

func BenchmarkDecode(b *testing.B) {
	for b.Loop() {
		_, _ = Decode("3n9KfQ2")
	}
}

func BenchmarkRandom7(b *testing.B) {
	for b.Loop() {
		_, _ = Random(7)
	}
}
