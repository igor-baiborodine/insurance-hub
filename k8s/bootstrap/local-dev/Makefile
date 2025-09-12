################################################################################
# Child Makefile for local dev insurance-hub K8s cluster
################################################################################

################################################################################
# Variables
################################################################################
KIND_CONFIG_FILE   			:= local-dev-insurance-hub.yaml
LOCAL_DEV_CLUSTER_NAME  	:= local-dev-insurance-hub

.PHONY: local-dev-create
local-dev-create: ## Create and start the local dev Kind cluster (single-node)
	@mkdir -p /tmp/data/local-dev-storage
	@kind create cluster --name $(LOCAL_DEV_CLUSTER_NAME) --config $(KIND_CONFIG_FILE)

.PHONY: local-dev-delete
local-dev-delete: ## Stop and delete the local dev Kind cluster with its persistent storage
	@rm -rf /tmp/data/local-dev-storage
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
