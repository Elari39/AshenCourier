package geoip

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCountryOfReturnsEmptyForBadInput：非法输入一律空串，不 panic、不猜。
//
// 用零值 Locator 是刻意的：`country` 列在库里是可空的，而「没有配置库文件」的降级路径
// 就是拿一个没打开的 Locator 去解析。这条用例钉住的就是那条降级路径 ——
// 它必须在**没有库文件**的机器上也能跑（CI 就没有库文件）。
func TestCountryOfReturnsEmptyForBadInput(t *testing.T) {
	t.Parallel()

	var zero Locator
	cases := []struct {
		name string
		ip   string
	}{
		{"空串", ""},
		{"不是 IP", "not-an-ip"},
		{"带端口的地址", "203.0.113.7:8080"},
		{"只写了网段", "203.0.113.0/24"},
		{"localhost 主机名", "localhost"},
		{"IPv6 里混了非法段", "2001:db8::zz"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// 零值 Locator：没有 db，内部会在解析之前就返回空串
			if got := zero.CountryOf(tc.ip); got != "" {
				t.Errorf("CountryOf(%q) = %q，期望空串", tc.ip, got)
			}
			// 顺便断言「有 db 但 IP 非法」这条路径也走通：这里用真实打开过的
			// Locator 拿不到（CI 没有库文件），所以下面 GatedRealDatabase 里再补。
		})
	}
}

// TestNilLocatorIsSafe：「没配置库文件」在 cmd 层表达为 nil，这里钉住它不会 panic。
func TestNilLocatorIsSafe(t *testing.T) {
	t.Parallel()

	var locator *Locator
	if got := locator.CountryOf("203.0.113.7"); got != "" {
		t.Errorf("nil Locator 应返回空串，实际 %q", got)
	}
	if err := locator.Close(); err != nil {
		t.Errorf("nil Locator 的 Close 应返回 nil，实际 %v", err)
	}
}

// TestOpenFailsForMissingFile：打开不存在的文件必须报错（而不是给出一个「空解析器」）。
//
// 这条是给 cmd 层用的：它靠这个错误把「配置了路径但文件不在」记成一条 warn 并降级。
// 如果这里悄悄返回一个空 Locator，运维就会以为 GeoIP 已经生效了。
func TestOpenFailsForMissingFile(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "not-here.mmdb")
	if _, err := Open(missing); err == nil {
		t.Fatal("打开不存在的库文件应报错")
	}
}

// TestOpenFailsForNonDatabaseFile：路径存在但内容不是 mmdb，同样要报错。
// 只按「文件存在」判断的话，把 README 误配成 GEOIP_DB_PATH 也会「成功」。
func TestOpenFailsForNonDatabaseFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "README.md")
	if err := os.WriteFile(path, []byte("# 这不是 mmdb\n"), 0o600); err != nil {
		t.Fatalf("造测试文件失败：%v", err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("打开非 mmdb 文件应报错")
	}
}

// TestOpenOrDefaultDegradesWithoutFailing 钉住降级行为：没配、配错都只是「返回 nil + 一条日志」，
// 绝不返回错误、更不 panic —— 跳转与计数不能因为国家维度缺失而起不来。
//
// 顺带断言日志的**级别**：没配是 info（正常形态，不该吓人），配错是 warn（需要运维看一眼）。
func TestOpenOrDefaultDegradesWithoutFailing(t *testing.T) {
	t.Parallel()

	t.Run("没配路径：返回 nil 且只记一条 info", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

		if locator := OpenOrDefault(logger, ""); locator != nil {
			t.Fatalf("未配置路径时应返回 nil，实际 %#v", locator)
		}
		out := buf.String()
		if !strings.Contains(out, "未配置 GEOIP_DB_PATH") {
			t.Errorf("日志里应说明未配置，实际：%q", out)
		}
		if strings.Contains(out, "level=WARN") {
			t.Errorf("未配置不该是 warn，实际：%q", out)
		}
	})

	t.Run("路径打不开：返回 nil、记一条 warn、不报错", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

		missing := filepath.Join(t.TempDir(), "not-here.mmdb")
		if locator := OpenOrDefault(logger, missing); locator != nil {
			t.Fatalf("打不开库文件时应返回 nil，实际 %#v", locator)
		}
		out := buf.String()
		if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "打开 GeoIP 库文件失败") {
			t.Errorf("应记一条 warn 说明打开失败，实际：%q", out)
		}
		if strings.Count(out, "level=WARN") != 1 {
			t.Errorf("应恰好一条 warn，实际：%q", out)
		}
	})
}

// TestCountryOfWithRealDatabase 是唯一会真的查库的用例，用 GEOIP_TEST_DB 门控
// （与 store 集成测试的 POSTGRES_TEST_DSN 同一套做法）。
//
// 为什么门控而不是把库文件入库：DB-IP Lite 每月更新、体积数 MB，进 Git 不划算。
// 本机跑法见 README「GeoIP 国家维度」。
func TestCountryOfWithRealDatabase(t *testing.T) {
	path := os.Getenv("GEOIP_TEST_DB")
	if path == "" {
		t.Skip("未设置 GEOIP_TEST_DB，跳过真实库查询")
	}

	locator, err := Open(path)
	if err != nil {
		t.Fatalf("打开 GEOIP_TEST_DB 失败：%v", err)
	}
	t.Cleanup(func() { _ = locator.Close() })

	// 1) 一个一定在库里的公网地址：只断言「是两位大写字母」，不钉死具体国家 ——
	//    库按月更新，把 US/GB 写死会让用例随数据源变动而红。
	got := locator.CountryOf("81.2.69.142")
	if len(got) != 2 || got != strings.ToUpper(got) {
		t.Errorf("公网地址 81.2.69.142 的国家码 = %q，期望两位大写字母", got)
	}

	// 2) 私网地址不在库里，必须空串：如果这里解析出国家，说明掩码或匹配逻辑错了
	if got := locator.CountryOf("10.11.12.13"); got != "" {
		t.Errorf("私网地址不该解析出国家，实际 %q", got)
	}

	// 3) 非法输入在有库的情况下也必须短路（不能先查库再报错）
	if got := locator.CountryOf("not-an-ip"); got != "" {
		t.Errorf("非法 IP 不该解析出国家，实际 %q", got)
	}

	// 4) IPv4-mapped IPv6 要能查到与原生 IPv4 相同的结果（Unmap 的意义）
	native := locator.CountryOf("81.2.69.142")
	mapped := locator.CountryOf("::ffff:81.2.69.142")
	if mapped != native {
		t.Errorf("IPv4-mapped 形态查出来是 %q，原生 IPv4 是 %q；应一致（Unmap 没生效？）", mapped, native)
	}
}
