################################################################################
# Child Makefile for QA insurance-hub K8s cluster
################################################################################

################################################################################
# Variables
################################################################################
NODES_MASTER		:= qa-master
NODES_WORKER		:= qa-worker1 qa-worker2
NODES_ALL			:= $(NODES_MASTER) $(NODES_WORKER)
QA_CLUSTER_NAME  	:= qa-insurance-hub
QA_KUBECTL_CONTEXT	:= $(QA_CLUSTER_NAME)
SNAPSHOT_NAME 		?= baseline

################################################################################
# QA Cluster Management Targets
# Targets for creating and managing the K3s cluster configuration
################################################################################
.PHONY: qa-create
qa-create: qa-nodes-create qa-cluster-create qa-cluster-pull-kubeconfig ## Create and configure QA cluster

.PHONY: qa-cluster-create
qa-cluster-create: ## Set up multi-node Rancher K3s QA cluster
	bash ./scripts/qa-cluster-create.sh

.PHONY: qa-cluster-pull-kubeconfig
qa-cluster-pull-kubeconfig: ## Pull kubeconfig from QA cluster
	bash ./scripts/qa-cluster-pull-kubeconfig.sh

################################################################################
# QA Nodes Management Targets 
# Targets for creating and managing the LXD virtual machines for QA cluster
################################################################################
.PHONY: qa-nodes-create
qa-nodes-create: ## Create three LXD VMs (master and two workers) for QA cluster
	bash ./scripts/qa-nodes-create.sh

.PHONY: qa-nodes-suspend
qa-nodes-suspend: ## Suspend (pause) all LXD QA VMs
	@for NODE in $(NODES_ALL); do \
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
qa-nodes-resume: ## Resume (start) all LXD QA VMs
	@for NODE in $(NODES_ALL); do \
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
qa-nodes-snapshot: ## Pause all VMs, delete existing snapshot, snapshot VMs, and resume. Usage: make qa-nodes-snapshot SNAPSHOT_NAME=your_new_snapshot
	bash ./scripts/qa-nodes-snapshot.sh $(SNAPSHOT_NAME)

.PHONY: qa-nodes-snapshots-list
qa-nodes-snapshots-list: ## List all snapshots for QA cluster nodes
	@for NODE in $(NODES_ALL); do \
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
qa-nodes-restore: ## Restore all QA cluster VMs from specified snapshot and start them. Usage: make qa-nodes-restore SNAPSHOT_NAME=your_existing_snapshot
	@echo "Restoring all QA cluster VMs from '$(SNAPSHOT_NAME)' snapshot..."
	@for NODE in $(NODES_ALL); do \
		if lxc info $$NODE &>/dev/null; then \
			echo "Restoring and starting VM $$NODE from snapshot '$(SNAPSHOT_NAME)'..."; \
			lxc restore $$NODE $(SNAPSHOT_NAME); \
		fi; \
	done
	@echo "All QA cluster VMs restored to '$(SNAPSHOT_NAME)' snapshot and started:"
	@lxc list

.PHONY: qa-nodes-delete
qa-nodes-delete: ## Delete all LXD VMs and its snapshots used by QA cluster
	@for NODE in $(NODES_ALL); do \
		if lxc info $$NODE >/dev/null 2>&1; then \
			echo "Deleting VM $$NODE..."; \
			lxc delete $$NODE --force; \
		else \
			echo "VM $$NODE does not exist, skipping."; \
		fi; \
	done
	@echo "All multi-node cluster VMs deleted."
