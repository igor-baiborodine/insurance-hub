################################################################################
# Child Makefile for QA insurance-hub
################################################################################

################################################################################
# Variables
################################################################################
NODE_LIST	:= qa-master qa-worker1 qa-worker2

.PHONY: help
help:
	@echo "QA insurance-hub cluster Makefile targets:"
	@grep -E '^\S+:.*## ' $(MAKEFILE_LIST) | sort | \
	  awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

.PHONY: lxd-install
lxd-install: ## Install LXD
	@if ! command -v lxd >/dev/null 2>&1; then \
		sudo snap install lxd; \
		sudo usermod -aG lxd $${SUDO_USER:-$$USER}; \
		echo "Initializing LXD with default settings..."; \
		sudo lxd init --auto; \
		echo "LXD installed. Please log out and log back in for group changes to take effect."; \
	else \
		echo "LXD is already installed."; \
	fi

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

.PHONY: qa-cluster-create
qa-cluster-create: qa-nodes-create ## Set up MicroK8s multi-node cluster with DNS, storage, ingress add-ons
	@echo "Starting and configuring MicroK8s on master node..."
	@lxc exec qa-master -- bash -c "microk8s stop || true; microk8s start"
	@lxc exec qa-master -- microk8s enable dns storage ingress
	@JOIN_CMD=$$(lxc exec qa-master -- microk8s add-node --token-ttl 300 | grep 'microk8s join' | head -n1); \
	echo "Joining qa-worker1 and qa-worker2 nodes..."; \
	lxc exec qa-worker1 -- bash -c "microk8s stop || true; microk8s start; $$JOIN_CMD"; \
	lxc exec qa-worker2 -- bash -c "microk8s stop || true; microk8s start; $$JOIN_CMD"
	@echo "MicroK8s multi-node cluster is ready. Use 'lxc exec qa-master -- microk8s kubectl get nodes' to verify."

.PHONY: qa-create
qa-create: qa-cluster-create ## Create nodes and set up the multi-node MicroK8s cluster with required add-ons

.PHONY: qa-suspend
qa-suspend: ## Suspend (pause) all LXD QA VMs
	@for NODE in $(NODE_LIST); do \
		if lxc info $$NODE >/dev/null 2>&1; then \
			echo "Suspending VM $$NODE..."; \
			lxc pause $$NODE; \
		else \
			echo "VM $$NODE does not exist, skipping."; \
		fi; \
	done
	@echo "All QA cluster VMs suspended (paused)."

.PHONY: qa-resume
qa-resume: ## Resume (unpause) all LXD QA VMs
	@for NODE in $(NODE_LIST); do \
		if lxc info $$NODE >/dev/null 2>&1; then \
			echo "Resuming VM $$NODE..."; \
			lxc unpause $$NODE; \
		else \
			echo "VM $$NODE does not exist, skipping."; \
		fi; \
	done
	@echo "All QA cluster VMs resumed."

.PHONY: qa-delete
qa-delete: ## Delete all LXD VMs used by the multi-node MicroK8s cluster
	@for NODE in $(NODE_LIST); do \
		if lxc info $$NODE >/dev/null 2>&1; then \
			echo "Deleting VM $$NODE..."; \
			lxc delete $$NODE --force; \
		else \
			echo "VM $$NODE does not exist, skipping."; \
		fi; \
	done
	@echo "All multi-node cluster VMs deleted."
