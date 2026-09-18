package handler

import (
	"time"

	"ashen-courier/internal/domain"
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
	CreatedAt  time.Time  `json:"created_at,omitzero"`
	UpdatedAt  time.Time  `json:"updated_at,omitzero"`
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
		CreatedAt:  link.CreatedAt,
		UpdatedAt:  link.UpdatedAt,
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
