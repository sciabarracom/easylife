package main

import (
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

//go:embed master_key_pub
var masterKeyPub string

// License is the payload of a licence; see ../spec/license_format.md. Unknown
// fields are ignored.
type License struct {
	V     int      `json:"v"`
	Sub   string   `json:"sub"`
	Hosts []string `json:"hosts"`
	Iat   string   `json:"iat"`
	Exp   string   `json:"exp"`
}

func masterKey() (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(masterKeyPub))
	if err != nil {
		return nil, fmt.Errorf("embedded master_key_pub: %v", err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("embedded master_key_pub: %d bytes, expected %d", len(b), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(b), nil
}

// parseLicense validates a lic_<payload>.<signature> token as the app does:
// signature over the original payload bytes, version 1, expiry valid through
// the whole exp day.
func parseLicense(token string, pub ed25519.PublicKey, now time.Time) (*License, error) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, "lic_") {
		return nil, errors.New("not a licence: missing the lic_ prefix")
	}
	parts := strings.Split(strings.TrimPrefix(token, "lic_"), ".")
	if len(parts) != 2 {
		return nil, errors.New("malformed licence: expected two parts separated by '.'")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[0], "="))
	if err != nil {
		return nil, fmt.Errorf("malformed licence payload: %v", err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil, fmt.Errorf("malformed licence signature: %v", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return nil, fmt.Errorf("malformed licence signature: %d bytes, expected %d", len(sig), ed25519.SignatureSize)
	}
	if !ed25519.Verify(pub, payload, sig) {
		return nil, errors.New("invalid licence: the signature does not match")
	}
	var lic License
	if err := json.Unmarshal(payload, &lic); err != nil {
		return nil, fmt.Errorf("malformed licence payload: %v", err)
	}
	if lic.V != 1 {
		return nil, fmt.Errorf("unsupported licence version %d", lic.V)
	}
	if lic.Exp != "" {
		exp, err := time.Parse("2006-01-02", lic.Exp)
		if err != nil {
			return nil, fmt.Errorf("invalid licence expiry %q", lic.Exp)
		}
		if !now.UTC().Before(exp.AddDate(0, 0, 1)) {
			return nil, fmt.Errorf("the licence expired on %s", lic.Exp)
		}
	}
	return &lic, nil
}

var hostnameRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// proxyHost returns the hostname to publish for a licensed host
// (scheme://host): a public DNS name, without port or path, outside the local
// miniops.me domain the proxy forwards to.
func proxyHost(h string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(h))
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if u.Port() != "" {
		return "", errors.New("hosts with a port are not supported")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("hosts with a path are not supported")
	}
	host := strings.ToLower(u.Hostname())
	if host == "miniops.me" || strings.HasSuffix(host, ".miniops.me") {
		return "", errors.New("local host, nothing to proxy")
	}
	if !hostnameRe.MatchString(host) {
		return "", errors.New("not a public host name")
	}
	return host, nil
}
