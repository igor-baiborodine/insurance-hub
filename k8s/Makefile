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
	@if [ -z "$$KUBECTL_VERSION" ]; then \
	  echo "Failed to fetch latest kubectl version!"; \
	  exit 1; \
	fi; \
	@curl -LO "https://dl.k8s.io/release/$${KUBECTL_VERSION}/bin/linux/amd64/kubectl"; \
	@chmod +x kubectl; \
	@echo "Moving kubectl to /usr/local/bin (you may be prompted for your password)..."; \
	@sudo mv kubectl /usr/local/bin/kubectl; \
	@kubectl version --client

.PHONY: prereq-k8s-kind
prereq-k8s-kind: ## Install latest Kind on Ubuntu 24.04.2 LTS
	@curl -Lo ./kind https://kind.sigs.k8s.io/dl/latest/kind-linux-amd64
	@chmod +x ./kind
	@echo "Moving kind binary to /usr/local/bin (you may be prompted for your password)..."
	@sudo mv ./kind /usr/local/bin/kind
	@kind --version

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

.PHONY: prereq-k8s-cnpg-plugin
prereq-k8s-cnpg-plugin: ## Install CloudNativePG kubectl plugin on Ubuntu 24.04.2 LTS
	@echo "Installing CloudNativePG kubectl plugin..."
	@curl -sSfL https://github.com/cloudnative-pg/cloudnative-pg/raw/main/hack/install-cnpg-plugin.sh | sudo sh -s -- -b /usr/local/bin
	@echo "kubectl-cnpg plugin installed at /usr/local/bin"

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

CUR_CTX := $(shell kubectl config current-context 2>/dev/null)

ifeq ($(CUR_CTX), qa-insurance-hub)
    ENV_NAME := qa
    SVC_NS := $(ENV_NAME)-svc
    DATA_NS := $(ENV_NAME)-data
	AUTH_NS := $(ENV_NAME)-auth
	NETWORKING_NS := $(ENV_NAME)-networking
	MONITORING_NS := $(ENV_NAME)-monitoring
else
    ifeq ($(CUR_CTX), kind-local-dev-insurance-hub)
        ENV_NAME := local-dev
        LOCAL_DEV_NS := $(ENV_NAME)-all
		SVC_NS := $(LOCAL_DEV_NS)
		DATA_NS := $(LOCAL_DEV_NS)
		AUTH_NS := $(LOCAL_DEV_NS)
		NETWORKING_NS := $(LOCAL_DEV_NS)
		MONITORING_NS := $(LOCAL_DEV_NS)
    else
        ENV_NAME := INVALID
    endif
endif

VALUES_FILE_PREFIX = env/$(ENV_NAME)

# Helper to validate environment, i.e., current kubectl context.
.PHONY: _env_check
_env_check:
	@if [ "$(ENV_NAME)" = "INVALID" ]; then \
		echo "ERROR: kubectl context '$$CUR_CTX' is not supported! Must be 'qa-insurance-hub' or 'kind-local-dev-insurance-hub'."; \
		exit 1; \
	else \
		echo "Using ENV_NAME=$(ENV_NAME), CUR_CTX=$(CUR_CTX)"; \
	fi

################################################################################
# Monitoring Deployment Targets
# Targes for deploying and managing monitoring infrastructure
# Current context must be "qa-insurance-hub"
################################################################################

KUBE_PROMETHEUS_STACK_VERSION := 77.10.0

.PHONY: prometheus-stack-install
prometheus-stack-install: ## Install Kube Prometheus Stack (Prometheus, Grafana, Alert Manager) in QA
	@CUR_CTX=$$(kubectl config current-context); \
	if [ "$$CUR_CTX" != "qa-insurance-hub" ]; then \
		echo "ERROR: Current context is '$$CUR_CTX' (expected: qa-insurance-hub for k3s). Aborting."; \
		exit 1; \
	fi
	@kubectl get namespace qa-monitoring >/dev/null 2>&1 || kubectl create namespace qa-monitoring
	@helm repo add prometheus-community https://prometheus-community.github.io/helm-charts || true
	@helm repo update
	@echo "Installing Kube Prometheus Stack v$(KUBE_PROMETHEUS_STACK_VERSION)..."
	@helm upgrade --install qa-prometheus prometheus-community/kube-prometheus-stack \
		--version "$(KUBE_PROMETHEUS_STACK_VERSION)" \
		--namespace qa-monitoring \
		--values "$(VALUES_FILE_PREFIX)"/prometheus-values.yaml \
		--wait \
		--timeout 15m

.PHONY: prometheus-ui
prometheus-ui: ## Access Prometheus UI via port-forward
	@echo "Forwarding Prometheus UI to http://localhost:9090"
	@echo "Press Ctrl+C to stop."
	@kubectl port-forward svc/qa-prometheus-kube-prometh-prometheus 9090:9090 --namespace qa-monitoring

.PHONY: grafana-ui
grafana-ui: ## Access Grafana UI via port-forward
	@echo "Forwarding Grafana UI to http://localhost:3000"
	@echo "Login with admin/adminpwd. Press Ctrl+C to stop."
	@kubectl port-forward svc/qa-prometheus-grafana 3000:80 --namespace qa-monitoring

################################################################################
# PostgreSQL Deployment Targets
# Targets for deploying and managing PostgreSQL cluster per app service in local dev and QA cluster
# Namespace is auto-detected from the current kubectl context
################################################################################

CLOUDNATIVE_PG_VERSION := 1.27.0
PG_SVC_USER_PWD ?= pgsvcpwd

.PHONY: postgres-svc-secret-create
postgres-svc-secret-create: _env_check ## Create/update secret for DB service user. Usage: make postgres-svc-secret-create SVC_NAME=<svc-name> PG_SVC_USER_PWD=<user-pwd>
	@if [ -z "$$SVC_NAME" ]; then \
		echo "Argument 'SVC_NAME' is required!"; \
		exit 1; \
	fi
	@echo "Ensuring namespace $(DATA_NS) exists..."
	@kubectl create namespace "$(DATA_NS)" --dry-run=client -o yaml | kubectl apply -f -
	@kubectl config set-context --current --namespace="$(DATA_NS)"

	@echo "Creating secret with user creds for '$(SVC_NAME)svc' database in PostgreSQL..."
	# username value should match spec.bootstrap.initdb.owner value from `env/<env-name>/postgres-<service-name>svc.yaml` manifest
	@kubectl create secret generic "$(ENV_NAME)"-postgres-"$(SVC_NAME)"svc-creds \
	    --namespace "$(DATA_NS)" \
	    --type kubernetes.io/basic-auth \
	    --from-literal=username="$(SVC_NAME)"svc \
	    --from-literal=password="$(PG_SVC_USER_PWD)" \
		--dry-run=client -o yaml | kubectl apply -f -
	@echo "Secret '"$(ENV_NAME)"-postgres-"$(SVC_NAME)"svc-creds' successfully created/updated in namespace $(DATA_NS)"

.PHONY: postgres-operator-deploy
postgres-operator-deploy: _env_check ## Install CloudNativePG operator
	@echo "Deploying CloudNativePG operator v$(CLOUDNATIVE_PG_VERSION) into `cnpg-system` namespace..."
	@kubectl apply --server-side -f https://github.com/cloudnative-pg/cloudnative-pg/releases/download/v$(CLOUDNATIVE_PG_VERSION)/cnpg-"$(CLOUDNATIVE_PG_VERSION)".yaml

.PHONY: postgres-svc-deploy
postgres-svc-deploy: _env_check ## Deploy PostgresSQL cluster for service. Usage: make postgres-svc-deploy SVC_NAME=<svc-name>
	@if [ -z "$$SVC_NAME" ]; then \
		echo "Argument 'SVC_NAME' is required!"; \
		exit 1; \
	fi
	@echo "Deploying PostgreSQL cluster for '$(SVC_NAME)svc' into '$(DATA_NS)' namespace...";
	@kubectl apply --server-side -n "$(DATA_NS)" -f "$(VALUES_FILE_PREFIX)"/postgres-"$(SVC_NAME)"svc.yaml;

.PHONY: postgres-svc-status
postgres-svc-status: _env_check ## Show PostgreSQL cluster/resource status for service. Usage: make postgres-svc-status SVC_NAME=<svc-name>
	@if [ -z "$$SVC_NAME" ]; then \
		echo "Argument 'SVC_NAME' is required!"; \
		exit 1; \
	fi
	@echo "Status for namespace: $(DATA_NS)"
	@kubectl cnpg status "$(ENV_NAME)-postgres-$(SVC_NAME)svc" || true

.PHONY: postgres-svc-purge
postgres-svc-purge: _env_check ## Uninstall cluster and permanently remove related secrets. Usage: make postgres-svc-purge SVC_NAME=<svc-name>
	@echo "Removing PostgreSQL cluster '$(ENV_NAME)-postgres-$(SVC_NAME)svc' in namespace '$(DATA_NS)'..."
	@kubectl delete cluster.postgresql.cnpg.io -n "$(DATA_NS)" $(ENV_NAME)-postgres-"$(SVC_NAME)"svc || true
	@echo "\nDeleting PostgreSQL-related user creds secret for '$(SVC_NAME)svc' in namespace '$(DATA_NS)'..."
	@kubectl delete secret -n "$(DATA_NS)" $(ENV_NAME)-postgres-$(SVC_NAME)svc-creds || true

################################################################################
# Elastic Cloud Deployment Targets
# Targets for deploying and managing Elastic Cloud in local dev and QA cluster
# Namespace and environment auto-detected from current kubectl context
################################################################################

ECK_OPERATOR_VERSION := 3.1.0

.PHONY: eck-operator-deploy
eck-operator-deploy: _env_check ## Install Elastic Cloud on Kubernetes (ECK) Operator
	@echo "Installing ECK operator v$(ECK_OPERATOR_VERSION) into 'elastic-system' namespace..."
	@kubectl create -f https://download.elastic.co/downloads/eck/$(ECK_OPERATOR_VERSION)/crds.yaml || true
	@kubectl apply --server-side -f https://download.elastic.co/downloads/eck/$(ECK_OPERATOR_VERSION)/operator.yaml

.PHONY: elasticsearch-deploy
elasticsearch-deploy: _env_check ## Deploy Elasticsearch cluster
	@echo "Deploying Elasticsearch cluster into '$(DATA_NS)' namespace...";
	@kubectl apply --server-side -n "$(DATA_NS)" -f "$(VALUES_FILE_PREFIX)"/elasticsearch.yaml;

################################################################################
# MongoDB Deployment Targets
# Targets for deploying and managing MongoDB in local dev and QA cluster
# Namespace and environment auto-detected from current kubectl context
################################################################################

MONGODB_OPERATOR_VERSION := 0.13.0
MONGO_ROOT_USER_PWD ?= rootpwd

.PHONY: mongodb-operator-install
mongodb-operator-install: _env_check ## Install MongoDB Community Kubernetes Operator
	@helm repo add mongodb https://mongodb.github.io/helm-charts
	@helm repo update
	@echo "Installing MongoDB Community Kubernetes Operator v$(MONGODB_OPERATOR_VERSION)..."
	@helm upgrade --install mongodb-operator mongodb/community-operator \
		--version "$(MONGODB_OPERATOR_VERSION)" \
		--namespace $(DATA_NS) \
		--create-namespace

.PHONY: mongodb-root-secret-create
mongodb-root-secret-create: _env_check ## Create/update secret for MongoDB 'root' user. Usage: make mongodb-root-secret-create MONGO_ROOT_USER_PWD=<root-pwd>
	@echo "Ensuring namespace $(DATA_NS) exists..."
	@kubectl create namespace "$(DATA_NS)" --dry-run=client -o yaml | kubectl apply -f -
	@kubectl config set-context --current --namespace="$(DATA_NS)"

	@echo "Creating secret with user creds for 'admin' database in MongoDB..."
	@kubectl create secret generic "$(ENV_NAME)"-mongodb-root-creds \
	    --namespace "$(DATA_NS)" \
	    --type Opaque \
	    --from-literal=password="$(MONGO_ROOT_USER_PWD)" \
		--dry-run=client -o yaml | kubectl apply -f -
	@echo "Secret '"$(ENV_NAME)"-mongodb-root-creds' successfully created/updated in namespace $(DATA_NS)"

.PHONY: mongodb-deploy
mongodb-deploy: _env_check ## Deploy MongoDB cluster
	@echo "Deploying MongoDB cluster to '$(DATA_NS)' namespace...";
	@kubectl apply --server-side -n "$(DATA_NS)" -f "$(VALUES_FILE_PREFIX)"/mongodb.yaml;

.PHONY: mongodb-status
mongodb-status: _env_check ## Show MongoDB cluster status
	@echo "Status for namespace: $(DATA_NS)"
	@echo "\nMongoDB Pods:"
	@kubectl get pods -n "$(DATA_NS)" -l app="$(ENV_NAME)"-mongodb-svc || true
	@echo "\nMongoDB Services:"
	@kubectl get svc -n "$(DATA_NS)" | grep "$(ENV_NAME)"-mongodb || true
	@echo "\nMongoDB PersistentVolumeClaims:"
	@kubectl get pvc -n "$(DATA_NS)" | grep "$(ENV_NAME)"-mongodb || true
	@echo "\nStatefulSets:"
	@kubectl get statefulset -n "$(DATA_NS)" | grep "$(ENV_NAME)"-mongodb || true

.PHONY: mongodb-delete
mongodb-delete: _env_check ## Delete MongoDB cluster only
	@echo "Deleting MongoDB cluster '$(ENV_NAME)-mongodb' from namespace $(DATA_NS)..."
	@kubectl delete mongodbcommunity "$(ENV_NAME)"-mongodb -n "$(DATA_NS)" --ignore-not-found || true

.PHONY: mongodb-purge
mongodb-purge: mongodb-delete ## Delete MongoDB cluster and permanently remove related secrets and PVCs
	@echo "Deleting MongoDB-related secrets and PVCs in namespace $(DATA_NS)..."
	@kubectl delete secret -n "$(DATA_NS)" -l app="$(ENV_NAME)"-mongodb-svc || true
	@kubectl delete pvc -n "$(DATA_NS)" -l app="$(ENV_NAME)"-mongodb-svc || true
