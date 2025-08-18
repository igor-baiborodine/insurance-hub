################################################################################
# Child Makefile for QA insurance-hub
################################################################################

################################################################################
# Variables
################################################################################
define LXD_MICROK8S_PROFILE
config:
  linux.kernel_modules: ip_tables,ip6_tables,netlink_diag,nf_nat,overlay
  raw.lxc: |
    lxc.apparmor.profile = unconfined
    lxc.mount.auto = proc:rw sys:rw
    lxc.cgroup.devices.allow = a
    lxc.cap.drop =
  security.nesting: "true"
  security.privileged: "true"
endef
export LXD_MICROK8S_PROFILE

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
		sudo usermod -aG lxd $$USER; \
		echo "Initializing LXD with default settings..."; \
		sudo lxd init --auto; \
		echo "LXD installed. Please log out and log back in for group changes to take effect."; \
	else \
		echo "LXD is already installed."; \
	fi

.PHONY: qa-lxd-profile-create
qa-lxd-profile-create: ## Create the LXD profile required by MicroK8s
	@if ! lxc profile show microk8s >/dev/null 2>&1; then \
		echo ">> Creating LXD profile for MicroK8s..."; \
		lxc profile create microk8s; \
		echo "$$LXD_MICROK8S_PROFILE" | lxc profile edit microk8s; \
	else \
		echo ">> LXD profile 'microk8s' already exists."; \
	fi

.PHONY: qa-nodes-create
qa-nodes-create: ## Create three LXD containers (master and two workers) with MicroK8s installed
	@for NODE in $(NODE_LIST); do \
		if ! lxc info $$NODE >/dev/null 2>&1; then \
			echo "Launching container $$NODE with MicroK8s profile..."; \
			lxc launch ubuntu:22.04 $$NODE --profile default --profile microk8s; \
			echo "Installing MicroK8s in $$NODE..."; \
			lxc exec $$NODE -- bash -c "snap wait system seed.loaded && snap install microk8s --classic && usermod -aG microk8s ubuntu"; \
		else \
			echo "Container $$NODE already exists, skipping."; \
		fi; \
	done
	@sleep 30
	@echo "All nodes have been created and set up."

.PHONY: qa-cluster-create
qa-cluster-create: ## Set up MicroK8s multi-node cluster with DNS, storage, ingress add-ons
	@echo "Starting and configuring MicroK8s on master node..."
	lxc exec qa-master -- bash -c "microk8s stop || true; microk8s start"
	lxc exec qa-master -- microk8s enable dns storage ingress
	$(eval JOIN_CMD := $(shell lxc exec qa-master -- microk8s add-node --token-ttl 300 | grep 'microk8s join' | head -n1))
	@echo "Joining qa-worker1 and qa-worker2 nodes..."
	lxc exec qa-worker1 -- bash -c "microk8s stop || true; microk8s start; $(JOIN_CMD)"
	lxc exec qa-worker2 -- bash -c "microk8s stop || true; microk8s start; $(JOIN_CMD)"
	@echo "MicroK8s multi-node cluster is ready. Use 'lxc exec qa-master -- microk8s kubectl get nodes' to verify."

.PHONY: qa-create
qa-create: ## Create nodes and set up the multi-node MicroK8s cluster with required add-ons
	$(MAKE) qa-nodes-create
	$(MAKE) qa-cluster-create

.PHONY: qa-delete
qa-delete: ## Delete all LXD containers used by the multi-node MicroK8s cluster
	@for NODE in $(NODE_LIST); do \
		if lxc info $$NODE >/dev/null 2>&1; then \
			echo "Deleting container $$NODE..."; \
			lxc delete $$NODE --force; \
		else \
			echo "Container $$NODE does not exist, skipping."; \
		fi; \
	done
	@echo "All multi-node cluster containers deleted."
