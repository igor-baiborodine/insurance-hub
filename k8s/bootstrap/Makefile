################################################################################
# Child Makefile for initial setup of local dev and QA insurance-hub K8s clusters
################################################################################

ifeq ($(abspath $(firstword $(MAKEFILE_LIST))),$(abspath $(lastword $(MAKEFILE_LIST))))
.DEFAULT_GOAL := bootstrap-help
endif

K8S_BOOTSTRAP_MAKEFILE_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
K8S_BOOTSTRAP_REPOSITORY_ROOT := $(abspath $(K8S_BOOTSTRAP_MAKEFILE_DIR)../..)
K8S_ADMIN_SCRIPT := $(K8S_BOOTSTRAP_REPOSITORY_ROOT)/k8s/scripts/admin/dispatch.py

ifndef INSURANCE_HUB_PYTHON_TOOLCHAIN_INCLUDED
include $(K8S_BOOTSTRAP_REPOSITORY_ROOT)/scripts/python/toolchain.mk
endif

QA_NODES_MASTER := qa-master
QA_NODES_WORKER := qa-worker1 qa-worker2
QA_NODES_ALL := $(QA_NODES_MASTER) $(QA_NODES_WORKER)
QA_CLUSTER_NAME := qa-insurance-hub
QA_KUBECTL_CONTEXT := $(QA_CLUSTER_NAME)
QA_SNAPSHOT_NAME ?= baseline
KIND_CONFIG_FILE := $(K8S_BOOTSTRAP_MAKEFILE_DIR)local-dev/local-dev-insurance-hub.yaml
LOCAL_DEV_CLUSTER_NAME := local-dev-insurance-hub
export MASTER_ONLY QA_SNAPSHOT_NAME

.PHONY: bootstrap-help
bootstrap-help: ## Show the safe cluster-bootstrap Make entry points
	@echo "Use an explicit local-dev-* or qa-* target."
	@echo "Every mutating Python target verifies the managed CPython runtime first."

.PHONY: all-suspend
all-suspend: local-dev-suspend qa-nodes-suspend ## Suspend local dev Kind cluster and all QA LXD VMs

.PHONY: all-resume
all-resume: local-dev-resume qa-nodes-resume ## Resume local dev Kind cluster and all QA LXD VMs

.PHONY: local-dev-create
local-dev-create: python-runtime-check ## Create the local Kind cluster with a ready Metrics Server
	@ENV_NAME=local-dev PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" local create

.PHONY: local-dev-delete
local-dev-delete: python-runtime-check ## Delete the local Kind cluster and its persistent storage
	@ENV_NAME=local-dev PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" local delete-storage
	@kind delete cluster --name $(LOCAL_DEV_CLUSTER_NAME)

.PHONY: local-dev-suspend
local-dev-suspend: ## Suspend (stop) the local dev Kind cluster
	@echo "Suspending Kind cluster '$(LOCAL_DEV_CLUSTER_NAME)'..."
	@docker stop $(LOCAL_DEV_CLUSTER_NAME)-control-plane
	@echo "Kind cluster '$(LOCAL_DEV_CLUSTER_NAME)' suspended."

.PHONY: local-dev-resume
local-dev-resume: ## Resume (start) the local dev Kind cluster
	@echo "Resuming Kind cluster '$(LOCAL_DEV_CLUSTER_NAME)'..."
	@docker start $(LOCAL_DEV_CLUSTER_NAME)-control-plane
	@echo "Kind cluster '$(LOCAL_DEV_CLUSTER_NAME)' resumed."

.PHONY: qa-create
qa-create: qa-nodes-create qa-cluster-create qa-cluster-pull-kubeconfig ## Create and configure QA cluster

.PHONY: qa-cluster-create
qa-cluster-create: python-runtime-check ## Set up K3s QA (MASTER_ONLY=true selects the master only)
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" qa-cluster create

.PHONY: qa-cluster-pull-kubeconfig
qa-cluster-pull-kubeconfig: python-runtime-check ## Atomically merge the QA kubeconfig
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" qa-cluster pull-kubeconfig

.PHONY: qa-nodes-create
qa-nodes-create: python-runtime-check ## Create QA LXD VMs (MASTER_ONLY=true selects the master only)
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" qa-nodes create

.PHONY: qa-nodes-suspend
qa-nodes-suspend: python-runtime-check ## Suspend (pause) all existing QA LXD VMs
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" qa-nodes suspend

.PHONY: qa-nodes-resume
qa-nodes-resume: python-runtime-check ## Resume (start) all existing QA LXD VMs
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" qa-nodes resume

.PHONY: qa-nodes-snapshot
qa-nodes-snapshot: python-runtime-check ## Replace a snapshot and always resume paused VMs (QA_SNAPSHOT_NAME=name)
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" qa-nodes snapshot

.PHONY: qa-nodes-snapshots-list
qa-nodes-snapshots-list: python-runtime-check ## List snapshots for QA cluster nodes
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" qa-nodes snapshots-list

.PHONY: qa-nodes-restore
qa-nodes-restore: python-runtime-check ## Restore and start QA VMs (QA_SNAPSHOT_NAME=name)
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" qa-nodes restore

.PHONY: qa-nodes-delete
qa-nodes-delete: python-runtime-check ## Delete all QA LXD VMs and their snapshots
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" qa-nodes delete
