package config

import (
	"strings"
	"testing"
)

// 这两个常量是「能通过校验」的样本值，避免测试与校验规则耦合太紧。
const (
	validDSN    = "postgres://ashen:pwd@localhost:5432/ashen?sslmode=disable"
	validSecret = "0123456789abcdef0123456789abcdef"
)

// setBaseEnv 把与用例无关的环境变量固定下来，让 LoadFor 的结果只反映被测项。
// 不用 t.Parallel：t.Setenv 与环境变量天然互斥。
func setBaseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", validDSN)
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("REDIS_DB", "0")
	t.Setenv("TRUST_PROXY", "true")
	t.Setenv("LOG_LEVEL", "info")
	t.Setenv("PUBLIC_BASE_URL", "http://localhost:8080")
}

// TestLoadForWorkerSkipsAPISecrets 守住一个曾经的真实缺陷：
// LoadFor 的 role 参数被完全忽略（函数体里从未引用），于是 worker 也被强制
// 要求 JWT_SECRET，与 Role 的注释、以及 cmd/worker 的说明互相矛盾。
func TestLoadForWorkerSkipsAPISecrets(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("JWT_SECRET", "")
	t.Setenv("PUBLIC_BASE_URL", "")
	t.Setenv("WORKER_ENABLED", "yes") // 顺带验证 yes 能解析（见 TestBoolEnvSpellings）

	cfg, err := LoadFor(RoleWorker)
	if err != nil {
		t.Fatalf("worker 角色不该强制要求 JWT_SECRET / PUBLIC_BASE_URL，却报错：%v", err)
	}
	if !cfg.WorkerEnabled {
		t.Fatal("WORKER_ENABLED=yes 应解析为 true")
	}
	if cfg.PublicBaseURL == "" {
		t.Fatal("PUBLIC_BASE_URL 未设置时应回落到默认值，而不是空串")
	}
}

// TestLoadForAPIChecksJWTSecret 确认放宽只针对 worker，api 的校验一条没少。
func TestLoadForAPIChecksJWTSecret(t *testing.T) {
	tests := []struct {
		name   string
		secret string
		wantOK bool
	}{
		{"未设置", "", false},
		{"占位值", "changeme", false},
		{"长度不足", "short", false},
		{"合法", validSecret, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setBaseEnv(t)
			t.Setenv("JWT_SECRET", tc.secret)

			_, err := LoadFor(RoleAPI)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("期望装载成功，实际报错：%v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("期望报错，实际装载成功")
			}
			if !strings.Contains(err.Error(), "JWT_SECRET") {
				t.Fatalf("错误信息里应提到 JWT_SECRET，实际：%v", err)
			}
		})
	}
}

// TestLoadForUnknownRoleStaysStrict 未知角色按 api 处理 —— 宁可多校验，
// 也不能因为拼错了角色名而把安全校验静默跳过。
func TestLoadForUnknownRoleStaysStrict(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("JWT_SECRET", "")

	if _, err := LoadFor(Role("wroker")); err == nil {
		t.Fatal("未知角色应仍要求 JWT_SECRET")
	}
}

// TestBoolEnvSpellings 守住注释与实现的落差：注释宣称支持 yes/no/on/off，
// 原先的实现只调 strconv.ParseBool，照注释写就是启动失败。
func TestBoolEnvSpellings(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{"数值 1", "1", true},
		{"数值 0", "0", false},
		{"小写 true", "true", true},
		{"小写 false", "false", false},
		{"大写 TRUE", "TRUE", true},
		{"混合 False", "False", false},
		{"yes", "yes", true},
		{"大写 YES", "YES", true},
		{"on", "on", true},
		{"On", "On", true},
		{"no", "no", false},
		{"off", "off", false},
		{"首尾空白", "  yes  ", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WORKER_ENABLED", tc.raw)

			got, err := boolEnv("WORKER_ENABLED", false)
			if err != nil {
				t.Fatalf("boolEnv(%q) 不该报错：%v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("boolEnv(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

// TestLoadRateLimitDisabled 守住限流应急开关真的能从环境变量装载。
// 这个开关在 store/redis 与 domain 里一直存在，但过去没有任何触发路径：
// Disable/Enable/Disabled 三个方法全仓零调用，是「文档里有、实际做不到」的死能力。
func TestLoadRateLimitDisabled(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{"未设置时默认关闭", "", false},
		{"true 打开", "true", true},
		{"yes 也打开", "yes", true},
		{"false 关闭", "false", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setBaseEnv(t)
			t.Setenv("JWT_SECRET", validSecret)
			t.Setenv("RATE_LIMIT_DISABLED", tc.raw)

			cfg, err := LoadFor(RoleAPI)
			if err != nil {
				t.Fatalf("装载配置失败：%v", err)
			}
			if cfg.RateLimitDisabled != tc.want {
				t.Fatalf("RateLimitDisabled = %v, want %v", cfg.RateLimitDisabled, tc.want)
			}
		})
	}
}

// TestBoolEnvFallbackAndReject 未设置时用默认值；真垃圾值必须报错而不是静默当成 false。
func TestBoolEnvFallbackAndReject(t *testing.T) {
	t.Run("未设置取默认值", func(t *testing.T) {
		t.Setenv("WORKER_ENABLED", "")

		got, err := boolEnv("WORKER_ENABLED", true)
		if err != nil {
			t.Fatalf("未设置不该报错：%v", err)
		}
		if !got {
			t.Fatal("未设置时应返回默认值 true")
		}
	})

	t.Run("垃圾值报错", func(t *testing.T) {
		t.Setenv("WORKER_ENABLED", "maybe")

		if _, err := boolEnv("WORKER_ENABLED", false); err == nil {
			t.Fatal("垃圾值应报错，不能静默当成 false")
		}
	})
}
