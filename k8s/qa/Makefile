################################################################################
# Child Makefile for QA insurance-hub
################################################################################

################################################################################
# Variables
################################################################################
NODES_MASTER		:= qa-master
NODES_WORKER		:= qa-worker1 qa-worker2
NODES_ALL			:= $(NODES_MASTER) $(NODES_WORKER)
QA_CLUSTER_NAME  	:= qa-insurance-hub
QA_KUBECTL_CONTEXT	:= $(QA_CLUSTER_NAME)

.PHONY: help
help:
	@echo "QA insurance-hub cluster Makefile targets:"
	@grep -E '^\S+:.*## ' $(MAKEFILE_LIST) | sort | \
	  awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-32s\033[0m %s\n", $$1, $$2}'

.PHONY: qa-create
qa-create: qa-cluster-create ## Create nodes and set up the multi-node k3s cluster and configure kubeconfig

.PHONY: qa-cluster-create
qa-cluster-create: qa-nodes-create ## Set up a multi-node k3s cluster (Rancher) and configure kubeconfig context
	@echo "Starting and configuring k3s server on master node..."
	@lxc exec qa-master -- bash -c "set -e; apt-get update -y && apt-get install -y curl ca-certificates; \
		curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC='--write-kubeconfig-mode 644' sh -s - server"

	# Wait for API server on port 6443 to be ready and responsive
	@echo "Waiting for Kubernetes API server to become accessible on k3s..."
	@lxc exec qa-master -- bash -c '\
		for i in {1..40}; do \
			curl -sk https://127.0.0.1:6443/readyz >/dev/null 2>&1 && break; \
			echo "Waiting for API server..."; sleep 5; \
		done'

	# Determine master IP and fetch node token
	@MASTER_IP=$$(lxc exec qa-master -- bash -c "hostname -I | awk '{print \$1}'"); \
	TOKEN=$$(lxc exec qa-master -- bash -c "cat /var/lib/rancher/k3s/server/node-token"); \
	echo "Master IP: $$MASTER_IP"; echo "Joining workers using token..."; \
	for NODE in qa-worker1 qa-worker2; do \
		echo "Installing k3s agent on $$NODE and joining cluster..."; \
		lxc exec $$NODE -- bash -c "set -e; apt-get update -y && apt-get install -y curl ca-certificates; \
			for i in {1..5}; do \
				curl -sfL https://get.k3s.io | K3S_URL=https://$$MASTER_IP:6443 K3S_TOKEN=$$TOKEN sh -s - agent && exit 0; \
				echo 'k3s agent install failed, retrying in 10s...'; sleep 10; \
			done; \
			echo 'k3s agent installation failed after retries.' >&2; exit 1"; \
	done

	# Wait until kubectl works on master and all nodes are registered
	@echo "Waiting for nodes to become Ready..."
	@lxc exec qa-master -- bash -c '\
		for i in {1..40}; do \
			k3s kubectl get nodes >/dev/null 2>&1 && [ $$(k3s kubectl get nodes --no-headers 2>/dev/null | wc -l) -ge 3 ] && break; \
			echo "Waiting for nodes to register..."; sleep 5; \
		done; \
		for i in {1..60}; do \
			READY=$$(k3s kubectl get nodes --no-headers | awk '{print $$2}' | grep -c '^Ready'); \
			[ $$READY -ge 3 ] && break; \
			echo "Waiting for nodes to be Ready..."; sleep 5; \
		done'

	@echo "k3s multi-node cluster is ready. Saving kubeconfig inside qa-master and pulling to host..."

	# Save kubeconfig inside VM and adjust server address to master IP
	@MASTER_IP=$$(lxc exec qa-master -- bash -c "hostname -I | awk '{print \$1}'"); \
	lxc exec qa-master -- bash -c "cp /etc/rancher/k3s/k3s.yaml /home/ubuntu/kubeconfig.yaml && chown ubuntu:ubuntu /home/ubuntu/kubeconfig.yaml && sed -i 's/127.0.0.1/$$MASTER_IP/' /home/ubuntu/kubeconfig.yaml"
	# Pull the kubeconfig file to host
	lxc file pull qa-master/home/ubuntu/kubeconfig.yaml ./$(QA_CLUSTER_NAME)-kubeconfig

	# Rename context dynamically
	ORIGINAL_CONTEXT=$$(kubectl --kubeconfig=$(QA_CLUSTER_NAME)-kubeconfig config current-context); \
	echo "Original kubeconfig context is '$$ORIGINAL_CONTEXT'"; \
	if [ "$$ORIGINAL_CONTEXT" != "$(QA_KUBECTL_CONTEXT)" ]; then \
		kubectl --kubeconfig=$(QA_CLUSTER_NAME)-kubeconfig config rename-context "$$ORIGINAL_CONTEXT" "$(QA_KUBECTL_CONTEXT)"; \
	fi

	# Merge kubeconfig into default kubeconfig (~/.kube/config)
	@echo "Merging $(QA_CLUSTER_NAME)-kubeconfig into default kubeconfig (~/.kube/config)..."
	@export KUBECONFIG=~/.kube/config:$(PWD)/$(QA_CLUSTER_NAME)-kubeconfig; \
	kubectl config view --flatten > $(HOME)/.kube/config.merged && \
	mv $(HOME)/.kube/config.merged $(HOME)/.kube/config

	@echo "Merged kubeconfigs. You can now switch contexts with kubectl config use-context $(QA_KUBECTL_CONTEXT)."

.PHONY: qa-cluster-reset
qa-cluster-reset: ## Reset MicroK8s on all QA nodes (remove cluster state without deleting VMs)
	@for NODE in $(NODES_ALL); do \
		echo "Resetting MicroK8s on $$NODE..."; \
		lxc exec $$NODE -- microk8s reset; \
	done
	@echo "MicroK8s cluster reset on all QA nodes."

.PHONY: qa-cluster-snapshot
qa-cluster-snapshot: ## Pause all VMs, take snapshot 'baseline' consistently, then resume VMs
	@echo "Pausing all QA cluster VMs for consistent snapshots..."
	@for NODE in $(NODES_ALL); do \
		if lxc info $$NODE >/dev/null 2>&1; then \
			echo "Pausing VM $$NODE..."; \
			lxc pause $$NODE; \
		fi; \
	done
	@echo "Taking snapshots named 'baseline'..."
	@for NODE in $(NODES_ALL); do \
		if lxc info $$NODE >/dev/null 2>&1; then \
			echo "Snapshotting VM $$NODE ..."; \
			lxc snapshot $$NODE baseline; \
		fi; \
	done
	@echo "Resuming all QA cluster VMs..."
	@for NODE in $(NODES_ALL); do \
		if lxc info $$NODE >/dev/null 2>&1; then \
			echo "Starting VM $$NODE..."; \
			lxc start $$NODE; \
		fi; \
	done
	@echo "All QA cluster VMs snapshotted consistently as 'baseline'."

.PHONY: qa-cluster-restore
qa-cluster-restore: ## Restore all QA cluster VMs from 'baseline' snapshot and start them
	@echo "Restoring all QA cluster VMs from 'baseline' snapshot..."
	@for NODE in $(NODES_ALL); do \
		if lxc info $$NODE >/dev/null 2>&1; then \
			echo "Restoring VM $$NODE to baseline..."; \
			lxc restore $$NODE baseline; \
			echo "Starting VM $$NODE..."; \
			lxc start $$NODE; \
		fi; \
	done
	@echo "All QA cluster VMs restored to 'baseline' snapshot and started."

.PHONY: qa-cluster-enable-all-addons
qa-cluster-enable-all-addons: qa-cluster-enable-dns qa-cluster-enable-storage qa-cluster-enable-ingress ## Enable all MicroK8s addons

.PHONY: qa-cluster-enable-dns
qa-cluster-enable-dns: ## Enable MicroK8s DNS addon with readiness waits and retry
	@echo "Enabling dns (with retries if API not available)..."
	@for i in {1..5}; do \
		lxc exec qa-master -- microk8s enable dns && break; \
		echo "dns enable failed, retrying in 15s..."; sleep 15; \
	done
	@lxc exec qa-master -- bash -c "microk8s status --wait-ready"
	@lxc exec qa-master -- bash -c 'for i in {1..12}; do microk8s kubectl get nodes >/dev/null 2>&1 && break; sleep 5; done'

.PHONY: qa-cluster-enable-storage
qa-cluster-enable-storage: ## Enable MicroK8s hostpath-storage addon with readiness waits and retry
	@echo "Enabling hostpath-storage (with retries if API not available)..."
	@for i in {1..5}; do \
		lxc exec qa-master -- microk8s enable hostpath-storage && break; \
		echo "hostpath-storage enable failed, retrying in 15s..."; sleep 15; \
	done
	@lxc exec qa-master -- bash -c "microk8s status --wait-ready"
	@lxc exec qa-master -- bash -c 'for i in {1..12}; do microk8s kubectl get storageclass >/dev/null 2>&1 && break; sleep 5; done'

.PHONY: qa-cluster-enable-ingress
qa-cluster-enable-ingress: ## Enable MicroK8s ingress addon with readiness waits and retry
	@echo "Enabling ingress (with retries if API not available)..."
	@for i in {1..5}; do \
		lxc exec qa-master -- microk8s enable ingress && break; \
		echo "ingress enable failed, retrying in 15s..."; sleep 15; \
	done
	@lxc exec qa-master -- bash -c "microk8s status --wait-ready"
	@lxc exec qa-master -- bash -c 'for i in {1..12}; do microk8s kubectl get daemonsets -n ingress >/dev/null 2>&1 && break; sleep 5; done'

.PHONY: qa-nodes-create
qa-nodes-create: ## Create three LXD VMs (master and two workers) prepared for k3s (Rancher)
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
	@echo "All QA cluster VMs suspended (paused)."

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
	@echo "All QA cluster VMs resumed (started)."

.PHONY: qa-nodes-delete
qa-nodes-delete: ## Delete all LXD VMs used by the multi-node MicroK8s cluster
	@for NODE in $(NODES_ALL); do \
		if lxc info $$NODE >/dev/null 2>&1; then \
			echo "Deleting VM $$NODE..."; \
			lxc delete $$NODE --force; \
		else \
			echo "VM $$NODE does not exist, skipping."; \
		fi; \
	done
	@echo "All multi-node cluster VMs deleted."

################################################################################
# MicroK8s
################################################################################
