# Apache OpenServerless Packages

Builds the `openserverless` Debian package: a self-contained, offline-installable
bundle of [k3s](https://k3s.io) with Apache OpenServerless already deployed into
it.

The package is not built from sources. Instead, a clean machine is used as a
mould: k3s is installed, OpenServerless is deployed on top of it, the resulting
cluster is trimmed and stopped, and the entire on-disk state — binaries, systemd
units and the whole `/var/lib/rancher/k3s` tree — is tarred straight from its
real location into a `.deb`. Installing that package on a target machine restores
a cluster that is already up and configured, with no image pulls and no network
access required.

Because the payload is a snapshot of a live k3s installation, **builds must run
on a disposable machine** (a CI runner or a throwaway VM). The scripts install
system packages, create a `trustant` user and take over k3s on the host.

## Layout

| File | Role |
| --- | --- |
| [setup.sh](setup.sh) | Installs build dependencies, the `trustant` user and the `ops` CLI |
| [prepare.sh](prepare.sh) | Installs k3s, deploys OpenServerless onto it, trims and stops it |
| [package.sh](package.sh) | Assembles the `.deb` from the prepared machine state |
| [publish.sh](publish.sh) | Uploads the `.deb` to S3 and records it in the GitHub release |
| [index.sh](index.sh) | Regenerates the bucket's `index.html` and `index.json` |
| [trudoctor/](trudoctor/) | Diagnostic tool, built by `prepare.sh` and shipped setuid root as `/usr/bin/trudoctor` |
| [trulicense/](trulicense/) | Licence activation, built by `prepare.sh` and shipped setuid root as `/usr/bin/trulicense` |
| [.github/workflows/build.yml](.github/workflows/build.yml) | Runs all four, for amd64 and arm64 |

Build output goes to `../dist` — a sibling of this directory, not inside it.

## Build

Run the scripts in order on a disposable Debian/Ubuntu machine:

```bash
./setup.sh          # dependencies, trustant user, ops CLI
./prepare.sh        # install k3s + deploy OpenServerless (slow)
sudo -E ./package.sh
```

This produces, in `../dist`:

- `openserverless_<version>_<arch>.deb`
- `version.txt` — the version string, so later steps need not re-derive it

`package.sh --test` builds a much smaller package that bundles only the k3s
control plane (`/var/lib/rancher/k3s/server`) and skips the data tree. It is for
validating the install flow quickly, not for release.

The first argument selects the flavour, `openserverless` (the default) or
`trustant`. It sets the package name and the product name shown in the
install messages. An optional second argument overrides the version:

```bash
sudo -E ./package.sh trustant          # dist/trustant_<version>_<arch>.deb
sudo -E ./package.sh trustant 1.0.0    # dist/trustant_1.0.0_<arch>.deb
```

### Versioning

The version is derived from `ops -info`, as the ops branch plus the first six
characters of the tasks commit hash — for example `0.1.0+f7613c` — unless one
is given on the command line. It is written
to `../dist/version.txt` by `package.sh` and read back by `publish.sh`.

## Publish

```bash
TRUPUB_ENDPOINT=... TRUPUB_REGION=... TRUPUB_KEY=... TRUPUB_SECRET=... ./publish.sh
```

`publish.sh` uploads `../dist/openserverless_<version>_<arch>.deb` to the
`openserverless/` folder of the `trupub` bucket, publicly served from
<https://pub.trustant.ai/openserverless>, then adds a link to the notes of the
`v<version>` GitHub release, creating the release if it does not yet exist.

The amd64 and arm64 jobs publish to the same release tag concurrently, so the
notes are updated additively: each run replaces only the line for its own
architecture and preserves the other's, retrying if a concurrent write wins the
race. Without `GH_TOKEN`/`GITHUB_TOKEN` the upload still runs and the release
step is skipped.

The workflow expects four repository secrets: `TRUPUB_ENDPOINT`, `TRUPUB_REGION`, `TRUPUB_KEY`
and `TRUPUB_SECRET`.

## Download index

`publish.sh` calls [index.sh](index.sh) after every upload, and it can also be
run on its own with the same four `TRUPUB_*` variables:

```bash
TRUPUB_ENDPOINT=... TRUPUB_REGION=... TRUPUB_KEY=... TRUPUB_SECRET=... ./index.sh
```

It lists the `.deb` files currently in the folder and writes two files next to them:

- `https://pub.trustant.ai/openserverless/index.html` — a human-readable page grouping packages by version, with each
  version's architectures and publication date, newest version first.
- `https://pub.trustant.ai/openserverless/index.json` — the same data for scripts, keyed by architecture:

```json
{
  "amd64": {
    "0.1.0+f1553b": "https://pub.trustant.ai/openserverless/openserverless_0.1.0+f1553b_amd64.deb",
    "latest": "https://pub.trustant.ai/openserverless/openserverless_0.1.0+f1553b_amd64.deb"
  }
}
```

`latest` points at the most recently published package for that architecture, so
an installer can fetch the current build without knowing a version number:

```bash
arch="$(dpkg --print-architecture)"
url="$(curl -sS https://pub.trustant.ai/openserverless/index.json | jq -r ".\"$arch\".latest")"
```

The page is generated purely from what the bucket holds, so it is always
consistent with reality: re-running it picks up packages published by other
builds and drops any that were deleted.

## CI

`Build` is a manual workflow (`workflow_dispatch`) with an `arch` input of
`all` (default), `amd` or `arm`. Each architecture runs the full
setup → prepare → package → publish chain on its own runner, and the two jobs
converge on a single `v<version>` release carrying both `.deb` files.

## Installing

```bash
sudo apt install ./openserverless_<version>_<arch>.deb
```

If the `.deb` sits in your home directory, apt prints a notice that it cannot
download unsandboxed as `_apt` — that is harmless (note the `N:` prefix): apt
falls back to copying the file as root and the install proceeds. Move the file
to `/tmp` to avoid it.

The package refuses to install if OpenServerless is already installed, if another
k3s installation exists at `/var/lib/rancher/k3s`, or if any of ports 80, 443 or 6443 is in use — it
bundles its own k3s and cannot coexist with another one. On success it creates
the `trustant` user (uid/gid 769) and starts k3s. When the package bundles
trustant it also ships the k3s drop-in
`/etc/systemd/system/k3s.service.d/blockports.conf` (from
[blockports.conf](blockports.conf), installed by `prepare.sh`), a firewall that
lets ports 80/443/6443 accept only local connections (loopback and the k3s pod
network, IPv4 and IPv6).

### The `trustant` home

`/home/trustant` ships inside the package, already owned `769:769`, so the
platform can be driven by a dedicated unprivileged account with no further
setup:

| Path | Contents |
| --- | --- |
| `.local/bin/ops` | the `ops` CLI, on `PATH` via `.bashrc` |
| `.ops` | the ops configuration: downloaded olaris tree, `config.json` |
| `.ops/tmp/kubeconfig` | the bundled cluster's credentials, where `ops` looks for them |
| `workspace` | the per-app workspace area |
| `.bashrc` / `.profile` | `OPS_REPO`, `OPS_BRANCH` and `PATH`; `.profile` just sources `.bashrc`, so login and non-login shells agree |

```bash
sudo -iu trustant ops -info
```

The platform is then reachable locally at <http://trustant.miniops.me>, or over
an SSH tunnel for a remote host:

```bash
ssh -L <port>:127.0.0.1:80 <your-server>
# then browse http://trustant.miniops.me:<port>
```

Removal (`apt-get remove`) stops k3s and wipes `/var/lib/rancher/k3s` but leaves
user data under `/home/trustant`; `apt-get purge openserverless` removes that
too.
