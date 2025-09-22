################################################################################
# Child Makefile common for local dev and QA insurance-hub cluster
################################################################################

.PHONY: kubeconfig-clear
kubeconfig-clear: ## Empty the ~/.kube/config file
	@echo "Clearing the ~/.kube/config file..."
	@> ~/.kube/config
	@echo "~/.kube/config file has been emptied."

################################################################################
# Kubernetes prerequisites
################################################################################
.PHONY: prereq-k8s-kubectl
prereq-k8s-kubectl: ## Install latest kubectl on Ubuntu 24.04.2 LTS
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

.PHONY: prereq-k8s-kind
prereq-k8s-kind: ## Install latest Kind on Ubuntu 24.04.2 LTS
	curl -Lo ./kind https://kind.sigs.k8s.io/dl/latest/kind-linux-amd64
	chmod +x ./kind
	echo "Moving kind binary to /usr/local/bin (you may be prompted for your password)..."
	sudo mv ./kind /usr/local/bin/kind
	kind --version

.PHONY: prereq-k8s-helm
prereq-k8s-helm: ## Install latest Helm on Ubuntu 24.04.2 LTS
	@echo "Installing Helm..."
	@curl -fsSL -o get_helm.sh https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3
	@chmod 700 get_helm.sh
	@./get_helm.sh
	@rm -f get_helm.sh
	@echo "Helm installed."

.PHONY: prereq-k8s-lxd
prereq-k8s-lxd: ## Install LXD on Ubuntu 24.04.2 LTS
	@if ! command -v lxd >/dev/null 2>&1; then \
		sudo snap install lxd; \
		sudo usermod -aG lxd $${SUDO_USER:-$$USER}; \
		echo "Initializing LXD with default settings..."; \
		sudo lxd init --auto; \
		echo "LXD installed. Please log out and log back in for group changes to take effect."; \
	else \
		echo "LXD is already installed."; \
	fi

.PHONY: prereq-k8s-kubectx
prereq-k8s-kubectx: ## Install latest kubectx on Ubuntu 24.04.2 LTS
	@echo "Installing kubectx..."
	@curl -LO https://github.com/ahmetb/kubectx/releases/latest/download/kubectx
	@chmod +x kubectx
	@sudo mv kubectx /usr/local/bin/kubectx
	@echo "kubectx installed at /usr/local/bin/kubectx"

.PHONY: prereq-k8s-kubens
prereq-k8s-kubens: ## Install latest kubens on Ubuntu 24.04.2 LTS
	@echo "Installing kubens..."
	@curl -LO https://github.com/ahmetb/kubectx/releases/latest/download/kubens
	@chmod +x kubens
	@sudo mv kubens /usr/local/bin/kubens
	@echo "kubens installed at /usr/local/bin/kubens"

.PHONY: prereq-k8s-all
prereq-k8s-all: k8s-prereq-kubectl k8s-prereq-kubectx k8s-prereq-kubens k8s-prereq-helm k8s-prereq-lxd k8s-prereq-kind ## Install all Kubernetes prerequisites

.PHONY: prereq-db-psql
prereq-db-psql: ## Install latest psql (PostgreSQL CLI) on Ubuntu 24.04.2 LTS
	@echo "Installing PostgreSQL client (psql)..."
	@sudo apt-get update
	@sudo apt-get install -y postgresql-client
	@psql --version

.PHONY: prereq-db-mongosh
prereq-db-mongosh: ## Install latest mongosh (MongoDB Shell) on Ubuntu 24.04.2 LTS
	@echo "Installing MongoDB Shell (mongosh)..."
	@sudo apt-get update
	@sudo apt-get install -y gnupg curl
	@curl -fsSL https://pgp.mongodb.com/server-7.0.asc | sudo gpg --dearmor -o /usr/share/keyrings/mongodb-server-7.0.gpg
	@echo "deb [ arch=amd64,arm64 signed-by=/usr/share/keyrings/mongodb-server-7.0.gpg ] https://repo.mongodb.org/apt/ubuntu jammy/mongodb-org/7.0 multiverse" | sudo tee /etc/apt/sources.list.d/mongodb-org-7.0.list
	@sudo apt-get update
	@sudo apt-get install -y mongodb-mongosh
	@mongosh --version

.PHONY: prereq-db-all
prereq-db-all: prereq-db-psql prereq-db-mongosh ## Install all database CLI tool prerequisites

.PHONY: prereq-all
prereq-all: prereq-k8s-all prereq-db-all ## Install all prerequisites

################################################################################
# Internal Helper Targets
################################################################################

CTX := $(shell kubectl config current-context 2>/dev/null)

ifeq ($(CTX), qa-insurance-hub)
	ENV_NAME := qa
	NAMESPACE := qa-data
else ifeq ($(CTX), kind-local-dev-insurance-hub)
	ENV_NAME := local-dev
	NAMESPACE := local-dev
else
	ENV_NAME := INVALID
	NAMESPACE := INVALID
endif

VALUES_FILE_PREFIX = env/$(ENV_NAME)

# Helper to determine environment name and namespace based on current kubectl context.
.PHONY: _context_namespace_check
_context_namespace_check:
	@CUR_CTX=$$(kubectl config current-context 2>/dev/null); \
	if [ "$(NAMESPACE)" = "INVALID" ]; then \
		echo "ERROR: kubectl context '$$CUR_CTX' is not supported! Must be 'qa-insurance-hub' or 'kind-local-dev-insurance-hub'."; \
		exit 1; \
	else \
		echo "Using namespace: $(NAMESPACE) (kubectl context: $$CUR_CTX)"; \
		echo "Using values file prefix: $(VALUES_FILE_PREFIX)"; \
	fi

################################################################################
# Monitoring Deployment Targets
# Targes for deploying and managing monitoring infrastructure
# Current context must be "qa-insurance-hub"
################################################################################

# Specify a chart version to ensure the CRDs installed match the chart itself.
CHART_PROMETHEUS_STACK_VERSION := 77.0.2

.PHONY: _prometheus-operator-crds-install
_prometheus-operator-crds-install:
	@CUR_CTX=$$(kubectl config current-context); \
	if [ "$$CUR_CTX" != "qa-insurance-hub" ]; then \
	   echo "ERROR: Current context is '$$CUR_CTX' (expected: qa-insurance-hub for k3s). Aborting."; \
	   exit 1; \
	fi
	@echo "Ensuring clean state by removing previous chart directory..."
	@rm -rf ./apps/infra/prometheus/kube-prometheus-stack
	@echo "Fetching Prometheus Operator chart to install CRDs..."
	@helm repo add prometheus-community https://prometheus-community.github.io/helm-charts || true
	@helm repo update
	@echo "Creating directory and fetching Prometheus chart version $(CHART_PROMETHEUS_STACK_VERSION)..."
	@mkdir -p ./apps/infra/prometheus
	@helm fetch prometheus-community/kube-prometheus-stack --version $(CHART_PROMETHEUS_STACK_VERSION) --untar --untardir ./apps/infra/prometheus
	@echo "Applying CRDs from explicit path... (This may take a moment)"
	@kubectl apply --server-side -f ./apps/infra/prometheus/kube-prometheus-stack/charts/crds/crds
	@echo "Deleting kube-prometheus-stack-$(CHART_PROMETHEUS_STACK_VERSION).tgz..."
	@rm -rf ./kube-prometheus-stack-$(CHART_PROMETHEUS_STACK_VERSION).tgz

.PHONY: prometheus-install-qa
prometheus-install-qa: _prometheus-operator-crds-install ## Install Prometheus in QA
	@CUR_CTX=$$(kubectl config current-context); \
	if [ "$$CUR_CTX" != "qa-insurance-hub" ]; then \
		echo "ERROR: Current context is '$$CUR_CTX' (expected: qa-insurance-hub for k3s). Aborting."; \
		exit 1; \
	fi
	@kubectl get namespace qa-monitoring >/dev/null 2>&1 || kubectl create namespace qa-monitoring
	@helm repo add prometheus-community https://prometheus-community.github.io/helm-charts || true
	@helm repo update
	@helm upgrade --install prometheus-operator prometheus-community/kube-prometheus-stack \
		--version $(CHART_PROMETHEUS_STACK_VERSION) \
		--namespace qa-monitoring \
		--values ./env/qa/prometheus-values.yaml \
		--skip-crds \
		--wait \
		--timeout 15m

.PHONY: prometheus-ui-qa
prometheus-ui-qa: ## Access Prometheus UI via port-forward
	@echo "Forwarding Prometheus UI to http://localhost:9090"
	@echo "Press Ctrl+C to stop."
	@kubectl port-forward svc/prometheus-prometheus-operator-kube-p-prometheus 9090:9090 --namespace qa-monitoring

################################################################################
# PostgreSQL Deployment Targets
# Targets for deploying and managing PostgreSQL in local dev and QA cluster
# Namespace is auto-detected from the current kubectl context
################################################################################

CNPG_OPERATOR_VERSION := v1.27.0

.PHONY: _postgres-operator-crds-install
_postgres-operator-crds-install:
	@CUR_CTX=$$(kubectl config current-context); \
	if [ "$$CUR_CTX" != "qa-insurance-hub" ]; then \
		echo "ERROR: Current context is '$$CUR_CTX' (expected: qa-insurance-hub for k3s). Aborting."; \
		exit 1; \
	fi
	@echo "Ensuring clean state by removing previous operator CRD directory..."
	@rm -rf ./apps/infra/postgres/crds
	@echo "Downloading CloudNativePG operator CRDs version $(CNPG_OPERATOR_VERSION)..."
	@mkdir -p ./apps/infra/postgres/crds
	@curl -L -o ./apps/infra/postgres/crds/cnpg-crds.yaml https://github.com/cloudnative-pg/cloudnative-pg/releases/download/$(CNPG_OPERATOR_VERSION)/cloudnative-pg.crds.yaml
	@echo "Applying CloudNativePG CRDs..."
	@kubectl apply --server-side -f ./apps/infra/postgres/crds/cnpg-crds.yaml

PHONY: postgres-secret-create-qa
postgres-secret-create-qa: _context_namespace_check ## Create/update PostgreSQL password secret in QA. Usage: make postgres-secret-create-qa POSTGRES_PASSWORD=your_password
	@if [ -z "$(POSTGRES_PASSWORD)" ]; then \
		echo "ERROR: POSTGRES_PASSWORD argument is required. Usage: make postgres-secret-create-qa POSTGRES_PASSWORD=your_password"; \
		exit 1; \
	fi
	@echo "Ensuring namespace 'qa-data' exists..."
	@kubectl create namespace qa-data --dry-run=client -o yaml | kubectl apply -f -
	@kubectl create secret generic qa-postgres-secret \
		--namespace qa-data \
		--from-literal=postgres-password="$(POSTGRES_PASSWORD)" \
		--from-literal=replication-password="$(POSTGRES_PASSWORD)" \
		--dry-run=client -o yaml | kubectl apply -f -
	@echo "Secret 'qa-postgres-secret' successfully created/updated in namespace qa-data."

.PHONY: postgres-deploy
postgres-deploy: _context_namespace_check ## Deploy PostgreSQL to cluster context (auto-detects env). QA prerequisite: postgres-secret-create-qa
	@echo "Linting PostgreSQL Helm chart for $(NAMESPACE)..."
	@helm lint ./apps/infra/postgres -f "$(VALUES_FILE_PREFIX)"/postgres-values.yaml --namespace "$(NAMESPACE)"
	@echo "Updating Helm dependencies..."
	@helm dependency update ./apps/infra/postgres
	@echo "Deploying PostgreSQL with explicit secret configuration..."
	@helm upgrade --install postgres ./apps/infra/postgres \
		-f "$(VALUES_FILE_PREFIX)"/postgres-values.yaml \
		--namespace "$(NAMESPACE)" \
		--create-namespace \
		--wait

.PHONY: postgres-status
postgres-status: _context_namespace_check ## Show PostgreSQL release/resource status (auto-detects env)
	@echo "Status for namespace: $(NAMESPACE)"
	@helm status postgres --namespace "$(NAMESPACE)" || true
	@echo "\nKubernetes Resources:"
	@kubectl get pods,svc,pvc -n "$(NAMESPACE)" -l app.kubernetes.io/instance=postgres || true

.PHONY: postgres-uninstall
postgres-uninstall: _context_namespace_check ## Uninstall PostgreSQL Helm release (chart only, auto-detects env)
	@echo "Uninstalling Helm release 'postgres' from namespace '$(NAMESPACE)'..."
	@helm uninstall postgres --namespace "$(NAMESPACE)" || true

.PHONY: postgres-purge
postgres-purge: postgres-uninstall ## Uninstall chart AND permanently remove related secrets and PVCs (auto-detects env)
	@echo "\nDeleting PostgreSQL-related secrets and PVCs in namespace '$(NAMESPACE)'..."
	@kubectl delete secret -n "$(NAMESPACE)" -l app.kubernetes.io/instance=postgres || true
	@kubectl delete pvc -n "$(NAMESPACE)" -l app.kubernetes.io/instance=postgres || true

################################################################################
# MongoDB Deployment Targets
# Targets for deploying and managing MongoDB in local dev and QA cluster
# Namespace and environment auto-detected from current kubectl context
################################################################################
.PHONY: mongodb-deploy
mongodb-deploy: _context_namespace_check ## Deploy MongoDB to current cluster context (auto-detect env).
	@echo "Linting MongoDB Helm chart for $(NAMESPACE)..."
	@helm lint ./apps/infra/mongodb -f "$(VALUES_FILE_PREFIX)"/mongodb-values.yaml --namespace "$(NAMESPACE)"
	@echo "Updating Helm dependencies..."
	@helm dependency update ./apps/infra/mongodb
	@echo "Deploying MongoDB Helm chart..."
	@helm upgrade --install mongodb ./apps/infra/mongodb \
		-f "$(VALUES_FILE_PREFIX)"/mongodb-values.yaml \
		--namespace "$(NAMESPACE)" \
		--create-namespace \
		--wait

.PHONY: mongodb-status
mongodb-status: _context_namespace_check ## Show MongoDB release/resource status (auto-detect env)
	@echo "Status for namespace: $(NAMESPACE)"
	@helm status mongodb --namespace "$(NAMESPACE)" || true
	@echo "\nKubernetes Resources:"
	@kubectl get pods,svc,pvc -n "$(NAMESPACE)" -l app.kubernetes.io/instance=mongodb || true

.PHONY: mongodb-uninstall
mongodb-uninstall: _context_namespace_check ## Uninstall MongoDB Helm release (chart only, auto-detect env)
	@echo "Uninstalling Helm release 'mongodb' from namespace $(NAMESPACE)..."
	@helm uninstall mongodb --namespace "$(NAMESPACE)" || true

.PHONY: mongodb-purge
mongodb-purge: mongodb-uninstall ## Uninstall chart AND permanently remove related secrets and PVCs (auto-detect env)
	@echo "\nDeleting MongoDB-related secrets and PVCs in namespace $(NAMESPACE)..."
	@kubectl delete secret -n "$(NAMESPACE)" -l app.kubernetes.io/instance=mongodb || true
	@kubectl delete pvc -n "$(NAMESPACE)" -l app.kubernetes.io/instance=mongodb || true
