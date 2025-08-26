################################################################################
# Child Makefile for local dev insurance-hub k8s cluster
################################################################################

################################################################################
# Variables
################################################################################
LOCAL_DEV_CLUSTER_NAME  	:= dev-insurance-hub
KIND_CONFIG_FILE   			:= kind-insurance-hub.yaml
LOCAL_DEV_KUBECTL_CONTEXT	:= kind-$(LOCAL_DEV_CLUSTER_NAME)

.PHONY: help
help:
	@echo "Local dev insurance-hub cluster Makefile targets:"
	@grep -E '^\S+:.*## ' $(MAKEFILE_LIST) | sort | \
	  awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

.PHONY: local-dev-create
local-dev-create: ## Create/start the local dev Kind cluster (single-node)
	mkdir -p /tmp/data/local-dev-storage
	kind create cluster --name $(LOCAL_DEV_CLUSTER_NAME) --config $(KIND_CONFIG_FILE)

.PHONY: local-dev-delete
local-dev-delete: ## Delete/stop the local dev Kind cluster with its persistent storage
	rm -rf /tmp/data/local-dev-storage
	kind delete cluster --name $(LOCAL_DEV_CLUSTER_NAME)

.PHONY: local-dev-context
local-dev-context: ## Switch kubectl context to insurance-hub's local dev Kind cluster
	kubectl config use-context $(LOCAL_DEV_KUBECTL_CONTEXT)

.PHONY: local-dev-status
local-dev-status: ## Show local dev Kind cluster nodes status
	kubectl --context $(LOCAL_DEV_KUBECTL_CONTEXT) get nodes
