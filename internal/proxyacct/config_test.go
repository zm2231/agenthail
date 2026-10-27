package proxyacct

import (
	"strings"
	"testing"
)

func TestEveryLoopbackAddressIsTheSameEndpoint(t *testing.T) {
	for _, raw := range []string{"http://localhost:8317", "http://127.0.0.1:8317/", "http://[::1]:8317/v1"} {
		if endpoint, err := Endpoint(raw); err != nil || endpoint != "http://loopback:8317" {
			t.Fatalf("%s: %s %v", raw, endpoint, err)
		}
	}
	if endpoint, _ := Endpoint("https://Proxy.Example"); endpoint != "https://proxy.example:443" {
		t.Fatal(endpoint)
	}
}

func TestProxiesConfigIsValidated(t *testing.T) {
	configs, err := ParseConfigs([]byte(`[{"name":"shared","url":"http://proxy.example:8317/","management":{"env":"SHARED_KEY"}}]`))
	if err != nil || configs[0].Kind != KindCLIProxyAPI || configs[0].URL != "http://proxy.example:8317" {
		t.Fatalf("configs=%+v err=%v", configs, err)
	}
	for raw, want := range map[string]string{
		`{}`:                                "must be a list",
		`[{"name":"a b","url":"http://x"}]`: "proxy name",
		`[{"name":"a","url":"ftp://x"}]`:    "http or https",
		`[{"name":"a","url":"http://x","kind":"other"}]`:                            "not supported",
		`[{"name":"a","url":"http://x"},{"name":"a","url":"http://y"}]`:             "listed twice",
		`[{"name":"a","url":"http://x","management":{"env":"A","file":"/k"}}]`:      "one of file",
		`[{"name":"a","url":"http://x","management":{"file":"k"}}]`:                 "absolute",
		`[{"name":"a","url":"http://x","management":{"keychain":{"service":"s"}}}]`: "service and account",
		`[{"name":"a","url":"http://x","management":{"command":["setec","get","k"]}}]`: "absolute path",
		`[{"name":"a","url":"http://x","management":{"command":["/bin/k"],"env":"A"}}]`: "one of file",
		`[{"name":"a","url":"http://x","management":{"command":[]}}]`:                  "one of file",
		`[{"name":"a","url":"http://x","management":{"file":""}}]`:                     "one of file",
		`[{"name":"a","url":"http://x","management":{}}]`:                              "one of file",
	} {
		if _, err := ParseConfigs([]byte(raw)); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v", raw, err)
		}
	}
}
