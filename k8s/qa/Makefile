################################################################################
# Child Makefile for QA insurance-hub k8s cluster
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

.PHONY: help
help:
	@echo "QA insurance-hub cluster Makefile targets:"
	@grep -E '^\S+:.*## ' $(MAKEFILE_LIST) | sort | \
	  awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-32s\033[0m %s\n", $$1, $$2}'

.PHONY: qa-cluster-create
qa-cluster-create: qa-nodes-create ## Set up multi-node Rancher K3s QA cluster
	bash ./scripts/qa-cluster-create.sh

.PHONY: qa-cluster-pull-kubeconfig
qa-cluster-pull-kubeconfig: ## Pull kubeconfig from QA cluster
	bash ./scripts/qa-cluster-pull-kubeconfig.sh

.PHONY: qa-cluster-add-all-addons
qa-cluster-add-all-addons: qa-cluster-add-dns qa-cluster-add-storage qa-cluster-add-ingress ## Add core K3s components (DNS, storage, ingress) to QA cluster

.PHONY: qa-cluster-add-dns
qa-cluster-add-dns: ## Add CoreDNS to QA cluster
	@echo "Adding CoreDNS in k3s..."
	@lxc exec qa-master -- bash -c '\
		for i in {1..24}; do \
			READY=$$(k3s kubectl -n kube-system get deploy coredns -o jsonpath="{.status.readyReplicas}" 2>/dev/null || echo 0); \
			[ "$$READY" != "" ] && [ $$READY -ge 1 ] && echo "CoreDNS is Ready." && exit 0; \
			echo "Waiting for CoreDNS to be Ready..."; sleep 5; \
		done; \
		echo "CoreDNS not Ready after timeout." >&2; exit 1'

.PHONY: qa-cluster-add-storage
qa-cluster-add-storage: ## Add local-path storage to QA cluster
	@echo "Adding local-path storage in k3s..."
	@lxc exec qa-master -- bash -c '\
		for i in {1..24}; do \
			k3s kubectl get storageclass local-path >/dev/null 2>&1 && break; \
			echo "Waiting for storageclass/local-path..."; sleep 5; \
		done; \
		for i in {1..24}; do \
			READY=$$(k3s kubectl -n kube-system get deploy local-path-provisioner -o jsonpath="{.status.readyReplicas}" 2>/dev/null || echo 0); \
			[ "$$READY" != "" ] && [ $$READY -ge 1 ] && echo "local-path-provisioner is Ready." && exit 0; \
			echo "Waiting for local-path-provisioner to be Ready..."; sleep 5; \
		done; \
		echo "local-path-provisioner not Ready after timeout." >&2; exit 1'

.PHONY: qa-cluster-add-ingress
qa-cluster-add-ingress: ## Add Traefik ingress controller to QA cluster
	@echo "Adding Traefik ingress controller in k3s..."
	@lxc exec qa-master -- bash -c '\
		for i in {1..24}; do \
			READY=$$(k3s kubectl -n kube-system get deploy traefik -o jsonpath="{.status.readyReplicas}" 2>/dev/null || echo 0); \
			[ "$$READY" != "" ] && [ $$READY -ge 1 ] && echo "Traefik is Ready." && exit 0; \
			echo "Waiting for Traefik to be Ready..."; sleep 5; \
		done; \
		echo "Traefik not Ready after timeout." >&2; exit 1'

.PHONY: qa-nodes-create
qa-nodes-create: ## Create three LXD VMs (master and two workers) for QA cluster
	@for NODE in $(NODES_ALL); do \
		if ! lxc info $$NODE >/dev/null 2>&1; then \
			echo "Launching VM $$NODE..."; \
			lxc launch ubuntu:24.04 --vm $$NODE; \
			echo "Waiting for LXD VM agent in $$NODE..."; \
			until lxc exec $$NODE -- true 2>/dev/null; do sleep 5; done; \
			echo "Ensuring network works inside $$NODE..."; \
			lxc exec $$NODE -- bash -c "until ping -c1 1.1.1.1 >/dev/null 2>&1; do echo Waiting for network...; sleep 5; done"; \
			echo "Installing prerequisites (curl, ca-certificates) in $$NODE..."; \
			lxc exec $$NODE -- bash -c "apt-get update -y && apt-get install -y curl ca-certificates"; \
		else \
			echo "VM $$NODE already exists, skipping."; \
		fi; \
	done
	@echo "All VM nodes have been created and prepared for k3s."

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
qa-nodes-snapshot: ## Pause all VMs, delete existing snapshot, snapshot VMs, and resume
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
qa-nodes-restore: ## Restore all QA cluster VMs from specified snapshot and start them
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
