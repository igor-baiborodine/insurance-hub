# Local Dev Cluster How-To's

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->
**Table of Contents**

- [Create Cluster](#create-cluster)
  - [Create Kind Cluster](#create-kind-cluster)
  - [Deploy `local-dev` Resources](#deploy-local-dev-resources)
- [Suspend and Resume Cluster](#suspend-and-resume-cluster)
- [Delete Cluster](#delete-cluster)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

## Create Cluster

Use the following sequence of [Make](https://www.gnu.org/software/make/) targets and shell commands
to create and manage the local dev Kubernetes cluster based
on [Kind](https://github.com/kubernetes-sigs/kind?tab=readme-ov-file).

### Create Kind Cluster

- `cd k8s/bootstrap`
- `make local-dev-create`
- `kubectl get nodes`
    ```bash
    NAME                                    STATUS     ROLES           AGE   VERSION
    local-dev-insurance-hub-control-plane   NotReady   control-plane   16s   v1.33.2
    ```

> Please note that after this step the `kubectl` current context will be automatically set to
`kind-local-dev-insurance-hub`.

### Deploy `local-dev` Resources

Once the cluster is running, deploy the necessary data stores into the `local-dev` namespace.

1. **Postgres**
- `make postgres-deploy`
- `kubectl get pods -n local-dev | grep postgres`
    ```bash
    NAME                    READY   STATUS    RESTARTS   AGE
    postgres-postgresql-0   1/1     Running   0          3m3s    
    ```
- `make postgres-status`

2. **MongoDB**  
- `make mongodb-deploy`
- `kubectl get pods -n local-dev | grep mongodb`
    ```bash
    mongodb-74d8b777cc-fr8xx   1/1     Running   0          109s
    ```
- `make mongodb-status`  

## Suspend and Resume Cluster

You can suspend the cluster to save resources and resume it later.
- `cd k8s/bootstrap`

1. **Suspend the cluster**:
- `make local-dev-suspend`
    ```bash
    Suspending Kind cluster 'local-dev-insurance-hub'...
    local-dev-insurance-hub-control-plane
    Kind cluster 'local-dev-insurance-hub' suspended.
    ```

2. **Resume the cluster**:
- `make local-dev-resume`
- `kubectl get nodes`
    ```bash
    NAME                                    STATUS   ROLES           AGE   VERSION
    local-dev-insurance-hub-control-plane   Ready    control-plane   92m   v1.33.2
    ```

## Delete Cluster

This command will permanently delete the cluster and its associated storage.

- `cd k8s/bootstrap` 
- `make local-dev-delete`
- `kind get clusters`
    ```bash
    No kind clusters found.
    ```