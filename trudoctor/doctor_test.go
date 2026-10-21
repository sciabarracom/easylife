package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func pod(t *testing.T, js string) *Pod {
	t.Helper()
	var p Pod
	if err := json.Unmarshal([]byte(js), &p); err != nil {
		t.Fatal(err)
	}
	return &p
}

func kinds(as []Anomaly) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.Kind+":"+a.Container)
	}
	return out
}

func TestDetectAnomalies(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name, pod string
		want      []string
	}{
		{"healthy", `{"metadata":{"name":"a"},"status":{"phase":"Running","containerStatuses":[{"name":"c","ready":true,"state":{"running":{}}}]}}`, nil},
		{"completed", `{"metadata":{"name":"a"},"status":{"phase":"Succeeded","containerStatuses":[{"name":"c","state":{"terminated":{"reason":"Completed","exitCode":0}}}]}}`, nil},
		{"crashloop", `{"status":{"phase":"Running","containerStatuses":[{"name":"c","state":{"waiting":{"reason":"CrashLoopBackOff"}},"lastState":{"terminated":{"reason":"Error","exitCode":1}}}]}}`, []string{"CrashLoopBackOff:c"}},
		{"imagepull", `{"status":{"phase":"Pending","containerStatuses":[{"name":"c","image":"x:1","state":{"waiting":{"reason":"ErrImagePull"}}}]}}`, []string{"ImagePullBackOff:c"}},
		{"config", `{"status":{"phase":"Pending","containerStatuses":[{"name":"c","state":{"waiting":{"reason":"CreateContainerConfigError","message":"configmap \"x\" not found"}}}]}}`, []string{"CreateContainerConfigError:c"}},
		{"oom", `{"status":{"phase":"Running","containerStatuses":[{"name":"c","ready":true,"state":{"running":{}},"lastState":{"terminated":{"reason":"OOMKilled","exitCode":137}}}]}}`, []string{"OOMKilled:c"}},
		{"pending", `{"status":{"phase":"Pending","message":"0/1 nodes available"}}`, []string{"Pending:"}},
		{"failed", `{"status":{"phase":"Failed","containerStatuses":[{"name":"c","state":{"terminated":{"reason":"Error","exitCode":2}}}]}}`, []string{"Failed:"}},
		{"error", `{"status":{"phase":"Running","containerStatuses":[{"name":"c","state":{"terminated":{"reason":"Error","exitCode":2}}}]}}`, []string{"Error:c"}},
		{"unknown", `{"status":{"phase":"Unknown"}}`, []string{"Unknown:"}},
		{"notready", `{"status":{"phase":"Running","containerStatuses":[{"name":"c","ready":false,"state":{"running":{}}}]}}`, []string{"Not Ready:c"}},
		{"evicted", `{"status":{"phase":"Failed","reason":"Evicted","message":"low on memory"}}`, []string{"Evicted:"}},
		{"terminating", `{"metadata":{"deletionTimestamp":"2026-01-01T11:50:00Z","finalizers":["x"]},"status":{"phase":"Running","containerStatuses":[{"name":"c","ready":true,"state":{"running":{}}}]}}`, []string{"Terminating:"}},
		{"recently deleted", `{"metadata":{"deletionTimestamp":"2026-01-01T11:59:50Z"},"status":{"phase":"Running"}}`, nil},
	}
	for _, c := range cases {
		got := kinds(detectAnomalies(pod(t, c.pod), now))
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestConfigRefs(t *testing.T) {
	p := pod(t, `{"spec":{"containers":[{"env":[{"valueFrom":{"configMapKeyRef":{"name":"cm1"}}},{"valueFrom":{"secretKeyRef":{"name":"s1"}}}],"envFrom":[{"configMapRef":{"name":"cm2"}}]}],
		"volumes":[{"configMap":{"name":"cm1"}},{"secret":{"secretName":"s2"}}]}}`)
	cms, secs := configRefs(p)
	if fmt.Sprint(cms) != "[cm1 cm2]" || fmt.Sprint(secs) != "[s1 s2]" {
		t.Fatalf("got %v %v", cms, secs)
	}
}

func TestBuildReport(t *testing.T) {
	d := &Doctor{
		checks:     []Check{{"DNS x", Pass, "ok"}, {"pod y", Fail, "not\nfound"}},
		logs:       []LogSection{{"AN", "Anomaly Error on ns/p/c", "a\nb\n"}, {"TR", "Logs of ns/trustant-0", ""}},
		k8sVersion: "v1.2.3",
	}
	r := d.buildReport()
	for _, want := range []string{
		"IN: kubernetes: v1.2.3\n",
		"IN: package: ",
		"CK: PASS DNS x: ok\nCK: FAIL pod y: not found\nCK: 2 checks performed, 1 failed\n",
		"AN: === Anomaly Error on ns/p/c ===\nAN: a\nAN: b\n",
		"TR: === Logs of ns/trustant-0 ===\nTR: (empty)\n",
	} {
		if !strings.Contains(r, want) {
			t.Errorf("report misses %q:\n%s", want, r)
		}
	}
	for _, l := range strings.Split(strings.TrimSuffix(r, "\n"), "\n") {
		if len(l) < 4 || l[2:4] != ": " {
			t.Errorf("line without prefix: %q", l)
		}
	}
}
