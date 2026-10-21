package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Pods whose logs are always reported, with their report prefix.
var logSources = []struct {
	Prefix string
	Pod    string
}{
	{"OP", "openserverless-operator-0"},
	{"CT", "controller-0"},
	{"TR", "trustant-0"},
}

func (d *Doctor) collectLogs() {
	if d.pods == nil {
		d.record("log collection", Warn, "skipped, pods are not available")
		return
	}
	for _, src := range logSources {
		p := d.findPod(src.Pod)
		if p == nil {
			d.record("logs of "+src.Pod, Warn, "pod not found")
			d.logs = append(d.logs, LogSection{src.Prefix, "Logs of " + src.Pod, "pod not found"})
			continue
		}
		ns, name := p.Metadata.Namespace, p.Metadata.Name
		text := d.kube.text("logs", "-n", ns, name, "--all-containers", "--prefix", d.tailArg())
		d.logs = append(d.logs, LogSection{src.Prefix, "Logs of " + p.ID(), text})
		d.record("logs of "+src.Pod, Pass, fmt.Sprintf("%s, %d lines", p.ID(), strings.Count(text, "\n")+1))
	}
}

// findPod returns the named pod, preferring the main namespace.
func (d *Doctor) findPod(name string) *Pod {
	var found *Pod
	for i := range d.pods {
		p := &d.pods[i]
		if p.Metadata.Name == name && (found == nil || p.Metadata.Namespace == d.namespace) {
			found = p
		}
	}
	return found
}

// packageVersion is the version of the installed platform package, in either
// of its flavours (see package.sh).
func packageVersion() string {
	for _, pkg := range []string{"openserverless", "trustant"} {
		out, err := exec.Command("dpkg-query", "-W", "-f=${db:Status-Status} ${Version}", pkg).Output()
		if status, version, ok := strings.Cut(strings.TrimSpace(string(out)), " "); err == nil && ok && status == "installed" {
			return pkg + " " + version
		}
	}
	return "not installed"
}

// buildReport renders the report as plain text, every line tagged with the
// prefix of its section: IN (info), CK (checks), AN (anomalies), OP, CT and
// TR (logs of the operator, controller and trustant pods).
func (d *Doctor) buildReport() string {
	var b strings.Builder
	line := func(prefix, format string, args ...any) {
		fmt.Fprintf(&b, "%s: %s\n", prefix, fmt.Sprintf(format, args...))
	}

	line("IN", "package: %s", packageVersion())
	line("IN", "hostname: %s", hostname())
	line("IN", "host ip: %s", hostIP())
	line("IN", "kubernetes: %s", orNone(d.k8sVersion))
	line("IN", "os/arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	line("IN", "date: %s", time.Now().Format(time.RFC3339))

	for _, c := range d.checks {
		line("CK", "%s %s: %s", c.Status, c.Name, oneLine(c.Detail))
	}
	line("CK", "%d checks performed, %d failed", len(d.checks), d.failed())

	for _, l := range d.logs {
		line(l.Prefix, "=== %s ===", l.Description)
		text := strings.TrimRight(l.Text, "\n")
		if text == "" {
			text = "(empty)"
		}
		for _, t := range strings.Split(text, "\n") {
			line(l.Prefix, "%s", t)
		}
	}
	return b.String()
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
