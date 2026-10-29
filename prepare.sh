#!/bin/bash
set -euo pipefail
cd "$(dirname $0)"

echo "=== PREPARE ==="

GO_VERSION="${GO_VERSION:-1.27.3}"
case "$(uname -m)" in
    x86_64) GO_ARCH=amd64 ;;
    aarch64|arm64) GO_ARCH=arm64 ;;
    *) echo "unsupported arch $(uname -m)"; exit 1 ;;
esac
export PATH="/usr/local/go/bin:${PATH}"
if ! go version 2>/dev/null | grep -q "go${GO_VERSION} "
then
    sudo rm -rf /usr/local/go
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz" | sudo tar xzf - -C /usr/local
fi
go version

echo "=== compile trulicense and trudoctor ==="

# Static, stripped binaries, installed setuid root: both need root for
# `k3s kubectl`.
for tool in trudoctor trulicense; do
    CGO_ENABLED=0 go build -C "${tool}" -trimpath -ldflags="-s -w" -o "${tool}" .
    sudo install -v -o root -g root -m 4755 "${tool}/${tool}" "/usr/bin/${tool}"
done

echo "=== remove go ==="
sudo rm -Rf /usr/local/go

echo "=== install k3s ==="

if which k3s-killall.sh
then sudo k3s-killall.sh &&  sudo k3s-uninstall.sh
fi

curl -sfL https://get.k3s.io | sudo sh -s - --node-name trustant

while ! sudo k3s kubectl get storageclass | grep default
do sleep 1
done

mkdir -p ~/.ops/tmp
sudo cat /etc/rancher/k3s/k3s.yaml >~/.ops/tmp/kubeconfig
ops debug kube nodes

echo "=== install openserverless ==="

ops config slim
ops config apihost miniops.me --protocol=http

if ! ops setup kubernetes create
then trudoctor ; exit 1
fi

if ! ops setup openserverless streamer deploy
then trudoctor ; exit 1
fi

if ! ops setup openserverless system-api deploy
then trudoctor ; exit 1
fi
if ! ops setup openserverless tika deploy
then trudoctor; exit 1
fi

if test -d oplugins-truinst
then
     if ! ops truinst trustant deploy
     then trudoctor ; exit 1
     fi
     sudo install -D -m 0644 blockports.conf /etc/systemd/system/k3s.service.d/blockports.conf
fi

echo "=== check ==="
trudoctor || true

echo "=== trim down openserverless ==="

# cleanup
sudo du -sh /var/lib/rancher
# Cleanup is best-effort: missing runtime images or busy artifacts should not
# abort payload preparation.
runtime_images="$(sudo k3s ctr images list -q | grep -E 'openserverless-runtime-(go|php|java)' || true)"
printf '%s\n' "${runtime_images}" | xargs -r -L1 sudo k3s crictl rmi || true

sudo k3s crictl rmp -a || true
sudo k3s crictl rmi --prune || true
sudo k3s ctr -n k8s.io content prune references || true
sudo du -sh /var/lib/rancher

echo "=== stopping openserverless ==="

sudo systemctl stop k3s
/usr/local/bin/k3s-killall.sh

sync
sleep 2

echo "=== ready to package ==="
