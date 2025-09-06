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

.PHONY: helm-install
helm-install: ## Install latest Helm on Ubuntu
	@echo "Installing Helm..."
	@curl -fsSL -o get_helm.sh https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3
	@chmod 700 get_helm.sh
	@./get_helm.sh
	@rm -f get_helm.sh
	@echo "Helm installed."

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
prerequisites-install-all: kubectl-install kubectx-install kubens-install helm-install lxd-install ## Install all k8s prerequisites

################################################################################
# PostgreSQL Deployment
# Namespace is auto-detected from the current kubectl context.
################################################################################

# Helper to determine namespace based on current kubectl context.
NAMESPACE := $(shell \
	ctx="$$(kubectl config current-context 2>/dev/null)"; \
	if [ "$$ctx" = "qa-insurance-hub" ]; then \
		echo "qa-data"; \
	elif [ "$$ctx" = "kind-local-dev-insurance-hub" ]; then \
		echo "local-dev"; \
	else \
		echo "INVALID"; \
	fi)

VALUES_FILE = env/$(NAMESPACE)/postgres-values.yaml
BACKUP_DIR = backups/$(NAMESPACE)

.PHONY: _context_namespace_check
_context_namespace_check:
	@CUR_CTX=$$(kubectl config current-context 2>/dev/null); \
	if [ "$(NAMESPACE)" = "INVALID" ]; then \
		echo "ERROR: kubectl context '$$CUR_CTX' is not supported! Must be 'qa-insurance-hub' or 'kind-local-dev-insurance-hub'."; \
		exit 1; \
	else \
		echo "Using namespace: $(NAMESPACE) (kubectl context: $$CUR_CTX)"; \
	fi

.PHONY: postgres-deploy
postgres-deploy: _context_namespace_check ## Deploy PostgreSQL to cluster context (auto-detects env)
	@echo "Linting PostgreSQL Helm chart for $(NAMESPACE)..."
	@helm lint ./apps/infra/postgres -f "$(VALUES_FILE)" --namespace "$(NAMESPACE)"
	@echo "Updating Helm dependencies..."
	@helm dependency update ./apps/infra/postgres
	@echo "Deploying PostgreSQL to $(NAMESPACE)..."
	@helm upgrade --install postgres ./apps/infra/postgres \
		-f "$(VALUES_FILE)" \
		--namespace "$(NAMESPACE)" \
		--create-namespace \
		--wait

.PHONY: postgres-status
postgres-status: _context_namespace_check ## Show PostgreSQL release/resource status (auto-detects env)
	@echo "Status for namespace: $(NAMESPACE)"
	@helm status postgres --namespace "$(NAMESPACE)" || true
	@echo "\nKubernetes Resources:"
	@kubectl get pods,svc,pvc -n "$(NAMESPACE)" -l app.kubernetes.io/instance=postgres || true

.PHONY: postgres-remove
postgres-remove: _context_namespace_check ## Uninstall PostgreSQL Helm release (auto-detects env)
	@echo "Uninstalling Helm release 'postgres' from namespace '$(NAMESPACE)'..."
	@helm uninstall postgres --namespace "$(NAMESPACE)" || true
	@echo "\nRemaining PVCs in namespace '$(NAMESPACE)' (if any):"
	@kubectl get pvc -n "$(NAMESPACE)" 2>/dev/null || true

.PHONY: postgres-connect
postgres-connect: _context_namespace_check ## Connect via psql to primary instance (auto-detects env)
	@PG_PASSWORD=$$(kubectl get secret --namespace "$(NAMESPACE)" postgres-postgresql -o jsonpath="{.data.postgres-password}" | base64 -d); \
	if [ -z "$$PG_PASSWORD" ]; then \
		echo "ERROR: Could not retrieve password."; \
		exit 1; \
	fi; \
	kubectl run postgres-client --rm --tty -i --restart='Never' --namespace "$(NAMESPACE)" \
		--image docker.io/bitnami/postgresql:latest \
		--env="PGPASSWORD=$$PG_PASSWORD" \
		--command -- psql --host postgres-primary -U postgres -d insurance_hub_$(NAMESPACE)

.PHONY: postgres-backup
postgres-backup: _context_namespace_check ## Create a logical backup of the PostgreSQL database (auto-detects env)
	@mkdir -p "$(BACKUP_DIR)"
	@BACKUP_FILE="$(BACKUP_DIR)/dump-$$(date +%Y-%m-%d_%H-%M-%S).sql"; \
	echo "Saving backup to $$BACKUP_FILE"; \
	kubectl exec -n "$(NAMESPACE)" postgres-primary-0 -- pg_dumpall -U postgres > $$BACKUP_FILE

.PHONY: postgres-restore
postgres-restore: _context_namespace_check ## Restore from backup. Pass FILE=path/to/dump.sql
	@if [ -z "$(FILE)" ]; then \
		echo "ERROR: FILE is required. Use 'make postgres-restore FILE=<path-to-backup>.sql'"; \
		exit 1; \
	fi
	@if [ ! -f "$(FILE)" ]; then \
		echo "ERROR: Backup file not found at $(FILE)"; \
		exit 1; \
	fi
	@cat "$(FILE)" | kubectl exec -i -n "$(NAMESPACE)" postgres-primary-0 -- psql -U postgres
