package gateway

import (
	"strings"
	"testing"

	"github.com/yuanleyao/ai-agent/internal/core"
)

// Literal expected origins were checked independently with Node's WHATWG URL
// implementation (new URL(configured).origin), not derived by server helpers.
func TestConsoleConfiguredOriginMatchesBrowserRequests(t *testing.T) {
	cases := []struct {
		configured, browserOrigin string
		accepted                  bool
	}{
		{"https://Family.Example", "https://family.example", false},
		{"https://family.example:443", "https://family.example", false},
		{"http://localhost:80", "http://localhost", false},
		{"http://LOCALHOST:080", "http://localhost", false},
		{"http://[::1]:80", "http://[::1]", false},
		{"https://[0:0:0:0:0:0:0:1]", "https://[::1]", false},
		{"https://[2001:0DB8:0:0:0:0:0:1]:8443", "https://[2001:db8::1]:8443", false},
		{"http://127.1:8080", "http://127.0.0.1:8080", false},
		{"http://2130706433", "http://127.0.0.1", false},
		{"http://0x7f000001", "http://127.0.0.1", false},
		{"https://家庭.example", "https://xn--fctw2e.example", false},
		{"https://family.example", "https://family.example", true},
		{"https://family.example:8443", "https://family.example:8443", true},
		{"http://localhost", "http://localhost", true},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080", true},
		{"http://[::1]", "http://[::1]", true},
		{"https://[2001:db8::1]:8443", "https://[2001:db8::1]:8443", true},
	}
	for _, tc := range cases {
		t.Run(tc.configured, func(t *testing.T) {
			app, err := core.Bootstrap(consoleTestConfigPath(t, tc.configured))
			if !tc.accepted {
				if err == nil {
					s := NewServerFromApp(app)
					w := consoleRequest(s, "POST", consolePrefix+"/auth/login", `{"username":"owner","password":"not-a-real-password"}`, nil, "", tc.browserOrigin, "")
					t.Fatalf("noncanonical configuration accepted; browser Origin %q login status=%d body=%s", tc.browserOrigin, w.Code, w.Body.String())
				}
				if !strings.Contains(err.Error(), "console.public_origin") || !strings.Contains(err.Error(), "location.origin") {
					t.Fatalf("configuration error is not actionable: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = app.Logger.Sync() })
			s := NewServerFromApp(app)
			consoleBootstrap(t, s)
			cookie, _, _ := consoleLogin(t, s, "owner", testConsolePassword, tc.browserOrigin)
			if cookie.Secure != strings.HasPrefix(tc.browserOrigin, "https://") {
				t.Fatal("cookie protection differs from browser scheme")
			}
			consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/auth/me", "", cookie, "", tc.browserOrigin, ""), 200)
		})
	}
}
