# trulicense

Activates a Trustant licence on a local OpenServerless installation. See
[../spec/trulicense.md](../spec/trulicense.md) and the licence format in
[../spec/license_format.md](../spec/license_format.md).

```bash
sudo trulicense          # paste the licence when asked
sudo trulicense -reset   # remove the proxy and the provisioning token
```

It must run as root: it is shipped setuid root, otherwise use `sudo`. It checks
that `k3s kubectl` works and the `openserverless` namespace exists, then:

1. reads the licence from stdin and verifies its Ed25519 signature with the
   embedded [master_key_pub](master_key_pub), its version and its expiry;
2. provisions in `openserverless` the `trulicense-proxy` nginx (image
   `nginx:stable-alpine`, pulled on first use) which, for each licensed host,
   sends `http://<host>` to `miniops.me` and `http://<app>.<host>` to
   `<app>.miniops.me` through the Traefik ingress controller, with websockets
   and bodies up to 1g;
3. creates the `openserverless-user-provisioning-token` secret with a random
   32-digit token, asking before replacing an existing one, and shows the token
   once.

Hosts accumulate: the `trulicense-proxy` configmap keeps every host provisioned
so far, so activating several licences publishes all their hosts, and running
it again with the same licence changes nothing. Hosts that are not public DNS
names (a port, a path, `localhost`, `*.miniops.me`) are skipped with a warning.

| Resource (namespace `openserverless`) | Content |
| --- | --- |
| `configmap/trulicense-proxy` | `hosts` provisioned, nginx `default.conf` |
| `deployment/trulicense-proxy` | nginx, restarted when the config changes |
| `service/trulicense-proxy` | port 80 |
| `ingress/trulicense-proxy` | `<host>` and `*.<host>` for each host |
| `secret/openserverless-user-provisioning-token` | `token` |

`-reset` asks for confirmation, then deletes all of them.
