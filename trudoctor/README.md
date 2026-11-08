# trudoctor

Checks a local OpenServerless/Trustant installation and prints a report. See
[../spec/trudoctor.md](../spec/trudoctor.md).

```bash
trudoctor > report.txt
```

Progress goes to stderr and the report to stdout. Every report line carries
the prefix of its section:

| Prefix | Content |
| --- | --- |
| `IN:` | package version, hostname, host IP, Kubernetes version |
| `CK:` | each check and its result |
| `AN:` | details of each anomaly detected |
| `OP:` | log of `openserverless-operator-0` |
| `CT:` | log of `controller-0` |
| `TR:` | log of `trustant-0` |

The `OP:`, `CT:` and `TR:` logs, and the container logs among the anomaly
details, are included only with `-v`.

Trustant is optional: when there is no `trustant-0` pod, trudoctor prints
`trustant not installed`, reports `IN: trustant: not installed` and skips the
`trustant-0` checks and logs, without counting it as a failure.

The exit status is 1 when any check fails.

Installed package reporting recognizes `truserverless`, `trustant-serverless`
and the legacy `openserverless`/`trustant` package names. It reports actual dpkg
registration only; a prebuilt WSL filesystem without that registration does not
provide an independently established package version.

A retained Failed pod is reported as a historical warning only when its owning
Job is terminally failed and a newer, completed Job belongs to the same CronJob
UID and namespace. Missing metadata, unreadable Job history, partial completion
or a recreated CronJob do not qualify. A failed execution is still retained:
a later successful run does not recover its lost work. Current failures remain
failures, and readiness, OOM and crash-loop checks retain their existing severity.
The general phase summary leaves Failed-pod classification to individual
anomalies, avoiding two FAIL entries for that same condition. No Job or pod is
deleted, and no retention setting is changed.

The doctor also reads at most 2,000 lines from the last ten minutes of operator
logs for S3 IAM diagnostic markers. Only fixed phase/category values and a
validated HTTP status reach the additional summary; raw log contents and IAM
documents are not copied into that summary. An older generic error is reported
as `unclassified`. A log observation is WARN, not proof of current policy health
or a completed retry. Normal verbose OP log collection retains its existing
behavior. Detailed categories require the corresponding operator image update.

A Running container with `ready=false` is described as not Ready; this alone
does not establish that its probe has run or failed, especially during its
configured startup delay. Probe configuration and events remain in the report.

## Focused qualification

The package/history/diagnostic change passes nine Go tests with 32 subtests,
the race detector and `go vet`. Linux AMD64/ARM64 and Windows AMD64 compilation
were checked; only Linux AMD64 was runtime-qualified.

On an existing WSL2 installation, the original doctor reported two FAIL entries
for a retained historical pod. The candidate passed 30 checks in default mode,
33 in verbose mode, and 30 when executed by an unprivileged account through the
existing setuid contract. The historical Job/pod UIDs, specs and status were
preserved. All 18 persistent pods remained Ready and all 11 PVC UIDs were retained.
The installed original binary is retained privately for rollback.

This WSL filesystem has no platform dpkg registration, so its package version
remains unavailable. Current/legacy installed-name cases were unit-tested;
this qualification does not claim a fresh package installation or ARM64/Windows
runtime acceptance. Synthetic S3 errors were tested on loopback, with a separate
read-only check of the real policy; no invalid policy was installed in a datastore.

| Flag | Default | |
| --- | --- | --- |
| `-namespace` | `openserverless` | Namespace of the platform pods |
| `-tail` | `2000` | Log lines collected per container |
| `-v` | off | Include the container logs in the report |

## Packaging

[../prepare.sh](../prepare.sh) builds it and installs it as `/usr/bin/trudoctor`,
and [../package.sh](../package.sh) ships it from there, setuid root, so any user can run it: kubectl runs
as `k3s kubectl`, which needs root (without setuid, trudoctor falls back to
`sudo k3s kubectl`).

When started setuid, trudoctor becomes fully root and replaces the caller's
environment with a fixed `PATH`, keeping only the proxy variables, so a user
cannot make it run their own `k3s`, `dig` or `docker`, or point `KUBECONFIG`
at an arbitrary file. It writes no files.
