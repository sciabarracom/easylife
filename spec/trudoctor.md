Implement in go a doctor that executes the following checks

# system tests

- check miniops.me, trustant.miniops.me, truacp.miniops.me and vite.miniops.me returns 127.0.0.1
  - Unix: `dig +short <host>`
  - Windows: PowerShell `Resolve-DnsName`
- check http://miniops.me/api/info returns a json and .description is OpenWhisk
- check github.com and ghcr.io is reachable

# tests with kubectl

if you are root use k3s kubectl otherwise sudo k3s kubectl

Use kubectl to test
- there is a node trustant
- there is a namespace openserverless
- check all the pods in namespace openserverless and verify they are all Running or Completed
- check there are the following pods running:

- couchdb-0
- openserverless-etcd-0
- openserverless-mongodb-0
- openserverless-operator-0
- openserverless-postgres-1-0
- openserverless-static-0
- openserverless-streamer-api-0
- openserverless-system-api-0
- openserverless-milvus-standalone-*
- redis-0
- registry-0
- seaweedfs-0
- trustant-0
- tika-0
- kubegres-controller-manager-*

and there are those completed jobs:

- couchdb-init-*
- cron-init-*
- preload-runtimes-*

# Anomaly Detection

- detect anomalies on pods and extracts info on the anomalies with describe and logs:
  - Pending: pod accepted but containers not yet running (scheduling, resource constraints)
  - Failed: all containers terminated with at least one failure
  - Unknown: pod state cannot be determined (node communication failure)
  - CrashLoopBackOff: container keeps crashing and kubelet is backing off restarts
  - ImagePullBackOff / ErrImagePull: cannot pull the container image
  - CreateContainerConfigError: bad config (missing ConfigMap/Secret, etc.)
  - OOMKilled: container killed for exceeding memory limits
  - Error: container exited with a non-zero exit code
  - Not Ready: pod is running but readiness probe is failing
  - Evicted: pod was evicted due to node pressure
  - Terminating: pod stuck in terminating state

- if you find anomalies, gather details:
  - ImagePullBackOff / ErrImagePull: check which image is not pulled, inform and try to pull with a docker command, reporting the result
  - CrashLoopBackOff: extract the logs of the failing pod using --previous
  - CreateContainerConfigError: extract the yaml of the config
  - OOMKilled: show the memory limits and last termination reason
  - Pending: describe the pod to show scheduling issues or unmet resource requests
  - Failed / Error: extract the logs of the failing container
  - Evicted: show the eviction reason from pod status
  - Not Ready: show readiness probe config and recent events
  - Terminating: show how long the pod has been terminating and if finalizers are blocking

# Reporting

Reports, adding the prefixes:

- IN: openserverless package version, hostname, host ip, kubernetes version
- CK: all the checks performed and the results
- AN: the log of any anomaly detected
- OP: the log of openserverless-operator-0
- CT: the log of controller-0
- TR: the log of trustant-0

