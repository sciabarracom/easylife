package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Anomaly kinds.
const (
	Pending                    = "Pending"
	Failed                     = "Failed"
	Unknown                    = "Unknown"
	CrashLoopBackOff           = "CrashLoopBackOff"
	ImagePullBackOff           = "ImagePullBackOff"
	CreateContainerConfigError = "CreateContainerConfigError"
	OOMKilled                  = "OOMKilled"
	Error                      = "Error"
	NotReady                   = "Not Ready"
	Evicted                    = "Evicted"
	Terminating                = "Terminating"
)

type Anomaly struct {
	Pod       *Pod
	Kind      string
	Container string // empty for pod-level anomalies
	Summary   string
}

func (a Anomaly) Where() string {
	if a.Container == "" {
		return a.Pod.ID()
	}
	return a.Pod.ID() + "/" + a.Container
}

// stuckAfter is how long past its grace period a pod may stay Terminating
// before it counts as stuck.
const stuckAfter = time.Minute

// detectAnomalies classifies what is wrong with a pod, if anything.
func detectAnomalies(p *Pod, now time.Time) []Anomaly {
	var out []Anomaly
	add := func(kind, container, summary string) {
		out = append(out, Anomaly{p, kind, container, summary})
	}

	if ts := p.Metadata.DeletionTimestamp; ts != nil && now.Sub(*ts) > stuckAfter {
		add(Terminating, "", fmt.Sprintf("terminating for %s", now.Sub(*ts).Round(time.Second)))
	}
	if p.Status.Reason == "Evicted" {
		add(Evicted, "", p.Status.Message)
		return out
	}

	phase := p.Status.Phase
	containerIssue := false
	statuses := append(append([]ContainerStatus{}, p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...)
	for _, cs := range statuses {
		if w := cs.State.Waiting; w != nil {
			switch w.Reason {
			case CrashLoopBackOff:
				add(CrashLoopBackOff, cs.Name, fmt.Sprintf("%d restarts", cs.RestartCount))
				containerIssue = true
			case "ImagePullBackOff", "ErrImagePull":
				add(ImagePullBackOff, cs.Name, w.Reason+": "+cs.Image)
				containerIssue = true
			case CreateContainerConfigError:
				add(CreateContainerConfigError, cs.Name, w.Message)
				containerIssue = true
			}
		}
		t, lt := cs.State.Terminated, cs.LastState.Terminated
		if (t != nil && t.Reason == OOMKilled) || (lt != nil && lt.Reason == OOMKilled) {
			add(OOMKilled, cs.Name, "killed for exceeding its memory limit")
			containerIssue = true
		}
		// A Failed pod's failing containers are reported under Failed.
		if t != nil && t.ExitCode != 0 && t.Reason != OOMKilled && phase != "Failed" {
			add(Error, cs.Name, fmt.Sprintf("exit code %d (%s)", t.ExitCode, t.Reason))
			containerIssue = true
		}
	}

	switch phase {
	case "Pending":
		// A waiting container already explains why the pod is pending.
		if !containerIssue {
			add(Pending, "", p.Status.Message)
		}
	case "Failed":
		add(Failed, "", strings.TrimSpace(p.Status.Reason+" "+p.Status.Message))
	case "Unknown":
		add(Unknown, "", p.Status.Message)
	case "Running":
		if p.Metadata.DeletionTimestamp == nil {
			for _, cs := range p.Status.ContainerStatuses {
				if !cs.Ready && cs.State.Running != nil {
					add(NotReady, cs.Name, "readiness probe failing")
				}
			}
		}
	}
	return out
}

func (d *Doctor) runAnomalyDetection() {
	now := time.Now()
	for i := range d.pods {
		d.anomalies = append(d.anomalies, detectAnomalies(&d.pods[i], now)...)
	}
	if len(d.anomalies) == 0 {
		d.record("pod anomalies", Pass, "none detected")
		return
	}
	for _, a := range d.anomalies {
		d.record("anomaly "+a.Kind, Fail, a.Where()+": "+a.Summary)
		progress("  gathering details on %s...", a.Where())
		d.logs = append(d.logs, LogSection{
			Prefix:      "AN",
			Description: fmt.Sprintf("Anomaly %s on %s", a.Kind, a.Where()),
			Text:        d.gather(a),
		})
	}
}

// gather collects the details that explain an anomaly.
func (d *Doctor) gather(a Anomaly) string {
	p := a.Pod
	ns, name := p.Metadata.Namespace, p.Metadata.Name
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	cs := p.status(a.Container)
	c := p.container(a.Container)

	switch a.Kind {
	case ImagePullBackOff:
		image := ""
		if cs != nil {
			image = cs.Image
			if w := cs.State.Waiting; w != nil {
				line("%s: %s", w.Reason, w.Message)
			}
		}
		if c != nil {
			image = c.Image
		}
		line("Image not pulled: %s", image)
		line("")
		line(d.pullImage(image))

	case CrashLoopBackOff:
		line("$ kubectl logs -n %s %s -c %s --previous", ns, name, a.Container)
		line("%s", d.containerLogs(ns, name, a.Container, "--previous"))

	case CreateContainerConfigError:
		if cs != nil && cs.State.Waiting != nil {
			line("%s", cs.State.Waiting.Message)
		}
		cms, secrets := configRefs(p)
		for _, cm := range cms {
			line("\n$ kubectl get configmap -n %s %s -o yaml", ns, cm)
			line("%s", d.kube.text("get", "configmap", "-n", ns, cm, "-o", "yaml"))
		}
		// Secret values must not end up in a shared report: only check they exist.
		for _, s := range secrets {
			line("\n$ kubectl get secret -n %s %s", ns, s)
			line("%s", d.kube.text("get", "secret", "-n", ns, s))
		}

	case OOMKilled:
		if c != nil {
			line("Memory limit:   %s", orNone(c.Resources.Limits["memory"]))
			line("Memory request: %s", orNone(c.Resources.Requests["memory"]))
		}
		if cs != nil {
			for _, t := range []struct {
				label string
				s     ContainerState
			}{{"Current state", cs.State}, {"Last termination", cs.LastState}} {
				if term := t.s.Terminated; term != nil {
					line("%s: reason=%s exitCode=%d finishedAt=%s", t.label, term.Reason, term.ExitCode, term.FinishedAt.Format(time.RFC3339))
				}
			}
			line("Restarts: %d", cs.RestartCount)
		}

	case Pending, Unknown:
		line("$ kubectl describe pod -n %s %s", ns, name)
		line("%s", d.kube.text("describe", "pod", "-n", ns, name))
		if a.Kind == Unknown && p.Spec.NodeName != "" {
			line("\n$ kubectl get node %s -o wide", p.Spec.NodeName)
			line("%s", d.kube.text("get", "node", p.Spec.NodeName, "-o", "wide"))
		}

	case Failed, Error:
		containers := []string{a.Container}
		if a.Container == "" {
			containers = failedContainers(p)
		}
		for _, cn := range containers {
			line("$ kubectl logs -n %s %s -c %s", ns, name, cn)
			line("%s\n", d.containerLogs(ns, name, cn))
		}

	case Evicted:
		line("Reason:  %s", p.Status.Reason)
		line("Message: %s", p.Status.Message)

	case NotReady:
		line("Readiness probe:")
		if c != nil && len(c.ReadinessProbe) > 0 {
			var pretty bytes.Buffer
			json.Indent(&pretty, c.ReadinessProbe, "", "  ")
			line("%s", pretty.String())
		} else {
			line("(none)")
		}
		line("\n$ kubectl get events -n %s --field-selector involvedObject.name=%s", ns, name)
		line("%s", d.kube.text("get", "events", "-n", ns, "--field-selector", "involvedObject.name="+name, "--sort-by=.lastTimestamp"))

	case Terminating:
		ts := p.Metadata.DeletionTimestamp
		line("Deletion requested at: %s (%s ago)", ts.Format(time.RFC3339), time.Since(*ts).Round(time.Second))
		if g := p.Metadata.DeletionGracePeriodSeconds; g != nil {
			line("Grace period: %ds", *g)
		}
		if len(p.Metadata.Finalizers) > 0 {
			line("Blocking finalizers: %s", strings.Join(p.Metadata.Finalizers, ", "))
		} else {
			line("No finalizers: the node (%s) is not confirming the deletion", orNone(p.Spec.NodeName))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// containerLogs returns the logs of a container, or a hint when they are left
// out of the report because -v was not given.
func (d *Doctor) containerLogs(ns, pod, container string, extra ...string) string {
	if !d.verbose {
		return "(logs omitted, run with -v to include them)"
	}
	args := append([]string{"logs", "-n", ns, pod, "-c", container}, extra...)
	return d.kube.text(append(args, d.tailArg())...)
}

func (d *Doctor) tailArg() string { return fmt.Sprintf("--tail=%d", d.tail) }

// pullImage tries to pull an image with docker as the spec asks, falling back
// to the k3s crictl when docker is not installed (the usual case on k3s).
func (d *Doctor) pullImage(image string) string {
	if image == "" {
		return "Cannot determine the image to pull."
	}
	if _, err := exec.LookPath("docker"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		progress("  docker pull %s", image)
		out, err := exec.CommandContext(ctx, "docker", "pull", image).CombinedOutput()
		return pullResult("docker pull "+image, string(out), err)
	}
	progress("  docker not found, k3s crictl pull %s", image)
	out, err := d.kube.k3s(10*time.Minute, "crictl", "pull", image)
	return pullResult("k3s crictl pull "+image+" (docker not found)", out, err)
}

func pullResult(cmd, out string, err error) string {
	res := "SUCCEEDED"
	if err != nil {
		res = "FAILED: " + err.Error()
	}
	return fmt.Sprintf("$ %s\n%s\nPull %s", cmd, strings.TrimSpace(out), res)
}

// configRefs lists the ConfigMaps and Secrets a pod references.
func configRefs(p *Pod) (configMaps, secrets []string) {
	cms, secs := map[string]bool{}, map[string]bool{}
	for _, c := range append(append([]Container{}, p.Spec.InitContainers...), p.Spec.Containers...) {
		for _, e := range c.Env {
			if e.ValueFrom == nil {
				continue
			}
			if r := e.ValueFrom.ConfigMapKeyRef; r != nil {
				cms[r.Name] = true
			}
			if r := e.ValueFrom.SecretKeyRef; r != nil {
				secs[r.Name] = true
			}
		}
		for _, e := range c.EnvFrom {
			if e.ConfigMapRef != nil {
				cms[e.ConfigMapRef.Name] = true
			}
			if e.SecretRef != nil {
				secs[e.SecretRef.Name] = true
			}
		}
	}
	for _, v := range p.Spec.Volumes {
		if v.ConfigMap != nil {
			cms[v.ConfigMap.Name] = true
		}
		if v.Secret != nil {
			secs[v.Secret.SecretName] = true
		}
	}
	return sortedKeys(cms), sortedKeys(secs)
}

// failedContainers lists the containers of a pod that exited with an error,
// or all of them when none did.
func failedContainers(p *Pod) []string {
	var failed, all []string
	for _, cs := range append(append([]ContainerStatus{}, p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...) {
		all = append(all, cs.Name)
		if t := cs.State.Terminated; t != nil && t.ExitCode != 0 {
			failed = append(failed, cs.Name)
		}
	}
	if len(failed) > 0 {
		return failed
	}
	return all
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		if k != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
