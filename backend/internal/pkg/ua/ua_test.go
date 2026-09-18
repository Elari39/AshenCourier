package ua

import "testing"

func TestParse(t *testing.T) {
	t.Parallel()

	// 真实 UA 片段，覆盖到主流组合即可
	const (
		chromeWin   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36"
		edgeWin     = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36 Edg/128.0.0.0"
		chromeMac   = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36"
		safariMac   = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Safari/605.1.15"
		firefoxLin  = "Mozilla/5.0 (X11; Linux x86_64; rv:129.0) Gecko/20100101 Firefox/129.0"
		safariIOS   = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Mobile/15E148 Safari/604.1"
		chromeAndro = "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Mobile Safari/537.36"
		ipadTablet  = "Mozilla/5.0 (iPad; CPU OS 17_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Safari/604.1"
		androidPad  = "Mozilla/5.0 (Linux; Android 14; SM-X710) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36"
		googlebot   = "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"
		curl        = "curl/8.4.0"
		goClient    = "Go-http-client/1.1"
		opera       = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36 OPR/113.0.0.0"
		empty       = ""
		garbage     = "!!!not-a-ua!!!"
	)

	tests := []struct {
		name string
		in   string
		want Info
	}{
		{"Chrome/Windows", chromeWin, Info{DeviceDesktop, "Chrome", "Windows"}},
		{"Edge 优先于 Chrome", edgeWin, Info{DeviceDesktop, "Edge", "Windows"}},
		{"Chrome/macOS", chromeMac, Info{DeviceDesktop, "Chrome", "macOS"}},
		{"Safari 不与 Chrome 混淆", safariMac, Info{DeviceDesktop, "Safari", "macOS"}},
		{"Firefox/Linux", firefoxLin, Info{DeviceDesktop, "Firefox", "Linux"}},
		{"Safari/iOS 手机", safariIOS, Info{DeviceMobile, "Safari", "iOS"}},
		{"Chrome/Android 手机", chromeAndro, Info{DeviceMobile, "Chrome", "Android"}},
		{"iPad 是平板", ipadTablet, Info{DeviceTablet, "Safari", "iOS"}},
		{"Android 平板（无 Mobile 标记）", androidPad, Info{DeviceTablet, "Chrome", "Android"}},
		{"Googlebot", googlebot, Info{DeviceBot, "Bot", "unknown"}},
		{"curl", curl, Info{DeviceBot, "Bot", "unknown"}},
		{"Go 客户端", goClient, Info{DeviceBot, "Bot", "unknown"}},
		{"Opera", opera, Info{DeviceDesktop, "Opera", "Windows"}},
		{"空 UA", empty, Info{DeviceUnknown, ValueUnknown, ValueUnknown}},
		{"无法识别", garbage, Info{DeviceUnknown, ValueUnknown, ValueUnknown}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := Parse(tc.in)
			if got != tc.want {
				t.Fatalf("Parse() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
