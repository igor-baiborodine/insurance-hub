################################################################################
# Child Makefile for QA insurance-hub
################################################################################

################################################################################
# Variables
################################################################################
NODE_LIST			:= qa-master qa-worker1 qa-worker2
QA_CLUSTER_NAME  	:= qa-insurance-hub
QA_KUBECTL_CONTEXT	:= $(QA_CLUSTER_NAME)

.PHONY: help
help:
	@echo "QA insurance-hub cluster Makefile targets:"
	@grep -E '^\S+:.*## ' $(MAKEFILE_LIST) | sort | \
	  awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

.PHONY: lxd-install
lxd-install: ## Install LXD on Ubuntu
	@if ! command -v lxd >/dev/null 2>&1; then \
		sudo snap install lxd; \
		sudo usermod -aG lxd $${SUDO_USER:-$$USER}; \
		echo "Initializing LXD with default settings..."; \
		sudo lxd init --auto; \
		echo "LXD installed. Please log out and log back in for group changes to take effect."; \
	else \
		echo "LXD is already installed."; \
	fi

.PHONY: qa-create
qa-create: qa-cluster-create ## Create nodes and set up the multi-node MicroK8s cluster with required add-ons

.PHONY: qa-cluster-create
qa-cluster-create: qa-nodes-create ## Set up MicroK8s multi-node cluster with DNS, storage, ingress add-ons and dynamic context rename
	@echo "Starting and configuring MicroK8s on master node..."
	@lxc exec qa-master -- bash -c "microk8s stop || true; microk8s start"
	@lxc exec qa-master -- bash -c "microk8s status --wait-ready"
	@lxc exec qa-master -- microk8s enable dns
	@lxc exec qa-master -- microk8s enable hostpath-storage
	@lxc exec qa-master -- microk8s enable ingress
	@echo "Joining qa-worker1 to cluster..."
	@lxc exec qa-worker1 -- bash -c "microk8s stop || true; microk8s start; microk8s status --wait-ready"
	@JOIN_CMD=$$(lxc exec qa-master -- microk8s add-node --token-ttl 300 | grep 'microk8s join' | head -n1); \
		lxc exec qa-worker1 -- bash -c "$$JOIN_CMD"
	@echo "Joining qa-worker2 to cluster..."
	@lxc exec qa-worker2 -- bash -c "microk8s stop || true; microk8s start; microk8s status --wait-ready"
	@JOIN_CMD=$$(lxc exec qa-master -- microk8s add-node --token-ttl 300 | grep 'microk8s join' | head -n1); \
		lxc exec qa-worker2 -- bash -c "$$JOIN_CMD"
	@echo "MicroK8s multi-node cluster is ready. Setting up kubeconfig context..."

	# Export kubeconfig from qa-master
	lxc exec qa-master -- bash -c "microk8s kubectl config view --raw" > $(QA_CLUSTER_NAME)-kubeconfig

	# Extract original context name
	ORIGINAL_CONTEXT=$$(kubectl --kubeconfig=$(QA_CLUSTER_NAME)-kubeconfig config current-context); \
	echo "Original kubeconfig context is '$$ORIGINAL_CONTEXT'"; \
	if [ "$$ORIGINAL_CONTEXT" != "$(QA_KUBECTL_CONTEXT)" ]; then \
		kubectl --kubeconfig=$(QA_CLUSTER_NAME)-kubeconfig config rename-context "$$ORIGINAL_CONTEXT" "$(QA_KUBECTL_CONTEXT)"; \
	fi

	@echo "Kubeconfig for cluster exported to $(QA_CLUSTER_NAME)-kubeconfig with context '$(QA_KUBECTL_CONTEXT)'."
	@echo "To use: export KUBECONFIG=$$(pwd)/$(QA_CLUSTER_NAME)-kubeconfig"
	@echo "  or copy the kubeconfig to ~/.kube/config as needed."

.PHONY: qa-cluster-reset
qa-cluster-reset: ## Reset MicroK8s on all QA nodes (remove cluster state without deleting VMs)
	@for NODE in $(NODE_LIST); do \
		echo "Resetting MicroK8s on $$NODE..."; \
		lxc exec $$NODE -- microk8s reset; \
	done
	@echo "MicroK8s cluster reset on all QA nodes."

.PHONY: qa-cluster-snapshot
qa-cluster-snapshot: ## Pause all VMs, take snapshot 'baseline' consistently, then resume VMs
	@echo "Pausing all QA cluster VMs for consistent snapshots..."
	@for NODE in $(NODE_LIST); do \
		if lxc info $$NODE >/dev/null 2>&1; then \
			echo "Pausing VM $$NODE..."; \
			lxc pause $$NODE; \
		fi; \
	done
	@echo "Taking snapshots named 'baseline'..."
	@for NODE in $(NODE_LIST); do \
		if lxc info $$NODE >/dev/null 2>&1; then \
			echo "Snapshotting VM $$NODE ..."; \
			lxc snapshot $$NODE baseline; \
		fi; \
	done
	@echo "Resuming all QA cluster VMs..."
	@for NODE in $(NODE_LIST); do \
		if lxc info $$NODE >/dev/null 2>&1; then \
			echo "Starting VM $$NODE..."; \
			lxc start $$NODE; \
		fi; \
	done
	@echo "All QA cluster VMs snapshotted consistently as 'baseline'."

.PHONY: qa-cluster-restore
qa-cluster-restore: ## Restore all QA cluster VMs from 'baseline' snapshot and start them
	@echo "Restoring all QA cluster VMs from 'baseline' snapshot..."
	@for NODE in $(NODE_LIST); do \
		if lxc info $$NODE >/dev/null 2>&1; then \
			echo "Restoring VM $$NODE to baseline..."; \
			lxc restore $$NODE baseline; \
			echo "Starting VM $$NODE..."; \
			lxc start $$NODE; \
		fi; \
	done
	@echo "All QA cluster VMs restored to 'baseline' snapshot and started."

.PHONY: qa-nodes-create
qa-nodes-create: ## Create three LXD VMs (master and two workers) with MicroK8s installed
	@for NODE in $(NODE_LIST); do \
		if ! lxc info $$NODE >/dev/null 2>&1; then \
			echo "Launching VM $$NODE..."; \
			lxc launch ubuntu:24.04 --vm $$NODE; \
			echo "Waiting for LXD VM agent in $$NODE..."; \
			until lxc exec $$NODE -- true 2>/dev/null; do sleep 5; done; \
			echo "Installing and starting snapd in $$NODE..."; \
			lxc exec $$NODE -- bash -c "apt update && apt install -y snapd"; \
			lxc exec $$NODE -- bash -c "systemctl enable snapd --now"; \
			lxc exec $$NODE -- bash -c "sleep 10"; \
			echo "Ensuring network works inside $$NODE..."; \
			lxc exec $$NODE -- bash -c "until ping -c1 api.snapcraft.io >/dev/null 2>&1; do echo Waiting for network...; sleep 5; done"; \
			echo "Installing MicroK8s in $$NODE..."; \
			lxc exec $$NODE -- bash -c '\
				for i in {1..5}; do \
					if snap install microk8s --classic && usermod -aG microk8s ubuntu; then exit 0; \
					else echo "Snap install or usermod failed, retrying in 10s..."; sleep 10; \
					fi; \
				done; \
				echo "MicroK8s installation failed after retries." >&2; exit 1' \
		else \
			echo "VM $$NODE already exists, skipping."; \
		fi; \
	done
	@echo "All VM nodes have been created and set up."

.PHONY: qa-nodes-suspend
qa-nodes-suspend: ## Suspend (pause) all LXD QA VMs
	@for NODE in $(NODE_LIST); do \
		if lxc info $$NODE >/dev/null 2>&1; then \
			echo "Suspending VM $$NODE..."; \
			lxc pause $$NODE; \
		else \
			echo "VM $$NODE does not exist, skipping."; \
		fi; \
	done
	@echo "All QA cluster VMs suspended (paused)."

.PHONY: qa-nodes-resume
qa-nodes-resume: ## Resume (start) all LXD QA VMs
	@for NODE in $(NODE_LIST); do \
		if lxc info $$NODE >/dev/null 2>&1; then \
			echo "Resuming (starting) VM $$NODE..."; \
			lxc start $$NODE; \
		else \
			echo "VM $$NODE does not exist, skipping."; \
		fi; \
	done
	@echo "All QA cluster VMs resumed (started)."

.PHONY: qa-nodes-delete
qa-nodes-delete: ## Delete all LXD VMs used by the multi-node MicroK8s cluster
	@for NODE in $(NODE_LIST); do \
		if lxc info $$NODE >/dev/null 2>&1; then \
			echo "Deleting VM $$NODE..."; \
			lxc delete $$NODE --force; \
		else \
			echo "VM $$NODE does not exist, skipping."; \
		fi; \
	done
	@echo "All multi-node cluster VMs deleted."
