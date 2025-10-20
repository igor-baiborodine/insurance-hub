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
		MONITORING_NS := $(LOCAL_DEV_NS)
    else
        ENV_NAME := INVALID
    endif
endif

VALUES_FILE_PREFIX = env/$(ENV_NAME)

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

.PHONY: _svc-name-check
_svc-name-check: ## Ensure that the SVC_NAME argument is provided 
	@if [ -z "$$SVC_NAME" ]; then \
		echo "Argument 'SVC_NAME' is required!"; \
		exit 1; \
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
	@echo "✅ Kube Prometheus Stack installed. Verifying pods..."
	@kubectl get pods -n qa-monitoring -l app.kubernetes.io/instance=qa-prometheus

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
postgres-svc-secret-create: _env-check _data-ns-check _svc-name-check ## Create/update secret for DB service user. Usage: make postgres-svc-secret-create SVC_NAME=<svc-name> PG_SVC_USER_PWD=<user-pwd>
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
postgres-operator-deploy: _env-check ## Install CloudNativePG operator
	@echo "Deploying CloudNativePG operator v$(CLOUDNATIVE_PG_VERSION) into `cnpg-system` namespace..."
	@kubectl apply --server-side -f https://github.com/cloudnative-pg/cloudnative-pg/releases/download/v$(CLOUDNATIVE_PG_VERSION)/cnpg-"$(CLOUDNATIVE_PG_VERSION)".yaml

.PHONY: postgres-svc-deploy
postgres-svc-deploy: _env-check _svc-name-check ## Deploy PostgresSQL cluster for service. Usage: make postgres-svc-deploy SVC_NAME=<svc-name>
	@echo "Deploying PostgreSQL cluster for '$(SVC_NAME)svc' into '$(DATA_NS)' namespace...";
	@kubectl apply --server-side -n "$(DATA_NS)" -f "$(VALUES_FILE_PREFIX)"/postgres-"$(SVC_NAME)"svc.yaml;

.PHONY: postgres-svc-status
postgres-svc-status: _env-check _svc-name-check ## Show PostgreSQL cluster/resource status for service. Usage: make postgres-svc-status SVC_NAME=<svc-name>
	@echo "Status for namespace: $(DATA_NS)"
	@kubectl cnpg status "$(ENV_NAME)-postgres-$(SVC_NAME)svc" || true

.PHONY: postgres-svc-purge
postgres-svc-purge: _env-check ## Uninstall cluster and permanently remove related secrets. Usage: make postgres-svc-purge SVC_NAME=<svc-name>
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
eck-operator-deploy: _env-check ## Install Elastic Cloud on Kubernetes (ECK) Operator
	@echo "Installing ECK operator v$(ECK_OPERATOR_VERSION) into 'elastic-system' namespace..."
	@kubectl create -f https://download.elastic.co/downloads/eck/$(ECK_OPERATOR_VERSION)/crds.yaml || true
	@kubectl apply --server-side -f https://download.elastic.co/downloads/eck/$(ECK_OPERATOR_VERSION)/operator.yaml

.PHONY: elasticsearch-deploy
elasticsearch-deploy: _env-check ## Deploy Elasticsearch cluster
	@echo "Deploying Elasticsearch cluster into '$(DATA_NS)' namespace...";
	@kubectl apply --server-side -n "$(DATA_NS)" -f "$(VALUES_FILE_PREFIX)"/elasticsearch.yaml;

.PHONY: elasticsearch-status
elasticsearch-status: _env-check ## Show Elasticsearch cluster status
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

.PHONY: elasticsearch-delete
elasticsearch-delete: _env-check ## Delete Elasticsearch cluster only
	@echo "Deleting Elasticsearch cluster '$(ENV_NAME)-elasticsearch' from namespace $(DATA_NS)..."
	@kubectl delete elasticsearch "$(ENV_NAME)-elasticsearch" -n "$(DATA_NS)" --ignore-not-found || true

.PHONY: elasticsearch-purge
elasticsearch-purge: elasticsearch-delete ## Delete Elasticsearch cluster and related secrets and PVCs
	@echo "Deleting Elasticsearch-related secrets and PVCs in namespace $(DATA_NS)..."
	@kubectl delete secret -n "$(DATA_NS)" -l elasticsearch.k8s.elastic.co/cluster-name=$(ENV_NAME)-elasticsearch || true
	@kubectl delete pvc -n "$(DATA_NS)" -l elasticsearch.k8s.elastic.co/cluster-name=$(ENV_NAME)-elasticsearch || true

.PHONY: elasticsearch-exporter-deploy
elasticsearch-exporter-deploy: _env-check ## Deploy Prometheus Elasticsearch Exporter for monitoring
	@echo "Deploying Elasticsearch Exporter into '$(DATA_NS)' namespace..."
	@kubectl apply -f "$(VALUES_FILE_PREFIX)"/elasticsearch-exporter.yaml

.PHONY: elasticsearch-exporter-delete
elasticsearch-exporter-delete: _env-check ## Delete Prometheus Elasticsearch Exporter
	@echo "Deleting Elasticsearch Exporter from namespace $(DATA_NS)..."
	@kubectl delete -f "$(VALUES_FILE_PREFIX)"/elasticsearch-exporter.yaml --ignore-not-found || true

################################################################################
# Kafka Deployment Targets
# Targets for deploying and managing Kafka in local dev and QA cluster
# Namespace and environment auto-detected from current kubectl context
################################################################################

STRIMZI_OPERATOR_VERSION := 0.48.0

kafka-strimzi-operator-install: _env-check ## Install Strimzi Kafka Operator into kafka-system namespace
	@kubectl get namespace kafka-system >/dev/null 2>&1 || kubectl create namespace kafka-system
	@helm repo add strimzi https://strimzi.io/charts/ || true
	@helm repo update
	@echo "Installing Strimzi Kafka operator v$(STRIMZI_OPERATOR_VERSION)..."
	@helm upgrade --install strimzi-operator strimzi/strimzi-kafka-operator \
		--version $(STRIMZI_OPERATOR_VERSION) \
		--namespace kafka-system \
		--set watchNamespaces={"$(DATA_NS)"} \
		--wait \
		--timeout 5m
	@echo "✅ Strimzi Kafka Operator installed. Verifying pods..."
	@kubectl get pods -n kafka-system --selector=name=strimzi-cluster-operator

kafka-strimzi-operator-uninstall: _env-check ## Uninstall Strimzi Kafka Operator from kafka-system namespace
	@echo "Uninstalling Strimzi Kafka Operator from 'kafka-system' namespace..."
	@helm uninstall strimzi-operator --namespace kafka-system || true
	@kubectl delete namespace kafka-system || true
	@echo "🗑 Strimzi Kafka Operator uninstalled and namespace 'kafka-system' deleted."

.PHONY: kafka-deploy
kafka-deploy: _env-check ## Deploy Kafka cluster
	@echo "Deploying Kafka cluster into '$(DATA_NS)' namespace..."
	@kubectl apply --server-side -n "$(DATA_NS)" -f "$(VALUES_FILE_PREFIX)"/kafka.yaml
	@echo "⏳ Waiting for Kafka cluster to be ready (this may take several minutes)..."
	@kubectl wait --for=condition=Ready kafka/$(ENV_NAME)-kafka -n "$(DATA_NS)" --timeout=5m || true
	@echo "✅ Kafka cluster deployment initiated. Check status with: make kafka-status"

.PHONY: kafka-status
kafka-status: _env-check ## Show Kafka cluster status
	@echo "Status for Kafka cluster in namespace: $(DATA_NS)"
	@echo "\n=== Kafka Custom Resource ==="
	@kubectl get kafka -n "$(DATA_NS)" "$(ENV_NAME)-kafka" -o wide || true
	@echo "\n=== Kafka Pods ==="
	@kubectl get pods -n "$(DATA_NS)" -l strimzi.io/cluster=$(ENV_NAME)-kafka || true
	@echo "\n=== Kafka Services ==="
	@kubectl get svc -n "$(DATA_NS)" -l strimzi.io/cluster=$(ENV_NAME)-kafka || true
	@echo "\n=== Kafka PersistentVolumeClaims ==="
	@kubectl get pvc -n "$(DATA_NS)" -l strimzi.io/cluster=$(ENV_NAME)-kafka || true
	@echo "\n=== Kafka StrimziPodSets ==="
	@kubectl get strimzipodsets -n "$(DATA_NS)"

.PHONY: kafka-delete
kafka-delete: _env-check ## Delete Kafka cluster only (preserves PVCs and StrimziPodSets)
	@echo "Deleting Kafka cluster '$(ENV_NAME)-kafka' from namespace $(DATA_NS)..."
	@kubectl delete kafka "$(ENV_NAME)-kafka" -n "$(DATA_NS)" --ignore-not-found || true
	@echo "⏳ Waiting 10s for Kafka resources to be cleaned up..."
	@sleep 10

.PHONY: kafka-purge
kafka-purge: kafka-delete ## Delete Kafka cluster and permanently remove PVCs, NodePools, and StrimziPodSets
	@echo "Deleting Kafka-related KafkaNodePools in namespace $(DATA_NS)..."
	@kubectl delete kafkanodepool -n "$(DATA_NS)" -l strimzi.io/cluster=$(ENV_NAME)-kafka || true

	@echo "Deleting Kafka-related StrimziPodSets in namespace $(DATA_NS)..."
	@kubectl delete strimzipodset -n "$(DATA_NS)" -l strimzi.io/cluster=$(ENV_NAME)-kafka || true

	@echo "Deleting Kafka-related PVCs in namespace $(DATA_NS)..."
	@kubectl delete pvc -n "$(DATA_NS)" -l strimzi.io/cluster=$(ENV_NAME)-kafka || true

	@echo "🗑️  Kafka cluster, all NodePools, StrimziPodSets, and persistent data removed."

.PHONY: kafka-topics-list
kafka-topics-list: _env-check ## List all Kafka topics
	@echo "Listing Kafka topics in cluster $(ENV_NAME)-kafka..."
	@kubectl get kafkatopic -n "$(DATA_NS)" || echo "No KafkaTopics found"

.PHONY: kafka-console-producer
kafka-console-producer: _env-check ## Access Kafka console producer (requires TOPIC_NAME)
	@if [ -z "$(TOPIC_NAME)" ]; then \
		echo "ERROR: TOPIC_NAME is required. Usage: make kafka-console-producer TOPIC_NAME=<topic-name>"; \
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
kafka-console-consumer: _env-check ## Access Kafka console consumer (requires TOPIC_NAME)
	@if [ -z "$(TOPIC_NAME)" ]; then \
		echo "ERROR: TOPIC_NAME is required. Usage: make kafka-console-consumer TOPIC_NAME=<topic-name>"; \
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
MINIO_CONSOLE_ACCESS_KEY ?= consoleAccess
MINIO_CONSOLE_SECRET_KEY ?= consoleSecret
# Length should be at least 3 characters
MINIO_ROOT_USER ?= rootUser
# Length should be at least 8 characters
MINIO_ROOT_PASSWORD ?= rootPassword

.PHONY: minio-operator-deploy
minio-operator-deploy: _env-check ## Install MinIO Operator using Kustomize
	@echo "Installing MinIO Operator v$(MINIO_OPERATOR_VERSION)..."
	@kubectl apply -k "$(VALUES_FILE_PREFIX)"/minio/operator

.PHONY: minio-operator-delete
minio-operator-delete: _env-check ## Delete MinIO Operator using Kustomize
	@echo "Deleting MinIO Operator v$(MINIO_OPERATOR_VERSION)..."
	@kubectl delete -k "$(VALUES_FILE_PREFIX)"/minio/operator

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
minio-storage-config-secret-create: _env-check _svc-name-check ## Create/update MinIO storage configuration secret. Usage: make minio-storage-config-secret-create SVC_NAME=<svc-name> MINIO_ROOT_USER=<root-user> MINIO_ROOT_PASSWORD=<root-password>
	@NAMESPACE="$(ENV_NAME)-minio-$$SVC_NAME"; \
	SECRET_NAME="$$NAMESPACE-storage-config"; \
	kubectl get namespace "$$NAMESPACE" >/dev/null 2>&1 || kubectl create namespace "$$NAMESPACE"; \
	echo "Creating MinIO storage configuration secret in namespace $$NAMESPACE..."; \
	printf "export MINIO_ROOT_USER=\"$(MINIO_ROOT_USER)\"\nexport MINIO_ROOT_PASSWORD=\"$(MINIO_ROOT_PASSWORD)\"\nexport MINIO_PROMETHEUS_AUTH_TYPE=\"public\"" | \
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
	kubectl kustomize "$(VALUES_FILE_PREFIX)/minio/$$SVC_NAME" | kubectl apply --server-side -f -; \
	\
	echo "Waiting for 'minio' Service to exist in namespace $$NAMESPACE..."; \
	for i in $$(seq 1 120); do \
	  kubectl get svc minio -n "$$NAMESPACE" >/dev/null 2>&1 && break; \
	  echo "  waiting for minio service... ($$i)"; \
	  sleep 5; \
	done; \
	\
	echo "Patching 'minio' service with label app=$$NAMESPACE in namespace $$NAMESPACE..."; \
	kubectl label svc minio app="$$NAMESPACE" -n "$$NAMESPACE" --overwrite

.PHONY: minio-tenant-status
minio-tenant-status: _env-check _svc-name-check ## Show MinIO tenant status for service. Usage: make minio-tenant-status SVC_NAME=<svc-name>
	@NAMESPACE="$(ENV_NAME)-minio-$$SVC_NAME"; \
	TENANT_NAME="$(ENV_NAME)-minio-tenant"; \
	echo "Status for MinIO tenant in namespace: $$NAMESPACE"; \
	echo "\nMinIO Tenant:"; \
	kubectl get tenant "$$TENANT_NAME" -n "$$NAMESPACE" -o wide || true; \
	echo "\nMinIO Pods:"; \
	kubectl get pods -n "$$NAMESPACE" || true; \
	echo "\nMinIO Services:"; \
	kubectl get svc -n "$$NAMESPACE" || true; \
	echo "\nMinIO PersistentVolumeClaims:"; \
	kubectl get pvc -n "$$NAMESPACE" || true

.PHONY: minio-tenant-delete
minio-tenant-delete: _env-check _svc-name-check ## Delete MinIO tenant for service. Usage: make minio-tenant-delete SVC_NAME=<svc-name>
	@NAMESPACE="$(ENV_NAME)-minio-$$SVC_NAME"; \
	TENANT_NAME="$(ENV_NAME)-minio-tenant"; \
	echo "Deleting MinIO tenant '$$TENANT_NAME' from namespace $$NAMESPACE..."; \
	kubectl delete tenant "$$TENANT_NAME" -n "$$NAMESPACE" --ignore-not-found || true; \
	kubectl delete servicemonitor "$(ENV_NAME)-minio-$$SVC_NAME-service-monitor" -n "$$NAMESPACE" --ignore-not-found || true

.PHONY: minio-tenant-purge
minio-tenant-purge: minio-tenant-delete _svc-name-check ## Delete MinIO tenant and related secrets/PVCs. Usage: make minio-tenant-purge SVC_NAME=<svc-name>
	@NAMESPACE="$(ENV_NAME)-minio-$$SVC_NAME"; \
	echo "Deleting MinIO-related secrets and PVCs in namespace $$NAMESPACE..."; \
	kubectl delete secret -n "$$NAMESPACE" --all || true; \
	kubectl delete pvc -n "$$NAMESPACE" --all || true; \
	echo "Deleting namespace $$NAMESPACE..."; \
	kubectl delete namespace "$$NAMESPACE" --ignore-not-found || true

################################################################################
# MongoDB Deployment Targets
# Targets for deploying and managing MongoDB in local dev and QA cluster
# Namespace and environment auto-detected from current kubectl context
################################################################################

MONGODB_OPERATOR_VERSION := 0.13.0
MONGO_ROOT_USER_PWD ?= rootpwd

.PHONY: mongodb-operator-install
mongodb-operator-install: _env-check ## Install MongoDB Community Operator
	@kubectl get namespace "$(DATA_NS)" >/dev/null 2>&1 || kubectl create namespace "$(DATA_NS)"
	@helm repo add mongodb https://mongodb.github.io/helm-charts
	@helm repo update
	@echo "Installing MongoDB Community Operator v$(MONGODB_OPERATOR_VERSION)..."
	@helm upgrade --install mongodb-operator mongodb/community-operator \
		--version "$(MONGODB_OPERATOR_VERSION)" \
		--namespace "$(DATA_NS)" \
		--wait
		--timeout 10m
	@echo "✅ MongoDB Community Operator installed. Verifying pods..."
	@kubectl get pods -n "$(DATA_NS)" -l name=mongodb-kubernetes-operator

.PHONY: mongodb-root-secret-create
mongodb-root-secret-create: _env-check _data-ns-check ## Create/update secret for MongoDB 'root' user. Usage: make mongodb-root-secret-create MONGO_ROOT_USER_PWD=<root-pwd>
	@echo "Creating secret with user creds for 'admin' database in MongoDB..."
	@kubectl create secret generic "$(ENV_NAME)"-mongodb-root-creds \
	    --namespace "$(DATA_NS)" \
	    --type Opaque \
	    --from-literal=password="$(MONGO_ROOT_USER_PWD)" \
		--dry-run=client -o yaml | kubectl apply -f -
	@echo "Secret '"$(ENV_NAME)"-mongodb-root-creds' successfully created/updated in namespace $(DATA_NS)"

.PHONY: mongodb-deploy
mongodb-deploy: _env-check _data-ns-check ## Deploy MongoDB cluster
	@echo "Deploying MongoDB cluster to '$(DATA_NS)' namespace...";
	@kubectl apply --server-side -n "$(DATA_NS)" -f "$(VALUES_FILE_PREFIX)"/mongodb.yaml;

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

.PHONY: mongodb-purge
mongodb-purge: mongodb-delete ## Delete MongoDB cluster and permanently remove related secrets and PVCs
	@echo "Deleting MongoDB-related secrets and PVCs in namespace $(DATA_NS)..."
	@kubectl delete secret -n "$(DATA_NS)" -l app="$(ENV_NAME)"-mongodb-svc || true
	@kubectl delete pvc -n "$(DATA_NS)" -l app="$(ENV_NAME)"-mongodb-svc || true
