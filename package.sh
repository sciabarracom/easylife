#!/bin/bash
# Build the trustant .deb per ./package.md.
set -euo pipefail
cd "$(dirname $0)"

echo "=== PACKAGE ==="
# Usage: ./package.sh [--test] [openserverless|trustant] [<version>]
#
# --test builds a reduced package that bundles ONLY /var/lib/rancher/k3s/server
# (the control plane), skipping the rest of the k3s state tree (the data). Use it
# to verify the install flow quickly without shipping the full data set.
#
# The first parameter selects the flavour: it sets the package name and the
# product name shown to the user. Default is "openserverless".
# The optional <version> overrides the version derived from ops, e.g.
# `./package.sh trustant 1.0.0` builds dist/trustant_1.0.0_<arch>.deb.
TEST_BUILD=0
POSITIONAL=()
for arg in "$@"; do
    case "$arg" in
        --test) TEST_BUILD=1 ;;
        -*) echo "Unknown option: $arg" >&2; exit 1 ;;
        *) POSITIONAL+=("$arg") ;;
    esac
done
if [ "${#POSITIONAL[@]}" -gt 2 ]; then
    echo "Usage: $0 [--test] [openserverless|trustant] [<version>]" >&2
    exit 1
fi

PKGNAME="${POSITIONAL[0]:-openserverless}"
case "${PKGNAME}" in
    openserverless) PRODUCT="Apache OpenServerless" ;;
    trustant) PRODUCT="Trustant" ;;
    *) echo "Invalid package name: ${PKGNAME} (expected openserverless or trustant)" >&2; exit 1 ;;
esac

OPS_INFO="$(ops -info)"
info_field() { printf '%s\n' "${OPS_INFO}" | awk -v k="$1:" '$1==k{print $2; exit}'; }
OPS_BRANCH="$(info_field OPS_BRANCH)"
OPS_TASKS="$(info_field OPS_TASKS)"
OPS_REPO="${OPS_REPO:-$(info_field OPS_REPO)}"
if [ -z "${OPS_BRANCH}" ]; then
    echo "ops -info did not report OPS_BRANCH" >&2
    exit 1
fi

if [ "${#POSITIONAL[@]}" -eq 2 ]; then
    VERSION="${POSITIONAL[1]}"
    # Debian policy: version starting with a digit.
    if ! [[ "${VERSION}" =~ ^[0-9][A-Za-z0-9.+~-]*$ ]]; then
        echo "Invalid version: ${VERSION}" >&2
        exit 1
    fi
else
    # Version is derived from `ops -info`: the ops branch plus the short (6
    # char) tasks commit hash, e.g. 0.1.0+f7613c
    if [ -z "${OPS_TASKS}" ]; then
        echo "Cannot determine version: ops -info did not report OPS_TASKS" >&2
        exit 1
    fi
    VERSION="${OPS_BRANCH}+${OPS_TASKS:0:6}"
fi
ARCH="$(dpkg --print-architecture)"
DISTDIR="$(cd .. && pwd)/dist"
if [ "${TEST_BUILD}" -eq 1 ]; then
    DEB="${DISTDIR}/${PKGNAME}_${VERSION}_${ARCH}-test.deb"
else
    DEB="${DISTDIR}/${PKGNAME}_${VERSION}_${ARCH}.deb"
fi

mkdir -p "${DISTDIR}"

# Record the version so downstream steps (CI) don't have to re-derive it.
echo "${VERSION}" > "${DISTDIR}/version.txt"
echo "Version: ${VERSION} (written to ${DISTDIR}/version.txt)"

# Ensure k3s is stopped before we read its files.
sudo systemctl stop k3s.service 2>/dev/null || true
if [ -x /usr/local/bin/k3s-killall.sh ]; then
    sudo /usr/local/bin/k3s-killall.sh >/dev/null 2>&1 || true
fi
sync

mkdir -p /var/tmp
# Staging dir holds ONLY the DEBIAN/ packaging metadata. Every payload file —
# including the generated helper scripts — lives at its real on-disk location
# and is tarred straight from there into data.tar, with no intermediate copy.
PKGROOT="/var/tmp/package"
sudo rm -rf "${PKGROOT}"
mkdir -p "${PKGROOT}"
cleanup() { sudo rm -rf "${PKGROOT}"; }
trap cleanup EXIT


# Home of the "trustant" user (uid/gid 769), shipped inside the package so the
# platform can be driven by a dedicated unprivileged account. setup.sh already
# created this home on the build host and ran `ops -update` as that user, so
# ${TRUHOME}/.ops is the real, trustant-owned config tree; we only top it up
# with the CLI, the kubeconfig and the shell environment, then tar it from its
# real location like everything else.
TRUHOME="/home/trustant"

# Fall back to the build operator's own ~/.ops if setup.sh did not run as
# expected and the trustant home has no config tree yet.
OPS_CONFIG_SRC="${TRUHOME}/.ops"
if [ ! -d "${OPS_CONFIG_SRC}" ]; then
    OPS_CONFIG_SRC="${HOME}/.ops"
fi

for f in /usr/bin/ops "${OPS_CONFIG_SRC}" /etc/rancher/k3s/k3s.yaml; do
    if [ ! -e "$f" ]; then
        echo "Missing required file: $f" >&2
        exit 1
    fi
done

sudo install -d -m 0755 "${TRUHOME}" "${TRUHOME}/.local" "${TRUHOME}/.local/bin"

# The ops CLI. setup.sh puts it at /usr/bin/ops on the build host, which is the
# source here; the package ships it ONLY under the trustant home, not in /usr/bin.
sudo cp -a /usr/bin/ops "${TRUHOME}/.local/bin/ops"
sudo chmod 0755 "${TRUHOME}/.local/bin/ops"

# The ops configuration (downloaded olaris tree, config.json...). Already in
# place when it is the trustant home's own; copied in when falling back.
if [ "${OPS_CONFIG_SRC}" != "${TRUHOME}/.ops" ]; then
    sudo rm -rf "${TRUHOME}/.ops"
    sudo cp -a "${OPS_CONFIG_SRC}" "${TRUHOME}/.ops"
fi

# The cluster credentials, where ops expects to find them.
sudo install -d -m 0755 "${TRUHOME}/.ops/tmp"
sudo cp /etc/rancher/k3s/k3s.yaml "${TRUHOME}/.ops/tmp/kubeconfig"
sudo chmod 0600 "${TRUHOME}/.ops/tmp/kubeconfig"

# The per-app workspace area, shipped with the right ownership rather than
# created by the postinst.
sudo install -d -m 0755 "${TRUHOME}/workspace"

# The environment lives in .bashrc so it applies to non-login shells too
# (sudo -u trustant, ssh "cmd", ...); .profile just sources it, so login shells
# get exactly the same settings from a single source of truth.
sudo tee "${TRUHOME}/.bashrc" >/dev/null <<BASHRC
export OPS_REPO=${OPS_REPO}
export OPS_BRANCH=${OPS_BRANCH}
export PATH=\$PATH:${TRUHOME}/.local/bin
BASHRC
sudo chmod 0644 "${TRUHOME}/.bashrc"

sudo tee "${TRUHOME}/.profile" >/dev/null <<PROFILE
[ -f ${TRUHOME}/.bashrc ] && . ${TRUHOME}/.bashrc
PROFILE
sudo chmod 0644 "${TRUHOME}/.profile"

# Everything under /home/trustant belongs to the trustant user, created by the
# postinst with the same numeric uid/gid. --numeric-owner records 769:769 in
# data.tar.
sudo chown -R 769:769 "${TRUHOME}"

# trudoctor (diagnostics) and trulicense (licence activation) are built and
# installed setuid root under /usr/bin by prepare.sh: any user can run them,
# as both need root for `k3s kubectl`. They are shipped from there as is.

# Files bundled verbatim from their real locations (no copy).
FILES=(
    /usr/bin/trudoctor
    /usr/bin/trulicense
    /usr/local/bin/k3s
    /usr/local/bin/crictl
    /usr/local/bin/k3s-killall.sh
    /usr/local/bin/k3s-uninstall.sh
    /etc/rancher/k3s/k3s.yaml
    /etc/systemd/system/k3s.service
    /etc/systemd/system/k3s.service.env
)

for f in "${FILES[@]}"; do
    if [ ! -e "$f" ]; then
        echo "Missing required file: $f" >&2
        exit 1
    fi
done

# Normal build bundles the whole k3s state tree; --test bundles only the
# control-plane subdir (server), skipping the data, to validate the install
# flow with a much smaller package.
if [ "${TEST_BUILD}" -eq 1 ]; then
    K3S_SUBTREE="/var/lib/rancher/k3s/server"
    echo "TEST build: bundling only ${K3S_SUBTREE} (skipping data)."
else
    K3S_SUBTREE="/var/lib/rancher/k3s"
fi

if [ ! -d "${K3S_SUBTREE}" ]; then
    echo "Missing required directory: ${K3S_SUBTREE}" >&2
    exit 1
fi

# The k3s drop-ins (the blockports.conf firewall) exist only when prepare.sh
# deployed trustant; the directory is bundled as a whole when present.
DROPIN_DIR="/etc/systemd/system/k3s.service.d"
TREES=("${TRUHOME}")
if [ -d "${DROPIN_DIR}" ]; then
    TREES+=("${DROPIN_DIR}")
else
    echo "No ${DROPIN_DIR}: packaging without k3s drop-ins."
fi

# Build data.tar directly from the source paths, no intermediate copy.
# -C / anchors every path at the filesystem root so each file lands at its
# absolute install location inside the package.
#
# dpkg needs the parent-directory entries present in data.tar (it creates each
# dir before unpacking files into it). tar does NOT emit ancestor dirs for
# individually-listed files, so we build an explicit member list: every file
# plus all of its ancestor directories, deduped and sorted so each dir entry
# precedes its contents. --no-recursion keeps listed dirs from pulling in
# siblings; the k3s state tree is added separately (recursively).
DATA_TAR="${PKGROOT}/data.tar.zst"
DATA_TAR_RAW="${PKGROOT}/data.tar"
echo "Building data.tar (tarring from source, no copy)..."

MEMBERS_FILE="${PKGROOT}/members.lst"
{
    # Each individual file plus all of its ancestor directories. The k3s state
    # dir and the other trees are listed too so their ancestors (./var,
    # ./var/lib, ./home, ...) are emitted as directory entries; their contents
    # are appended recursively in passes 2 and 3.
    for f in "${FILES[@]}" "${K3S_SUBTREE}" "${TREES[@]}"; do
        d="$f"
        while [ "$d" != "/" ]; do
            echo ".${d}"
            d="$(dirname "$d")"
        done
    done
} | sort -u | sudo tee "${MEMBERS_FILE}" >/dev/null

# Build an UNCOMPRESSED tar in two passes, then compress once at the end
# (you cannot --append to a zstd stream). Ownership is preserved as-is from
# disk (--numeric-owner records the real uid/gid); we do NOT force root:root,
# because the k3s state tree contains files that must keep their original
# ownership to work on the target.
#
# Pass 1: directory skeleton + individual files, no recursion (the k3s dir
# entry itself is included here, but not its contents).
sudo tar --create --numeric-owner \
    -f "${DATA_TAR_RAW}" \
    -C / \
    --no-recursion -T "${MEMBERS_FILE}"

# Pass 2: append the k3s subtree recursively, excluding per-node TLS material.
sudo tar --append --numeric-owner \
    -f "${DATA_TAR_RAW}" \
    -C / \
    --exclude='./var/lib/rancher/k3s/server/tls' \
    ".${K3S_SUBTREE}"

# Pass 3: append the other trees recursively: the staged trustant home (owned
# 769:769 on disk, so --numeric-owner carries that ownership straight into the
# package) and the k3s drop-ins.
for t in "${TREES[@]}"; do
    sudo tar --append --numeric-owner \
        -f "${DATA_TAR_RAW}" \
        -C / \
        ".${t}"
done

# Compress to data.tar.zst.
sudo zstd -q -f --rm "${DATA_TAR_RAW}" -o "${DATA_TAR}"

# Installed-Size = uncompressed payload size, minus the excluded TLS dir.
INSTALLED_SIZE=$(
    {
        for f in "${FILES[@]}"; do sudo du -sk "$f"; done
        sudo du -sk --exclude='*/server/tls' "${K3S_SUBTREE}"
        for t in "${TREES[@]}"; do sudo du -sk "$t"; done
    } | awk '{s+=$1} END{print s}'
)

sudo mkdir -p "${PKGROOT}/DEBIAN"

sudo tee "${PKGROOT}/DEBIAN/control" >/dev/null <<EOF
Package: ${PKGNAME}
Version: ${VERSION}
Section: admin
Priority: optional
Architecture: ${ARCH}
Depends: iptables, systemd
Installed-Size: ${INSTALLED_SIZE}
Maintainer: Nuvolaris <info@nuvolaris.io>
Description: ${PRODUCT} k3s-based platform package
 Bundles k3s with ${PRODUCT} preinstalled for offline installation.
EOF


# Maintainer scripts live in ./debian; @PKGNAME@ and @PRODUCT@ placeholders
# are filled in here.
for s in preinst postinst prerm postrm; do
    sed -e "s/@PKGNAME@/${PKGNAME}/g" -e "s/@PRODUCT@/${PRODUCT}/g" "debian/${s}" \
        | sudo tee "${PKGROOT}/DEBIAN/${s}" >/dev/null
    sudo chmod 0755 "${PKGROOT}/DEBIAN/${s}"
done

echo Packaging
# data.tar.zst was already built directly from the source paths (no copy).
# Build control.tar.zst from the generated DEBIAN dir, then assemble the .deb
# manually with ar (dpkg-deb --build needs a single tree, which we avoid here).
sudo chown -R 0:0 "${PKGROOT}/DEBIAN"
sudo tar --create --zstd --numeric-owner --owner=0 --group=0 \
    -f "${PKGROOT}/control.tar.zst" \
    -C "${PKGROOT}/DEBIAN" .

echo "2.0" | sudo tee "${PKGROOT}/debian-binary" >/dev/null

rm -f "${DEB}"
( cd "${PKGROOT}" && sudo ar rc "${DEB}" debian-binary control.tar.zst data.tar.zst )
echo Done

echo "Built: ${DEB}"
