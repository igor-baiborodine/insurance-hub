# QA Cluster How-To's

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->
**Table of Contents**

- [Create Cluster](#create-cluster)
  - [Prerequisites](#prerequisites)
  - [Create LXD Virtual Machines](#create-lxd-virtual-machines)
  - [Create Multi-node Rancher's K3s Cluster](#create-multi-node-ranchers-k3s-cluster)
  - [Deploy qa-monitoring Resources](#deploy-qa-monitoring-resources)
  - [Deploy qa-data Resources](#deploy-qa-data-resources)
- [Monitor Cluster Load](#monitor-cluster-load)
- [Suspend and Resume Cluster](#suspend-and-resume-cluster)
- [Current Snapshots](#current-snapshots)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

## Create Cluster

Use the following sequence of [Make](https://www.gnu.org/software/make/) targets and shell commands
to create and manage the QA cluster based on [K3s](https://www.rancher.com/products/k3s).

### Prerequisites

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

- Restart key services:
    ```bash
    sudo systemctl restart docker
    sudo snap restart lxd
    ```

- Validate if the rule was applied:
    ```bash
    sudo iptables -L FORWARD -v -n | grep "Chain FORWARD"
    Chain FORWARD (policy ACCEPT 340 packets, 335K bytes)
    ```

### Create LXD Virtual Machines

- `cd k8s/bootstrap`
- `lxc list`
    ```bash
    +------+-------+------+------+------+-----------+
    | NAME | STATE | IPV4 | IPV6 | TYPE | SNAPSHOTS |
    +------+-------+------+------+------+-----------+
    ```
- `make qa-nodes-create`
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

### Create Multi-node Rancher's K3s Cluster

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
- `make qa-cluster-pull-kubeconfig`, current context should be `qa-insurance-hub` after execution
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

### Deploy qa-monitoring Resources
- `cd ..`, change directory from `k8s/bootstrap` to `k8s`
- `make prometheus-stack-install-qa`
- `kubectl get pods -n qa-monitoring`
    ```bash
    alertmanager-prometheus-community-kube-alertmanager-0      2/2     Running   0          2m4s
    prometheus-community-grafana-7d9b9c8496-4v7bl              3/3     Running   0          2m36s
    prometheus-community-kube-operator-7bfc556b55-cmgzf        1/1     Running   0          2m35s
    prometheus-community-kube-state-metrics-699f796bb6-dnjd8   1/1     Running   0          2m35s
    prometheus-prometheus-community-kube-prometheus-0          2/2     Running   0          2m4s
- `make -C bootstrap qa-nodes-snapshot QA_SNAPSHOT_NAME=prometheus-stack-install-qa-<iso-date>`


### Deploy qa-data Resources

- `make postgres-secret-create-qa POSTGRES_PASSWORD=your_password`
- `make postgres-deploy`
- `kubectl get pods -n qa-data`
    ```bash
    NAME                            READY   STATUS    RESTARTS   AGE
    postgres-postgresql-primary-0   2/2     Running   0          3m20s
    postgres-postgresql-read-0      2/2     Running   0          3m20s
    ```
- `make postgres-status`
- `make -C bootstrap qa-nodes-snapshot qa-nodes-snapshot QA_SNAPSHOT_NAME=postgres-deploy-<iso-date>`

## Monitor Cluster Load

- `lxc exec <node-name> -- /bin/bash`
    ```bash
    lxc exec qa-master -- /bin/bash
    root@qa-master:~# htop
    ```

## Suspend and Resume Cluster

- `cd k8s/bootstrap`

1. Suspend the cluster:

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

2. Resume the cluster:

- `make qa-nodes-resume`, wait until all nodes are in `Ready` state
- `kubectl get nodes`
    ```bash
    NAME         STATUS   ROLES                  AGE     VERSION
    qa-master    Ready    control-plane,master   7h41m   v1.33.4+k3s1
    qa-worker1   Ready    <none>                 7h41m   v1.33.4+k3s1
    qa-worker2   Ready    <none>                 7h41m   v1.33.4+k3s1
    ```

## Current Snapshots

Log of current snapshots on your local machine.

- `cd k8s/bootstrap`
- Create a new snapshot: `make qa-nodes-snapshot QA_SNAPSHOT_NAME=your_new_snapshot`
- Restore from existing snapshot: `make qa-nodes-restore QA_SNAPSHOT_NAME=your_existing_snapshot`
- List snapshots: `make qa-nodes-snapshots-list`

| Name                                          | Description                                        |
|-----------------------------------------------|----------------------------------------------------|
| **qa-nodes-create-2025-09-10**                | Base cluster image without K8s installed           |
| **qa-cluster-create-2025-09-10**              | Cluster image with K8s, DNS, and storage installed |
| **qa-prometheus-operator-install-2025-09-10** | Cluster image with Prometheus operator installed   |
| **qa-postgres-deploy-2025-09-16**             | Cluster image with PostgreSQL installed            |
| **qa-mongodb-deploy-2025-09-16**              | Cluster image with MongoDB installed               |
