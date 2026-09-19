// Package geoip 用 MaxMind DB 格式的库文件把 IP 解析成国家代码。
//
// 放在 store 下（而不是 pkg 下）是按仓库自己的分层来的：pkg 里是 base62 / ua 那类
// 零第三方依赖的纯工具，而这里要读一个外部数据源，与 store/postgres、store/redis 同类。
//
// 四个刻意的选择：
//
//  1. **解析放在 worker，不放跳转路径**。跳转路径的承诺是「零数据库写入 + 只碰 Redis」，
//     mmdb 查询虽然只是一次内存映射读，但它会引入文件句柄与页缓存的不确定性；
//     而点击事件本来就是异步落库的，多解析一次国家码完全在 worker 的预算内。
//  2. **库文件不入库**。DB-IP Lite 每月更新一次、体积数 MB，进 Git 只会让仓库变大，
//     还会让人误以为它是项目源码。放 deploy/geoip/（已被 .gitignore 忽略），
//     由运维自行下载，见 README 的「GeoIP 国家维度」一节。
//  3. **「不知道」永远是空串**。库没配置、IP 非法、库里没有这个网段、库里没有那个字段、
//     库文件损坏 —— 全部收敛成空串。调用方只会把它写成 NULL，
//     不需要也不应该区分这几种情况（区分了也没人处理）。
//  4. **打不开库不致命**。国家维度是可选增强，缺了它跳转与统计一切照旧；
//     所以配置了路径但文件不在时只记一条 warn（见 OpenOrDefault）。
package geoip

import (
	"fmt"
	"log/slog"
	"net/netip"
	"strings"

	"github.com/oschwald/maxminddb-golang/v2"
)

// Locator 是一个国家解析器。
//
// maxminddb.Reader 的查询本身是线程安全的（官方文档明确说明），
// 因此一个 Locator 可以直接被 worker 的多个 goroutine 共用，不需要额外加锁。
type Locator struct {
	db *maxminddb.Reader
}

// Open 打开 path 指向的 mmdb 文件。
//
// 文件不存在、不是 mmdb、或 mmdb 版本不受支持都会返回错误 ——
// 这里刻意不吞错：调用方（见 OpenOrDefault）要靠这个错误决定「降级 + 打一条 warn」。
func Open(path string) (*Locator, error) {
	reader, err := maxminddb.Open(path)
	if err != nil {
		return nil, fmt.Errorf("geoip: 打开库文件 %s: %w", path, err)
	}
	return &Locator{db: reader}, nil
}

// OpenOrDefault 按配置打开国家库，把「没配」与「配了但打不开」都收敛成 nil。
//
// 返回 nil 时调用方不需要判空：worker.New 会把 nil 兜底成「永远返回空串」的实现，
// 于是降级路径与正常路径走同一行代码。
//
// 放在这里而不是两个 cmd 里各写一遍：api（内嵌 worker）与独立 worker 两个入口的
// 降级行为必须一致，抄一遍就多一处抄漏日志的机会。
func OpenOrDefault(logger *slog.Logger, path string) *Locator {
	if path == "" {
		// 没配是正常形态，不值得 warn —— 否则每个没开 GeoIP 的部署启动时都要吓一跳
		logger.Info("未配置 GEOIP_DB_PATH，点击明细的国家字段将保持为空")
		return nil
	}

	locator, err := Open(path)
	if err != nil {
		// 只 warn 不退出：国家维度缺了不影响跳转、计数与其余统计
		logger.Warn("打开 GeoIP 库文件失败，国家字段将保持为空", "path", path, "err", err)
		return nil
	}
	logger.Info("GeoIP 库文件已加载", "path", path)
	return locator
}

// Close 释放库文件的内存映射。未打开的 Locator（零值）调用它是安全的。
func (l *Locator) Close() error {
	if l == nil || l.db == nil {
		return nil
	}
	return l.db.Close()
}

// CountryOf 返回 ip 所属国家的 ISO 3166-1 alpha-2 代码（大写，如 "CN"、"US"）。
// 解析不出来返回空串，理由见包注释。
func (l *Locator) CountryOf(ip string) string {
	// 零值安全：先判空再查库，于是 `&Locator{}` 在单测里可以直接用，
	// 也可以被当成「没有配置库文件」的降级实现传下去。
	if l == nil || l.db == nil {
		return ""
	}

	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return ""
	}
	// IPv4-mapped IPv6（::ffff:1.2.3.4）必须先 Unmap 再查：
	// DB-IP Lite 的 IPv4 段是按原生 IPv4 前缀存的，拿映射形态去查一定查不到，
	// 表现就是「明明是公网地址却永远没有国家」。
	addr = addr.Unmap()

	result := l.db.Lookup(addr)
	if !result.Found() {
		return ""
	}

	// 只解出需要的那一个字段：mmdb 的解码是按 struct tag 走的，
	// 多声明一个字段就多一次解码开销 —— 这个函数在消费路径上每秒要跑成千上万次。
	var record struct {
		Country struct {
			ISOCode string `maxminddb:"iso_code"`
		} `maxminddb:"country"`
	}
	if err := result.Decode(&record); err != nil {
		return ""
	}
	return record.Country.ISOCode
}
