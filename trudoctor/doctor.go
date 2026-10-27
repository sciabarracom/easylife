package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type Status string

const (
	Pass Status = "PASS"
	Fail Status = "FAIL"
	Warn Status = "WARN"
)

type Check struct {
	Name   string
	Status Status
	Detail string
}

// LogSection is one log source, reported with every line under Prefix.
type LogSection struct {
	Prefix      string
	Description string
	Text        string
}

type Doctor struct {
	namespace  string
	tail       int
	verbose    bool   // include container logs
	trustant   string // "installed", "not installed", or "" when unknown
	kube       *Kube
	checks     []Check
	pods       []Pod // all pods, every namespace
	anomalies  []Anomaly
	logs       []LogSection
	k8sVersion string
}

func newDoctor(namespace string, tail int, verbose bool) *Doctor {
	return &Doctor{namespace: namespace, tail: tail, verbose: verbose, kube: newKube()}
}

func (d *Doctor) record(name string, st Status, detail string) {
	d.checks = append(d.checks, Check{name, st, detail})
	progress("[%s] %s: %s", st, name, detail)
}

func (d *Doctor) recordErr(name string, err error, okDetail string) {
	if err != nil {
		d.record(name, Fail, err.Error())
	} else {
		d.record(name, Pass, okDetail)
	}
}

func (d *Doctor) failed() int {
	n := 0
	for _, c := range d.checks {
		if c.Status == Fail {
			n++
		}
	}
	return n
}

var dnsHosts = []string{"miniops.me", "trustant.miniops.me", "truacp.miniops.me", "vite.miniops.me"}

func (d *Doctor) runSystemChecks() {
	for _, h := range dnsHosts {
		addrs, err := resolve(h)
		switch {
		case err != nil:
			d.record("DNS "+h, Fail, err.Error())
		case contains(addrs, "127.0.0.1"):
			d.record("DNS "+h, Pass, "resolves to 127.0.0.1")
		default:
			d.record("DNS "+h, Fail, fmt.Sprintf("resolves to %v, expected 127.0.0.1", addrs))
		}
	}
	d.recordErr("API http://miniops.me/api/info", checkAPIInfo(), "description is OpenWhisk")
	for _, h := range []string{"github.com", "ghcr.io"} {
		d.recordErr(h+" reachable", checkReachable("https://"+h), "reachable")
	}
}

// resolve looks a host up with dig on Unix and Resolve-DnsName on Windows,
// falling back to the Go resolver when the tool is missing.
func resolve(host string) ([]string, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
			fmt.Sprintf("(Resolve-DnsName -Name '%s' -Type A -ErrorAction Stop).IPAddress", host))
	} else {
		cmd = exec.Command("dig", "+short", host)
	}
	out, err := cmd.Output()
	if errors.Is(err, exec.ErrNotFound) {
		return net.LookupHost(host)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %v", cmd.Args[0], err)
	}
	addrs := strings.Fields(string(out))
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no address found")
	}
	return addrs, nil
}

var httpClient = &http.Client{Timeout: 15 * time.Second}

func checkAPIInfo() error {
	resp, err := httpClient.Get("http://miniops.me/api/info")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var info struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return fmt.Errorf("HTTP %d, not a JSON response: %s", resp.StatusCode, truncate(string(data), 200))
	}
	if info.Description != "OpenWhisk" {
		return fmt.Errorf("description is %q, expected \"OpenWhisk\"", info.Description)
	}
	return nil
}

func checkReachable(url string) error {
	resp, err := httpClient.Get(url)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// hostIP is the address of the interface carrying the default route. Dialing
// UDP sends no packet, it only picks the route.
func hostIP() string {
	if c, err := net.Dial("udp", "8.8.8.8:80"); err == nil {
		defer c.Close()
		return c.LocalAddr().(*net.UDPAddr).IP.String()
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && ipn.IP.To4() != nil {
			return ipn.IP.String()
		}
	}
	return "unknown"
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
