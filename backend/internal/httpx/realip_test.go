package httpx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNginxRestoresRealClientIP(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("..", "..", "..", path))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	inner := read("deploy/nginx/nginx.conf")
	defaults := read("deploy/nginx/trusted-proxies.conf")
	outer := read("deploy/1panel/site.conf.example")
	ranges := read("deploy/1panel/cloudflare-realip.conf")
	for _, required := range []string{"include /etc/nginx/trusted-proxies.conf;", "real_ip_header X-Real-IP;", "$ac_trusted_proxy:$http_x_forwarded_proto", "X-Forwarded-Proto $ac_forwarded_proto"} {
		if !strings.Contains(inner, required) {
			t.Errorf("application proxy missing %q", required)
		}
	}
	if strings.Contains(defaults, "set_real_ip_from") || !strings.Contains(defaults, "default 0;") {
		t.Fatal("default deployment must trust no proxy")
	}
	if !strings.Contains(outer, "real_ip_header CF-Connecting-IP;") || !strings.Contains(outer, "proxy_set_header X-Real-IP $remote_addr;") {
		t.Fatal("outer proxy must normalize Cloudflare client identity")
	}
	if strings.Count(ranges, "set_real_ip_from ") < 10 || !strings.Contains(ranges, "172.64.0.0/13") || strings.Contains(ranges, "0.0.0.0/0") {
		t.Fatal("invalid Cloudflare trust list")
	}
}
