package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func sign(t *testing.T, priv ed25519.PrivateKey, payload string) string {
	t.Helper()
	enc := base64.RawURLEncoding
	return "lic_" + enc.EncodeToString([]byte(payload)) + "." + enc.EncodeToString(ed25519.Sign(priv, []byte(payload)))
}

func TestMasterKey(t *testing.T) {
	if _, err := masterKey(); err != nil {
		t.Fatal(err)
	}
}

func TestParseLicense(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	_, other, _ := ed25519.GenerateKey(nil)
	now := time.Date(2026, 10, 26, 23, 0, 0, 0, time.UTC)
	good := `{"v":1,"sub":"a@b.c","hosts":["http://foo.n7s.co"],"iat":"2026-10-26","exp":"2026-10-26","uid":"x"}`

	lic, err := parseLicense("  "+sign(t, priv, good)+"\n", pub, now)
	if err != nil {
		t.Fatal(err)
	}
	if lic.Sub != "a@b.c" || len(lic.Hosts) != 1 || lic.Hosts[0] != "http://foo.n7s.co" {
		t.Fatalf("unexpected licence %+v", lic)
	}

	tampered := sign(t, priv, good)
	tampered = "lic_" + base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(good, "foo", "bar", 1))) +
		tampered[strings.Index(tampered, "."):]

	for name, token := range map[string]string{
		"prefix":     strings.TrimPrefix(sign(t, priv, good), "lic_"),
		"parts":      sign(t, priv, good) + ".x",
		"key":        sign(t, other, good),
		"tampered":   tampered,
		"version":    sign(t, priv, `{"v":2}`),
		"expired":    sign(t, priv, `{"v":1,"exp":"2026-10-25"}`),
		"bad expiry": sign(t, priv, `{"v":1,"exp":"soon"}`),
	} {
		if _, err := parseLicense(token, pub, now); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}

	if _, err := parseLicense(sign(t, priv, `{"v":1}`), pub, now); err != nil {
		t.Errorf("no exp never expires: %v", err)
	}
}

func TestProxyHost(t *testing.T) {
	for in, want := range map[string]string{
		"http://foo.n7s.co":   "foo.n7s.co",
		"https://Foo.N7S.co/": "foo.n7s.co",
	} {
		if got, err := proxyHost(in); err != nil || got != want {
			t.Errorf("%s: got %q, %v", in, got, err)
		}
	}
	for _, in := range []string{
		"foo.n7s.co", "ftp://foo.n7s.co", "http://foo.n7s.co:8080", "http://foo.n7s.co/x",
		"http://localhost", "http://127.0.0.1", "http://miniops.me", "http://a.miniops.me", "http://-x.n7s.co",
	} {
		if got, err := proxyHost(in); err == nil {
			t.Errorf("%s: expected an error, got %q", in, got)
		}
	}
}

func TestMergeHosts(t *testing.T) {
	got := strings.Join(mergeHosts([]string{"b.n7s.co", "a.n7s.co"}, []string{"a.n7s.co", "c.n7s.co"}), ",")
	if got != "a.n7s.co,b.n7s.co,c.n7s.co" {
		t.Fatal(got)
	}
}

func TestNginxConf(t *testing.T) {
	conf := nginxConf([]string{"foo.n7s.co"})
	for _, want := range []string{
		"client_max_body_size 1g;",
		"server_name foo.n7s.co;",
		`server_name ~^(?<app>[a-z0-9-]+)\.foo\.n7s\.co$;`,
		"proxy_set_header Host miniops.me;",
		"proxy_set_header Host $app.miniops.me;",
		"server_name s3.foo.n7s.co;",
		"proxy_pass " + s3URL + ";",
		"proxy_set_header Host $host;",
		"proxy_set_header Upgrade $http_upgrade;",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q in:\n%s", want, conf)
		}
	}
}

func TestProxyManifests(t *testing.T) {
	data, err := json.Marshal(proxyManifests([]string{"foo.n7s.co"}))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{`"host":"foo.n7s.co"`, `"host":"*.foo.n7s.co"`, `"ingressClassName":"traefik"`, `"hosts":"foo.n7s.co"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s", want)
		}
	}
}

func TestGenerateToken(t *testing.T) {
	a, _ := generateToken()
	b, _ := generateToken()
	if len(a) != 32 || strings.Trim(a, "0123456789") != "" || a == b {
		t.Fatalf("bad tokens %q %q", a, b)
	}
}
