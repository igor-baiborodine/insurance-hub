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

.PHONY: help
help:
	@echo "QA insurance-hub cluster Makefile targets:"
	@grep -E '^\S+:.*## ' $(MAKEFILE_LIST) | sort | \
	  awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-32s\033[0m %s\n", $$1, $$2}'

.PHONY: qa-create
qa-create: qa-cluster-create ## Create nodes and set up the multi-node k3s cluster and configure kubeconfig

.PHONY: qa-cluster-create
qa-cluster-create: qa-nodes-create ## Set up multi-node Rancher k3s cluster and export kubeconfig
	@echo "Setting up Rancher k3s cluster on qa-master node..."

	# Install k3s server on master
	@lxc exec qa-master -- bash -c "curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC='--write-kubeconfig-mode 644' sh -"

	# Wait for k3s server to be active
	@echo "Waiting for k3s server on qa-master to become active..."
	@lxc exec qa-master -- bash -c 'for i in {1..20}; do systemctl is-active k3s && break || (echo Waiting for k3s server...; sleep 5); done'

	# Get node token and master IP for joining workers
	@MASTER_IP=$$(lxc list qa-master -c 4 --format json | jq -r '.[0].state.network.eth0.addresses[] | select(.family=="inet").address'); \
		TOKEN=$$(lxc exec qa-master -- sudo cat /var/lib/rancher/k3s/server/node-token); \
		echo "Master IP: $$MASTER_IP"; \
		echo "Node Token: $$TOKEN"; \

	# Join worker nodes to master
	@for WORKER in qa-worker1 qa-worker2; do \
		echo "Installing k3s agent on $$WORKER..."; \
		lxc exec $$WORKER -- bash -c "curl -sfL https://get.k3s.io | K3S_URL=https://$$MASTER_IP:6443 K3S_TOKEN=$$TOKEN sh -"; \
	done

	# Wait for nodes to join cluster
	@echo "Waiting for all nodes to register in the cluster..."
	@lxc exec qa-master -- bash -c 'for i in {1..20}; do kubectl get nodes &>/dev/null && break || (echo Waiting for nodes...; sleep 5); done'

	# Export kubeconfig from master and pull to host
	@echo "Exporting kubeconfig from qa-master..."
	@lxc exec qa-master -- bash -c "sudo cat /etc/rancher/k3s/k3s.yaml" > ./$(QA_CLUSTER_NAME)-kubeconfig

	# Adjust kubeconfig server address from 127.0.0.1 to actual master IP
	@MASTER_IP=$$(lxc list qa-master -c 4 --format json | jq -r '.[0].state.network.eth0.addresses[] | select(.family=="inet").address'); \
		sed -i "s/127.0.0.1/$$MASTER_IP/g" ./$(QA_CLUSTER_NAME)-kubeconfig

	# Rename context to qa-insurance-hub
	@ORIGINAL_CONTEXT=$$(kubectl --kubeconfig=$(QA_CLUSTER_NAME)-kubeconfig config current-context); \
	if [ "$$ORIGINAL_CONTEXT" != "$(QA_KUBECTL_CONTEXT)" ]; then \
		kubectl --kubeconfig=$(QA_CLUSTER_NAME)-kubeconfig config rename-context "$$ORIGINAL_CONTEXT" "$(QA_KUBECTL_CONTEXT)"; \
	fi

	@echo "Cluster kubeconfig saved to $(QA_CLUSTER_NAME)-kubeconfig with context '$(QA_KUBECTL_CONTEXT)'."
	@echo "To use the cluster run the following command:"
	@echo "  export KUBECONFIG=$$(pwd)/$(QA_CLUSTER_NAME)-kubeconfig"

.PHONY: qa-cluster-reset
qa-cluster-reset: ## Reset k3s on all QA nodes (remove cluster state without deleting VMs)
	@for NODE in $(NODES_ALL); do \
		echo "Resetting k3s on $$NODE..."; \
		lxc exec $$NODE -- bash -c "set -e; \
			[ -f /usr/local/bin/k3s-uninstall.sh ] && /usr/local/bin/k3s-uninstall.sh || true; \
			[ -f /usr/local/bin/k3s-agent-uninstall.sh ] && /usr/local/bin/k3s-agent-uninstall.sh || true; \
			rm -rf /var/lib/rancher/k3s /etc/rancher/k3s || true"; \
	done
	@echo "k3s cluster reset on all QA nodes."
	@echo "Removing local kubeconfig file ($(QA_CLUSTER_NAME)-kubeconfig) if it exists..."
	@rm -f ./$(QA_CLUSTER_NAME)-kubeconfig

.PHONY: qa-cluster-enable-all-addons
qa-cluster-enable-all-addons: qa-cluster-enable-dns qa-cluster-enable-storage qa-cluster-enable-ingress ## Validate core k3s components (CoreDNS, local-path storage, Traefik)

.PHONY: qa-cluster-enable-dns
qa-cluster-enable-dns: ## Validate CoreDNS is running and Ready in k3s
	@echo "Validating CoreDNS readiness in k3s..."
	@lxc exec qa-master -- bash -c '\
		for i in {1..24}; do \
			READY=$$(k3s kubectl -n kube-system get deploy coredns -o jsonpath="{.status.readyReplicas}" 2>/dev/null || echo 0); \
			[ "$$READY" != "" ] && [ $$READY -ge 1 ] && echo "CoreDNS is Ready." && exit 0; \
			echo "Waiting for CoreDNS to be Ready..."; sleep 5; \
		done; \
		echo "CoreDNS not Ready after timeout." >&2; exit 1'

.PHONY: qa-cluster-enable-storage
qa-cluster-enable-storage: ## Validate local-path storage class and provisioner in k3s
	@echo "Validating local-path storage in k3s..."
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

.PHONY: qa-cluster-enable-ingress
qa-cluster-enable-ingress: ## Validate Traefik ingress controller in k3s
	@echo "Validating Traefik ingress controller in k3s..."
	@lxc exec qa-master -- bash -c '\
		for i in {1..24}; do \
			READY=$$(k3s kubectl -n kube-system get deploy traefik -o jsonpath="{.status.readyReplicas}" 2>/dev/null || echo 0); \
			[ "$$READY" != "" ] && [ $$READY -ge 1 ] && echo "Traefik is Ready." && exit 0; \
			echo "Waiting for Traefik to be Ready..."; sleep 5; \
		done; \
		echo "Traefik not Ready after timeout." >&2; exit 1'

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
qa-nodes-snapshot: ## Pause all VMs, take snapshot 'baseline' consistently, then resume VMs
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

.PHONY: qa-nodes-restore
qa-nodes-restore: ## Restore all QA cluster VMs from 'baseline' snapshot and start them
	@echo "Restoring all QA cluster VMs from 'baseline' snapshot..."
	@for NODE in $(NODES_ALL); do \
		if lxc info $$NODE >/dev/null 2>&1; then \
			echo "Restoring and starting VM $$NODE..."; \
			lxc restore $$NODE baseline; \
		fi; \
	done
	@echo "All QA cluster VMs restored to 'baseline' snapshot and started:"
	@lxc list

.PHONY: qa-nodes-delete
qa-nodes-delete: ## Delete all LXD VMs used by the multi-node k3s cluster
	@for NODE in $(NODES_ALL); do \
		if lxc info $$NODE >/dev/null 2>&1; then \
			echo "Deleting VM $$NODE..."; \
			lxc delete $$NODE --force; \
		else \
			echo "VM $$NODE does not exist, skipping."; \
		fi; \
	done
	@echo "All multi-node cluster VMs deleted."
