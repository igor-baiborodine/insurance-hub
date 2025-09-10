## QA Cluster How-To's

### How to Create a QA Cluster

Use the following sequence of [Make](https://www.gnu.org/software/make/) targets and shell commands
to create a new cluster.

#### Create LXD Virtual Machines

- `cd k8s/bootstrap/qa`
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
- `make qa-nodes-snapshot SNAPSHOT_NAME=qa-nodes-create-<iso-date>`
- `make qa-nodes-snapshots-list`
    ```bash
    Snapshots for qa-master:
      qa-nodes-create-2025-09-10

    Snapshots for qa-worker1:
      qa-nodes-create-2025-09-10

    Snapshots for qa-worker2:
      qa-nodes-create-2025-09-10
    ```

#### Create Multi-node Rancher's K3s Cluster

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
- `qa-nodes-snapshot SNAPSHOT_NAME=qa-cluster-create-<iso-date>`

#### Install qa-data Resources

- `cd ../..`, change current directory to `k8s`
- `prometheus-operator-install`, Prometheus operator is a prerequisite for installing PostgreSQL
- `kubectl get pods -n qa-monitoring`
    ```bash
    NAME                                                      READY   STATUS    RESTARTS   AGE
    prometheus-operator-kube-p-operator-bf675cb9d-8p7vk       1/1     Running   0          4m9s
    prometheus-operator-kube-state-metrics-6fcf458c69-l826d   1/1     Running   0          4m9s
    prometheus-operator-prometheus-node-exporter-pfq7v        1/1     Running   0          4m9s
    prometheus-operator-prometheus-node-exporter-sqfsr        1/1     Running   0          4m9s
    prometheus-operator-prometheus-node-exporter-w98bg        1/1     Running   0          4m9s
    prometheus-prometheus-operator-kube-p-prometheus-0        2/2     Running   0          3m46s  
    ```
- `qa-nodes-snapshot SNAPSHOT_NAME=prometheus-operator-install-<iso-date>`, execute from
  `k8s/bootstrap/qa` directory`
- `make postgres-secret-create-qa POSTGRES_PASSWORD=your_password`
- `make postgres-deploy`
- `kubectl get pods -n qa-data`
    ```bash
    NAME                            READY   STATUS    RESTARTS   AGE
    postgres-postgresql-primary-0   2/2     Running   0          3m20s
    postgres-postgresql-read-0      2/2     Running   0          3m20s
    postgres-postgresql-read-1      2/2     Running   0          112s`
    ```
- `make postgres-status`
- `qa-nodes-snapshot SNAPSHOT_NAME=postgres-deploy-<iso-date>`, execute from
  `k8s/bootstrap/qa` directory`

### Monitoring

- `lxc exec <node-name> -- /bin/bash`
    ```bash
    lxc exec qa-master -- /bin/bash
    root@qa-master:~# htop
    ```

### Current Snapshots

Log of current snapshots on your local machine.

- Create a new snapshot: `make qa-nodes-snapshot SNAPSHOT_NAME=your_new_snapshot`
- Restore from existing snapshot: `make qa-nodes-restore SNAPSHOT_NAME=your_existing_snapshot`
- List snapshots: `make qa-nodes-snapshots-list`

| Name                                       | Description                                        |
|--------------------------------------------|----------------------------------------------------|
| **qa-nodes-create-2025-09-10**             | Base cluster image without K8s installed           |
| **qa-cluster-create-2025-09-10**           | Cluster image with K8s, DNS, and storage installed |
| **prometheus-operator-install-2025-09-10** | Cluster image Prometheus operator installed        |
| **postgres-deploy-2025-09-10**             | Cluster image PostgreSQL installed                 |
