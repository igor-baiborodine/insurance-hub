################################################################################
# Child Makefile for local dev insurance-hub
################################################################################

KIND_CLUSTER_NAME  := local-dev-insurance-hub
KIND_CONFIG_FILE   := kind-local-dev-insurance-hub.yaml
KUBECTL_CONTEXT    := kind-$(KIND_CLUSTER_NAME)

.PHONY: help
help:
	@echo "Local dev insurance-hub cluster Makefile targets:"
	@grep -E '^\S+:.*## ' $(MAKEFILE_LIST) | sort | \
	  awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

.PHONY: kind-install
kind-install: ## Install the latest Kind on Ubuntu
	curl -Lo ./kind https://kind.sigs.k8s.io/dl/latest/kind-linux-amd64
	chmod +x ./kind
	echo "Moving kind binary to /usr/local/bin (you may be prompted for your password)..."
	sudo mv ./kind /usr/local/bin/kind
	kind --version

.PHONY: kubectl-install
kubectl-install: ## Install the latest stable kubectl on Ubuntu
	@KUBECTL_VERSION=$$(curl -L -s https://dl.k8s.io/release/stable.txt); \
	if [ -z "$$KUBECTL_VERSION" ]; then \
	  echo "Failed to fetch latest kubectl version!"; \
	  exit 1; \
	fi; \
	curl -LO "https://dl.k8s.io/release/$${KUBECTL_VERSION}/bin/linux/amd64/kubectl"; \
	chmod +x kubectl; \
	echo "Moving kubectl to /usr/local/bin (you may be prompted for your password)..."; \
	sudo mv kubectl /usr/local/bin/kubectl; \
	kubectl version --client

.PHONY: local-dev-create
local-dev-create: ## Create/start the local dev Kind cluster (single-node)
	mkdir -p /tmp/data/local-dev-storage
	kind create cluster --name $(KIND_CLUSTER_NAME) --config $(KIND_CONFIG_FILE)

.PHONY: local-dev-delete
local-dev-delete: ## Delete/stop the local dev Kind cluster with its persistent storage
	rm -rf /tmp/data/local-dev-storage
	kind delete cluster --name $(KIND_CLUSTER_NAME)

.PHONY: local-dev-context
local-dev-context: ## Switch kubectl context to insurance-hub's local dev Kind cluster
	kubectl config use-context $(KUBECTL_CONTEXT)

.PHONY: local-dev-status
local-dev-status: ## Show local dev Kind cluster nodes status
	kubectl --context $(KUBECTL_CONTEXT) get nodes
