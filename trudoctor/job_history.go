package main

import (
	"fmt"
	"strings"
	"time"
)

type OwnerReference struct {
	APIVersion, Kind, Name, UID string
	Controller                  bool
}

func controllerOwner(refs []OwnerReference, kind string) *OwnerReference {
	for i := range refs {
		r := &refs[i]
		if r.Controller && r.APIVersion == "batch/v1" && r.Kind == kind && r.Name != "" && r.UID != "" {
			return r
		}
	}
	return nil
}

func jobCondition(j Job, kind string) time.Time {
	for _, c := range j.Status.Conditions {
		if c.Type == kind && c.Status == "True" {
			return c.LastTransitionTime
		}
	}
	return time.Time{}
}

// A newer successful Job must belong to the same CronJob UID, not merely
// share its name. Age, a successful pod, or partial completions are insufficient.
func recoveredJobFailure(p *Pod, jobs []Job) string {
	if p.Status.Phase != "Failed" || p.Metadata.Namespace == "" {
		return ""
	}
	owner := controllerOwner(p.Metadata.OwnerReferences, "Job")
	if owner == nil {
		return ""
	}
	for _, failed := range jobs {
		if failed.Metadata.UID != owner.UID || failed.Metadata.Name != owner.Name || failed.Metadata.Namespace != p.Metadata.Namespace {
			continue
		}
		cron := controllerOwner(failed.Metadata.OwnerReferences, "CronJob")
		failedAt := jobCondition(failed, "Failed")
		if cron == nil || failedAt.IsZero() || failed.Status.Active != 0 || !jobCondition(failed, "Complete").IsZero() {
			return ""
		}
		for _, cs := range append(append([]ContainerStatus{}, p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...) {
			if cs.State.Terminated != nil && cs.State.Terminated.FinishedAt.After(failedAt) {
				failedAt = cs.State.Terminated.FinishedAt
			}
		}
		for _, success := range jobs {
			next := controllerOwner(success.Metadata.OwnerReferences, "CronJob")
			if next != nil && next.UID == cron.UID && next.Name == cron.Name && success.Metadata.Namespace == p.Metadata.Namespace &&
				success.Metadata.UID != "" && success.Metadata.UID != failed.Metadata.UID && success.Metadata.CreationTimestamp.After(failedAt) &&
				success.Status.Succeeded > 0 && success.Status.Active == 0 && !jobCondition(success, "Complete").IsZero() &&
				jobCondition(success, "Failed").IsZero() && success.Status.CompletionTime.After(success.Metadata.CreationTimestamp) {
				return success.Metadata.Name
			}
		}
	}
	return ""
}

func (d *Doctor) anomalyCheck(a Anomaly) (Status, string, string) {
	if a.Kind == Failed {
		if next := recoveredJobFailure(a.Pod, d.jobs); next != "" {
			return Warn, "historical job failure", a.Where() + ": later CronJob job " + next + " completed; original failure retained"
		}
	}
	return Fail, "anomaly " + a.Kind, a.Where() + ": " + a.Summary
}

func (d *Doctor) recordPodPhases(pods []*Pod) {
	var bad, failed []string
	for _, p := range pods {
		if p.Status.Phase == "Failed" {
			failed = append(failed, p.Metadata.Name)
		} else if p.Status.Phase != "Running" && p.Status.Phase != "Succeeded" {
			bad = append(bad, fmt.Sprintf("%s (%s)", p.Metadata.Name, p.Status.Phase))
		}
	}
	switch {
	case len(bad) > 0:
		d.record("pods Running or Completed", Fail, strings.Join(bad, ", "))
	case len(failed) > 0:
		d.record("pods Running or Completed", Warn, "Failed pods reported individually under anomalies: "+strings.Join(failed, ", "))
	default:
		d.record("pods Running or Completed", Pass, fmt.Sprintf("all %d pods", len(pods)))
	}
}
