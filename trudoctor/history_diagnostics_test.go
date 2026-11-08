package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func historyFixture(t *testing.T) (*Pod, []Job) {
	t.Helper()
	p := pod(t, `{"metadata":{"name":"cron-old-pod","namespace":"test","uid":"pod-uid","ownerReferences":[{"apiVersion":"batch/v1","kind":"Job","name":"cron-old","uid":"old-job","controller":true}]},"status":{"phase":"Failed"}}`)
	var jobs []Job
	err := json.Unmarshal([]byte(`[
 {"metadata":{"name":"cron-old","namespace":"test","uid":"old-job","creationTimestamp":"2026-01-01T10:00:00Z","ownerReferences":[{"apiVersion":"batch/v1","kind":"CronJob","name":"cron","uid":"cron-uid","controller":true}]},"status":{"conditions":[{"type":"Failed","status":"True","lastTransitionTime":"2026-01-01T10:00:05Z"}]}},
 {"metadata":{"name":"cron-new","namespace":"test","uid":"new-job","creationTimestamp":"2026-01-01T10:01:00Z","ownerReferences":[{"apiVersion":"batch/v1","kind":"CronJob","name":"cron","uid":"cron-uid","controller":true}]},"status":{"succeeded":1,"completionTime":"2026-01-01T10:01:05Z","conditions":[{"type":"Complete","status":"True","lastTransitionTime":"2026-01-01T10:01:05Z"}]}}
]`), &jobs)
	if err != nil {
		t.Fatal(err)
	}
	return p, jobs
}

func TestRecoveredJobFailure(t *testing.T) {
	cases := []struct {
		name      string
		change    func(*Pod, []Job)
		recovered bool
	}{
		{"later owned complete job", func(*Pod, []Job) {}, true},
		{"different cron UID", func(_ *Pod, j []Job) { j[1].Metadata.OwnerReferences[0].UID = "recreated" }, false},
		{"different namespace", func(_ *Pod, j []Job) { j[1].Metadata.Namespace = "peer" }, false},
		{"wrong job UID", func(p *Pod, _ []Job) { p.Metadata.OwnerReferences[0].UID = "wrong" }, false},
		{"not controller owned", func(p *Pod, _ []Job) { p.Metadata.OwnerReferences[0].Controller = false }, false},
		{"no owner", func(p *Pod, _ []Job) { p.Metadata.OwnerReferences = nil }, false},
		{"partial success", func(_ *Pod, j []Job) { j[1].Status.Conditions = nil }, false},
		{"still active", func(_ *Pod, j []Job) { j[1].Status.Active = 1 }, false},
		{"older successful run", func(_ *Pod, j []Job) { j[1].Metadata.CreationTimestamp = time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC) }, false},
		{"failure time missing", func(_ *Pod, j []Job) { j[0].Status.Conditions[0].LastTransitionTime = time.Time{} }, false},
		{"job not terminal failed", func(_ *Pod, j []Job) { j[0].Status.Conditions[0].Status = "False" }, false},
		{"completion missing", func(_ *Pod, j []Job) { j[1].Status.CompletionTime = time.Time{} }, false},
		{"nonterminal pod", func(p *Pod, _ []Job) { p.Status.Phase = "Pending" }, false},
		{"owner in wrong API group", func(p *Pod, _ []Job) { p.Metadata.OwnerReferences[0].APIVersion = "example.org/v1" }, false},
		{"no cron controller", func(_ *Pod, j []Job) { j[1].Metadata.OwnerReferences[0].Controller = false }, false},
		{"complete condition false", func(_ *Pod, j []Job) { j[1].Status.Conditions[0].Status = "False" }, false},
		{"failed job still active", func(_ *Pod, j []Job) { j[0].Status.Active = 1 }, false},
		{"no successful completion", func(_ *Pod, j []Job) { j[1].Status.Succeeded = 0 }, false},
		{"missing namespace", func(p *Pod, _ []Job) { p.Metadata.Namespace = "" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, j := historyFixture(t)
			tc.change(p, j)
			got := recoveredJobFailure(p, j)
			if (got != "") != tc.recovered {
				t.Fatalf("recovery=%q expected recovered=%v", got, tc.recovered)
			}
		})
	}
	p, _ := historyFixture(t)
	if recoveredJobFailure(p, nil) != "" {
		t.Fatal("missing/unreadable job history accepted")
	}
}

func TestNewFailureAfterRecoveryRemainsFailure(t *testing.T) {
	p, jobs := historyFixture(t)
	newPod := *p
	newPod.Metadata.Name = "latest-failed-pod"
	newPod.Metadata.OwnerReferences = []OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: "latest-failed", UID: "latest-job", Controller: true}}
	// Decode a separate object so mutating it cannot alter the old fixture's slices.
	encoded, err := json.Marshal(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	var latest Job
	if err := json.Unmarshal(encoded, &latest); err != nil {
		t.Fatal(err)
	}
	latest.Metadata.Name, latest.Metadata.UID = "latest-failed", "latest-job"
	latest.Status.Conditions[0].LastTransitionTime = time.Date(2026, 1, 1, 10, 2, 0, 0, time.UTC)
	d := &Doctor{jobs: append(jobs, latest)}
	d.recordPodPhases([]*Pod{p, &newPod})
	for _, item := range []*Pod{p, &newPod} {
		status, name, detail := d.anomalyCheck(Anomaly{Pod: item, Kind: Failed})
		d.record(name, status, detail)
	}
	if d.failed() != 1 {
		t.Fatalf("latest failure hidden or historical failure counted twice: %v", d.checks)
	}
}

func TestHistoricalFailureReportingAndExitStatus(t *testing.T) {
	for _, recover := range []bool{false, true} {
		t.Run(fmt.Sprint(recover), func(t *testing.T) {
			p, j := historyFixture(t)
			d := &Doctor{jobs: j}
			if !recover {
				d.jobs = nil
			}
			d.recordPodPhases([]*Pod{p})
			status, name, detail := d.anomalyCheck(Anomaly{Pod: p, Kind: Failed})
			d.record(name, status, detail)
			want := 1
			if recover {
				want = 0
			}
			if d.failed() != want {
				t.Fatalf("failed=%d want=%d; checks=%v", d.failed(), want, d.checks)
			}
			if recover && (!strings.Contains(detail, "cron-new") || !strings.Contains(name, "historical")) {
				t.Fatal("recovery evidence missing")
			}
		})
	}
	for _, kind := range []string{NotReady, OOMKilled, CrashLoopBackOff, Evicted, Terminating} {
		p, j := historyFixture(t)
		d := &Doctor{jobs: j}
		status, _, _ := d.anomalyCheck(Anomaly{Pod: p, Kind: kind})
		if status != Fail {
			t.Fatalf("%s was downgraded", kind)
		}
	}
}

func TestNotReadyDoesNotInventAProbeFailure(t *testing.T) {
	p := pod(t, `{"spec":{"containers":[{"name":"service","readinessProbe":{"initialDelaySeconds":90}}]},"status":{"phase":"Running","containerStatuses":[{"name":"service","ready":false,"state":{"running":{"startedAt":"2026-01-01T10:00:00Z"}}}]}}`)
	a := detectAnomalies(p, time.Date(2026, 1, 1, 10, 1, 12, 0, time.UTC))
	if len(a) != 1 || a[0].Kind != NotReady || strings.Contains(a[0].Summary, "probe failing") {
		t.Fatalf("unsupported probe-failure claim: %v", a)
	}
}

func TestInstalledPackageNamesAndAbsence(t *testing.T) {
	for _, name := range []string{"truserverless", "trustant-serverless", "openserverless", "trustant"} {
		t.Run(name, func(t *testing.T) {
			got := installedPackageVersion(func(pkg string) (string, error) {
				if pkg == name {
					return "installed 0.9.0+test", nil
				}
				return "", errors.New("absent")
			})
			if got != name+" 0.9.0+test" {
				t.Fatal(got)
			}
		})
	}
	if got := installedPackageVersion(func(string) (string, error) { return "installed 0.9.0", errors.New("query failed") }); got != "not installed" {
		t.Fatal(got)
	}
	for _, output := range []string{"", "config-files 0.9.0", "installed ", "unpacked 0.9.0"} {
		got := installedPackageVersion(func(string) (string, error) { return output, nil })
		if got != "not installed" {
			t.Fatal(got)
		}
	}
}

func TestS3SafeCategoriesAndLegacyLogs(t *testing.T) {
	for _, category := range []string{"transport_timeout", "transport_error", "http_error", "json_error", "schema_error", "anonymous_identity", "grant_mismatch"} {
		t.Run(category, func(t *testing.T) {
			d := &Doctor{}
			phase := "read_policy"
			if category == "json_error" {
				phase = "decode_policy"
			}
			if category == "schema_error" || category == "anonymous_identity" || category == "grant_mismatch" {
				phase = "validate_policy"
			}
			d.recordS3Diagnostics("PRIVATE_CANARY password=secret\n" + s3VerificationError + " [phase=" + phase + " category=" + category + " status=503] body=PRIVATE_CANARY")
			if len(d.checks) != 1 || !strings.Contains(d.checks[0].Detail, "category="+category) {
				t.Fatal(d.checks)
			}
			if strings.Contains(d.checks[0].Detail, "PRIVATE_CANARY") || strings.Contains(d.checks[0].Detail, "password") {
				t.Fatal("raw log leaked")
			}
			if d.failed() != 0 {
				t.Fatal("historical log treated as current failure")
			}
		})
	}
	d := &Doctor{}
	d.recordS3Diagnostics(s3VerificationError + ": PRIVATE_CANARY")
	if !strings.Contains(d.checks[0].Detail, "category=unclassified") || strings.Contains(d.checks[0].Detail, "PRIVATE_CANARY") {
		t.Fatal(d.checks)
	}
	d = &Doctor{}
	d.recordS3Diagnostics("normal operator startup")
	if len(d.checks) != 0 {
		t.Fatal("absence of an error used as policy verification")
	}
	d = &Doctor{}
	d.recordS3Diagnostics(s3VerificationError + " [phase=PRIVATE_CANARY category=PRIVATE_CANARY status=PRIVATE_CANARY]")
	if strings.Contains(d.checks[0].Detail, "PRIVATE_CANARY") || !strings.Contains(d.checks[0].Detail, "category=unclassified") {
		t.Fatal(d.checks)
	}
}
