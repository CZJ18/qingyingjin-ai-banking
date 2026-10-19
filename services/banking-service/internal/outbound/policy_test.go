package outbound

import "testing"

func TestExplicitEnvironmentAndOriginPolicy(t *testing.T) {
	for _, tc := range []struct {
		env, allow, url string
		ok              bool
	}{
		{"production", "", "http://127.0.0.1:8090", false},
		{"", "", "http://127.0.0.1:8090", false},
		{"production", "", "https://example:99999", false},
		{"test", "http://*.internal", "http://coze.internal", false},
		{"production", "", "http://remote.example", false},
		{"production", "", "https://remote.example", true},
		{"development", "", "http://127.0.0.1:8888", true},
		{"test", "http://coze-server-local:8888", "http://coze-server-local:8888", true},
		{"test", "http://coze-server-local:8888", "http://coze-server-local.evil:8888", false},
		{"test", "http://coze-server-local:8888", "http://coze-server-local:8888/?redirect=http://evil", false},
		{"test", "", "http://coze.internal", false},
		{"unknown", "", "https://remote.example", false},
		{"production", "http://coze-server-local:8888", "http://coze-server-local:8888", false},
	} {
		p, e := Parse(tc.env, tc.allow)
		if e == nil {
			e = p.Validate(tc.url)
		}
		if (e == nil) != tc.ok {
			t.Fatalf("%+v err=%v", tc, e)
		}
	}
}
