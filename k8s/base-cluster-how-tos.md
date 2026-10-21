# Base Cluster How-To's

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Prerequisites](#prerequisites)
- [Clean Reconstruction](#clean-reconstruction)
- [Local Dev](#local-dev)
  - [Manage Cluster](#manage-cluster)
- [QA](#qa)
  - [Create LXD Virtual Machines](#create-lxd-virtual-machines)
  - [Deploy K3s Cluster](#deploy-k3s-cluster)
  - [Synchronize Host and QA VM Clocks](#synchronize-host-and-qa-vm-clocks)
  - [Manage Cluster](#manage-cluster-1)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

## Prerequisites

1. **Kubernetes cluster dependencies**
- `cd k8s`
- **QA**: `make prereq-k8s-flux`
- `make prereq-k8s-all`

2. **QA cluster configuration**
- Make sure that the host uses the `nftables` instead of `iptables-legacy`:
    ```bash
    sudo update-alternatives --config iptables
    There are 2 choices for the alternative iptables (providing /usr/sbin/iptables).
    
      Selection    Path                       Priority   Status
    ------------------------------------------------------------
      0            /usr/sbin/iptables-nft      20        auto mode
      1            /usr/sbin/iptables-legacy   10        manual mode
    * 2            /usr/sbin/iptables-nft      20        manual mode
    ```

- `iptables` rules should be modified to allow your host's firewall to let forward traffic by
  default.
    ```bash
    sudo iptables -P FORWARD ACCEPT
    ``` 

- Make changes to `iptables` rules permanent:
    ```bash
    sudo apt-get update
    sudo apt-get install iptables-persistent
    sudo netfilter-persistent save
    ```

- The forwarding change takes effect without restarting Docker or LXD.

- Validate if the rule was applied:
    ```bash
    sudo iptables -L FORWARD -v -n | grep "Chain FORWARD"
    Chain FORWARD (policy ACCEPT 340 packets, 335K bytes)
    ```

## Clean Reconstruction

Run these commands from the repository root when both clusters must be rebuilt without retained
workloads or data:

```bash
make -C k8s/bootstrap local-dev-delete
make -C k8s/bootstrap qa-nodes-delete

make -C k8s/bootstrap local-dev-create
kubectl --context kind-local-dev-insurance-hub get nodes

make -C k8s/bootstrap qa-create
kubectl --context qa-insurance-hub get nodes
```

`qa-nodes-delete` removes all three QA virtual machines and their snapshots. The QA LXD profiles
remain and are reused by `qa-nodes-create`. Run the clock synchronization checks below after
`qa-create` and before deploying credentials or applications.

## Local Dev

- `cd k8s/bootstrap`
- `make local-dev-create`
- `kubectl get nodes`
    ```bash
    NAME                                    STATUS   ROLES           AGE     VERSION
    local-dev-insurance-hub-control-plane   Ready    control-plane   2m19s   v1.33.2
    ```

> Please note that after this step the `kubectl` current context will be automatically set to
`kind-local-dev-insurance-hub`.

### Manage Cluster

- `cd k8s/bootstrap`

1. **Suspend cluster**
- `make local-dev-suspend`

2. **Resume cluster**
- `make local-dev-resume`

3. **Delete cluster**
- `make local-dev-delete`

`local-dev-create` creates `~/tmp/data/local-dev-storage`, the `local-dev-insurance-hub` Kind
cluster, and Metrics Server resources. If setup fails after Kind creates the cluster, the target
retains that cluster and reports the partial state for inspection and rerun. `local-dev-delete`
removes the storage directory and then deletes the named Kind cluster.

## QA

Use the following sequence of [Make](https://www.gnu.org/software/make/) targets and shell commands
to create and manage the QA cluster based on [K3s](https://www.rancher.com/products/k3s).

### Create LXD Virtual Machines

- `cd k8s/bootstrap`
- `lxc list`
    ```bash
    +------+-------+------+------+------+-----------+
    | NAME | STATE | IPV4 | IPV6 | TYPE | SNAPSHOTS |
    +------+-------+------+------+------+-----------+
    ```
- `make qa-nodes-create` or `MASTER_ONLY=true make qa-nodes-create` with only the master node

The create target preserves pre-existing VMs and profiles. If provisioning fails, it retains and
reports the VMs and profiles created by that run so they can be inspected before a rerun.
- `lxc list`
    ```bash
    +------------+---------+------------------------+-------------------------------------------------+-----------------+-----------+
    |    NAME    |  STATE  |          IPV4          |                      IPV6                       |      TYPE       | SNAPSHOTS |
    +------------+---------+------------------------+-------------------------------------------------+-----------------+-----------+
    | qa-master  | RUNNING | 10.43.248.181 (enp5s0) | fd42:454f:1973:a457:216:3eff:fe09:5da6 (enp5s0) | VIRTUAL-MACHINE | 0         |
    +------------+---------+------------------------+-------------------------------------------------+-----------------+-----------+
    | qa-worker1 | RUNNING | 10.43.248.133 (enp5s0) | fd42:454f:1973:a457:216:3eff:feec:825a (enp5s0) | VIRTUAL-MACHINE | 0         |
    +------------+---------+------------------------+-------------------------------------------------+-----------------+-----------+
    | qa-worker2 | RUNNING | 10.43.248.113 (enp5s0) | fd42:454f:1973:a457:216:3eff:fe59:c479 (enp5s0) | VIRTUAL-MACHINE | 0         |
    +------------+---------+------------------------+-------------------------------------------------+-----------------+-----------+
    ```
- `make qa-nodes-snapshot QA_SNAPSHOT_NAME=qa-nodes-create-<iso-date>`
- `make qa-nodes-snapshots-list`
    ```bash
    Snapshots for qa-master:
      qa-nodes-create-2025-09-10

    Snapshots for qa-worker1:
      qa-nodes-create-2025-09-10

    Snapshots for qa-worker2:
      qa-nodes-create-2025-09-10
    ```

### Deploy K3s Cluster

- `make qa-cluster-create`
- `lxc list`
    ```bash
    +------------+---------+------------------------+-------------------------------------------------+-----------------+-----------+
    |    NAME    |  STATE  |          IPV4          |                      IPV6                       |      TYPE       | SNAPSHOTS |
    +------------+---------+------------------------+-------------------------------------------------+-----------------+-----------+
    | qa-master  | RUNNING | 10.43.248.181 (enp5s0) | fd42:454f:1973:a457:216:3eff:fe09:5da6 (enp5s0) | VIRTUAL-MACHINE | 1         |
    |            |         | 10.42.0.1 (cni0)       |                                                 |                 |           |
    |            |         | 10.42.0.0 (flannel.1)  |                                                 |                 |           |
    +------------+---------+------------------------+-------------------------------------------------+-----------------+-----------+
    | qa-worker1 | RUNNING | 10.43.248.133 (enp5s0) | fd42:454f:1973:a457:216:3eff:feec:825a (enp5s0) | VIRTUAL-MACHINE | 1         |
    |            |         | 10.42.1.0 (flannel.1)  |                                                 |                 |           |
    +------------+---------+------------------------+-------------------------------------------------+-----------------+-----------+
    | qa-worker2 | RUNNING | 10.43.248.113 (enp5s0) | fd42:454f:1973:a457:216:3eff:fe59:c479 (enp5s0) | VIRTUAL-MACHINE | 1         |
    |            |         | 10.42.2.0 (flannel.1)  |                                                 |                 |           |
    +------------+---------+------------------------+-------------------------------------------------+-----------------+-----------+  
    ```
- `make qa-cluster-pull-kubeconfig`, current context should be `qa-insurance-hub` after execution.
  The target atomically merges into `~/.kube/config` with mode `0600`. When that file already
  exists, its previous contents remain at `~/.kube/config.insurance-hub-backup` with mode `0600`;
  the next successful merge replaces that backup. Temporary pull and write files are removed.
- `kubectl get nodes`
    ```bash
    NAME         STATUS   ROLES                  AGE     VERSION
    qa-master    Ready    control-plane,master   7h41m   v1.33.4+k3s1
    qa-worker1   Ready    <none>                 7h41m   v1.33.4+k3s1
    qa-worker2   Ready    <none>                 7h41m   v1.33.4+k3s1
    ```
- `make qa-nodes-snapshot QA_SNAPSHOT_NAME=qa-cluster-create-<iso-date>`

> Please note that after this step the `kubectl` current context will be automatically set to
`qa-insurance-hub`.

### Synchronize Host and QA VM Clocks

1. **Measure initial host-to-node drift**

    Measure the drift before provisioning credentials, validating certificates, or changing a live
    QA workload:

    ```bash
    for node in qa-master qa-worker1 qa-worker2; do
      host_epoch=$(date +%s)
      node_epoch=$(lxc exec "$node" -- date +%s)
      drift=$((host_epoch - node_epoch))
      ((drift < 0)) && drift=$((-drift))
      printf '%s drift_seconds=%s\n' "$node" "$drift"
    done
    ```

    Any result greater than two seconds requires investigation.

2. **Check host time synchronization**

    Check whether host NTP is enabled and synchronized:

    ```bash
    timedatectl show -p CanNTP -p NTP -p NTPSynchronized -p TimeUSec
    systemctl is-enabled systemd-timesyncd || true
    systemctl is-active systemd-timesyncd || true
    ```

3. **Enable persistent host time synchronization when required**

    If the output reports `NTP=no`, `NTPSynchronized=no`, or `systemd-timesyncd` is disabled or
    inactive, run these commands with interactive `sudo` access:

    ```bash
    sudo systemctl enable --now systemd-timesyncd
    sudo timedatectl set-ntp true
    ```

4. **Wait for synchronization**

    Wait up to 60 seconds and fail the check if the host does not report
    `NTPSynchronized=yes`:

    ```bash
    for attempt in $(seq 1 30); do
      [[ "$(timedatectl show -p NTPSynchronized --value)" == "yes" ]] && break
      sleep 2
    done

    test "$(timedatectl show -p NTPSynchronized --value)" = "yes"
    timedatectl timesync-status
    ```

5. **Verify the synchronized host**

    Verify the host state and current UTC time again:

    ```bash
    date -u '+host %Y-%m-%dT%H:%M:%S.%NZ' & timedatectl show -p CanNTP -p NTP -p NTPSynchronized -p TimeUSec & wait
    ```

6. **Verify final host-to-node drift**

    Require each QA VM to be within two seconds of the synchronized host. Track failures across
    all nodes so a passing final node cannot hide an earlier failure:

    ```bash
    clock_drift_failed=0
    for node in qa-master qa-worker1 qa-worker2; do
      host_epoch=$(date +%s)
      node_epoch=$(lxc exec "$node" -- date +%s)
      drift=$((host_epoch - node_epoch))
      ((drift < 0)) && drift=$((-drift))
      printf '%s drift_seconds=%s\n' "$node" "$drift"
      ((drift <= 2)) || clock_drift_failed=1
    done
    test "$clock_drift_failed" -eq 0
    ```

Do not continue the live operation unless the synchronization and drift checks pass. If
synchronization stalls, inspect `journalctl -u systemd-timesyncd --since '-10 minutes' --no-pager`
before retrying. Do not use a manual `date` adjustment as the persistent fix. Enabling host time
synchronization does not require restarting Docker, LXD, or the QA VMs.

### Manage Cluster

- `cd k8s/bootstrap`

1. **Suspend cluster**

- `make qa-nodes-suspend`
- `lxc list`
    ```bash
    +------------+--------+----------------------+-----------------------------------------------+-----------------+-----------+
    |    NAME    | STATE  |         IPV4         |                     IPV6                      |      TYPE       | SNAPSHOTS |
    +------------+--------+----------------------+-----------------------------------------------+-----------------+-----------+
    | qa-master  | FROZEN | 10.43.248.181 (eth0) | fd42:454f:1973:a457:216:3eff:fe09:5da6 (eth0) | VIRTUAL-MACHINE | 4         |
    +------------+--------+----------------------+-----------------------------------------------+-----------------+-----------+
    | qa-worker1 | FROZEN | 10.43.248.133 (eth0) | fd42:454f:1973:a457:216:3eff:feec:825a (eth0) | VIRTUAL-MACHINE | 4         |
    +------------+--------+----------------------+-----------------------------------------------+-----------------+-----------+
    | qa-worker2 | FROZEN | 10.43.248.113 (eth0) | fd42:454f:1973:a457:216:3eff:fe59:c479 (eth0) | VIRTUAL-MACHINE | 4         |
    +------------+--------+----------------------+-----------------------------------------------+-----------------+-----------+
    ```

2. **Resume cluster**

- `make qa-nodes-resume`, wait until all nodes are in `Ready` state
- `kubectl get nodes`
    ```bash
    NAME         STATUS   ROLES                  AGE     VERSION
    qa-master    Ready    control-plane,master   7h41m   v1.33.4+k3s1
    qa-worker1   Ready    <none>                 7h41m   v1.33.4+k3s1
    qa-worker2   Ready    <none>                 7h41m   v1.33.4+k3s1
    ```

3. **Current QA Snapshots**

Log of current QA snapshots on your local machine.

- `cd k8s/bootstrap`
- Create a new snapshot: `make qa-nodes-snapshot QA_SNAPSHOT_NAME=your_new_snapshot`
- Restore from existing snapshot: `make qa-nodes-restore QA_SNAPSHOT_NAME=your_existing_snapshot`
- List snapshots: `make qa-nodes-snapshots-list`

Creating a snapshot replaces the same named snapshot on each existing QA VM. The target pauses
only VMs that were running and resumes those VMs in reverse order on success or failure; VMs that
were already stopped remain stopped.

| Name                                    | Description                                        |
|-----------------------------------------|----------------------------------------------------|
| **qa-nodes-create-2025-09-29**          | Base cluster image without K8s installed           |
| **qa-cluster-create-2025-09-29**        | Cluster image with K8s, DNS, and storage installed |
| **qa-observability-install-2025-09-29** | Cluster image with observability apps installed    |

4. **Delete cluster**

- `make qa-nodes-delete`

This removes the QA virtual machines and their snapshots. It preserves the reusable LXD profiles.
