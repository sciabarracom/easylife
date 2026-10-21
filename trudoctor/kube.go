package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
	"time"
)

// Kube runs k3s commands, through sudo unless we already are root.
type Kube struct {
	sudo bool
}

func newKube() *Kube {
	return &Kube{sudo: os.Geteuid() != 0}
}

func (k *Kube) k3s(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	argv := append([]string{"k3s"}, args...)
	if k.sudo {
		argv = append([]string{"sudo"}, argv...)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), errors.New(msg)
	}
	return stdout.String(), nil
}

func (k *Kube) kubectl(args ...string) (string, error) {
	return k.k3s(2*time.Minute, append([]string{"kubectl"}, args...)...)
}

// text returns the output of a kubectl command for a report, with the error
// appended instead of returned.
func (k *Kube) text(args ...string) string {
	out, err := k.kubectl(args...)
	if err != nil {
		out += "\nERROR: " + err.Error()
	}
	return strings.TrimRight(out, "\n")
}

func (k *Kube) json(v any, args ...string) error {
	out, err := k.kubectl(append(args, "-o", "json")...)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(out), v)
}

// Pod is the subset of a v1.Pod the doctor looks at. encoding/json matches
// field names case-insensitively, so the camelCase keys need no tags.
type Pod struct {
	Metadata struct {
		Name                       string
		Namespace                  string
		DeletionTimestamp          *time.Time
		DeletionGracePeriodSeconds *int64
		Finalizers                 []string
	}
	Spec struct {
		NodeName       string
		Containers     []Container
		InitContainers []Container
		Volumes        []struct {
			ConfigMap *struct{ Name string }
			Secret    *struct{ SecretName string }
		}
	}
	Status struct {
		Phase                 string
		Reason                string
		Message               string
		ContainerStatuses     []ContainerStatus
		InitContainerStatuses []ContainerStatus
	}
}

type ref struct{ Name string }

type Container struct {
	Name      string
	Image     string
	Resources struct {
		Limits   map[string]string
		Requests map[string]string
	}
	ReadinessProbe json.RawMessage
	Env            []struct {
		ValueFrom *struct {
			ConfigMapKeyRef *ref
			SecretKeyRef    *ref
		}
	}
	EnvFrom []struct {
		ConfigMapRef *ref
		SecretRef    *ref
	}
}

type ContainerState struct {
	Waiting *struct {
		Reason  string
		Message string
	}
	Running *struct {
		StartedAt time.Time
	}
	Terminated *struct {
		Reason     string
		Message    string
		ExitCode   int
		FinishedAt time.Time
	}
}

type ContainerStatus struct {
	Name         string
	Image        string
	Ready        bool
	RestartCount int
	State        ContainerState
	LastState    ContainerState
}

func (p *Pod) ID() string { return p.Metadata.Namespace + "/" + p.Metadata.Name }

func (p *Pod) container(name string) *Container {
	for _, cs := range [][]Container{p.Spec.Containers, p.Spec.InitContainers} {
		for i := range cs {
			if cs[i].Name == name {
				return &cs[i]
			}
		}
	}
	return nil
}

func (p *Pod) status(name string) *ContainerStatus {
	for _, cs := range [][]ContainerStatus{p.Status.ContainerStatuses, p.Status.InitContainerStatuses} {
		for i := range cs {
			if cs[i].Name == name {
				return &cs[i]
			}
		}
	}
	return nil
}

// Pods the installation must have Running; '*' stands for a generated suffix.
var requiredPods = []string{
	"openserverless-0",
	"couchdb-0",
	"openserverless-etcd-0",
	"openserverless-mongodb-0",
	"openserverless-operator-0",
	"openserverless-postgres-1-0",
	"openserverless-static-0",
	"openserverless-streamer-api-0",
	"openserverless-system-api-0",
	"openserverless-milvus-standalone-*",
	"redis-0",
	"registry-0",
	"seaweedfs-0",
	"trustant-0",
	"tika-0",
	"kubegres-controller-manager-*",
}

// Jobs that must have Completed.
var requiredJobs = []string{
	"couchdb-init-*",
	"cron-init-*",
	"preload-runtimes-*",
}

func (d *Doctor) runKubeChecks() {
	k3s := "k3s kubectl"
	if d.kube.sudo {
		k3s = "sudo " + k3s
	}
	progress("Using %q", k3s)

	var ver struct {
		ServerVersion struct{ GitVersion string }
	}
	if err := d.kube.json(&ver, "version"); err != nil {
		d.record("Kubernetes API", Fail, err.Error())
		return
	}
	d.k8sVersion = ver.ServerVersion.GitVersion
	d.record("Kubernetes API", Pass, "server version "+d.k8sVersion)

	_, err := d.kube.kubectl("get", "node", "trustant")
	d.recordErr("node trustant", err, "present")
	_, err = d.kube.kubectl("get", "namespace", d.namespace)
	d.recordErr("namespace "+d.namespace, err, "present")

	var list struct{ Items []Pod }
	if err := d.kube.json(&list, "get", "pods", "--all-namespaces"); err != nil {
		d.record("list pods", Fail, err.Error())
		return
	}
	d.pods = list.Items
	var nsPods []*Pod
	for i := range d.pods {
		if d.pods[i].Metadata.Namespace == d.namespace {
			nsPods = append(nsPods, &d.pods[i])
		}
	}

	var bad []string
	for _, p := range nsPods {
		if p.Status.Phase != "Running" && p.Status.Phase != "Succeeded" {
			bad = append(bad, fmt.Sprintf("%s (%s)", p.Metadata.Name, p.Status.Phase))
		}
	}
	if len(bad) > 0 {
		d.record("pods Running or Completed", Fail, strings.Join(bad, ", "))
	} else {
		d.record("pods Running or Completed", Pass, fmt.Sprintf("all %d pods", len(nsPods)))
	}

	for _, pat := range requiredPods {
		d.checkPhase(nsPods, pat, "Running", "pod")
	}

	var jobs struct{ Items []Job }
	jobsErr := d.kube.json(&jobs, "get", "jobs", "-n", d.namespace)
	for _, pat := range requiredJobs {
		// A completed job's pod may already be gone (TTL), so fall back to the
		// Job object itself.
		if d.findPods(nsPods, pat, "Succeeded") == nil && jobsErr == nil {
			if j := completedJob(jobs.Items, pat); j != "" {
				d.record("job "+pat, Pass, "job "+j+" Completed")
				continue
			}
		}
		d.checkPhase(nsPods, pat, "Succeeded", "job")
	}
}

type Job struct {
	Metadata struct{ Name string }
	Status   struct{ Succeeded int }
}

func completedJob(jobs []Job, pattern string) string {
	for _, j := range jobs {
		ok, _ := path.Match(pattern, j.Metadata.Name)
		if (ok || j.Metadata.Name == strings.TrimSuffix(pattern, "-*")) && j.Status.Succeeded > 0 {
			return j.Metadata.Name
		}
	}
	return ""
}

func (d *Doctor) findPods(pods []*Pod, pattern, phase string) *Pod {
	for _, p := range pods {
		if ok, _ := path.Match(pattern, p.Metadata.Name); ok && p.Status.Phase == phase {
			return p
		}
	}
	return nil
}

func (d *Doctor) checkPhase(pods []*Pod, pattern, phase, kind string) {
	name := kind + " " + pattern
	label := map[string]string{"Running": "Running", "Succeeded": "Completed"}[phase]
	if p := d.findPods(pods, pattern, phase); p != nil {
		d.record(name, Pass, p.Metadata.Name+" "+label)
		return
	}
	var seen []string
	for _, p := range pods {
		if ok, _ := path.Match(pattern, p.Metadata.Name); ok {
			seen = append(seen, fmt.Sprintf("%s (%s)", p.Metadata.Name, p.Status.Phase))
		}
	}
	if len(seen) == 0 {
		d.record(name, Fail, "not found")
	} else {
		sort.Strings(seen)
		d.record(name, Fail, "not "+label+": "+strings.Join(seen, ", "))
	}
}
