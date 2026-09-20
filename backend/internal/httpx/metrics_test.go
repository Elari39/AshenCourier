package httpx

import (
	"strconv"
	"strings"
	"testing"
)

// promSample 是一条采样行解析后的形状。
type promSample struct {
	// Labels 是花括号里的原文（没有标签时为空串）。
	Labels string
	Value  float64
}

// parsePromText 把文本格式解析成 {指标名: 采样}，并顺带校验三段式结构。
//
// 刻意不引 Prometheus 的解析库：本包的存在前提就是「零依赖」，
// 为了测试引一个解析器只会让 go.mod 长出一条服务不了生产的依赖树。
// 这里真正要断言的也只是「格式没坏到抓取端读不了」—— 几十行足够。
//
// 校验的规则（对应文本格式的硬要求）：
//   - 每条采样之前必须先出现同名（不含标签）的 `# TYPE`
//   - 不带标签的采样行是「名字 空格 值」两段；带标签则以 `}` 收尾
//   - 值必须能解析成浮点
func parsePromText(t *testing.T, text string) map[string]promSample {
	t.Helper()

	types := map[string]string{}
	samples := map[string]promSample{}

	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "# TYPE "):
			rest := strings.TrimPrefix(line, "# TYPE ")
			name, typ, ok := strings.Cut(rest, " ")
			if !ok {
				t.Fatalf("TYPE 行缺少类型：%q", line)
			}
			types[name] = typ
			continue
		case strings.HasPrefix(line, "# HELP "):
			continue
		case strings.HasPrefix(line, "#"):
			t.Fatalf("不认识的注释行：%q", line)
		}

		name, labels, value, ok := splitSample(line)
		if !ok {
			t.Fatalf("采样行不是「名字 值」或「名字{标签} 值」：%q", line)
		}
		if _, ok := types[name]; !ok {
			t.Fatalf("%s 的采样出现在 # TYPE 之前（或缺了 # TYPE）", name)
		}
		if !strings.HasPrefix(name, metricsPrefix) {
			t.Fatalf("指标名 %q 缺少 %s 前缀", name, metricsPrefix)
		}
		if _, dup := samples[name]; dup {
			t.Fatalf("指标 %s 出现了多条采样；本端点每条只输出一条（这是有意的：将来真要用标签区分时再加）", name)
		}
		samples[name] = promSample{Labels: labels, Value: value}
	}
	return samples
}

// splitSample 拆一条采样行。
func splitSample(line string) (name, labels string, value float64, ok bool) {
	name, raw, ok := strings.Cut(line, " ")
	if !ok {
		return "", "", 0, false
	}
	if strings.HasSuffix(name, "}") {
		open := strings.Index(name, "{")
		if open < 0 {
			return "", "", 0, false
		}
		labels, name = name[open+1:len(name)-1], name[:open]
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return "", "", 0, false
	}
	return name, labels, value, true
}

// TestRenderMetricsShape 用一份「全字段都有值」的报告钉住输出的形状。
//
// 这里覆盖三件事，每一件都是抓取端真会踩的坑：
//   - 每条指标都有 # TYPE（没有 TYPE 的序列在 Prometheus 里是 untyped，
//     rate() 会直接报错）
//   - 每条指标都有 # HELP（没有 HELP 时自带的 exposition 检查工具会告警）
//   - counter 与 gauge 的声明正确：名称带 _total 的必须声明成 counter
func TestRenderMetricsShape(t *testing.T) {
	t.Parallel()

	report := HealthReport{
		Status:            "ok",
		Version:           "v1.2.3",
		Postgres:          "ok",
		Redis:             "ok",
		WorkerEnabled:     true,
		UptimeSeconds:     4242,
		DroppedClicks:     1,
		FailedClicks:      2,
		ConsumedClicks:    3,
		WorkerErrors:      4,
		QueueLen:          5,
		StreamLen:         6,
		StreamPending:     7,
		PGFallbacks:       8,
		RateLimitDegraded: 9,
		RateLimitByNative: true,
		RateLimitDisabled: false,
		Errors:            []string{"postgres 不可用"},
	}

	text := RenderMetrics(report)

	// 行数必须正好是 指标数 × 3：多出来的行说明有人往输出里插了东西
	// （最典型的就是把自由文本 Errors 直接 append 进去），少一行说明漏了 TYPE 或 HELP。
	metrics := strings.Count(text, "# TYPE ")
	if metrics == 0 {
		t.Fatal("输出里没有任何 # TYPE 行")
	}
	if got := strings.Count(text, "\n"); got != metrics*3 {
		t.Fatalf("输出 %d 行，期望 %d 行（%d 条指标 × 3 行）", got, metrics*3, metrics)
	}

	samples := parsePromText(t, text)

	want := map[string]float64{
		"ashen_build_info":               1,
		"ashen_up":                       1,
		"ashen_postgres_up":              1,
		"ashen_redis_up":                 1,
		"ashen_uptime_seconds":           4242,
		"ashen_worker_enabled":           1,
		"ashen_dropped_clicks_total":     1,
		"ashen_failed_clicks_total":      2,
		"ashen_consumed_clicks_total":    3,
		"ashen_worker_errors_total":      4,
		"ashen_click_queue_length":       5,
		"ashen_click_stream_length":      6,
		"ashen_click_stream_pending":     7,
		"ashen_pg_fallbacks_total":       8,
		"ashen_ratelimit_degraded_total": 9,
		// Redis 支持原生 INCREX → 1；应急开关没开 → 0
		"ashen_ratelimit_native_increx": 1,
		"ashen_ratelimit_disabled":      0,
	}
	for name, value := range want {
		got, ok := samples[name]
		if !ok {
			t.Errorf("输出里缺少指标 %s", name)
			continue
		}
		if got.Value != value {
			t.Errorf("%s = %v，期望 %v", name, got.Value, value)
		}
	}
	if got, want := len(samples), len(want); got != want {
		t.Errorf("共输出 %d 条指标，期望 %d 条", got, want)
	}

	// 命名约定：只有 counter 才带 _total，其余必须是 gauge。
	// 这条是给人看的（写新指标时容易把只增的错标成 gauge）。
	for name := range want {
		typ := metricTypeOf(t, text, name)
		if wantCounter := strings.HasSuffix(name, "_total"); wantCounter != (typ == "counter") {
			t.Errorf("%s 声明为 %s，但名字里 _total 的存在性与之矛盾", name, typ)
		}
	}
}

// metricTypeOf 取某个指标的 TYPE 声明。
func metricTypeOf(t *testing.T, text, name string) string {
	t.Helper()

	for _, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(line, "# TYPE "+name+" "); ok {
			return rest
		}
	}
	t.Fatalf("没有找到 %s 的 # TYPE 行", name)
	return ""
}

// TestRenderMetricsZeroCounters 钉住「值为 0 的计数器不能省略」。
//
// 这不是风格问题：Prometheus 的 rate() / increase() 比较的是相邻样本，
// 序列一旦缺席就断成两段；而且「缺席」在查询侧另有含义 —— 它区分
// 「没有数据」与「真的是 0」。一个「从不丢点击」的进程恰恰需要把
// dropped_clicks_total 0 稳定地报出来，否则告警规则里的 rate() 会一直空。
func TestRenderMetricsZeroCounters(t *testing.T) {
	t.Parallel()

	// 一份「什么坏事都没发生」的报告：所有计数器都是零值
	text := RenderMetrics(HealthReport{Status: "ok", Postgres: "ok", Redis: "ok"})
	samples := parsePromText(t, text)

	counters := []string{
		"ashen_dropped_clicks_total",
		"ashen_failed_clicks_total",
		"ashen_consumed_clicks_total",
		"ashen_worker_errors_total",
		"ashen_pg_fallbacks_total",
		"ashen_ratelimit_degraded_total",
	}
	for _, name := range counters {
		got, ok := samples[name]
		if !ok {
			t.Errorf("计数器 %s 在零值时必须仍然输出", name)
			continue
		}
		if got.Value != 0 {
			t.Errorf("%s = %v，期望 0", name, got.Value)
		}
	}
}

// TestRenderMetricsDegraded 钉住「探针异常 → 对应 *_up 为 0」。
//
// 变异验证：把 up 的判定从 `r.Status == "ok"` 改成恒 1 → 本用例变红。
// 注意整体 up 与单个探针是**两条独立指标**：PG 挂了会让 ashen_up 与
// ashen_postgres_up 同时为 0，而 ashen_redis_up 仍应为 1 —— 只看整体
// 无法定位是哪个依赖出了问题。
func TestRenderMetricsDegraded(t *testing.T) {
	t.Parallel()

	text := RenderMetrics(HealthReport{
		Status:   "degraded",
		Postgres: "error",
		Redis:    "ok",
		Errors:   []string{"postgres 不可用"},
	})
	samples := parsePromText(t, text)

	want := map[string]float64{
		"ashen_up":          0,
		"ashen_postgres_up": 0,
		"ashen_redis_up":    1,
	}
	for name, value := range want {
		if got := samples[name].Value; got != value {
			t.Errorf("%s = %v，期望 %v", name, got, value)
		}
	}

	// status 既不是 ok 也不是 degraded 时（比如探针写了空串）按「不健康」处理：
	// 指标端点宁可少报健康，也不该把未知状态当成健康。
	if got := parsePromText(t, RenderMetrics(HealthReport{}))["ashen_up"].Value; got != 0 {
		t.Errorf("空报告时 ashen_up = %v，期望 0", got)
	}
}

// TestRenderMetricsEscapesLabelValues 钉住标签值的转义（反斜杠 / 双引号 / 换行）。
//
// 为什么这条必须单独测：Prometheus 遇到解析失败是**整份响应一起丢**，
// 不是跳过那一行。所以一个带引号的版本号（`-ldflags "-X main.version=..."` 里
// 传错一个字符就能做到）会让整个 /metrics 永久不可用，而症状是「抓取端说
// 格式错误」，与本端点的日志毫无关联。
func TestRenderMetricsEscapesLabelValues(t *testing.T) {
	t.Parallel()

	// 三种必须转义的字符都塞进去，外加一个中文字符（应当原样保留）
	raw := "v1\"2\\3\n4中文"
	text := RenderMetrics(HealthReport{Status: "ok", Version: raw})

	const wantLine = `ashen_build_info{version="v1\"2\\3\n4中文"} 1`
	if !strings.Contains(text, wantLine) {
		t.Fatalf("标签值没有按规范转义。\n期望包含：%s\n实际输出：\n%s", wantLine, text)
	}

	// 原始换行若泄漏，行数会比 指标数×3 多 —— 抓取端会看到一条无法解析的垃圾行。
	// 这里用「行数守恒」来断言，而不是再数一遍换行的出现次数：
	// 后者会把「转义后的字面量 \n」也算进去。
	metrics := strings.Count(text, "# TYPE ")
	if got, want := strings.Count(text, "\n"), metrics*3; got != want {
		t.Fatalf("标签值的换行没有被转义：输出 %d 行，期望 %d 行", got, want)
	}
}
