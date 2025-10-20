################################################################################
# Child Makefile for initial setup of local dev and QA insurance-hub K8s clusters
################################################################################

################################################################################
# Variables
################################################################################
QA_NODES_MASTER			:= qa-master
QA_NODES_WORKER			:= qa-worker1 qa-worker2
QA_NODES_ALL			:= $(QA_NODES_MASTER) $(QA_NODES_WORKER)
QA_CLUSTER_NAME  		:= qa-insurance-hub
QA_KUBECTL_CONTEXT		:= $(QA_CLUSTER_NAME)
QA_SNAPSHOT_NAME 		?= baseline
KIND_CONFIG_FILE   		:= local-dev/local-dev-insurance-hub.yaml
LOCAL_DEV_CLUSTER_NAME  := local-dev-insurance-hub

.PHONY: all-suspend
all-suspend: local-dev-suspend qa-nodes-suspend ## Suspend local dev Kind cluster and all QA LXD VMs

.PHONY: all-resume
all-resume: local-dev-resume qa-nodes-resume ## Resume local dev Kind cluster and all QA LXD VMs

################################################################################
# Local Dev Cluster Management Targets
# Targets for creating and managing the Kind cluster configuration
################################################################################
.PHONY: local-dev-create
local-dev-create: ## Create and start the local dev Kind cluster (single-node)
	@mkdir -p $(HOME)/tmp/data/local-dev-storage
	@kind create cluster --name $(LOCAL_DEV_CLUSTER_NAME) --config $(KIND_CONFIG_FILE)

.PHONY: local-dev-delete
local-dev-delete: ## Stop and delete the local dev Kind cluster with its persistent storage
	@rm -rf $(HOME)/tmp/data/local-dev-storage
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

################################################################################
# QA Cluster Management Targets
# Targets for creating and managing the K3s cluster configuration
################################################################################
.PHONY: qa-create
qa-create: qa-nodes-create qa-cluster-create qa-cluster-pull-kubeconfig ## Create and configure QA cluster

.PHONY: qa-cluster-create
qa-cluster-create: ## Set up multi-node Rancher K3s QA cluster
	bash qa/scripts/qa-cluster-create.sh

.PHONY: qa-cluster-pull-kubeconfig
qa-cluster-pull-kubeconfig: ## Pull kubeconfig from QA cluster
	bash qa/scripts/qa-cluster-pull-kubeconfig.sh

################################################################################
# QA Nodes Management Targets 
# Targets for creating and managing the LXD virtual machines for QA cluster
################################################################################
.PHONY: qa-nodes-create
qa-nodes-create: ## Create three LXD VMs (master and two workers) for QA cluster
	bash qa/scripts/qa-nodes-create.sh

.PHONY: qa-nodes-suspend
qa-nodes-suspend: ## Suspend (pause) all QA LXD VMs
	@for NODE in $(QA_NODES_ALL); do \
		if lxc info $$NODE >/dev/null 2>&1; then \
			echo "Suspending VM $$NODE..."; \
			lxc pause $$NODE; \
		else \
			echo "VM $$NODE does not exist, skipping."; \
		fi; \
	done
	@echo "All QA cluster VMs suspended (paused):"
	@lxc list

.PHONY: qa-nodes-resume
qa-nodes-resume: ## Resume (start) all QA LXD VMs
	@for NODE in $(QA_NODES_ALL); do \
		if lxc info $$NODE >/dev/null 2>&1; then \
			echo "Resuming (starting) VM $$NODE..."; \
			lxc start $$NODE; \
		else \
			echo "VM $$NODE does not exist, skipping."; \
		fi; \
	done
	@echo "All QA cluster VMs resumed (started):"
	@lxc list

.PHONY: qa-nodes-snapshot
qa-nodes-snapshot: ## Pause all VMs, delete existing snapshot, snapshot VMs, and resume. Usage: make qa-nodes-snapshot QA_SNAPSHOT_NAME=your_new_snapshot
	bash qa/scripts/qa-nodes-snapshot.sh $(QA_SNAPSHOT_NAME)

.PHONY: qa-nodes-snapshots-list
qa-nodes-snapshots-list: ## List all snapshots for QA cluster nodes
	@for NODE in $(QA_NODES_ALL); do \
		echo "Snapshots for $$NODE:"; \
		SNAPS=$$(lxc query "/1.0/instances/$$NODE/snapshots" 2>/dev/null | jq -r '.[] | split("/") | .[-1]' || echo ""); \
		if [ -z "$$SNAPS" ]; then \
			echo "  No snapshots exist for $$NODE"; \
		else \
			echo "$$SNAPS" | sed 's/^/  /'; \
		fi; \
		echo ""; \
	done

.PHONY: qa-nodes-restore
qa-nodes-restore: ## Restore all QA cluster VMs from specified snapshot and start them. Usage: make qa-nodes-restore QA_SNAPSHOT_NAME=your_existing_snapshot
	@echo "Restoring all QA cluster VMs from '$(QA_SNAPSHOT_NAME)' snapshot..."
	@for NODE in $(QA_NODES_ALL); do \
		if lxc info $$NODE &>/dev/null; then \
			echo "Restoring and starting VM $$NODE from snapshot '$(QA_SNAPSHOT_NAME)'..."; \
			lxc restore $$NODE $(QA_SNAPSHOT_NAME); \
		fi; \
	done
	@echo "All QA cluster VMs restored to '$(QA_SNAPSHOT_NAME)' snapshot and started:"
	@lxc list

.PHONY: qa-nodes-delete
qa-nodes-delete: ## Delete all LXD VMs and its snapshots used by QA cluster
	@for NODE in $(QA_NODES_ALL); do \
		if lxc info $$NODE >/dev/null 2>&1; then \
			echo "Deleting VM $$NODE..."; \
			lxc delete $$NODE --force; \
		else \
			echo "VM $$NODE does not exist, skipping."; \
		fi; \
	done
	@echo "All multi-node cluster VMs deleted."
