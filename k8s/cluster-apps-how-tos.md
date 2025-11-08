# Local Dev Cluster How-To's

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->
**Table of Contents**

- [QA—Observability](#qaobservability)
- [Data](#data)
- [QA—Cluster Load Monitoring](#qacluster-load-monitoring)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

Use the following sequence of [Make](https://www.gnu.org/software/make/) targets and shell commands
to deploy cluster apps including infrastructure and "Insurance Hub" services.

## QA—Observability
- `cd k8s`
1. **Prometheus & Grafana**
- `make prometheus-stack-install`
- `make prometheus-stack-status`
- `kubectl get pods -n qa-monitoring | grep prometheus`
    ```shell
    NAME                                                     READY   STATUS    RESTARTS   AGE
    alertmanager-qa-prometheus-kube-prometh-alertmanager-0   2/2     Running   0          4m29s
    prometheus-qa-prometheus-kube-prometh-prometheus-0       2/2     Running   0          4m29s
    qa-prometheus-grafana-6595b55444-tlb9t                   3/3     Running   0          5m28s
    qa-prometheus-kube-prometh-operator-779bb6fd9c-5lthm     1/1     Running   0          5m28s
    qa-prometheus-kube-state-metrics-79cf8d8f9b-dqgm7        1/1     Running   0          5m28s
    qa-prometheus-prometheus-node-exporter-97p7f             1/1     Running   0          5m28s
    qa-prometheus-prometheus-node-exporter-hn696             1/1     Running   0          5m28s
    qa-prometheus-prometheus-node-exporter-zcd5z             1/1     Running   0          5m28s    
    ```
- **QA/Snapshot**: `make -C bootstrap qa-nodes-snapshot QA_SNAPSHOT_NAME=observability-install-<iso-date>`

2. **Zipkin**
- **Prerequisites**: Elasticsearch 
- `make zipkin-es-user-secret-create`
- `make zipkin-es-user-create`
- `make zipkin-install`
- `make zipkin-status`
- `make zipkin-ui` and go to `http://localhost:9411`
- **QA/Snapshot**: `make -C bootstrap qa-nodes-snapshot QA_SNAPSHOT_NAME=zipkin-install-<iso-date>`

## Data

Deploy the necessary data resources into the either `local-dev-all` or `qa-data` namespaces.

1. **Postgres**
- `make postgres-operator-deploy`

  **auth** service: 
  - `make postgres-svc-secret-create SVC_NAME=auth PG_SVC_USER_PWD=<user-pwd>`
  - `make postgres-svc-deploy SVC_NAME=auth`, wait at least one minute for the cluster to be ready.
  
  **document** service: 
  - `make postgres-svc-secret-create SVC_NAME=document PG_SVC_USER_PWD=<user-pwd>`
  - `make postgres-svc-deploy SVC_NAME=document`, wait at least one minute for the cluster to be ready.
  
  **payment** service: 
  - `make postgres-svc-secret-create SVC_NAME=payment PG_SVC_USER_PWD=<user-pwd>`
  - `make postgres-svc-deploy SVC_NAME=payment`, wait at least one minute for the cluster to be ready.
  
  **policy** service: 
  - `make postgres-svc-secret-create SVC_NAME=policy PG_SVC_USER_PWD=<user-pwd>`
  - `make postgres-svc-status SVC_NAME=policy`

  **product** service: 
  - `make postgres-svc-secret-create SVC_NAME=product PG_SVC_USER_PWD=<user-pwd>`
  - `make postgres-svc-deploy SVC_NAME=product`, wait at least one minute for the cluster to be ready.

- `kubectl get pods -n qa-data | grep postgres`
    ```shell
    NAME                     READY   STATUS    RESTARTS   AGE
    qa-postgres-auth-1       1/1     Running   0          47m
    qa-postgres-auth-2       1/1     Running   0          45m
    qa-postgres-document-1   1/1     Running   0          36m
    qa-postgres-document-2   1/1     Running   0          35m
    qa-postgres-payment-1    1/1     Running   0          28m
    qa-postgres-payment-2    1/1     Running   0          27m
    qa-postgres-policy-1     1/1     Running   0          11m
    qa-postgres-policy-2     1/1     Running   0          11m
    qa-postgres-product-1    1/1     Running   0          33s
    qa-postgres-product-2    1/1     Running   0          12s
    ```
- `make grafana-ui`
- **QA/Grafana**: In _Dashboards > New > Import_, add the "CloudNativePG" dashboard using the following
  URL: https://grafana.com/grafana/dashboards/20417-cloudnativepg/.

- **QA/Snapshot**: `make -C bootstrap qa-nodes-snapshot QA_SNAPSHOT_NAME=postgres-deploy-<iso-date>`

2. **MongoDB** 
- `make mongodb-root-secret-create MONGO_ROOT_USER_PWD=<root-pwd>`
- `make mongodb-operator-install`
- `make mongodb-deploy`
- `kubectl get pods -n local-dev-all | grep mongodb`
    ```shell
    local-dev-mongodb-0                            2/2     Running   0          4m41s
    mongodb-kubernetes-operator-7898cfb5f8-rkc7r   1/1     Running   0          5m34s
    ```
- `make mongodb-status`  
- **QA**: `make -C bootstrap qa-nodes-snapshot QA_SNAPSHOT_NAME=mongodb-deploy-<iso-date>`

3. **Elasticsearch**
- `make eck-operator-deploy`
- `make elasticsearch-deploy`
- `make elasticsearch-status`
- `make elasticsearch-exporter-deploy`
- `make grafana-ui`
- **QA/Grafana**: In _Dashboards > New > Import_, add the "ElasticSearch" dashboard using the following
  URL: https://grafana.com/grafana/dashboards/2322-elasticsearch/.
- **QA/Snapshot**: `make -C bootstrap qa-nodes-snapshot QA_SNAPSHOT_NAME=elasticsearch-deploy-<iso-date>`

4. **MinIO**
- `make minio-operator-deploy`

  **document** service:
  - `make minio-storage-user-secret-create SVC_NAME=document MINIO_CONSOLE_ACCESS_KEY=<access-key> MINIO_CONSOLE_SECRET_KEY=<secret-key>`
  - `make minio-storage-config-secret-create SVC_NAME=document MINIO_ROOT_USER=<root-user> MINIO_ROOT_PASSWORD=<root-password>`
  - `make minio-tenant-deploy SVC_NAME=document`
  - `kubectl get pods -n local-dev-minio-document`
    ```shell
    NAME                                READY   STATUS    RESTARTS   AGE
    local-dev-minio-document-pool-0-0   2/2     Running   0          101s
    ```
  - `make minio-tenant-status SVC_NAME=document`

- **QA/Snapshot**: `make -C bootstrap qa-nodes-snapshot QA_SNAPSHOT_NAME=minio-deploy-<iso-date>`

5. **Kafka**

- `make kafka-strimzi-operator-install`
- `make kafka-deploy`
- `kubectl get pods -n local-dev-all | grep kafka`
    ```shell
    local-dev-kafka-broker-controller-0                1/1     Running   0             22m
    local-dev-kafka-entity-operator-76bb947d7c-gzgb6   2/2     Running   0             21m    
    ```
- `make kafka-status`
- `make grafana-ui`
- **QA/Grafana**: In _Dashboards > New > Import_, add the "Strimzi Kafka" dashboard using the
  following [JSON](https://github.com/strimzi/strimzi-kafka-operator/blob/0.48.0/examples/metrics/grafana-dashboards/strimzi-kafka.json) file
- **QA/Snapshot**: `make -C bootstrap qa-nodes-snapshot QA_SNAPSHOT_NAME=elasticsearch-deploy-<iso-date>`

5. **JSReport**

- `make jsreport-deploy`
- `kubectl get pods -n local-dev-all | grep jsreport`
    ```shell
    local-dev-jsreport-5548585d57-q5sdc   1/1     Running   0          47s
    ```
- `make jsreport-status`
- `make jsreport-ui` and go to `http://localhost:5488`
- **QA/Snapshot**: `make -C bootstrap qa-nodes-snapshot QA_SNAPSHOT_NAME=jsreport-deploy-<iso-date>``

## QA—Cluster Load Monitoring

1. **Prometheus**

- `cd k8s`
- `make prometheus-ui` and go to `http://localhost:9090`
- In Prometheus UI, go to `Status -> Target Health` and verify that all targets are `UP`

2. **Grafana**

- `make grafana-ui` and go to `http://localhost:3000`
- In Grafana UI, go to `Home -> Manage -> Dashboard` and verify that the default dashboards are
  available:
    - Kubernetes / Compute Resources / Cluster
    - Kubernetes / Compute Resources / Node
    - Kubernetes / Compute Resources / Pod
    - etc.

3. **htop & df**

- `kubectl get nodes`
- `lxc exec <node-name> -- /bin/bash`
    ```shell
    lxc exec qa-master -- /bin/bash
    root@qa-master:~# htop
    root@qa-master:~# df -h
    ```
- Verify node capacity and limits:
    ```shell
    kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}: {.status.allocatable.cpu}{"\n"}{end}'
    qa-master: 6
    qa-worker1: 3
    qa-worker2: 3
    ```
- Check current pod CPU usage:
    ```shell
    kubectl top nodes
    kubectl top pod --all-namespaces  
    ```
