package main

import (
	"regexp"
	"strings"
)

const s3VerificationError = "Could not verify anonymous S3 web-only grants"

var safeS3Category = regexp.MustCompile(`Could not verify anonymous S3 web-only grants \[phase=(read_policy|decode_policy|validate_policy) category=(transport_timeout|transport_error|http_error|json_error|schema_error|anonymous_identity|grant_mismatch)(?: status=([1-5][0-9]{2}|unknown))?\]`)

// Read bounded, recent operator logs without reporting their raw contents.
// An old exception is diagnostic evidence, not proof of current policy failure.
func (d *Doctor) collectS3Diagnostics() {
	p := d.findPod("openserverless-operator-0")
	if p == nil {
		return
	}
	text, err := d.kube.kubectl("logs", "-n", p.Metadata.Namespace, p.Metadata.Name,
		"--all-containers", "--since=10m", "--tail=2000")
	if err != nil {
		d.record("S3 IAM diagnostics", Warn, "recent operator logs unavailable; cause and retry outcome unknown")
		return
	}
	d.recordS3Diagnostics(text)
}

func (d *Doctor) recordS3Diagnostics(text string) {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if !strings.Contains(lines[i], s3VerificationError) {
			continue
		}
		detail := "phase=unknown category=unclassified"
		if m := safeS3Category.FindStringSubmatch(lines[i]); m != nil {
			detail = "phase=" + m[1] + " category=" + m[2]
			if m[2] == "http_error" && m[3] != "" {
				detail += " status=" + m[3]
			}
		}
		d.record("S3 IAM diagnostic observed", Warn, detail+"; recent log evidence only, current policy and retry outcome unknown")
		return
	}
}
