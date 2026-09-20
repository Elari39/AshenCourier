package httpx

import (
	"fmt"
	"strings"
)

// metricsPrefix 是所有指标名的公共前缀。
//
// 带前缀不是洁癖：抓取端会把同一个 job 下所有目标的指标并到一起，
// 而 ashen_up 这种裸名字很容易与别的 exporter 撞车。
const metricsPrefix = "ashen_"

// labelEscaper 按 Prometheus 文本格式转义标签值。
//
// 必须转义的是三个字符：反斜杠、双引号、换行。漏掉任何一个的后果都被放大过 ——
// 一个带引号的版本号就能让**整份响应**在抓取端变成语法错误，而 Prometheus
// 遇到解析失败是整个 scrape 一起丢，不是跳过那一行。
//
// 放在包级而不是每次调用新建：strings.Replacer 内部会先建一张查找表。
var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

// RenderMetrics 把一份健康报告渲染成 Prometheus 文本格式（版本 0.0.4）。
//
// 为什么不引官方客户端库：§13 的取舍是「零依赖」，而这个场景需要的只是一段能被
// 抓走的文本 —— 文本格式存在的原因之一就是让「不引库的进程」也能暴露指标，
// 手写几十行换来的是没有依赖树、也没有版式升级要跟。
//
// 与 /healthz 的关系是**同源不同形**：两者都调用同一次 HealthProbe.Report，
// 所以每个数字只有一套口径，不会出现「指标说 0、健康检查说 ok」这种自相矛盾。
// 差别在两处，都是刻意的：
//   - 这里**不用 status code 表达故障**（healthz 会回 503）。指标端点的职责是把
//     当前观测值交出去，而 ashen_postgres_up 0 本身就是要被看到的那条数据；
//     回 503 会让抓取方连数字都拿不到，恰好在最需要观测的时候失去观测能力。
//   - 自由文本（report.Errors）**不进标签**：它含空格、引号与中文，做标签既会
//     撑爆时间序列基数，也没人真的按它查询。故障由 *_up 这一组指标表达。
//
// 一条容易漏的规范细节：**值为 0 的计数器也必须输出**。Prometheus 的
// rate() / increase() 比较的是相邻样本，序列一旦缺席就断成两段；而「缺席」
// 在查询侧另有含义（区分「没有数据」与「真的是 0」）。所以这里刻意没有
// omitempty 语义 —— 对照 HealthReport 上的 omitzero 标签，那是给 JSON 看的。
func RenderMetrics(r HealthReport) string {
	var b strings.Builder
	// 一次抓取的输出量级：十几条指标 × 三行，先留够，免得反复扩容
	b.Grow(1536)

	// build_info 放在最前：抓取方拿到一份响应，第一眼就该知道它是哪次构建。
	// 值恒为 1 是通行做法 —— 有意义的不是值，而是 version 这个标签。
	writeMetric(&b, "build_info", "构建信息，值恒为 1；版本号在 version 标签里",
		"gauge", 1, `version="`+labelEscaper.Replace(r.Version)+`"`)

	// 探针：与 /healthz 的 status / postgres / redis 同源
	writeMetric(&b, "up", "整体健康（与 /healthz 的 status 同一口径）",
		"gauge", boolMetric(r.Status == "ok"), "")
	writeMetric(&b, "postgres_up", "Postgres 探针是否可用",
		"gauge", boolMetric(r.Postgres == "ok"), "")
	writeMetric(&b, "redis_up", "Redis 探针是否可用",
		"gauge", boolMetric(r.Redis == "ok"), "")
	writeMetric(&b, "uptime_seconds", "进程已运行秒数",
		"gauge", r.UptimeSeconds, "")
	writeMetric(&b, "worker_enabled", "本进程是否内嵌了 worker",
		"gauge", boolMetric(r.WorkerEnabled), "")

	// 累计量：都是「出了问题才非零」，也正因如此必须输出 0 值
	writeMetric(&b, "dropped_clicks_total", "因队列满而丢弃的点击数",
		"counter", r.DroppedClicks, "")
	writeMetric(&b, "failed_clicks_total", "写库失败的点击数",
		"counter", r.FailedClicks, "")
	writeMetric(&b, "consumed_clicks_total", "worker 已消费的点击数（仅内嵌 worker 有值）",
		"counter", r.ConsumedClicks, "")
	writeMetric(&b, "worker_errors_total", "worker 处理出错的次数（仅内嵌 worker 有值）",
		"counter", r.WorkerErrors, "")
	writeMetric(&b, "pg_fallbacks_total", "短码缓存未命中而回源 PG 的次数（缓存击穿的观测口径）",
		"counter", r.PGFallbacks, "")
	writeMetric(&b, "ratelimit_degraded_total", "限流器降级（Redis 不可用）的次数",
		"counter", r.RateLimitDegraded, "")

	// 瞬时量：反映当前积压与开关状态
	writeMetric(&b, "click_queue_length", "待写库的点击队列长度",
		"gauge", int64(r.QueueLen), "")
	writeMetric(&b, "click_stream_length", "Redis Stream 长度",
		"gauge", r.StreamLen, "")
	writeMetric(&b, "click_stream_pending", "Redis Stream 未 ACK 条数",
		"gauge", r.StreamPending, "")
	writeMetric(&b, "ratelimit_native_increx", "限流是否走 Redis 原生 INCREX（0 = Lua 回落实现）",
		"gauge", boolMetric(r.RateLimitByNative), "")
	writeMetric(&b, "ratelimit_disabled", "限流的应急开关是否被打开（1 = 全量放行）",
		"gauge", boolMetric(r.RateLimitDisabled), "")

	return b.String()
}

// writeMetric 写一个指标的三行：HELP、TYPE、一条采样。
//
// labels 为空时不写花括号 —— 空标签集写成 `name{}` 虽然合法，
// 但让「无标签」与「有标签但为空」在文本上无法区分，读起来更绕。
func writeMetric(b *strings.Builder, name, help, typ string, value int64, labels string) {
	fmt.Fprintf(b, "# HELP %s%s %s\n", metricsPrefix, name, help)
	fmt.Fprintf(b, "# TYPE %s%s %s\n", metricsPrefix, name, typ)
	if labels == "" {
		fmt.Fprintf(b, "%s%s %d\n", metricsPrefix, name, value)
		return
	}
	fmt.Fprintf(b, "%s%s{%s} %d\n", metricsPrefix, name, labels, value)
}

// boolMetric 把布尔状态变成指标取值（1 / 0）。
func boolMetric(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
