package tailscale

import "testing"

const okStatus = `{"BackendState":"Running","Self":{"DNSName":"fake-mac.tailnet-fake.ts.net.","UserID":1},
"CurrentTailnet":{"MagicDNSEnabled":true},"CertDomains":["fake-mac.tailnet-fake.ts.net"],
"User":{"1":{"LoginName":"alice@example.com"}}}`

func TestEvaluateServeRules(t *testing.T) {
	const host = "fake-mac.tailnet-fake.ts.net"
	cases := []struct {
		name       string
		serve      string
		wantURL    string
		wantFunnel bool
	}{
		{"443 proxy", `{"Web":{"` + host + `:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7420"}}}}}`, "https://" + host + "/", false},
		{"other port in the URL", `{"Web":{"` + host + `:8443":{"Handlers":{"/":{"Proxy":"http://localhost:7420"}}}}}`, "https://" + host + ":8443/", false},
		{"proxy to another port", `{"Web":{"` + host + `:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:9999"}}}}}`, "", false},
		{"funnel on the same key", `{"Web":{"` + host + `:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7420"}}}},"AllowFunnel":{"` + host + `:443":true}}`, "https://" + host + "/", true},
		{"funnel on a different key", `{"Web":{"` + host + `:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7420"}}}},"AllowFunnel":{"` + host + `:8443":true}}`, "https://" + host + "/", false},
		{"foreground funnel", `{"Foreground":{"s1":{"Web":{"` + host + `:8443":{"Handlers":{"/":{"Proxy":"127.0.0.1:7420"}}}},"AllowFunnel":{"` + host + `:8443":true}}}}`, "https://" + host + ":8443/", true},
		{"nothing served", `{}`, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rep := Evaluate([]byte(okStatus), nil, []byte(c.serve), nil, "7420")
			if rep.URL != c.wantURL || rep.Funnel != c.wantFunnel {
				t.Errorf("url %q funnel %t, want %q %t", rep.URL, rep.Funnel, c.wantURL, c.wantFunnel)
			}
		})
	}
}

func TestEvaluateLoggedOutHasNoURL(t *testing.T) {
	st := `{"BackendState":"NeedsLogin","Self":{"DNSName":"x.ts.net."}}`
	rep := Evaluate([]byte(st), nil, []byte(`{"Web":{"x.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7420"}}}}}`), nil, "7420")
	if rep.URL != "" {
		t.Errorf("url %q for a logged-out node", rep.URL)
	}
	if c, ok := rep.Failed(); !ok || c.ID != CheckRunning {
		t.Errorf("first failed check %+v", c)
	}
}
