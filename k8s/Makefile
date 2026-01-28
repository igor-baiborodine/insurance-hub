################################################################################
# Child Makefile common for local dev and QA insurance-hub cluster
################################################################################

.PHONY: kubeconfig-clear
kubeconfig-clear: ## Empty the ~/.kube/config file
	@echo "Clearing the ~/.kube/config file..."
	@> ~/.kube/config
	@echo "~/.kube/config file has been emptied."

.PHONY: local-dev-port-forward
local-dev-port-forward: ## Set up local port-forwarding for Kafka and PostgreSQL services in local-dev.
	@echo "Starting local-dev port-forwards. Use 'killall kubectl' or stop manually when done."
	@echo "  Kafka (local:9094 -> cluster:9094)..."
	@# For Kafka, we typically port-forward to the external bootstrap service that Strimzi creates.
	@kubectl port-forward svc/local-dev-kafka-broker-controller-0 9094:9094 -n local-dev-all > /dev/null 2>&1 &
	@echo "  PostgreSQL auth (local:5442 -> cluster:5432)..."
	@kubectl port-forward svc/local-dev-postgres-auth-rw 5442:5432 -n local-dev-all > /dev/null 2>&1 &
	@echo "  PostgreSQL document (local:5452 -> cluster:5432)..."
	@kubectl port-forward svc/local-dev-postgres-document-rw 5452:5432 -n local-dev-all > /dev/null 2>&1 &
	@echo "  PostgreSQL payment (local:5462 -> cluster:5432)..."
	@kubectl port-forward svc/local-dev-postgres-payment-rw 5462:5432 -n local-dev-all > /dev/null 2>&1 &
	@echo "  PostgreSQL policy (local:5472 -> cluster:5432)..."
	@kubectl port-forward svc/local-dev-postgres-policy-rw 5472:5432 -n local-dev-all > /dev/null 2>&1 &
	@echo "  PostgreSQL product (local:5482 -> cluster:5432)..."
	@kubectl port-forward svc/local-dev-postgres-product-rw 5482:5432 -n local-dev-all > /dev/null 2>&1 &
	@echo "  MinIO document (local:9001 -> cluster:9000)..."
	@kubectl port-forward svc/local-dev-minio-document-hl 9001:9000 -n local-dev-minio-document > /dev/null 2>&1 &
	@echo "  MinIO payment (local:9002 -> cluster:9000)..."
	@kubectl port-forward svc/local-dev-minio-payment-hl 9002:9000 -n local-dev-minio-payment > /dev/null 2>&1 &
	@echo "  jsreport (local:5488 -> cluster:5488)..."
	@kubectl port-forward svc/local-dev-jsreport 5488:5488 -n local-dev-all > /dev/null 2>&1 &
	@echo "All local-dev port-forwards initiated in the background."
	@echo "To stop them, you can use 'killall kubectl' or find their process IDs and kill them individually."

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
prereq-k8s-all: prereq-k8s-kubectl prereq-k8s-kubectx prereq-k8s-kubens prereq-k8s-helm prereq-k8s-lxd prereq-k8s-kind ## Install all Kubernetes prerequisites

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

.PHONY: prereq-minio-mc
prereq-minio-mc: ## Install MinIO mc command line utility on Ubuntu 24.04.2 LTS
	@echo "Installing MinIO mc (MinIO Client)..."
	@sudo apt-get update
	@sudo apt-get install -y curl
	@curl -fsSL https://dl.min.io/client/mc/release/linux-amd64/mc -o mc
	@chmod +x mc
	@sudo mv mc /usr/local/bin/
	@mc --version

.PHONY: prereq-data-all
prereq-data-all: prereq-db-psql prereq-db-mongosh prereq-minio-mc ## Install all database CLI tool prerequisites

.PHONY: prereq-all
prereq-all: prereq-k8s-all prereq-data-all ## Install all prerequisites

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
		MONITORING_NS := INVALID
    else
        ENV_NAME := INVALID
    endif
endif

OVERLAYS_FILE_PATH = overlays/$(ENV_NAME)

.PHONY: _env-check
_env-check: ## Ensure env name is valid and set based on the current context
	@if [ "$(ENV_NAME)" = "INVALID" ]; then \
		echo "ERROR: kubectl context '$$CUR_CTX' is not supported! Must be 'kind-local-dev-insurance-hub' or 'qa-insurance-hub'."; \
		exit 1; \
	else \
		echo "Using ENV_NAME=$(ENV_NAME), CUR_CTX=$(CUR_CTX)"; \
	fi

.PHONY: _data-ns-check
_data-ns-check: ## Ensure data namespace exists and set it as the current context
	@echo "Ensuring namespace $(DATA_NS) exists and is set as current..."
	@kubectl create namespace "$(DATA_NS)" --dry-run=client -o yaml | kubectl apply -f -
	@kubectl config set-context --current --namespace="$(DATA_NS)"

.PHONY: _monitoring-ns-check
_monitoring-ns-check: ## Ensure monitoring namespace exists and set it as the current context
	@CUR_CTX=$$(kubectl config current-context); \
	if [ "$$CUR_CTX" != "qa-insurance-hub" ]; then \
		echo "ERROR: Current context is '$$CUR_CTX' (expected: qa-insurance-hub for k3s). Aborting."; \
		exit 1; \
	fi
	@echo "Ensuring namespace $(MONITORING_NS) exists and is set as current..."
	@kubectl get namespace "$(MONITORING_NS)" >/dev/null 2>&1 || kubectl create namespace "$(MONITORING_NS)"
	@kubectl config set-context --current --namespace="$(MONITORING_NS)"

.PHONY: _svc-ns-check
_svc-ns-check: ## Ensure svc(service) namespace exists and set it as the current context
	@echo "Ensuring namespace $(SVC_NS) exists and is set as current..."
	@kubectl create namespace "$(SVC_NS)" --dry-run=client -o yaml | kubectl apply -f -
	@kubectl config set-context --current --namespace="$(SVC_NS)"

.PHONY: _svc-name-check
_svc-name-check: ## Ensure that the SVC_NAME argument is provided
	@if [ -z "$$SVC_NAME" ]; then \
		echo "ERROR: Argument 'SVC_NAME' is required!"; \
		exit 1; \
	fi

################################################################################
# Monitoring Deployment Targets
# Targes for deploying and managing monitoring infrastructure
# Current context must be "qa-insurance-hub"
################################################################################

KUBE_PROMETHEUS_STACK_VERSION := 79.3.0

.PHONY: prometheus-stack-install
prometheus-stack-install: _monitoring-ns-check ## Install Kube Prometheus Stack (Prometheus, Grafana, Alert Manager) in QA
	@helm repo add prometheus-community https://prometheus-community.github.io/helm-charts || true
	@helm repo update
	@echo "Installing Kube Prometheus Stack using Helm chart v$(KUBE_PROMETHEUS_STACK_VERSION) into $(MONITORING_NS) namespace..."
	@helm upgrade --install "$(ENV_NAME)"-prometheus prometheus-community/kube-prometheus-stack \
		--version "$(KUBE_PROMETHEUS_STACK_VERSION)" \
		--namespace $(MONITORING_NS) \
		--values "$(OVERLAYS_FILE_PATH)"/infra/prometheus/values.yaml
	@echo "✅ Kube Prometheus Stack deployment initiated. Check status with: make prometheus-stack-status"

.PHONY: prometheus-stack-uninstall
prometheus-stack-uninstall: _monitoring-ns-check ## Uninstall Kube Prometheus Stack
	@echo "Uninstalling Kube Prometheus Stack from '$(MONITORING_NS)' namespace..."
	@helm uninstall "$(ENV_NAME)"-prometheus --namespace $(MONITORING_NS) || true
	@echo "🗑 Kube Prometheus Stack uninstalled."

.PHONY: prometheus-stack-status
prometheus-stack-status: _monitoring-ns-check ## Show Kube Prometheus Stack deployment status
	@echo "Status for Prometheus Stack in namespace: $(MONITORING_NS)"
	@echo "\nPrometheus StatefulSet:"
	@kubectl get statefulset -n $(MONITORING_NS) -l app.kubernetes.io/name=prometheus || true
	@echo "\nPrometheus Services:"
	@kubectl get svc -n $(MONITORING_NS) -l app=kube-prometheus-stack-prometheus || true
	@echo "\nPrometheus Pods:"
	@kubectl get pods -n $(MONITORING_NS) -l app.kubernetes.io/name=prometheus
	@echo "\nAlertmanager StatefulSet:"
	@kubectl get statefulset -n $(MONITORING_NS) -l app.kubernetes.io/name=alertmanager || true
	@echo "\nGrafana Deployment:"
	@kubectl get deployment -n $(MONITORING_NS) -l app.kubernetes.io/name=grafana || true
	@echo "\nGrafana Service:"
	@kubectl get svc -n $(MONITORING_NS) -l app.kubernetes.io/name=grafana || true

.PHONY: prometheus-ui
prometheus-ui: ## Access Prometheus UI via port-forward
	@echo "Forwarding Prometheus UI to http://localhost:9090"
	@echo "Press Ctrl+C to stop."
	@kubectl port-forward svc/qa-prometheus-kube-prometh-prometheus 9090:9090 -n qa-monitoring

.PHONY: grafana-ui
grafana-ui: ## Access Grafana UI via port-forward
	@echo "Forwarding Grafana UI to http://localhost:3000"
	@echo "Login with admin/adminpwd. Press Ctrl+C to stop."
	@kubectl port-forward svc/qa-prometheus-grafana 3000:80 -n qa-monitoring

################################################################################
# PostgreSQL Deployment Targets
# Targets for deploying and managing PostgreSQL cluster per app service in local dev and QA cluster
# Namespace is auto-detected from the current kubectl context
################################################################################

CLOUDNATIVE_PG_VERSION := 1.27.0
PG_SVC_USER_PWD ?= pgSvcUserPwd

.PHONY: postgres-operator-deploy
postgres-operator-deploy: _env-check ## Deploy CloudNativePG operator
	@echo "Deploying CloudNativePG operator v$(CLOUDNATIVE_PG_VERSION) into 'cnpg-system' namespace..."
	@kubectl apply --server-side -f https://github.com/cloudnative-pg/cloudnative-pg/releases/download/v$(CLOUDNATIVE_PG_VERSION)/cnpg-"$(CLOUDNATIVE_PG_VERSION)".yaml
	@echo "✅ CloudNativePG operator deployment initiated."

.PHONY: postgres-operator-delete
postgres-operator-delete: _env-check ## Delete CloudNativePG operator with its namespace
	@echo "Deleting CloudNativePG operator from 'cnpg-system' namespace..."
	@kubectl delete -f https://github.com/cloudnative-pg/cloudnative-pg/releases/download/v$(CLOUDNATIVE_PG_VERSION)/cnpg-"$(CLOUDNATIVE_PG_VERSION)".yaml
	@echo "🗑 CloudNativePG operator deleted."

.PHONY: postgres-svc-secret-create
postgres-svc-secret-create: _env-check _data-ns-check _svc-ns-check _svc-name-check ## Create/update secret for DB service user. Usage: make postgres-svc-secret-create SVC_NAME=<svc-name> PG_SVC_USER_PWD=<user-pwd>
	@SECRET_NAME=$(ENV_NAME)-postgres-$(SVC_NAME)-user-creds ; \
	echo "Creating secret with user creds for '$(SVC_NAME)' database in PostgreSQL..." ; \
	kubectl create secret generic "$$SECRET_NAME" \
		--namespace "$(DATA_NS)" \
		--type kubernetes.io/basic-auth \
		--from-literal=username="$(SVC_NAME)" \
		--from-literal=password="$(PG_SVC_USER_PWD)" \
		--dry-run=client -o yaml | kubectl apply -f - ; \
	if [ "$(ENV_NAME)" = "qa" ]; then \
		echo "Copying user creds secret from $(DATA_NS) to $(SVC_NS)..." ; \
		kubectl get secret "$$SECRET_NAME" \
			-n "$(DATA_NS)" -o yaml | \
			sed "s/namespace: $(DATA_NS)/namespace: $(SVC_NS)/" | \
			kubectl apply -f - ; \
	fi ; \
	echo "Secret '$$SECRET_NAME' successfully created/updated in namespace $(DATA_NS)" ; \
	if [ "$(ENV_NAME)" = "qa" ]; then \
		echo "and copied to $(SVC_NS)" ; \
	fi

.PHONY: postgres-svc-deploy
postgres-svc-deploy: _env-check _data-ns-check _svc-name-check ## Deploy PostgresSQL cluster for service. Usage: make postgres-svc-deploy SVC_NAME=<svc-name>
	@echo "Deploying PostgreSQL cluster for '$(SVC_NAME)' into '$(DATA_NS)' namespace...";
	@kubectl kustomize "$(OVERLAYS_FILE_PATH)/infra/postgres/$(SVC_NAME)" | kubectl apply --server-side -f -;
	@echo "✅ Postgres cluster deployment for '$(SVC_NAME)' initiated. Check status with: make postgres-svc-status SVC_NAME=$(SVC_NAME)"

.PHONY: postgres-svc-status
postgres-svc-status: _env-check _svc-name-check ## Show PostgreSQL cluster/resource status for service. Usage: make postgres-svc-status SVC_NAME=<svc-name>
	@echo "Status for namespace: $(DATA_NS)"
	@kubectl cnpg status "$(ENV_NAME)-postgres-$(SVC_NAME)" || true

.PHONY: postgres-svc-delete
postgres-svc-delete: _env-check ## Delete PostgreSQL cluster for service. Usage: make postgres-svc-delete SVC_NAME=<svc-name>
	@echo "Deleting PostgreSQL cluster '$(ENV_NAME)-postgres-$(SVC_NAME)' from namespace '$(DATA_NS)'..."
	@kubectl delete cluster.postgresql.cnpg.io -n "$(DATA_NS)" $(ENV_NAME)-postgres-"$(SVC_NAME)" || true
	@echo "🗑 Postgres cluster for '$(SVC_NAME)' deleted."

.PHONY: postgres-svc-purge
postgres-svc-purge: _env-check _data-ns-check _svc-ns-check _svc-name-check ## Delete PostgreSQL cluster for service and its related secrets. Usage: make postgres-svc-purge SVC_NAME=<svc-name>
	@SECRET_NAME=$(ENV_NAME)-postgres-$(SVC_NAME)-user-creds ; \
	echo "Deleting PostgreSQL cluster '$(ENV_NAME)-postgres-$(SVC_NAME)' from namespace '$(DATA_NS)'..." ; \
	kubectl delete cluster.postgresql.cnpg.io -n "$(DATA_NS)" $(ENV_NAME)-postgres-"$(SVC_NAME)" || true ; \
	echo "\nDeleting PostgreSQL-related user creds secret for '$(SVC_NAME)' from namespace '$(DATA_NS)'..." ; \
	kubectl delete secret -n "$(DATA_NS)" "$$SECRET_NAME" || true ; \
	if [ "$(ENV_NAME)" = "qa" ]; then \
		echo "Deleting copied user creds secret from namespace '$(SVC_NS)'..." ; \
		kubectl delete secret -n "$(SVC_NS)" "$$SECRET_NAME" || true ; \
	fi ; \
	echo "🗑️ Postgres cluster for '$(SVC_NAME)' and its user creds deleted from $(DATA_NS)" ; \
	if [ "$(ENV_NAME)" = "qa" ]; then \
		echo "and $(SVC_NS)" ; \
	fi

################################################################################
# Elastic Cloud Deployment Targets
# Targets for deploying and managing Elastic Cloud in local dev and QA cluster
# Namespace and environment auto-detected from current kubectl context
################################################################################

ECK_OPERATOR_VERSION := 3.2.0

.PHONY: es-operator-deploy
es-operator-deploy: _env-check ## Deploy Elastic Cloud on Kubernetes (ECK) Operator
	@echo "Deploying ECK operator v$(ECK_OPERATOR_VERSION) into 'elastic-system' namespace..."
	@kubectl create -f https://download.elastic.co/downloads/eck/$(ECK_OPERATOR_VERSION)/crds.yaml || true
	@kubectl apply --server-side -f https://download.elastic.co/downloads/eck/$(ECK_OPERATOR_VERSION)/operator.yaml
	@echo "✅ ECK operator deployment initiated."
	
.PHONY: es-operator-delete
es-operator-delete: _env-check ## Delete Elastic Cloud on Kubernetes (ECK) Operator
	@echo "Deleting ECK operator v$(ECK_OPERATOR_VERSION) from 'elastic-system' namespace..."
	@kubectl delete -f https://download.elastic.co/downloads/eck/$(ECK_OPERATOR_VERSION)/operator.yaml --ignore-not-found
	@kubectl delete -f https://download.elastic.co/downloads/eck/$(ECK_OPERATOR_VERSION)/crds.yaml --ignore-not-found
	@echo "🗑 ECK operator and namespace 'elastic-system' deleted."

.PHONY: es-deploy
es-deploy: _env-check ## Deploy Elasticsearch cluster
	@echo "Deploying Elasticsearch cluster into '$(DATA_NS)' namespace...";
	@kubectl apply --server-side -n "$(DATA_NS)" -f "$(OVERLAYS_FILE_PATH)"/infra/elastic/elasticsearch.yaml;
	@echo "✅ Elasticsearch cluster deployment initiated. Check status with: make es-status"

.PHONY: es-status
es-status: _env-check ## Show Elasticsearch cluster status
	@echo "Status for namespace: $(DATA_NS)"
	@echo "\nElasticsearch Pods:"
	@kubectl get pods -n "$(DATA_NS)" -l elasticsearch.k8s.elastic.co/cluster-name=$(ENV_NAME)-elasticsearch || true
	@echo "\nElasticsearch Services:"
	@kubectl get svc -n "$(DATA_NS)" | grep "$(ENV_NAME)-elasticsearch" || true
	@echo "\nElasticsearch PersistentVolumeClaims:"
	@kubectl get pvc -n "$(DATA_NS)" | grep "$(ENV_NAME)-elasticsearch" || true
	@echo "\nElasticsearch StatefulSets:"
	@kubectl get statefulset -n "$(DATA_NS)" | grep "$(ENV_NAME)-elasticsearch" || true
	@echo "\nElasticsearch Cluster:"
	@kubectl get elasticsearch -n "$(DATA_NS)" "$(ENV_NAME)-elasticsearch" -o wide || true

.PHONY: es-delete
es-delete: _env-check ## Delete Elasticsearch cluster only
	@echo "Deleting Elasticsearch cluster '$(ENV_NAME)-elasticsearch' from namespace $(DATA_NS)..."
	@kubectl delete elasticsearch "$(ENV_NAME)-elasticsearch" -n "$(DATA_NS)" --ignore-not-found || true
	@echo "🗑 Elasticsearch cluster deleted."

.PHONY: es-purge
es-purge: es-delete ## Delete Elasticsearch cluster and its related secrets and PVCs
	@echo "Deleting Elasticsearch-related secrets and PVCs from namespace $(DATA_NS)..."
	@kubectl delete secret -n "$(DATA_NS)" -l elasticsearch.k8s.elastic.co/cluster-name=$(ENV_NAME)-elasticsearch || true
	@kubectl delete pvc -n "$(DATA_NS)" -l elasticsearch.k8s.elastic.co/cluster-name=$(ENV_NAME)-elasticsearch || true
	@echo "🗑 Elasticsearch-related secrets and PVCs deleted."

.PHONY: es-exporter-deploy
es-exporter-deploy: _env-check ## Deploy Prometheus Elasticsearch exporter for monitoring
	@echo "Deploying Elasticsearch exporter into '$(DATA_NS)' namespace..."
	@kubectl apply -f "$(OVERLAYS_FILE_PATH)"/infra/elastic/elasticsearch-exporter.yaml
	@echo "✅ Elasticsearch exporter deployment initiated."

.PHONY: es-exporter-delete
es-exporter-delete: _env-check ## Delete Prometheus Elasticsearch exporter
	@echo "Deleting Elasticsearch exporter from namespace $(DATA_NS)..."
	@kubectl delete -f "$(OVERLAYS_FILE_PATH)"/infra/elastic/elasticsearch-exporter.yaml --ignore-not-found || true
	@echo "🗑 Elasticsearch exporter deleted."

################################################################################
# Kafka Deployment Targets
# Targets for deploying and managing Kafka in local dev and QA cluster
# Namespace and environment auto-detected from current kubectl context
################################################################################

STRIMZI_OPERATOR_VERSION := 0.48.0

kafka-strimzi-operator-install: _env-check ## Install Strimzi Kafka operator
	@kubectl get namespace kafka-system >/dev/null 2>&1 || kubectl create namespace kafka-system
	@helm repo add strimzi https://strimzi.io/charts/ || true
	@helm repo update
	@echo "Installing Strimzi Kafka operator v$(STRIMZI_OPERATOR_VERSION) into 'kafka-system' namespace..."
	@helm upgrade --install strimzi-operator strimzi/strimzi-kafka-operator \
		--version $(STRIMZI_OPERATOR_VERSION) \
		--namespace kafka-system \
		--set watchNamespaces={"$(DATA_NS)"}
	@echo "✅ Strimzi Kafka operator installation initiated."

kafka-strimzi-operator-uninstall: _env-check ## Uninstall Strimzi Kafka operator
	@echo "Uninstalling Strimzi Kafka operator..."
	@helm uninstall strimzi-operator -n kafka-system || true
	@kubectl wait --for=delete namespace/kafka-system --timeout=60s 2>/dev/null || true
	@echo "🗑 Strimzi Kafka operator uninstalled and namespace 'kafka-system' deleted."

.PHONY: kafka-deploy
kafka-deploy: _env-check ## Deploy Kafka cluster
	@echo "Deploying Kafka cluster into '$(DATA_NS)' namespace..."
	@kubectl apply --server-side -n "$(DATA_NS)" -f "$(OVERLAYS_FILE_PATH)"/infra/kafka/resources.yaml
	@echo "✅ Kafka cluster deployment initiated. Check status with: make kafka-status"

.PHONY: kafka-status
kafka-status: _env-check ## Show Kafka cluster status
	@echo "Status for Kafka cluster in namespace: $(DATA_NS)"
	@echo "\nKafka Custom Resource:"
	@kubectl get kafka -n "$(DATA_NS)" "$(ENV_NAME)-kafka" -o wide || true
	@echo "\nKafka Pods:"
	@kubectl get pods -n "$(DATA_NS)" -l strimzi.io/cluster=$(ENV_NAME)-kafka || true
	@echo "\nKafka Services:"
	@kubectl get svc -n "$(DATA_NS)" -l strimzi.io/cluster=$(ENV_NAME)-kafka || true
	@echo "\nKafka PersistentVolumeClaims:"
	@kubectl get pvc -n "$(DATA_NS)" -l strimzi.io/cluster=$(ENV_NAME)-kafka || true
	@echo "\nKafka StrimziPodSets:"
	@kubectl get strimzipodsets -n "$(DATA_NS)"

.PHONY: kafka-delete
kafka-delete: _env-check ## Delete Kafka cluster only (preserves PVCs and StrimziPodSets)
	@echo "Deleting Kafka cluster '$(ENV_NAME)-kafka' from namespace $(DATA_NS)..."
	@kubectl delete kafka "$(ENV_NAME)-kafka" -n "$(DATA_NS)" --ignore-not-found || true
	@echo "⏳ Waiting 5s for Kafka resources to be cleaned up..."
	@sleep 5
	@echo "🗑️ Kafka cluster deleted."

.PHONY: kafka-purge
kafka-purge: kafka-delete ## Delete Kafka cluster and permanently remove PVCs, NodePools, and StrimziPodSets
	@echo "Deleting Kafka-related KafkaNodePools from namespace $(DATA_NS)..."
	@kubectl delete kafkanodepool -n "$(DATA_NS)" -l strimzi.io/cluster=$(ENV_NAME)-kafka || true

	@echo "Deleting Kafka-related StrimziPodSets from namespace $(DATA_NS)..."
	@kubectl delete strimzipodset -n "$(DATA_NS)" -l strimzi.io/cluster=$(ENV_NAME)-kafka || true

	@echo "Deleting Kafka-related PVCs from namespace $(DATA_NS)..."
	@kubectl delete pvc -n "$(DATA_NS)" -l strimzi.io/cluster=$(ENV_NAME)-kafka || true

	@echo "🗑️ Kafka-related NodePools, StrimziPodSets, and PVCs deleted."

.PHONY: kafka-topics-list
kafka-topics-list: _env-check ## List all Kafka topics
	@echo "Listing Kafka topics in cluster $(ENV_NAME)-kafka..."
	@kubectl get kafkatopic -n "$(DATA_NS)" || echo "No KafkaTopics found"

.PHONY: kafka-console-producer
kafka-console-producer: _env-check ## Access Kafka console producer. Usage: make kafka-console-producer TOPIC_NAME=<topic-name>
	@if [ -z "$(TOPIC_NAME)" ]; then \
		echo "ERROR: Argument 'TOPIC_NAME' is required! Usage: make kafka-console-producer TOPIC_NAME=<topic-name>"; \
		exit 1; \
	fi
	@echo "Starting Kafka console producer for topic: $(TOPIC_NAME)"
	@echo "Type messages and press Enter to send. Press Ctrl+C to exit."
	@kubectl run kafka-producer -ti --rm=true --restart=Never \
		--image=quay.io/strimzi/kafka:0.48.0-kafka-4.1.0 \
		-n "$(DATA_NS)" -- \
		bin/kafka-console-producer.sh \
		--bootstrap-server $(ENV_NAME)-kafka-kafka-bootstrap:9092 \
		--topic $(TOPIC_NAME)

.PHONY: kafka-console-consumer
kafka-console-consumer: _env-check ## Access Kafka console consumer. Usage: make kafka-console-consumer TOPIC_NAME=<topic-name>"
	@if [ -z "$(TOPIC_NAME)" ]; then \
		echo "ERROR: Argument 'TOPIC_NAME' is required! Usage: make kafka-console-consumer TOPIC_NAME=<topic-name>"; \
		exit 1; \
	fi
	@echo "Starting Kafka console consumer for topic: $(TOPIC_NAME)"
	@echo "Press Ctrl+C to exit."
	@kubectl run kafka-consumer -ti --rm=true --restart=Never \
		--image=quay.io/strimzi/kafka:0.48.0-kafka-4.1.0 \
		-n "$(DATA_NS)" -- \
		bin/kafka-console-consumer.sh \
		--bootstrap-server $(ENV_NAME)-kafka-kafka-bootstrap:9092 \
		--topic $(TOPIC_NAME) \
		--from-beginning

################################################################################
# MinIO Deployment Targets
# Targets for deploying and managing MinIO in local dev and QA cluster
# Namespace and environment auto-detected from current kubectl context
################################################################################

MINIO_OPERATOR_VERSION := 7.1.1
MINIO_CONSOLE_ACCESS_KEY ?= minioConsoleAccessKey
MINIO_CONSOLE_SECRET_KEY ?= minioConsoleSecretKey
MINIO_ROOT_USER ?= minioRootUser
MINIO_ROOT_USER_PWD ?= minioRootUserPwd

.PHONY: minio-operator-deploy
minio-operator-deploy: _env-check ## Deploy MinIO operator
	@echo "Deploying MinIO operator v$(MINIO_OPERATOR_VERSION) into 'minio-operator' namespace..."
	@kubectl kustomize "$(OVERLAYS_FILE_PATH)"/infra/minio/operator | kubectl apply --server-side -f -
	@echo "✅ MinIO operator deployment initiated."

.PHONY: minio-operator-delete
minio-operator-delete: _env-check ## Delete MinIO operator
	@echo "Deleting MinIO operator v$(MINIO_OPERATOR_VERSION) from 'minio-operator' namespace..."
	@kubectl delete -k "$(OVERLAYS_FILE_PATH)"/infra/minio/operator
	@echo "🗑 MinIO operator deleted."

.PHONY: minio-storage-user-secret-create
minio-storage-user-secret-create: _env-check _svc-name-check ## Create/update MinIO storage user credentials secret. Usage: make minio-storage-user-secret-create SVC_NAME=<svc-name> MINIO_CONSOLE_ACCESS_KEY=<access-key> MINIO_CONSOLE_SECRET_KEY=<secret-key>
	@NAMESPACE="$(ENV_NAME)-minio-$$SVC_NAME"; \
	SECRET_NAME="$$NAMESPACE-storage-user-creds"; \
	kubectl get namespace "$$NAMESPACE" >/dev/null 2>&1 || kubectl create namespace "$$NAMESPACE"; \
	echo "Creating MinIO Console credentials secret in namespace $$NAMESPACE..."; \
	kubectl create secret generic "$$SECRET_NAME" \
	   --namespace "$$NAMESPACE" \
	   --type Opaque \
	   --from-literal=CONSOLE_ACCESS_KEY="$(MINIO_CONSOLE_ACCESS_KEY)" \
	   --from-literal=CONSOLE_SECRET_KEY="$(MINIO_CONSOLE_SECRET_KEY)" \
	   --dry-run=client -o yaml | kubectl apply -f -; \
	echo "Secret '$$SECRET_NAME' successfully created/updated in namespace $$NAMESPACE"

.PHONY: minio-storage-config-secret-create
minio-storage-config-secret-create: _env-check _svc-name-check ## Create/update MinIO storage configuration secret. Usage: make minio-storage-config-secret-create SVC_NAME=<svc-name> MINIO_ROOT_USER=<root-user> MINIO_ROOT_USER_PWD=<root-password>
	@NAMESPACE="$(ENV_NAME)-minio-$$SVC_NAME"; \
	SECRET_NAME="$$NAMESPACE-storage-config"; \
	kubectl get namespace "$$NAMESPACE" >/dev/null 2>&1 || kubectl create namespace "$$NAMESPACE"; \
	echo "Creating MinIO storage configuration secret in namespace $$NAMESPACE..."; \
	printf "export MINIO_ROOT_USER=\"$(MINIO_ROOT_USER)\"\nexport MINIO_ROOT_PASSWORD=\"$(MINIO_ROOT_USER_PWD)\"\nexport MINIO_PROMETHEUS_AUTH_TYPE=\"public\"" | \
	kubectl create secret generic "$$SECRET_NAME" \
	   --namespace "$$NAMESPACE" \
	   --type Opaque \
	   --from-file=config.env=/dev/stdin \
	   --dry-run=client -o yaml | kubectl apply -f -; \
	echo "Secret '$$SECRET_NAME' successfully created/updated in namespace $$NAMESPACE"

.PHONY: minio-tenant-deploy
minio-tenant-deploy: _env-check _svc-name-check ## Deploy MinIO tenant for service. Usage: make minio-tenant-deploy SVC_NAME=<svc-name>
	@NAMESPACE="$(ENV_NAME)-minio-$$SVC_NAME"; \
	kubectl get namespace "$$NAMESPACE" >/dev/null 2>&1 || kubectl create namespace "$$NAMESPACE"; \
	echo "Deploying MinIO tenant for '$$SVC_NAME' into '$$NAMESPACE' namespace..."; \
	kubectl kustomize "$(OVERLAYS_FILE_PATH)/infra/minio/$$SVC_NAME" | kubectl apply --server-side -f -; \
	echo "✅ MinIO cluster for '$$SVC_NAME' deployment initiated. Check status with: make minio-tenant-status SVC_NAME=$$SVC_NAME"

.PHONY: minio-tenant-status
minio-tenant-status: _env-check _svc-name-check ## Show MinIO tenant status for service. Usage: make minio-tenant-status SVC_NAME=<svc-name>
	@NAMESPACE="$(ENV_NAME)-minio-$$SVC_NAME"; \
	TENANT_NAME="$$NAMESPACE"; \
	echo "Status for MinIO tenant in namespace: $$NAMESPACE"; \
	echo "\nMinIO Tenant:"; \
	kubectl get tenant "$$TENANT_NAME" -n "$$NAMESPACE" -o wide || true; \
	echo "\nMinIO Pods:"; \
	kubectl get pods -n "$$NAMESPACE" || true; \
	echo "\nMinIO Services:"; \
	kubectl get svc -n "$$NAMESPACE" || true; \
	echo "\nMinIO PersistentVolumeClaims:"; \
	kubectl get pvc -n "$$NAMESPACE" || true; \
	echo "\nMinIO ServiceMonitors:"; \
	kubectl get servicemonitor -n "$$NAMESPACE" || true

.PHONY: minio-console-ui
minio-console-ui: _env-check _svc-name-check ## Access MinIO Console UI via port-forward. Usage: make minio-console-ui SVC_NAME=<svc-name>
	@NAMESPACE="$(ENV_NAME)-minio-$(SVC_NAME)"; \
	echo "Forwarding MinIO Console for '$(SVC_NAME)' to http://localhost:9090"; \
	echo "Press Ctrl+C to stop."; \
	kubectl port-forward svc/$$NAMESPACE-console 9090:9090 -n $$NAMESPACE

.PHONY: minio-tenant-delete
minio-tenant-delete: _env-check _svc-name-check ## Delete MinIO tenant for service. Usage: make minio-tenant-delete SVC_NAME=<svc-name>
	@NAMESPACE="$(ENV_NAME)-minio-$$SVC_NAME"; \
	TENANT_NAME="$$NAMESPACE"; \
	echo "Deleting MinIO tenant '$$TENANT_NAME' from namespace '$$NAMESPACE'..."; \
	kubectl delete tenant "$$TENANT_NAME" -n "$$NAMESPACE" --ignore-not-found || true; \
	kubectl delete servicemonitor "$(ENV_NAME)-minio-$$SVC_NAME-service-monitor" -n "$$NAMESPACE" --ignore-not-found || true

.PHONY: minio-tenant-purge
minio-tenant-purge: minio-tenant-delete _svc-name-check ## Delete MinIO tenant and related secrets/PVCs. Usage: make minio-tenant-purge SVC_NAME=<svc-name>
	@NAMESPACE="$(ENV_NAME)-minio-$$SVC_NAME"; \
	echo "Deleting MinIO-related secrets and PVCs from namespace $$NAMESPACE..."; \
	kubectl delete secret -n "$$NAMESPACE" --all || true; \
	kubectl delete pvc -n "$$NAMESPACE" --all || true; \
	echo "Deleting namespace $$NAMESPACE..."; \
	kubectl delete namespace "$$NAMESPACE" --ignore-not-found || true

################################################################################
# MinIO Management Targets
# Targets for managing MinIO buckets and policies for services.
# Namespace and environment auto-detected from current kubectl context
################################################################################

MINIO_SVC_ACCESS_KEY ?= minioSvcAccessKey
MINIO_SVC_SECRET_KEY ?= minioSvcSecretKey

.PHONY: minio-svc-bucket-create
minio-svc-bucket-create: _env-check _svc-name-check ## Create a MinIO bucket for service. Usage: make minio-svc-bucket-create SVC_NAME=<svc-name> BUCKET_NAME=<bucket-name>
	@if [ -z "$(BUCKET_NAME)" ]; then \
		echo "ERROR: Argument 'BUCKET_NAME' is required! Usage: make minio-svc-bucket-create SVC_NAME=<svc-name> BUCKET_NAME=<bucket-name>"; \
		exit 1; \
	fi
	@bash apps/scripts/minio-svc-bucket-create.sh "$(ENV_NAME)" "$(SVC_NAME)" "$(BUCKET_NAME)"

.PHONY: minio-svc-user-secret-create
minio-svc-user-secret-create: _env-check _svc-name-check _svc-ns-check ## Create/update MinIO credentials secret for service account. Usage: make minio-svc-user-secret-create SVC_NAME=<svc-name> MINIO_SVC_ACCESS_KEY=<access-key> MINIO_SVC_SECRET_KEY=<secret-key>
	@NAMESPACE="$(ENV_NAME)-minio-$$SVC_NAME"; \
	SECRET_NAME="$$NAMESPACE-svc-user-creds"; \
	kubectl get namespace "$$NAMESPACE" >/dev/null 2>&1 || kubectl create namespace "$$NAMESPACE"; \
	echo "Creating MinIO service credentials secret in namespace $$NAMESPACE..."; \
	kubectl create secret generic "$$SECRET_NAME" \
	   --namespace "$$NAMESPACE" \
	   --type Opaque \
	   --from-literal=MINIO_SVC_ACCESS_KEY="$(MINIO_SVC_ACCESS_KEY)" \
	   --from-literal=MINIO_SVC_SECRET_KEY="$(MINIO_SVC_SECRET_KEY)" \
	   --dry-run=client -o yaml | kubectl apply -f -; \
	echo "Copying MinIO service credentials secret from $$NAMESPACE to $(SVC_NS)..."; \
	kubectl get secret "$$SECRET_NAME" \
	   -n "$$NAMESPACE" -o yaml | \
	   sed "s/namespace: $$NAMESPACE/namespace: $(SVC_NS)/" | \
	   kubectl apply -f -; \
	echo "Secret '$$SECRET_NAME' successfully created/updated in namespace $$NAMESPACE and copied to $(SVC_NS)"

.PHONY: minio-svc-user-with-policy-create
minio-svc-user-with-policy-create: _env-check _svc-name-check ## Create a MinIO service account and apply an S3 policy. Usage: make minio-svc-user-with-policy-create SVC_NAME=<svc-name> POLICY_FILE=<path/to/policy.json>
	@if [ -z "$(POLICY_FILE)" ]; then \
		echo "ERROR: Argument 'POLICY_FILE' is required! Usage: make minio-svc-user-with-policy-create SVC_NAME=<svc-name> POLICY_FILE=<path/to/policy.json>"; \
		exit 1; \
	fi
	@bash apps/scripts/minio-svc-user-with-policy-create.sh "$(SVC_NAME)" "$(ENV_NAME)" "$(POLICY_FILE)"

################################################################################
# MongoDB Deployment Targets
# Targets for deploying and managing MongoDB in local dev and QA cluster
# Namespace and environment auto-detected from current kubectl context
################################################################################

MONGODB_OPERATOR_VERSION := 0.13.0
MONGO_ROOT_USER_PWD ?= mongoRootUserPwd
MONGO_PRODUCT_USER_PWD ?= mongoProductUserPwd

.PHONY: mongodb-operator-install
mongodb-operator-install: _env-check ## Install MongoDB Community Operator
	@kubectl get namespace "$(DATA_NS)" >/dev/null 2>&1 || kubectl create namespace "$(DATA_NS)"
	@helm repo add mongodb https://mongodb.github.io/helm-charts
	@helm repo update
	@echo "Installing MongoDB operator v$(MONGODB_OPERATOR_VERSION) into $(DATA_NS) namespace..."
	@helm upgrade --install mongodb-operator mongodb/community-operator \
		--version "$(MONGODB_OPERATOR_VERSION)" \
		--namespace "$(DATA_NS)"
	@echo "✅ MongoDB operator installation initiated."

.PHONY: mongodb-operator-uninstall
mongodb-operator-uninstall: _env-check ## Delete MongoDB Community Operator
	@echo "Uninstalling MongoDB operator from namespace $(DATA_NS)..."
	@helm uninstall mongodb-operator -n "$(DATA_NS)" || true
	@echo "🗑 MongoDB operator uninstalled."

.PHONY: mongodb-root-user-secret-create
mongodb-root-user-secret-create: _env-check _data-ns-check ## Create/update secret for MongoDB 'root' user. Usage: make mongodb-root-secret-create MONGO_ROOT_USER_PWD=<root-pwd>
	@echo "Creating secret with 'root' user creds for 'admin' database in MongoDB..."
	@kubectl create secret generic "$(ENV_NAME)"-mongodb-root-user-creds \
	    --namespace "$(DATA_NS)" \
	    --type Opaque \
	    --from-literal=username="root" \
	    --from-literal=password="$(MONGO_ROOT_USER_PWD)" \
		--dry-run=client -o yaml | kubectl apply -f -
	@echo "Secret '"$(ENV_NAME)"-mongodb-root-user-creds' successfully created/updated in namespace $(DATA_NS)"

.PHONY: mongodb-product-user-secret-create
mongodb-product-user-secret-create: _env-check _data-ns-check _svc-ns-check ## Create/update secret for MongoDB product-service user. Usage: make mongodb-product-user-secret-create MONGO_PRODUCT_USER_PWD=<user-pwd>
	@SECRET_NAME=$(ENV_NAME)-mongodb-product-user-creds ; \
	echo "Creating secret with 'product' user creds for 'products-demo' database in MongoDB..." ; \
	kubectl create secret generic "$$SECRET_NAME" \
		--namespace "$(DATA_NS)" \
		--type Opaque \
		--from-literal=username="product" \
		--from-literal=password="$(MONGO_PRODUCT_USER_PWD)" \
		--from-literal=database="products-demo" \
		--dry-run=client -o yaml | kubectl apply -f - ; \
	if [ "$(ENV_NAME)" = "qa" ]; then \
		echo "Copying product user creds secret from $(DATA_NS) to $(SVC_NS)..." ; \
		kubectl get secret "$$SECRET_NAME" \
			-n "$(DATA_NS)" -o yaml | \
			sed "s/namespace: $(DATA_NS)/namespace: $(SVC_NS)/" | \
			kubectl apply -f - ; \
	fi ; \
	echo "Secret '$$SECRET_NAME' successfully created/updated in namespace $(DATA_NS)" ; \
	if [ "$(ENV_NAME)" = "qa" ]; then \
		echo "and copied to $(SVC_NS)" ; \
	fi

.PHONY: mongodb-deploy
mongodb-deploy: _env-check _data-ns-check ## Deploy MongoDB cluster
	@echo "Deploying MongoDB cluster to '$(DATA_NS)' namespace...";
	@kubectl apply --server-side -n "$(DATA_NS)" -f "$(OVERLAYS_FILE_PATH)"/infra/mongodb/mongodbcommunity.yaml;
	@echo "✅ MongoDB cluster deployment initiated. Check status with: make mongodb-status"

.PHONY: mongodb-status
mongodb-status: _env-check ## Show MongoDB cluster status
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
mongodb-delete: _env-check ## Delete MongoDB cluster only
	@echo "Deleting MongoDB cluster '$(ENV_NAME)-mongodb' from namespace $(DATA_NS)..."
	@kubectl delete mongodbcommunity "$(ENV_NAME)"-mongodb -n "$(DATA_NS)" --ignore-not-found || true
	@echo "🗑 MongoDB cluster deleted."

.PHONY: mongodb-purge
mongodb-purge: mongodb-delete ## Delete MongoDB cluster and permanently remove related secrets and PVCs
	@echo "Deleting MongoDB-related secrets and PVCs from namespace $(DATA_NS)..."
	@kubectl delete secret -n "$(DATA_NS)" $(ENV_NAME)-mongodb-root-user-creds || true
	@kubectl delete secret -n "$(DATA_NS)" $(ENV_NAME)-mongodb-product-user-creds || true
	@kubectl delete pvc -n "$(DATA_NS)" -l app="$(ENV_NAME)"-mongodb-svc || true
	@echo "🗑 MongoDB-related secrets and PVCs deleted."

################################################################################
# JSReport Deployment Targets
# Targets for deploying and managing JSReport in local dev and QA cluster
# Namespace and environment auto-detected from current kubectl context
################################################################################

.PHONY: jsreport-deploy
jsreport-deploy: _env-check ## Deploy JSReport instance
	@kubectl get namespace "$(SVC_NS)" >/dev/null 2>&1 || kubectl create namespace "$(SVC_NS)"
	@echo "Deploying JSReport cluster into '$(SVC_NS)' namespace..."
	@kubectl kustomize "$(OVERLAYS_FILE_PATH)/svc/jsreport" | kubectl apply --server-side -f -
	@echo "✅ JSReport deployment initiated. Check status with: make jsreport-status"

.PHONY: jsreport-status
jsreport-status: _env-check ## Show JSReport status (pod, service, PVC only)
	@echo "Status for JSReport in namespace: $(SVC_NS)"
	@echo "\nJSReport Pod:"
	@kubectl get pod -n "$(SVC_NS)" -l app=jsreport || true
	@echo "\nJSReport Service:"
	@kubectl get svc "$(ENV_NAME)"-jsreport -n "$(SVC_NS)" || true
	@echo "\nJSReport PVC:"
	@kubectl get pvc "$(ENV_NAME)"-jsreport-cache-pvc -n "$(SVC_NS)" || true

.PHONY: jsreport-delete
jsreport-delete: _env-check ## Delete JSReport deployment
	@echo "Deleting JSReport resources from namespace $(SVC_NS)..."
	@kubectl delete -k "$(OVERLAYS_FILE_PATH)/svc/jsreport" -n "$(SVC_NS)" --ignore-not-found || true
	@echo "⏳ Waiting 5s for JSReport resources to be cleaned up..."
	@sleep 5

.PHONY: jsreport-purge
jsreport-purge: jsreport-delete _env-check ## Purge JSReport PVC
	@echo "Deleting JSReport-related PVC from namespace $(SVC_NS)..."
	@kubectl delete pvc "$(ENV_NAME)"-jsreport-cache-pvc -n "$(SVC_NS)" --ignore-not-found || true
	@echo "🗑 JSReport cluster and its persistent data deleted."

.PHONY: jsreport-ui
jsreport-ui: _env-check ## Access Grafana UI via port-forward
	@echo "Forwarding JSReport UI to http://localhost:5488"
	@echo "Press Ctrl+C to stop."
	@kubectl port-forward svc/"$(ENV_NAME)"-jsreport 5488:5488 -n $(SVC_NS)

################################################################################
# Zipkin Deployment Targets
# Targets for deploying and managing Zipkin cluster per app service in QA cluster
# Namespace is auto-detected from the current kubectl context
################################################################################

ZIPKIN_VERSION := 0.4.0
ZIPKIN_ES_ROLE := zipkin_role
ZIPKIN_ES_USER := zipkin_user
ZIPKIN_ES_USER_PWD := zipkinEsUserPwd

.PHONY: zipkin-es-user-secret-create
zipkin-es-user-secret-create: _env-check _monitoring-ns-check ## Create/update secret for Elasticsearch's Zipkin user
	@echo "Creating secret with user creds for Zipkin user in Elasticsearch..."
	@kubectl create secret generic "$(ENV_NAME)"-zipkin-es-user-creds \
		--namespace "$(MONITORING_NS)" \
		--type Opaque \
		--from-literal=username="$(ZIPKIN_ES_USER)" \
		--from-literal=password="$(ZIPKIN_ES_USER_PWD)" \
		--dry-run=client -o yaml | kubectl apply -f -
	@echo "Secret '"$(ENV_NAME)"-zipkin-es-user-creds' successfully created/updated in namespace $(MONITORING_NS)"

.PHONY: zipkin-es-user-create
zipkin-es-user-create: _env-check _monitoring-ns-check ## Create role and user for Zipkin in Elasticsearch in QA cluster.
	@echo "Creating Elasticsearch role and user for Zipkin in QA cluster..."
	@# Retrieve credentials from secrets
	@ELASTIC_USER_PWD=$$(kubectl get secret "$(ENV_NAME)"-elasticsearch-es-elastic-user -n $(DATA_NS) -o go-template='{{index .data "elastic"}}' | base64 -d); \
	ZIPKIN_ES_USER_PWD=$$(kubectl get secret "$(ENV_NAME)"-zipkin-es-user-creds -n $(MONITORING_NS) -o go-template='{{index .data "password"}}' | base64 -d); \
	ES_URL="https://$(ENV_NAME)-elasticsearch-es-http.$(DATA_NS).svc.cluster.local:9200"; \
	\
	echo "Creating Elasticsearch Zipkin role via test pod..."; \
	kubectl run zipkin-es-setup --image=curlimages/curl:latest --rm -i --tty -n $(DATA_NS) --restart=Never -- \
	  sh -c "curl -u 'elastic:$$ELASTIC_USER_PWD' -k -X POST '$$ES_URL/_security/role/$(ZIPKIN_ES_ROLE)' \
	  -H 'Content-Type: application/json' \
	  -d '{\"cluster\": [\"monitor\",\"manage_index_templates\"],\"indices\":[{\"names\":[\"zipkin*\"],\"privileges\":[\"create_index\",\"write\",\"read\",\"manage\"]}]}'&& \
	  curl -u 'elastic:$$ELASTIC_USER_PWD' -k -X GET '$$ES_URL/_security/role/$(ZIPKIN_ES_ROLE)'"; \
	echo ""; \
	\
	echo "Creating Elasticsearch Zipkin user via test pod..."; \
	kubectl run zipkin-es-setup --image=curlimages/curl:latest --rm -i --tty -n $(DATA_NS) --restart=Never -- \
	  sh -c "curl -u 'elastic:$$ELASTIC_USER_PWD' -k -X POST '$$ES_URL/_security/user/$(ZIPKIN_ES_USER)' \
	  -H 'Content-Type: application/json' \
	  -d '{\"password\":\"$$ZIPKIN_ES_USER_PWD\",\"roles\":[\"$(ZIPKIN_ES_ROLE)\"],\"full_name\":\"Zipkin Service Account\",\"email\":\"zipkin@insurance-hub.com\"}'&& \
	  curl -u 'elastic:$$ELASTIC_USER_PWD' -k -X GET '$$ES_URL/_security/user/$(ZIPKIN_ES_USER)'"; \
	echo ""; \
	\
	echo "Elasticsearch Zipkin role and user creation completed."

.PHONY: zipkin-install
zipkin-install: _env-check _monitoring-ns-check ## Install Zipkin in QA cluster
	@echo "Installing Zipkin using Helm chart v$(ZIPKIN_VERSION) into $(MONITORING_NS) namespace..."
	@helm repo add zipkin https://zipkin.io/zipkin-helm
	@helm repo update
	@helm upgrade --install $(ENV_NAME)-zipkin zipkin/zipkin \
	   --version $(ZIPKIN_VERSION) \
	   --values "$(OVERLAYS_FILE_PATH)"/infra/zipkin/values.yaml \
	   --namespace $(MONITORING_NS)
	@echo "Disabling Zipkin deployment readiness probe..."
	@kubectl patch deployment $(ENV_NAME)-zipkin -n $(MONITORING_NS) --type=json -p='[{"op": "remove", "path": "/spec/template/spec/containers/0/readinessProbe"}]'
	@echo "Waiting for Zipkin rollout after disabling probe..."
	@kubectl rollout status deployment/$(ENV_NAME)-zipkin -n $(MONITORING_NS) --timeout=2m
	@echo "✅ Zipkin installation initiated. Check status with: make zipkin-status"

.PHONY: zipkin-uninstall
zipkin-uninstall: _env-check _monitoring-ns-check ## Uninstall Zipkin from QA cluster
	@echo "Uninstalling Zipkin from namespace '$(MONITORING_NS)'..."
	@helm uninstall $(ENV_NAME)-zipkin -n $(MONITORING_NS) || true
	@echo "🗑 Zipkin uninstalled."

.PHONY: zipkin-status
zipkin-status: _env-check _monitoring-ns-check ## Show Zipkin deployment status in QA cluster
	@echo "Status for Zipkin in namespace: $(MONITORING_NS)"
	@echo "\nZipkin Deployment:"
	@kubectl get deployment $(ENV_NAME)-zipkin -n $(MONITORING_NS) -o wide || true
	@echo "\nZipkin Pods:"
	@kubectl get pods -n $(MONITORING_NS) -l app.kubernetes.io/name=zipkin || true
	@echo "\nZipkin Service:"
	@kubectl get svc $(ENV_NAME)-zipkin -n $(MONITORING_NS) || true
	@echo "\nZipkin Endpoint Slice:"
	@kubectl get endpointslice -l kubernetes.io/service-name=$(ENV_NAME)-zipkin -n $(MONITORING_NS) || true

.PHONY: zipkin-ui
zipkin-ui: ## Access Zipkin UI via port-forward
	@echo "Forwarding Zipkin UI to http://localhost:9411"
	@echo "Press Ctrl+C to stop."
	@kubectl port-forward svc/$(ENV_NAME)-zipkin 9411:9411 -n $(MONITORING_NS)

################################################################################
# Services Deployment Targets
# Targets for deploying and managing Insurance Hub services and front-end apps
# in local dev and QA cluster
# Namespace is auto-detected from the current kubectl context
################################################################################

.PHONY: svc-image-local-dev-load
svc-image-local-dev-load: _svc-name-check ## Loads a locally built Docker image into the local-dev cluster. Usage: make svc-image-local-dev-load SVC_NAME=<svc-name>
	@if [ "$(ENV_NAME)" != "local-dev" ]; then \
		echo "ERROR: This target is only applicable for the 'local-dev' environment."; \
		exit 1; \
	fi
	@IMAGE_NAME="insurance-hub-$(SVC_NAME)-api-legacy:latest"; \
	if [ "$(SVC_NAME)" = "agent-portal-gateway" ] || [ "$(SVC_NAME)" = "web-vue" ]; then \
		IMAGE_NAME="insurance-hub-$(SVC_NAME)-legacy:latest"; \
	fi; \
	echo "Loading Docker image '$$IMAGE_NAME' into 'local-dev' cluster..."; \
	kind load docker-image --name local-dev-insurance-hub "$$IMAGE_NAME"; \
	echo "✅ Docker image loaded into 'local-dev' cluster."

.PHONY: svc-image-local-dev-unload
svc-image-local-dev-unload: _svc-name-check ## Unloads (removes) a Docker image from the local-dev Kind cluster nodes. Usage: make svc-image-local-dev-unload SVC_NAME=<svc-name>
	@if [ "$(ENV_NAME)" != "local-dev" ]; then \
		echo "ERROR: This target is only applicable for the 'local-dev' environment."; \
		exit 1; \
	fi
	@IMAGE_NAME="insurance-hub-$(SVC_NAME)-api-legacy:latest"; \
	if [ "$(SVC_NAME)" = "agent-portal-gateway" ] || [ "$(SVC_NAME)" = "web-vue" ]; then \
		IMAGE_NAME="insurance-hub-$(SVC_NAME)-legacy:latest"; \
	fi; \
	echo "Unloading Docker image '$$IMAGE_NAME' from 'local-dev' cluster nodes..."; \
	KIND_NODES=$$(docker ps --filter "label=io.x-k8s.kind.cluster=local-dev-insurance-hub" --format "{{.Names}}"); \
	for NODE in $$KIND_NODES; do \
		echo "  -> Removing from node $$NODE..."; \
		docker exec $$NODE ctr -n k8s.io images rm "docker.io/library/$$IMAGE_NAME" || true; \
	done; \
	echo "🗑 Docker image '$$IMAGE_NAME' unloaded from all Kind nodes."


.PHONY: svc-image-qa-load
svc-image-qa-load: _svc-name-check ## Loads a locally built Docker image into the QA LXD cluster nodes. Usage: make svc-image-qa-load SVC_NAME=<svc-name>
	@if [ "$(ENV_NAME)" != "qa" ]; then \
	   echo "ERROR: This target is only applicable for the 'qa' environment (LXD/K3s)."; \
	   exit 1; \
	fi
	@IMAGE_NAME="insurance-hub-$(SVC_NAME)-api-legacy:latest"; \
	if [ "$(SVC_NAME)" = "agent-portal-gateway" ] || [ "$(SVC_NAME)" = "web-vue" ]; then \
	   IMAGE_NAME="insurance-hub-$(SVC_NAME)-legacy:latest"; \
	fi; \
	echo "Saving Docker image '$$IMAGE_NAME' to a temporary file..."; \
	docker save "$$IMAGE_NAME" -o /tmp/$(SVC_NAME)-image.tar; \
	echo "Loading Docker image '$$IMAGE_NAME' into QA LXD cluster nodes..."; \
	LXD_NODES=$$(lxc list -c n --format csv | grep -E "^qa-(master|worker[0-9]+)" ); \
	if [ -z "$$LXD_NODES" ]; then \
	   echo "ERROR: No QA LXD nodes found. Is the LXD cluster running and named 'qa-master', 'qa-workerX'?"; \
	   rm /tmp/$(SVC_NAME)-image.tar; \
	   exit 1; \
	fi; \
	for NODE in $$LXD_NODES; do \
	   echo "  -> Processing node $$NODE..."; \
	   lxc file push /tmp/$(SVC_NAME)-image.tar $$NODE/tmp/$(SVC_NAME)-image.tar; \
	   lxc exec $$NODE -- /usr/local/bin/k3s ctr images import /tmp/$(SVC_NAME)-image.tar; \
	   lxc exec $$NODE -- rm /tmp/$(SVC_NAME)-image.tar; \
	   echo "  -> Image loaded on $$NODE."; \
	done; \
	rm /tmp/$(SVC_NAME)-image.tar; \
	echo "✅ Docker image '$$IMAGE_NAME' loaded into all QA LXD cluster nodes."

.PHONY: svc-deploy
svc-deploy: _env-check _svc-ns-check _svc-name-check ## Deploy a Java service or web app using Kustomize. Usage: make svc-deploy SVC_NAME=<svc-name>
	@SVC_NAME_LABEL="$(SVC_NAME)-api-legacy"; \
	if [ "$(SVC_NAME)" = "agent-portal-gateway" ] || [ "$(SVC_NAME)" = "web-vue" ]; then \
		SVC_NAME_LABEL="$(SVC_NAME)-legacy"; \
	fi; \
	echo "Deploying legacy service '$(SVC_NAME)' to '$(SVC_NS)' namespace..."; \
	kubectl apply -k "overlays/$(ENV_NAME)/svc/$(SVC_NAME)/legacy" -n "$(SVC_NS)"; \
	echo "✅ Service '$(SVC_NAME)' deployment initiated."; \
	echo "Check status with: kubectl get deploy,rs,po,svc,endpointslices -n $(SVC_NS) -l app.kubernetes.io/name=$$SVC_NAME_LABEL"

.PHONY: svc-delete
svc-delete: _env-check _svc-name-check ## Delete (purge) a deployed Java service or web app using Kustomize. Usage: make svc-delete SVC_NAME=<svc-name>
	@echo "Deleting legacy service '$(SVC_NAME)' from '$(SVC_NS)' namespace..."
	@kubectl delete -k "overlays/$(ENV_NAME)/svc/$(SVC_NAME)/legacy" -n "$(SVC_NS)" --ignore-not-found
	@echo "🗑 Service '$(SVC_NAME)' deleted."

.PHONY: svc-load-delete-deploy
svc-load-delete-deploy: _env-check _svc-ns-check _svc-name-check svc-image-local-dev-load svc-delete svc-deploy ## Load Docker image, delete and deploy a Java service or web app using Kustomize. Usage: make svc-load-delete-deploy SVC_NAME=<svc-name>
