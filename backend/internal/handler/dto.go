package handler

import (
	"time"

	"ashen-courier/internal/domain"
	"ashen-courier/internal/pkg/ipmask"
	"ashen-courier/internal/service"
)

// 关于 JSON tag 的取舍（对应 PLAN.md §8.1）：
//   - 可选数值 / 布尔 / time.Time 用 omitzero，零值即「没有这个信息」
//   - 字符串、切片、map 用 omitempty
//   - 例外：click_count 这类「0 是有意义的值」的字段一律输出，
//     否则前端要区分 undefined 与 0，反而更容易写出 bug

// userDTO 是账号的对外表示。绝不包含 PasswordHash。
type userDTO struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitzero"`
}

func toUserDTO(u *domain.User) userDTO {
	return userDTO{
		ID:          u.ID.String(),
		Email:       u.Email,
		DisplayName: u.DisplayName,
		CreatedAt:   u.CreatedAt,
	}
}

// linkDTO 是短链的对外表示。
type linkDTO struct {
	ID         string     `json:"id"`
	ShortCode  string     `json:"short_code"`
	ShortURL   string     `json:"short_url"`
	TargetURL  string     `json:"target_url"`
	Title      string     `json:"title,omitempty"`
	Tags       []string   `json:"tags,omitempty"`
	Status     string     `json:"status"`
	ClickCount int64      `json:"click_count"`
	ExpiresAt  *time.Time `json:"expires_at,omitzero"`
	Anonymous  bool       `json:"anonymous"`
	// PasswordProtected 表示跳转需要口令。**只回这个布尔，绝不回摘要**。
	PasswordProtected bool      `json:"password_protected,omitzero"`
	CreatedAt         time.Time `json:"created_at,omitzero"`
	UpdatedAt         time.Time `json:"updated_at,omitzero"`
}

// toLinkDTO 把领域实体转成 DTO。baseURL 由 service 统一提供，避免各处手拼。
func toLinkDTO(link *domain.Link, shortURL string) linkDTO {
	return linkDTO{
		ID:         link.ID.String(),
		ShortCode:  link.ShortCode,
		ShortURL:   shortURL,
		TargetURL:  link.TargetURL,
		Title:      link.Title,
		Tags:       link.Tags,
		Status:     link.Status.String(),
		ClickCount: link.ClickCount,
		ExpiresAt:  link.ExpiresAt,
		Anonymous:  link.IsAnonymous(),

		PasswordProtected: link.HasPassword(),
		CreatedAt:         link.CreatedAt,
		UpdatedAt:         link.UpdatedAt,
	}
}

// ---- 请求体 ----

// registerRequest 是注册入参。
type registerRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

// loginRequest 是登录入参。
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// createLinkRequest 是创建短链入参。
type createLinkRequest struct {
	TargetURL  string     `json:"target_url"`
	CustomCode string     `json:"custom_code"`
	Title      string     `json:"title"`
	Tags       []string   `json:"tags"`
	ExpiresAt  *time.Time `json:"expires_at"`
	// Password 是可选访问口令（明文，只在本次请求里存在）；留空表示不设口令。
	Password string `json:"password"`
	// Domain 是可选的自定义域名（纯主机名，大小写与端口都会被归一化）；
	// 留空表示默认域名。只认已登记的域名，未登记报字段级 422。
	//
	// 不下发「可选域名列表」接口：域表是运维登记的配置，前端不做下拉，
	// 由使用方按 README 里的登记方式自己填。
	Domain string `json:"domain"`
}

// updateLinkRequest 是修改短链入参。指针字段区分「没传」与「传了零值」。
type updateLinkRequest struct {
	TargetURL    *string    `json:"target_url"`
	Title        *string    `json:"title"`
	Status       *string    `json:"status"`
	ExpiresAt    *time.Time `json:"expires_at"`
	ClearExpires bool       `json:"clear_expires"`
	// Tags 指向新标签集合；传 [] 表示清空，不传表示保持原样。
	Tags *[]string `json:"tags"`
	// Password 指向新口令；不传表示保持原样。传空串是 422（清空请用 ClearPassword）。
	Password *string `json:"password"`
	// ClearPassword 为 true 时清除口令（改为无需口令）。
	ClearPassword bool `json:"clear_password"`
}

// ---- 响应体 ----

// sessionResponse 是注册 / 登录响应。
type sessionResponse struct {
	User      userDTO   `json:"user"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
}

func toSessionResponse(s *service.Session) sessionResponse {
	return sessionResponse{
		User:      toUserDTO(s.User),
		Token:     s.Token,
		ExpiresAt: s.ExpiresAt,
	}
}

// createLinkResponse 是创建短链响应；manage_key 仅在匿名创建时出现一次。
type createLinkResponse struct {
	Link      linkDTO `json:"link"`
	ManageKey string  `json:"manage_key,omitempty"`
}

// linkListResponse 是链接列表响应。
type linkListResponse struct {
	Links      []linkDTO `json:"links"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

// clickDTO 是一条点击明细的对外表示。
//
// ip 是**掩码后**的网络前缀（203.0.113.0/24），不是原始地址：明细页是给人看的，
// 网段足够定位问题，而原始 IP 留在库里供风控/排障直接查询，不必经 API 外流。
type clickDTO struct {
	ID         int64     `json:"id"`
	OccurredAt time.Time `json:"occurred_at,omitzero"`
	Referer    string    `json:"referer,omitempty"`
	UserAgent  string    `json:"user_agent,omitempty"`
	IP         string    `json:"ip,omitempty"`
	Country    string    `json:"country,omitempty"`
	Device     string    `json:"device,omitempty"`
	Browser    string    `json:"browser,omitempty"`
	OS         string    `json:"os,omitempty"`
}

// toClickDTO 把领域实体转成对外表示，并在这一步完成 IP 掩码。
// 掩码放在 DTO 边界而不是 service/store：领域模型与数据库里始终是原始 IP，
// 只有「对外」这一层才降级，将来加一个内部接口也不会被误掩码。
func toClickDTO(e *domain.ClickEvent) clickDTO {
	return clickDTO{
		ID:         e.ID,
		OccurredAt: e.OccurredAt,
		Referer:    e.Referer,
		UserAgent:  e.UserAgent,
		IP:         ipmask.Mask(e.IP),
		Country:    e.Country,
		Device:     e.Device,
		Browser:    e.Browser,
		OS:         e.OS,
	}
}

// clickListResponse 是点击明细列表响应。days / since 回显实际生效的窗口，
// 免得前端自己算一遍（两边算出来的边界不一致时，条数对不上会很费解）。
type clickListResponse struct {
	Clicks     []clickDTO `json:"clicks"`
	NextCursor string     `json:"next_cursor,omitempty"`
	Days       int        `json:"days,omitzero"`
	Since      time.Time  `json:"since,omitzero"`
}

// statsResponse 是统计响应，字段名与 PLAN.md §7 的契约一致。
type statsResponse struct {
	TotalClicks  int64        `json:"total_clicks"`
	WindowClicks int64        `json:"window_clicks,omitzero"`
	Days         int          `json:"days"`
	Since        time.Time    `json:"since,omitzero"`
	Daily        []dailyDTO   `json:"daily"`
	TopReferers  []refererDTO `json:"top_referers"`
	Devices      []deviceDTO  `json:"devices"`
	Browsers     []browserDTO `json:"browsers"`
	// Countries 只含已知国家；未部署 GeoIP 库文件时是空数组（M5-2）。
	// 前端据此整块隐藏国家分布，而不是画一个空的「未知」条。
	Countries []countryDTO `json:"countries"`
}

// dailyDTO 是趋势图的一个数据点。
type dailyDTO struct {
	Date   string `json:"date"`
	Clicks int64  `json:"clicks"`
}

// refererDTO 是来源分布的一项。referer 为空串表示直接访问。
type refererDTO struct {
	Referer string `json:"referer"`
	Clicks  int64  `json:"clicks"`
}

// deviceDTO 是设备分布的一项。
type deviceDTO struct {
	Device string `json:"device"`
	Clicks int64  `json:"clicks"`
}

// browserDTO 是浏览器分布的一项。
type browserDTO struct {
	Browser string `json:"browser"`
	Clicks  int64  `json:"clicks"`
}

// countryDTO 是国家分布的一项。country 是 ISO 3166-1 alpha-2 代码（大写）。
type countryDTO struct {
	Country string `json:"country"`
	Clicks  int64  `json:"clicks"`
}

// toStatsResponse 把聚合结果转成响应 DTO。
func toStatsResponse(s *service.StatsResult) statsResponse {
	resp := statsResponse{
		TotalClicks:  s.TotalClicks,
		WindowClicks: windowClicks(s),
		Days:         s.Days,
		Since:        s.Since,
		Daily:        make([]dailyDTO, 0, len(s.Daily)),
		TopReferers:  make([]refererDTO, 0, len(s.Referers)),
		Devices:      make([]deviceDTO, 0, len(s.Devices)),
		Browsers:     make([]browserDTO, 0, len(s.Browsers)),
		Countries:    make([]countryDTO, 0, len(s.Countries)),
	}
	// dateLayout 与 service 内部保持一致：趋势图横轴固定 YYYY-MM-DD
	const dateLayout = "2006-01-02"
	for _, d := range s.Daily {
		resp.Daily = append(resp.Daily, dailyDTO{Date: d.Date.Format(dateLayout), Clicks: d.Clicks})
	}
	for _, r := range s.Referers {
		resp.TopReferers = append(resp.TopReferers, refererDTO{Referer: r.Name, Clicks: r.Clicks})
	}
	for _, d := range s.Devices {
		resp.Devices = append(resp.Devices, deviceDTO{Device: d.Name, Clicks: d.Clicks})
	}
	for _, b := range s.Browsers {
		resp.Browsers = append(resp.Browsers, browserDTO{Browser: b.Name, Clicks: b.Clicks})
	}
	for _, c := range s.Countries {
		resp.Countries = append(resp.Countries, countryDTO{Country: c.Name, Clicks: c.Clicks})
	}
	return resp
}

// windowClicks 汇总窗口内明细条数，供前端显示「近 N 天」口径。
func windowClicks(s *service.StatsResult) int64 {
	var sum int64
	for _, d := range s.Daily {
		sum += d.Clicks
	}
	return sum
}
