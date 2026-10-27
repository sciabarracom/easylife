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
