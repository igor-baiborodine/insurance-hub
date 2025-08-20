################################################################################
# Child Makefile common for local dev/qa insurance-hub
################################################################################

.PHONY: help
help:
	@echo "Common local dev/qa insurance-hub cluster Makefile targets:"
	@grep -E '^\S+:.*## ' $(MAKEFILE_LIST) | sort | \
	  awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

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
