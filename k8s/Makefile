################################################################################
# Child Makefile common for local dev and qa insurance-hub cluster
################################################################################

.PHONY: help
help:
	@echo "Common local dev/qa insurance-hub cluster Makefile targets:"
	@grep -E '^\S+:.*## ' $(MAKEFILE_LIST) | sort | \
	  awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-26s\033[0m %s\n", $$1, $$2}'

.PHONY: kubeconfig-clear
kubeconfig-clear: ## Empty the ~/.kube/config file
	@echo "Clearing the ~/.kube/config file..."
	@> ~/.kube/config
	@echo "~/.kube/config file has been emptied."

################################################################################
# Kubernetes prerequisites
################################################################################
.PHONY: kubectl-install
kubectl-install: ## Install latest kubectl on Ubuntu
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

.PHONY: kind-install
kind-install: ## Install latest Kind on Ubuntu
	curl -Lo ./kind https://kind.sigs.k8s.io/dl/latest/kind-linux-amd64
	chmod +x ./kind
	echo "Moving kind binary to /usr/local/bin (you may be prompted for your password)..."
	sudo mv ./kind /usr/local/bin/kind
	kind --version

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

.PHONY: kubectx-install
kubectx-install: ## Install latest kubectx on Ubuntu
	@echo "Installing kubectx..."
	@curl -LO https://github.com/ahmetb/kubectx/releases/latest/download/kubectx
	@chmod +x kubectx
	@sudo mv kubectx /usr/local/bin/kubectx
	@echo "kubectx installed at /usr/local/bin/kubectx"

.PHONY: kubens-install
kubens-install: ## Install latest kubens on Ubuntu
	@echo "Installing kubens..."
	@curl -LO https://github.com/ahmetb/kubectx/releases/latest/download/kubens
	@chmod +x kubens
	@sudo mv kubens /usr/local/bin/kubens
	@echo "kubens installed at /usr/local/bin/kubens"

.PHONY: prerequisites-install-all
prerequisites-install-all: kubectl-install kubectx-install kubens-install lxd-install ## Install all k8s prerequisites

################################################################################
# PostgreSQL deployment
################################################################################
.PHONY: postgres-deploy
postgres-deploy: ## Deploy PostgreSQL with ENV_NAME=(local-dev|qa)
	@if [ -z "$(ENV_NAME)" ]; then \
		echo "ERROR: ENV_NAME is required. Use 'local-dev' or 'qa'."; \
		exit 1; \
	fi
	@if [ "$(ENV_NAME)" != "local-dev" ] && [ "$(ENV_NAME)" != "qa" ]; then \
		echo "ERROR: ENV_NAME must be 'local-dev' or 'qa'. Received: '$(ENV_NAME)'"; \
		exit 1; \
	fi
	@echo "--- Linting PostgreSQL chart for $(ENV_NAME) ---"
	@helm lint ./apps/infra/postgres -f "env/$(ENV_NAME)/postgres-values.yaml"

	@echo "--- Updating Helm dependencies for PostgreSQL ---"
	@helm dependency update ./apps/infra/postgres

	@echo "--- Deploying PostgreSQL to $(ENV_NAME) cluster ---"
	@VALUES_FILE="env/$(ENV_NAME)/postgres-values.yaml"; \
	if [ ! -f "$$VALUES_FILE" ]; then \
		echo "ERROR: $$VALUES_FILE not found"; \
		exit 1; \
	fi; \
	helm upgrade --install postgres ./apps/infra/postgres \
		-f $$VALUES_FILE \
		--namespace database \
		--create-namespace \
		--wait


.PHONY: postgres-status
postgres-status: ## Show PostgreSQL release and k8s resource status
	@echo "Helm release status (database/postgres):"
	@helm status postgres --namespace database || true
	@echo ""
	@echo "Kubernetes resources in namespace 'database' (labeled instance=postgres):"
	@kubectl get pods,svc,pvc -n database -l app.kubernetes.io/instance=postgres || true

.PHONY: postgres-remove
postgres-remove: ## Uninstall PostgreSQL Helm release from current cluster (namespace 'database')
	@echo "Uninstalling Helm release 'postgres' from namespace 'database'..."
	@helm uninstall postgres --namespace database || true
	@echo "Remaining PVCs in namespace 'database' (if any):"
	@kubectl get pvc -n database 2>/dev/null || true
