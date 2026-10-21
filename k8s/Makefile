################################################################################
# Child Makefile common for local dev and QA insurance-hub cluster
################################################################################

ifeq ($(abspath $(firstword $(MAKEFILE_LIST))),$(abspath $(lastword $(MAKEFILE_LIST))))
.DEFAULT_GOAL := k8s-help
endif

K8S_MAKEFILE_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
K8S_REPOSITORY_ROOT := $(abspath $(K8S_MAKEFILE_DIR)..)
K8S_ADMIN_SCRIPT := $(K8S_MAKEFILE_DIR)scripts/admin/dispatch.py

.PHONY: k8s-help
k8s-help: ## Show the safe Kubernetes Make entry points
	@echo "Use an explicit target; run 'make -C k8s -qp' to inspect the complete interface."
	@echo "Common checks: python-k8s-admin-check"
	@echo "Administrative targets validate the active context before mutation."

.PHONY: kubeconfig-clear
kubeconfig-clear: ## Empty the ~/.kube/config file
	@echo "Clearing the ~/.kube/config file..."
	@> ~/.kube/config
	@echo "~/.kube/config file has been emptied."

.PHONY: local-dev-port-forward
local-dev-port-forward: python-runtime-check ## Own local-dev port-forwards until Ctrl+C, then stop them
	@ENV_NAME=local-dev PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" local port-forward

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
	kubectl --context "$(CUR_CTX)" version --client

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

.PHONY: prereq-k8s-cnpg-plugin
prereq-k8s-cnpg-plugin: ## Install CloudNativePG kubectl plugin on Ubuntu 24.04.2 LTS
	@echo "Installing CloudNativePG kubectl plugin..."
	@curl -sSfL https://github.com/cloudnative-pg/cloudnative-pg/raw/main/hack/install-cnpg-plugin.sh | sudo sh -s -- -b /usr/local/bin
	@echo "kubectl-cnpg plugin installed at /usr/local/bin"

.PHONY: prereq-k8s-flux
prereq-k8s-flux: ## Install Flux CLI on Ubuntu 24.04.2 LTS
	@echo "Installing Flux CLI..."
	@curl -s https://fluxcd.io/install.sh | sudo bash
	@flux --version

.PHONY: prereq-k8s-all
prereq-k8s-all: prereq-k8s-kubectl prereq-k8s-kubectx prereq-k8s-kubens prereq-k8s-helm prereq-k8s-lxd prereq-k8s-kind prereq-k8s-cnpg-plugin ## Install all Kubernetes prerequisites

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
	FLUX_SYSTEM_NS := flux-system
else
    ifeq ($(CUR_CTX), kind-local-dev-insurance-hub)
        ENV_NAME := local-dev
        LOCAL_DEV_NS := $(ENV_NAME)-all
		SVC_NS := $(LOCAL_DEV_NS)
		DATA_NS := $(LOCAL_DEV_NS)
		AUTH_NS := $(LOCAL_DEV_NS)
		NETWORKING_NS := $(LOCAL_DEV_NS)
		MONITORING_NS := INVALID
		FLUX_SYSTEM_NS := INVALID
    else
        ENV_NAME := INVALID
    endif
endif

OVERLAYS_FILE_PATH = overlays/$(ENV_NAME)
COPY_SECRET_NS ?= $(SVC_NS)
export ENV_NAME CUR_CTX SVC_NAME DATA_NS SVC_NS MONITORING_NS FLUX_SYSTEM_NS COPY_SECRET_NS

SECRET_INPUT_NAMES := PG_SVC_USER_PWD MINIO_CONSOLE_ACCESS_KEY \
	MINIO_CONSOLE_SECRET_KEY MINIO_ROOT_USER_PWD MINIO_SVC_ACCESS_KEY \
	MINIO_SVC_SECRET_KEY MONGO_ROOT_USER_PWD MONGO_PRODUCT_USER_PWD \
	ZIPKIN_ES_USER_PWD

define reject-secret-command-line
ifneq (,$(filter command line override,$(origin $(1))))
$$(error $(1) must not be supplied as a Make command-line assignment; use $(1)_FILE with an absolute mode-0600 file)
endif
endef
$(foreach name,$(SECRET_INPUT_NAMES),$(eval $(call reject-secret-command-line,$(name))))

export $(SECRET_INPUT_NAMES)
export $(addsuffix _FILE,$(SECRET_INPUT_NAMES))

.PHONY: _env-check
_env-check: python-runtime-check ## Verify the exact supported Kubernetes context and environment
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" guard environment

.PHONY: _data-ns-check
_data-ns-check: _env-check ## Verify the data namespace without changing the current context
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" guard data

.PHONY: _monitoring-ns-check
_monitoring-ns-check: _env-check ## Verify the QA monitoring namespace without changing context
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" guard monitoring

.PHONY: _flux-system-ns-check
_flux-system-ns-check: _env-check ## Verify the QA Flux namespace without changing context
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" guard flux

.PHONY: _svc-ns-check
_svc-ns-check: _env-check ## Verify the service namespace without changing the current context
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" guard service

.PHONY: _data-ns-ensure _monitoring-ns-ensure _flux-system-ns-ensure _svc-ns-ensure
_data-ns-ensure: _data-ns-check ## Ensure the validated data namespace exists
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" namespace ensure data
_monitoring-ns-ensure: _monitoring-ns-check ## Ensure the validated monitoring namespace exists
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" namespace ensure monitoring
_flux-system-ns-ensure: _flux-system-ns-check ## Ensure the validated Flux namespace exists
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" namespace ensure flux
_svc-ns-ensure: _svc-ns-check ## Ensure the validated service namespace exists
	@PYTHONDWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" namespace ensure service

.PHONY: _svc-name-check
_svc-name-check: python-runtime-check ## Validate SVC_NAME before using it as a resource coordinate
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" guard service-name

################################################################################
# Flux Deployment Targets
# Targes for deploying and managing Flux
# Current context must be "qa-insurance-hub"
################################################################################

FLUX_GITHUB_OWNER ?= igor-baiborodine
FLUX_GITHUB_REPOSITORY ?= insurance-hub
FLUX_GITHUB_BRANCH ?= main
FLUX_QA_PATH ?= k8s/flux/qa

.PHONY: flux-bootstrap
flux-bootstrap: _flux-system-ns-ensure ## Bootstrap Flux into cluster and connect it to this GitHub repo
	@echo "Running Flux pre-flight checks..."
	@flux check --pre --context "$(CUR_CTX)"
	@echo "Bootstrapping Flux from repository $(FLUX_GITHUB_OWNER)/$(FLUX_GITHUB_REPOSITORY)..."
	@flux bootstrap github \
		--owner=$(FLUX_GITHUB_OWNER) \
		--repository=$(FLUX_GITHUB_REPOSITORY) \
		--branch=$(FLUX_GITHUB_BRANCH) \
		--path=$(FLUX_QA_PATH) \
		--context "$(CUR_CTX)" \
		--namespace "$(FLUX_SYSTEM_NS)" \
		--token-auth \
		--personal
		
	@echo "✅ Flux bootstrap completed in '$(ENV_NAME)' cluster."
	@echo "Verifying Flux installation..."
	@flux check --context "$(CUR_CTX)"
	@echo "Listing Flux resources..."
	@flux get all -A --context "$(CUR_CTX)"

.PHONY: flux-uninstall
flux-uninstall: _flux-system-ns-check ## Uninstall Flux from the QA cluster and remove its bootstrap manifests from Git
	@echo "Uninstalling Flux from '$(ENV_NAME)' cluster and removing bootstrap manifests from Git..."
	@flux uninstall --silent --context "$(CUR_CTX)" --namespace "$(FLUX_SYSTEM_NS)"
	@rm -rf $(FLUX_QA_PATH)
	@echo "🗑 Flux uninstalled."
	@echo "🗑 Local bootstrap path $(FLUX_QA_PATH) has been deleted."
	@rm -rf ../$(FLUX_QA_PATH)
	@echo "If the path was tracked by Git, commit and push the deletion."

.PHONY: flux-status
flux-status: _env-check ## Show Flux status in the QA cluster
	@echo "Checking Flux status in '$(ENV_NAME)' cluster..."
	@flux check --context "$(CUR_CTX)"
	@flux get all -A --context "$(CUR_CTX)"

.PHONY: flux-reconcile
flux-reconcile: _env-check ## Force reconcile the QA Flux Kustomization
	@echo "Reconciling Flux Kustomization in '$(ENV_NAME)' cluster..."
	@flux reconcile kustomization qa-svc -n flux-system --with-source --context "$(CUR_CTX)"
	@echo "✅ Flux reconciliation requested."

.PHONY: flux-suspend
flux-suspend: _env-check ## Suspend QA Flux reconciliation
	@echo "Suspending Flux Kustomization in '$(ENV_NAME)' cluster..."
	@flux suspend kustomization qa-svc -n flux-system --context "$(CUR_CTX)"
	@echo "✅ Flux reconciliation suspended."

################################################################################
# Monitoring Deployment Targets
# Targes for deploying and managing monitoring infrastructure
# Current context must be "qa-insurance-hub"
################################################################################

#########################################
# Prometheus & Grafana
#########################################

PROMETHEUS_OPERATOR_CRDS_VERSION := 32.0.1
KUBE_PROMETHEUS_STACK_VERSION := 92.6.0
PROMETHEUS_VALUES_PATH := $(K8S_MAKEFILE_DIR)overlays/qa/infra/prometheus/values.yaml
MONITORING_ASSETS_ROOT := $(K8S_MAKEFILE_DIR)observability
MONITORING_RENDER_RULE_SCRIPT := $(K8S_MAKEFILE_DIR)scripts/monitoring/render_prometheus_rule.py
MONITORING_RENDER_DASHBOARD_SCRIPT := $(K8S_MAKEFILE_DIR)scripts/monitoring/render_grafana_dashboard.py
MONITORING_RESOLVE_SCRIPT := $(K8S_MAKEFILE_DIR)scripts/monitoring/resolve_monitoring.py

.PHONY: prometheus-operator-crds-install
prometheus-operator-crds-install: _monitoring-ns-ensure
	@helm repo add prometheus-community https://prometheus-community.github.io/helm-charts --force-update
	@helm repo update prometheus-community
	@echo "Installing Prometheus Operator CRDs chart v$(PROMETHEUS_OPERATOR_CRDS_VERSION) into $(MONITORING_NS) namespace..."
	@helm upgrade --install prometheus-operator-crds prometheus-community/prometheus-operator-crds \
		--version "$(PROMETHEUS_OPERATOR_CRDS_VERSION)" \
		--kube-context "$(CUR_CTX)" \
		--namespace $(MONITORING_NS) --create-namespace \
		--wait --timeout 10m
	@echo "Waiting for monitoring.coreos.com CRDs to be registered..."
	@kubectl --context "$(CUR_CTX)" wait --for=condition=Established --timeout=60s \
		crd/alertmanagerconfigs.monitoring.coreos.com \
		crd/alertmanagers.monitoring.coreos.com \
		crd/podmonitors.monitoring.coreos.com \
		crd/probes.monitoring.coreos.com \
		crd/prometheusagents.monitoring.coreos.com \
		crd/prometheuses.monitoring.coreos.com \
		crd/prometheusrules.monitoring.coreos.com \
		crd/scrapeconfigs.monitoring.coreos.com \
		crd/servicemonitors.monitoring.coreos.com \
		crd/thanosrulers.monitoring.coreos.com
	@echo "Confirming existing monitoring custom resources remain readable..."
	@for resource in \
		alertmanagers.monitoring.coreos.com \
		podmonitors.monitoring.coreos.com \
		probes.monitoring.coreos.com \
		prometheuses.monitoring.coreos.com \
		prometheusrules.monitoring.coreos.com \
		scrapeconfigs.monitoring.coreos.com \
		servicemonitors.monitoring.coreos.com; do \
		kubectl --context "$(CUR_CTX)" get "$$resource" --all-namespaces >/dev/null; \
	done

.PHONY: prometheus-stack-install
prometheus-stack-install: prometheus-operator-crds-install ## Install Kube Prometheus Stack (Prometheus, Grafana, Alert Manager) in QA
	@echo "Installing Kube Prometheus Stack using Helm chart v$(KUBE_PROMETHEUS_STACK_VERSION) into $(MONITORING_NS) namespace..."
	@helm upgrade --install "$(ENV_NAME)"-prometheus prometheus-community/kube-prometheus-stack \
		--version "$(KUBE_PROMETHEUS_STACK_VERSION)" \
		--kube-context "$(CUR_CTX)" \
		--namespace $(MONITORING_NS) \
		--values "$(PROMETHEUS_VALUES_PATH)" \
		--atomic --wait --timeout 10m
	@echo "✅ Kube Prometheus Stack deployment initiated. Check status with: make prometheus-stack-status"

.PHONY: prometheus-stack-uninstall
prometheus-stack-uninstall: _monitoring-ns-check ## Uninstall Kube Prometheus Stack
	@echo "Uninstalling Kube Prometheus Stack from '$(MONITORING_NS)' namespace..."
	@helm uninstall "$(ENV_NAME)"-prometheus --kube-context "$(CUR_CTX)" --namespace $(MONITORING_NS) || true
	@echo "🗑 Kube Prometheus Stack uninstalled."

.PHONY: prometheus-stack-status
prometheus-stack-status: _monitoring-ns-check ## Show Kube Prometheus Stack deployment status
	@echo "Status for Prometheus Stack in namespace: $(MONITORING_NS)"
	@echo "\nPrometheus StatefulSet:"
	@kubectl --context "$(CUR_CTX)" get statefulset -n $(MONITORING_NS) -l app.kubernetes.io/name=prometheus || true
	@echo "\nPrometheus Services:"
	@kubectl --context "$(CUR_CTX)" get svc -n $(MONITORING_NS) -l app=kube-prometheus-stack-prometheus || true
	@echo "\nPrometheus Pods:"
	@kubectl --context "$(CUR_CTX)" get pods -n $(MONITORING_NS) -l app.kubernetes.io/name=prometheus
	@echo "\nAlertmanager StatefulSet:"
	@kubectl --context "$(CUR_CTX)" get statefulset -n $(MONITORING_NS) -l app.kubernetes.io/name=alertmanager || true
	@echo "\nGrafana Deployment:"
	@kubectl --context "$(CUR_CTX)" get deployment -n $(MONITORING_NS) -l app.kubernetes.io/name=grafana || true
	@echo "\nGrafana Service:"
	@kubectl --context "$(CUR_CTX)" get svc -n $(MONITORING_NS) -l app.kubernetes.io/name=grafana || true

.PHONY: _monitoring-assets-args-check
_monitoring-assets-args-check: ## Require an explicit environment and service for a monitoring asset operation
	@command -v python3 >/dev/null || { echo "ERROR: Python 3.9 or newer is required."; exit 1; }
	@python3 -c 'import sys; raise SystemExit(sys.version_info < (3, 9))' || { \
		echo "ERROR: Python 3.9 or newer is required."; exit 1; \
	}
	@if [ "$(origin ENV_NAME)" != "command line" ] || [ -z "$(ENV_NAME)" ]; then \
		echo "ERROR: ENV_NAME is required on the make command line."; \
		exit 1; \
	fi
	@if [ "$(origin SVC_NAME)" != "command line" ] || [ -z "$(SVC_NAME)" ]; then \
		echo "ERROR: SVC_NAME is required on the make command line."; \
		exit 1; \
	fi
	@PYTHONDONTWRITEBYTECODE=1 python3 "$(MONITORING_RESOLVE_SCRIPT)" \
		--assets-root "$(MONITORING_ASSETS_ROOT)" \
		--environment "$(ENV_NAME)" \
		--service "$(SVC_NAME)" >/dev/null

.PHONY: prometheus-rules-render
prometheus-rules-render: _monitoring-assets-args-check ## Render one service PrometheusRule to stdout (ENV_NAME, SVC_NAME)
	@PYTHONDONTWRITEBYTECODE=1 python3 "$(MONITORING_RENDER_RULE_SCRIPT)" \
		--assets-root "$(MONITORING_ASSETS_ROOT)" \
		--environment "$(ENV_NAME)" \
		--service "$(SVC_NAME)"

.PHONY: prometheus-rules-apply
prometheus-rules-apply: _monitoring-assets-args-check ## Validate and apply one service PrometheusRule through Kubernetes (ENV_NAME, SVC_NAME)
	@$(MAKE) --no-print-directory -C "$(K8S_REPOSITORY_ROOT)" monitoring-assets-check \
		ENV_NAME="$(ENV_NAME)" SVC_NAME="$(SVC_NAME)"
	@command -v kubectl >/dev/null || { echo "ERROR: kubectl is required."; exit 1; }
	@set -eu; \
	eval "$$(PYTHONDONTWRITEBYTECODE=1 python3 "$(MONITORING_RESOLVE_SCRIPT)" \
		--assets-root "$(MONITORING_ASSETS_ROOT)" \
		--environment "$(ENV_NAME)" \
		--service "$(SVC_NAME)")"; \
	actual_context=$$(kubectl config current-context); \
	if [ "$$actual_context" != "$$MONITORING_KUBE_CONTEXT" ]; then \
		echo "ERROR: Current context is '$$actual_context' (expected '$$MONITORING_KUBE_CONTEXT')."; \
		exit 1; \
	fi; \
	tmp_file=$$(mktemp); \
	trap 'rm -f "$$tmp_file"' EXIT INT TERM; \
	PYTHONDONTWRITEBYTECODE=1 python3 "$(MONITORING_RENDER_RULE_SCRIPT)" \
		--assets-root "$(MONITORING_ASSETS_ROOT)" \
		--environment "$(ENV_NAME)" \
		--service "$(SVC_NAME)" \
		--output "$$tmp_file"; \
	kubectl --context "$$MONITORING_KUBE_CONTEXT" --namespace "$$MONITORING_NAMESPACE" \
		apply --dry-run=server -f "$$tmp_file" >/dev/null; \
	kubectl --context "$$MONITORING_KUBE_CONTEXT" --namespace "$$MONITORING_NAMESPACE" \
		apply -f "$$tmp_file"; \
	kubectl --context "$$MONITORING_KUBE_CONTEXT" --namespace "$$MONITORING_NAMESPACE" \
		get prometheusrule "$$MONITORING_PROMETHEUS_RULE" >/dev/null
	@echo "✅ Applied $(SVC_NAME) alert rules to $(ENV_NAME)."

.PHONY: grafana-dashboard-render
grafana-dashboard-render: _monitoring-assets-args-check ## Render one service dashboard sidecar ConfigMap to stdout (ENV_NAME, SVC_NAME)
	@PYTHONDONTWRITEBYTECODE=1 python3 "$(MONITORING_RENDER_DASHBOARD_SCRIPT)" \
		--assets-root "$(MONITORING_ASSETS_ROOT)" \
		--environment "$(ENV_NAME)" \
		--service "$(SVC_NAME)"

.PHONY: grafana-dashboard-apply
grafana-dashboard-apply: _monitoring-assets-args-check ## Validate and apply one immutable service dashboard through Kubernetes (ENV_NAME, SVC_NAME)
	@$(MAKE) --no-print-directory -C "$(K8S_REPOSITORY_ROOT)" monitoring-assets-check \
		ENV_NAME="$(ENV_NAME)" SVC_NAME="$(SVC_NAME)"
	@command -v kubectl >/dev/null || { echo "ERROR: kubectl is required."; exit 1; }
	@set -eu; \
	eval "$$(PYTHONDONTWRITEBYTECODE=1 python3 "$(MONITORING_RESOLVE_SCRIPT)" \
		--assets-root "$(MONITORING_ASSETS_ROOT)" \
		--environment "$(ENV_NAME)" \
		--service "$(SVC_NAME)")"; \
	actual_context=$$(kubectl config current-context); \
	if [ "$$actual_context" != "$$MONITORING_KUBE_CONTEXT" ]; then \
		echo "ERROR: Current context is '$$actual_context' (expected '$$MONITORING_KUBE_CONTEXT')."; \
		exit 1; \
	fi; \
	tmp_file=$$(mktemp); \
	trap 'rm -f "$$tmp_file"' EXIT INT TERM; \
	PYTHONDONTWRITEBYTECODE=1 python3 "$(MONITORING_RENDER_DASHBOARD_SCRIPT)" \
		--assets-root "$(MONITORING_ASSETS_ROOT)" \
		--environment "$(ENV_NAME)" \
		--service "$(SVC_NAME)" \
		--output "$$tmp_file"; \
	kubectl --context "$$MONITORING_KUBE_CONTEXT" --namespace "$$MONITORING_NAMESPACE" \
		apply --dry-run=server -f "$$tmp_file" >/dev/null; \
	kubectl --context "$$MONITORING_KUBE_CONTEXT" --namespace "$$MONITORING_NAMESPACE" \
		apply -f "$$tmp_file"; \
	kubectl --context "$$MONITORING_KUBE_CONTEXT" --namespace "$$MONITORING_NAMESPACE" \
		get configmap "$$MONITORING_GRAFANA_DASHBOARD_CONFIG_MAP" >/dev/null
	@echo "✅ Applied immutable $(SVC_NAME) dashboard to $(ENV_NAME)."

.PHONY: prometheus-ui
prometheus-ui: ## Access Prometheus UI via port-forward
	@echo "Forwarding Prometheus UI to http://localhost:9090"
	@echo "Press Ctrl+C to stop."
	@kubectl --context "$(CUR_CTX)" port-forward svc/qa-prometheus-kube-prometh-prometheus 9090:9090 -n qa-monitoring

.PHONY: grafana-ui
grafana-ui: ## Access Grafana UI via port-forward
	@echo "Forwarding Grafana UI to http://localhost:3000"
	@echo "Login with admin/adminpwd. Press Ctrl+C to stop."
	@kubectl --context "$(CUR_CTX)" port-forward svc/qa-prometheus-grafana 3000:80 -n qa-monitoring

#########################################
# Loki
#########################################

LOKI_HELM_CHART_VERSION := 18.15.1

.PHONY: loki-install
loki-install: _monitoring-ns-ensure ## Install Grafana Loki in QA cluster
	@if [ ! -f "$(OVERLAYS_FILE_PATH)/infra/loki/values.yaml" ]; then \
		echo "ERROR: Missing Loki values file at '$(OVERLAYS_FILE_PATH)/infra/loki/values.yaml'."; \
		exit 1; \
	fi
	@helm repo add grafana-community https://grafana-community.github.io/helm-charts --force-update
	@helm repo update grafana-community
	@echo "Installing Grafana Loki using Helm chart v$(LOKI_HELM_CHART_VERSION) into $(MONITORING_NS) namespace..."
	@helm upgrade --install "$(ENV_NAME)"-loki grafana-community/loki \
		--version "$(LOKI_HELM_CHART_VERSION)" \
		--kube-context "$(CUR_CTX)" \
		--namespace $(MONITORING_NS) \
		--values "$(OVERLAYS_FILE_PATH)"/infra/loki/values.yaml \
		--atomic --wait --timeout 10m
	@echo "✅ Loki deployment initiated. Check status with: make loki-status"

.PHONY: loki-uninstall
loki-uninstall: _monitoring-ns-check ## Uninstall Loki from QA cluster
	@echo "Uninstalling Loki from '$(MONITORING_NS)' namespace..."
	@helm uninstall "$(ENV_NAME)"-loki \
		--kube-context "$(CUR_CTX)" \
		--namespace $(MONITORING_NS) \
		--wait --timeout 10m
	@echo "🗑 Loki uninstalled."

.PHONY: loki-status
loki-status: _monitoring-ns-check ## Show Loki deployment status in QA cluster
	@echo "Status for Loki in namespace: $(MONITORING_NS)"
	@echo "\nLoki Pods:"
	@kubectl --context "$(CUR_CTX)" get pods -n $(MONITORING_NS) -l app.kubernetes.io/name=loki || true
	@echo "\nLoki Services:"
	@kubectl --context "$(CUR_CTX)" get svc -n $(MONITORING_NS) -l app.kubernetes.io/name=loki || true

.PHONY: loki-ui
loki-ui: _monitoring-ns-check ## Access Loki HTTP API via port-forward
	@echo "Forwarding Loki HTTP API to http://localhost:3100"
	@echo "Press Ctrl+C to stop."
	@kubectl --context "$(CUR_CTX)" port-forward svc/$(ENV_NAME)-loki 3100:3100 -n $(MONITORING_NS)

#########################################
# Tempo
#########################################

TEMPO_HELM_CHART_VERSION := 3.2.0

.PHONY: tempo-install
tempo-install: _monitoring-ns-ensure ## Install Grafana Tempo in QA cluster
	@if [ ! -f "$(OVERLAYS_FILE_PATH)/infra/tempo/values.yaml" ]; then \
		echo "ERROR: Missing Tempo values file at '$(OVERLAYS_FILE_PATH)/infra/tempo/values.yaml'."; \
		exit 1; \
	fi
	@helm repo add grafana-community https://grafana-community.github.io/helm-charts --force-update
	@helm repo update grafana-community
	@echo "Installing Grafana Tempo using Helm chart v$(TEMPO_HELM_CHART_VERSION) into $(MONITORING_NS) namespace..."
	@helm upgrade --install "$(ENV_NAME)"-tempo grafana-community/tempo \
		--version "$(TEMPO_HELM_CHART_VERSION)" \
		--kube-context "$(CUR_CTX)" \
		--namespace $(MONITORING_NS) \
		--values "$(OVERLAYS_FILE_PATH)"/infra/tempo/values.yaml \
		--atomic --wait --timeout 10m
	@echo "✅ Tempo deployment initiated. Check status with: make tempo-status"

.PHONY: tempo-uninstall
tempo-uninstall: _monitoring-ns-check ## Uninstall Tempo from QA cluster
	@echo "Uninstalling Tempo from '$(MONITORING_NS)' namespace..."
	@helm uninstall "$(ENV_NAME)"-tempo \
		--kube-context "$(CUR_CTX)" \
		--namespace $(MONITORING_NS) \
		--wait --timeout 10m
	@echo "🗑 Tempo uninstalled."

.PHONY: tempo-status
tempo-status: _monitoring-ns-check ## Show Tempo deployment status in QA cluster
	@echo "Status for Tempo in namespace: $(MONITORING_NS)"
	@echo "\nTempo Pods:"
	@kubectl --context "$(CUR_CTX)" get pods -n $(MONITORING_NS) -l app.kubernetes.io/name=tempo || true
	@echo "\nTempo Services:"
	@kubectl --context "$(CUR_CTX)" get svc -n $(MONITORING_NS) -l app.kubernetes.io/name=tempo || true

.PHONY: tempo-ui
tempo-ui: _monitoring-ns-check ## Access Tempo HTTP API via port-forward
	@echo "Forwarding Tempo HTTP API to http://localhost:3200"
	@echo "Press Ctrl+C to stop."
	@kubectl --context "$(CUR_CTX)" port-forward svc/$(ENV_NAME)-tempo 3200:3200 -n $(MONITORING_NS)

#########################################
# Alloy
#########################################

ALLOY_HELM_CHART_VERSION := 1.13.1
ALLOY_CONFIG_PATH := $(K8S_MAKEFILE_DIR)overlays/qa/infra/alloy/config.alloy
ALLOY_VALUES_PATH := $(K8S_MAKEFILE_DIR)overlays/qa/infra/alloy/values.yaml

.PHONY: alloy-install
alloy-install: _monitoring-ns-ensure ## Install Grafana Alloy in QA cluster
	@if [ ! -f "$(ALLOY_VALUES_PATH)" ]; then \
		echo "ERROR: Missing Alloy values file at '$(ALLOY_VALUES_PATH)'."; \
		exit 1; \
	fi
	@helm repo add grafana https://grafana.github.io/helm-charts --force-update
	@helm repo update grafana
	@echo "Installing Grafana Alloy using Helm chart v$(ALLOY_HELM_CHART_VERSION) into $(MONITORING_NS) namespace..."
	@helm upgrade --install "$(ENV_NAME)"-alloy grafana/alloy \
		--version "$(ALLOY_HELM_CHART_VERSION)" \
		--kube-context "$(CUR_CTX)" \
		--namespace $(MONITORING_NS) \
		--values "$(ALLOY_VALUES_PATH)" \
		--set-file alloy.configMap.content="$(ALLOY_CONFIG_PATH)" \
		--atomic --wait --timeout 10m
	@echo "✅ Alloy deployment initiated. Check status with: make alloy-status"

.PHONY: alloy-uninstall
alloy-uninstall: _monitoring-ns-check ## Uninstall Alloy from QA cluster
	@echo "Uninstalling Alloy from '$(MONITORING_NS)' namespace..."
	@helm uninstall "$(ENV_NAME)"-alloy --kube-context "$(CUR_CTX)" --namespace $(MONITORING_NS) || true
	@echo "🗑 Alloy uninstalled."

.PHONY: alloy-status
alloy-status: _monitoring-ns-check ## Show Alloy deployment status in QA cluster
	@echo "Status for Alloy in namespace: $(MONITORING_NS)"
	@echo "\nAlloy Pods:"
	@kubectl --context "$(CUR_CTX)" get pods -n $(MONITORING_NS) -l app.kubernetes.io/name=alloy || true
	@echo "\nAlloy Services:"
	@kubectl --context "$(CUR_CTX)" get svc -n $(MONITORING_NS) -l app.kubernetes.io/name=alloy || true

.PHONY: alloy-ui
alloy-ui: _monitoring-ns-check ## Access Alloy HTTP endpoint via port-forward
	@echo "Forwarding Alloy HTTP endpoint to http://localhost:12345"
	@echo "Press Ctrl+C to stop."
	@kubectl --context "$(CUR_CTX)" port-forward svc/$(ENV_NAME)-alloy 12345:12345 -n $(MONITORING_NS)

################################################################################
# PostgreSQL Deployment Targets
# Targets for deploying and managing PostgreSQL cluster per app service in local dev and QA cluster
# Namespace is auto-detected from the current kubectl context
################################################################################

CLOUDNATIVE_PG_VERSION := 1.27.0
export SVC_NAME ROLE_PROFILE

.PHONY: postgres-operator-deploy
postgres-operator-deploy: _env-check ## Deploy CloudNativePG operator
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" operator postgres
.PHONY: postgres-operator-delete
postgres-operator-delete: _env-check ## Delete CloudNativePG operator with its namespace
	@echo "Deleting CloudNativePG operator from 'cnpg-system' namespace..."
	@kubectl --context "$(CUR_CTX)" delete -f https://github.com/cloudnative-pg/cloudnative-pg/releases/download/v$(CLOUDNATIVE_PG_VERSION)/cnpg-"$(CLOUDNATIVE_PG_VERSION)".yaml
	@echo "🗑 CloudNativePG operator deleted."

.PHONY: postgres-svc-secret-create
postgres-svc-secret-create: _env-check _data-ns-ensure _svc-ns-ensure _svc-name-check ## Create/update secret for DB service user. Usage: make postgres-svc-secret-create SVC_NAME=<svc-name> PG_SVC_USER_PWD_FILE=</absolute/mode-0600-file>
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" secret postgres

# Service-owned database role profiles are administered inside an existing cluster.
# These targets never depend on postgres-svc-deploy or namespace-changing legacy prerequisites.
.PHONY: postgres-svc-role-inventory postgres-svc-role-snapshot postgres-svc-role-provision \
	postgres-svc-role-rotate postgres-svc-role-revoke
postgres-svc-role-inventory: ## Inspect a selected service-owned database role profile. Requires SVC_NAME, ROLE_PROFILE, and explicit target coordinates.
postgres-svc-role-snapshot: ## Capture sanitized catalog/schema identity for a selected service-owned profile. Requires explicit target coordinates.
postgres-svc-role-provision: ## Provision/reapply a selected service-owned database role profile. Requires explicit target coordinates.
postgres-svc-role-rotate: ## Rotate credentials for a selected role profile. Requires explicit target coordinates.
postgres-svc-role-revoke: ## Revoke a selected role profile. Requires explicit target coordinates and profile-specific confirmation.
postgres-svc-role-inventory postgres-svc-role-snapshot postgres-svc-role-provision \
	postgres-svc-role-rotate postgres-svc-role-revoke: python-runtime-check
	@PYTHONDONTWRITEBYTECODE=1 $(PYTHON) "$(K8S_REPOSITORY_ROOT)/scripts/postgres/service/dispatch.py" '$(@:postgres-svc-role-%=%)'

.PHONY: postgres-svc-deploy
postgres-svc-deploy: _env-check _data-ns-ensure _svc-name-check ## Deploy PostgresSQL cluster for service. Usage: make postgres-svc-deploy SVC_NAME=<svc-name>
	@echo "Deploying PostgreSQL cluster for '$(SVC_NAME)' into '$(DATA_NS)' namespace...";
	@kubectl --context "$(CUR_CTX)" kustomize "$(OVERLAYS_FILE_PATH)/infra/postgres/$(SVC_NAME)" | kubectl --context "$(CUR_CTX)" -n "$(DATA_NS)" apply --server-side -f -;
	@echo "✅ Postgres cluster deployment for '$(SVC_NAME)' initiated. Check status with: make postgres-svc-status SVC_NAME=$(SVC_NAME)"

.PHONY: postgres-svc-status
postgres-svc-status: _env-check _svc-name-check ## Show PostgreSQL cluster/resource status for service. Usage: make postgres-svc-status SVC_NAME=<svc-name>
	@echo "Status for namespace: $(DATA_NS)"
	@kubectl --context "$(CUR_CTX)" cnpg status "$(ENV_NAME)-postgres-$(SVC_NAME)" -n "$(DATA_NS)" || true

.PHONY: postgres-svc-delete
postgres-svc-delete: _env-check ## Delete PostgreSQL cluster for service. Usage: make postgres-svc-delete SVC_NAME=<svc-name>
	@echo "Deleting PostgreSQL cluster '$(ENV_NAME)-postgres-$(SVC_NAME)' from namespace '$(DATA_NS)'..."
	@kubectl --context "$(CUR_CTX)" delete cluster.postgresql.cnpg.io -n "$(DATA_NS)" $(ENV_NAME)-postgres-"$(SVC_NAME)" || true
	@echo "🗑 Postgres cluster for '$(SVC_NAME)' deleted."

.PHONY: postgres-svc-purge
postgres-svc-purge: _env-check _data-ns-check _svc-ns-check _svc-name-check ## Delete PostgreSQL cluster for service and its related secrets. Usage: make postgres-svc-purge SVC_NAME=<svc-name>
	@SECRET_NAME=$(ENV_NAME)-postgres-$(SVC_NAME)-user-creds ; \
	echo "Deleting PostgreSQL cluster '$(ENV_NAME)-postgres-$(SVC_NAME)' from namespace '$(DATA_NS)'..." ; \
	kubectl --context "$(CUR_CTX)" delete cluster.postgresql.cnpg.io -n "$(DATA_NS)" $(ENV_NAME)-postgres-"$(SVC_NAME)" || true ; \
	echo "\nDeleting PostgreSQL-related user creds secret for '$(SVC_NAME)' from namespace '$(DATA_NS)'..." ; \
	kubectl --context "$(CUR_CTX)" delete secret -n "$(DATA_NS)" "$$SECRET_NAME" || true ; \
	if [ "$(ENV_NAME)" = "qa" ]; then \
		echo "Deleting copied user creds secret from namespace '$(SVC_NS)'..." ; \
		kubectl --context "$(CUR_CTX)" delete secret -n "$(SVC_NS)" "$$SECRET_NAME" || true ; \
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
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" operator eck

.PHONY: es-operator-delete
es-operator-delete: _env-check ## Delete Elastic Cloud on Kubernetes (ECK) Operator
	@echo "Deleting ECK operator v$(ECK_OPERATOR_VERSION) from 'elastic-system' namespace..."
	@kubectl --context "$(CUR_CTX)" delete -f https://download.elastic.co/downloads/eck/$(ECK_OPERATOR_VERSION)/operator.yaml --ignore-not-found
	@kubectl --context "$(CUR_CTX)" delete -f https://download.elastic.co/downloads/eck/$(ECK_OPERATOR_VERSION)/crds.yaml --ignore-not-found
	@echo "🗑 ECK operator and namespace 'elastic-system' deleted."

.PHONY: es-deploy
es-deploy: _env-check _data-ns-ensure es-operator-deploy ## Deploy Elasticsearch cluster
	@echo "Deploying Elasticsearch cluster into '$(DATA_NS)' namespace...";
	@kubectl --context "$(CUR_CTX)" apply --server-side -n "$(DATA_NS)" -f "$(OVERLAYS_FILE_PATH)"/infra/elastic/elasticsearch.yaml;
	@echo "✅ Elasticsearch cluster deployment initiated. Check status with: make es-status"

.PHONY: es-elastic-user-secret-copy
es-elastic-user-secret-copy: _env-check _data-ns-ensure _svc-ns-ensure ## Copy Elasticsearch elastic user creds secret from data namespace to service namespace (QA only). Usage: make es-elastic-user-secret-copy
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" secret elasticsearch-copy

.PHONY: es-status
es-status: _env-check ## Show Elasticsearch cluster status
	@echo "Status for namespace: $(DATA_NS)"
	@echo "\nElasticsearch Pods:"
	@kubectl --context "$(CUR_CTX)" get pods -n "$(DATA_NS)" -l elasticsearch.k8s.elastic.co/cluster-name=$(ENV_NAME)-elasticsearch || true
	@echo "\nElasticsearch Services:"
	@kubectl --context "$(CUR_CTX)" get svc -n "$(DATA_NS)" | grep "$(ENV_NAME)-elasticsearch" || true
	@echo "\nElasticsearch PersistentVolumeClaims:"
	@kubectl --context "$(CUR_CTX)" get pvc -n "$(DATA_NS)" | grep "$(ENV_NAME)-elasticsearch" || true
	@echo "\nElasticsearch StatefulSets:"
	@kubectl --context "$(CUR_CTX)" get statefulset -n "$(DATA_NS)" | grep "$(ENV_NAME)-elasticsearch" || true
	@echo "\nElasticsearch Cluster:"
	@kubectl --context "$(CUR_CTX)" get elasticsearch -n "$(DATA_NS)" "$(ENV_NAME)-elasticsearch" -o wide || true

.PHONY: es-delete
es-delete: _env-check ## Delete Elasticsearch cluster only
	@echo "Deleting Elasticsearch cluster '$(ENV_NAME)-elasticsearch' from namespace $(DATA_NS)..."
	@kubectl --context "$(CUR_CTX)" delete elasticsearch "$(ENV_NAME)-elasticsearch" -n "$(DATA_NS)" --ignore-not-found || true
	@echo "🗑 Elasticsearch cluster deleted."

.PHONY: es-purge
es-purge: es-delete ## Delete Elasticsearch cluster and its related secrets and PVCs
	@echo "Deleting Elasticsearch-related secrets and PVCs from namespace $(DATA_NS)..."
	@kubectl --context "$(CUR_CTX)" delete secret -n "$(DATA_NS)" -l elasticsearch.k8s.elastic.co/cluster-name=$(ENV_NAME)-elasticsearch || true
	@kubectl --context "$(CUR_CTX)" delete pvc -n "$(DATA_NS)" -l elasticsearch.k8s.elastic.co/cluster-name=$(ENV_NAME)-elasticsearch || true
	@echo "🗑 Elasticsearch-related secrets and PVCs deleted."

.PHONY: es-exporter-deploy
es-exporter-deploy: _env-check ## Deploy Prometheus Elasticsearch exporter for monitoring
	@echo "Deploying Elasticsearch exporter into '$(DATA_NS)' namespace..."
	@kubectl --context "$(CUR_CTX)" -n "$(DATA_NS)" apply -f "$(OVERLAYS_FILE_PATH)"/infra/elastic/elasticsearch-exporter.yaml
	@echo "✅ Elasticsearch exporter deployment initiated."

.PHONY: es-exporter-delete
es-exporter-delete: _env-check ## Delete Prometheus Elasticsearch exporter
	@echo "Deleting Elasticsearch exporter from namespace $(DATA_NS)..."
	@kubectl --context "$(CUR_CTX)" -n "$(DATA_NS)" delete -f "$(OVERLAYS_FILE_PATH)"/infra/elastic/elasticsearch-exporter.yaml --ignore-not-found || true
	@echo "🗑 Elasticsearch exporter deleted."

################################################################################
# Kafka Deployment Targets
# Targets for deploying and managing Kafka in local dev and QA cluster
# Namespace and environment auto-detected from current kubectl context
################################################################################

STRIMZI_OPERATOR_VERSION := 0.48.0

.PHONY: kafka-strimzi-operator-install
kafka-strimzi-operator-install: _env-check ## Install Strimzi Kafka operator
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" operator kafka

kafka-strimzi-operator-uninstall: _env-check ## Uninstall Strimzi Kafka operator
	@echo "Uninstalling Strimzi Kafka operator..."
	@helm uninstall strimzi-operator --kube-context "$(CUR_CTX)" -n kafka-system || true
	@kubectl --context "$(CUR_CTX)" wait --for=delete namespace/kafka-system --timeout=60s 2>/dev/null || true
	@echo "🗑 Strimzi Kafka operator uninstalled and namespace 'kafka-system' deleted."

.PHONY: kafka-deploy
kafka-deploy: _env-check ## Deploy Kafka cluster
	@echo "Deploying Kafka cluster into '$(DATA_NS)' namespace..."
	@kubectl --context "$(CUR_CTX)" apply --server-side -n "$(DATA_NS)" -f "$(OVERLAYS_FILE_PATH)"/infra/kafka/resources.yaml
	@echo "✅ Kafka cluster deployment initiated. Check status with: make kafka-status"

.PHONY: kafka-status
kafka-status: _env-check ## Show Kafka cluster status
	@echo "Status for Kafka cluster in namespace: $(DATA_NS)"
	@echo "\nKafka Custom Resource:"
	@kubectl --context "$(CUR_CTX)" get kafka -n "$(DATA_NS)" "$(ENV_NAME)-kafka" -o wide || true
	@echo "\nKafka Pods:"
	@kubectl --context "$(CUR_CTX)" get pods -n "$(DATA_NS)" -l strimzi.io/cluster=$(ENV_NAME)-kafka || true
	@echo "\nKafka Services:"
	@kubectl --context "$(CUR_CTX)" get svc -n "$(DATA_NS)" -l strimzi.io/cluster=$(ENV_NAME)-kafka || true
	@echo "\nKafka PersistentVolumeClaims:"
	@kubectl --context "$(CUR_CTX)" get pvc -n "$(DATA_NS)" -l strimzi.io/cluster=$(ENV_NAME)-kafka || true
	@echo "\nKafka StrimziPodSets:"
	@kubectl --context "$(CUR_CTX)" get strimzipodsets -n "$(DATA_NS)"

.PHONY: kafka-delete
kafka-delete: _env-check ## Delete Kafka cluster only (preserves PVCs and StrimziPodSets)
	@echo "Deleting Kafka cluster '$(ENV_NAME)-kafka' from namespace $(DATA_NS)..."
	@kubectl --context "$(CUR_CTX)" delete kafka "$(ENV_NAME)-kafka" -n "$(DATA_NS)" --ignore-not-found || true
	@echo "⏳ Waiting 5s for Kafka resources to be cleaned up..."
	@sleep 5
	@echo "🗑️ Kafka cluster deleted."

.PHONY: kafka-purge
kafka-purge: kafka-delete ## Delete Kafka cluster and permanently remove PVCs, NodePools, and StrimziPodSets
	@echo "Deleting Kafka-related KafkaNodePools from namespace $(DATA_NS)..."
	@kubectl --context "$(CUR_CTX)" delete kafkanodepool -n "$(DATA_NS)" -l strimzi.io/cluster=$(ENV_NAME)-kafka || true

	@echo "Deleting Kafka-related StrimziPodSets from namespace $(DATA_NS)..."
	@kubectl --context "$(CUR_CTX)" delete strimzipodset -n "$(DATA_NS)" -l strimzi.io/cluster=$(ENV_NAME)-kafka || true

	@echo "Deleting Kafka-related PVCs from namespace $(DATA_NS)..."
	@kubectl --context "$(CUR_CTX)" delete pvc -n "$(DATA_NS)" -l strimzi.io/cluster=$(ENV_NAME)-kafka || true

	@echo "🗑️ Kafka-related NodePools, StrimziPodSets, and PVCs deleted."

.PHONY: kafka-topics-list
kafka-topics-list: _env-check ## List all Kafka topics
	@echo "Listing Kafka topics in cluster $(ENV_NAME)-kafka..."
	@kubectl --context "$(CUR_CTX)" get kafkatopic -n "$(DATA_NS)" || echo "No KafkaTopics found"

.PHONY: kafka-console-producer
kafka-console-producer: _env-check ## Access Kafka console producer. Usage: make kafka-console-producer TOPIC_NAME=<topic-name>
	@if [ -z "$(TOPIC_NAME)" ]; then \
		echo "ERROR: Argument 'TOPIC_NAME' is required! Usage: make kafka-console-producer TOPIC_NAME=<topic-name>"; \
		exit 1; \
	fi
	@echo "Starting Kafka console producer for topic: $(TOPIC_NAME)"
	@echo "Type messages and press Enter to send. Press Ctrl+C to exit."
	@kubectl --context "$(CUR_CTX)" run kafka-producer -ti --rm=true --restart=Never \
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
	@kubectl --context "$(CUR_CTX)" run kafka-consumer -ti --rm=true --restart=Never \
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
export MINIO_ROOT_USER

.PHONY: minio-operator-deploy
minio-operator-deploy: _env-check ## Deploy MinIO operator
	@PYTHONDWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" operator minio

.PHONY: minio-operator-delete
minio-operator-delete: _env-check ## Delete MinIO operator
	@echo "Deleting MinIO operator v$(MINIO_OPERATOR_VERSION) from 'minio-operator' namespace..."
	@kubectl --context "$(CUR_CTX)" delete -k "$(OVERLAYS_FILE_PATH)"/infra/minio/operator
	@echo "🗑 MinIO operator deleted."

.PHONY: minio-storage-user-secret-create
minio-storage-user-secret-create: _env-check _svc-name-check ## Create/update MinIO storage user credentials secret. Usage: make minio-storage-user-secret-create SVC_NAME=<svc-name> MINIO_CONSOLE_ACCESS_KEY_FILE=</absolute/mode-0600-file> MINIO_CONSOLE_SECRET_KEY_FILE=</absolute/mode-0600-file>
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" secret minio-storage-user

.PHONY: minio-storage-config-secret-create
minio-storage-config-secret-create: _env-check _svc-name-check ## Create/update MinIO storage configuration secret. Usage: make minio-storage-config-secret-create SVC_NAME=<svc-name> MINIO_ROOT_USER=<root-user> MINIO_ROOT_USER_PWD_FILE=</absolute/mode-0600-file>
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" secret minio-storage-config

.PHONY: minio-tenant-deploy
minio-tenant-deploy: _env-check _svc-name-check ## Deploy MinIO tenant for service. Usage: make minio-tenant-deploy SVC_NAME=<svc-name>
	@NAMESPACE="$(ENV_NAME)-minio-$$SVC_NAME"; \
	kubectl --context "$(CUR_CTX)" get namespace "$$NAMESPACE" >/dev/null 2>&1 || kubectl --context "$(CUR_CTX)" create namespace "$$NAMESPACE"; \
	echo "Deploying MinIO tenant for '$$SVC_NAME' into '$$NAMESPACE' namespace..."; \
	kubectl --context "$(CUR_CTX)" kustomize "$(OVERLAYS_FILE_PATH)/infra/minio/$$SVC_NAME" | kubectl --context "$(CUR_CTX)" -n "$$NAMESPACE" apply --server-side -f -; \
	echo "✅ MinIO cluster for '$$SVC_NAME' deployment initiated. Check status with: make minio-tenant-status SVC_NAME=$$SVC_NAME"

.PHONY: minio-tenant-status
minio-tenant-status: _env-check _svc-name-check ## Show MinIO tenant status for service. Usage: make minio-tenant-status SVC_NAME=<svc-name>
	@NAMESPACE="$(ENV_NAME)-minio-$$SVC_NAME"; \
	TENANT_NAME="$$NAMESPACE"; \
	echo "Status for MinIO tenant in namespace: $$NAMESPACE"; \
	echo "\nMinIO Tenant:"; \
	kubectl --context "$(CUR_CTX)" get tenant "$$TENANT_NAME" -n "$$NAMESPACE" -o wide || true; \
	echo "\nMinIO Pods:"; \
	kubectl --context "$(CUR_CTX)" get pods -n "$$NAMESPACE" || true; \
	echo "\nMinIO Services:"; \
	kubectl --context "$(CUR_CTX)" get svc -n "$$NAMESPACE" || true; \
	echo "\nMinIO PersistentVolumeClaims:"; \
	kubectl --context "$(CUR_CTX)" get pvc -n "$$NAMESPACE" || true; \
	echo "\nMinIO ServiceMonitors:"; \
	kubectl --context "$(CUR_CTX)" get servicemonitor -n "$$NAMESPACE" || true

.PHONY: minio-console-ui
minio-console-ui: _env-check _svc-name-check ## Access MinIO Console UI via port-forward. Usage: make minio-console-ui SVC_NAME=<svc-name>
	@NAMESPACE="$(ENV_NAME)-minio-$(SVC_NAME)"; \
	echo "Forwarding MinIO Console for '$(SVC_NAME)' to https://localhost:9443"; \
	echo "Press Ctrl+C to stop."; \
	kubectl --context "$(CUR_CTX)" port-forward -n $$NAMESPACE svc/$$NAMESPACE-console 9443:9443

.PHONY: minio-tenant-delete
minio-tenant-delete: _env-check _svc-name-check ## Delete MinIO tenant for service. Usage: make minio-tenant-delete SVC_NAME=<svc-name>
	@NAMESPACE="$(ENV_NAME)-minio-$$SVC_NAME"; \
	TENANT_NAME="$$NAMESPACE"; \
	echo "Deleting MinIO tenant '$$TENANT_NAME' from namespace '$$NAMESPACE'..."; \
	kubectl --context "$(CUR_CTX)" delete tenant "$$TENANT_NAME" -n "$$NAMESPACE" --ignore-not-found || true; \
	kubectl --context "$(CUR_CTX)" delete servicemonitor "$(ENV_NAME)-minio-$$SVC_NAME-service-monitor" -n "$$NAMESPACE" --ignore-not-found || true

.PHONY: minio-tenant-purge
minio-tenant-purge: minio-tenant-delete _svc-name-check ## Delete MinIO tenant and related secrets/PVCs. Usage: make minio-tenant-purge SVC_NAME=<svc-name>
	@NAMESPACE="$(ENV_NAME)-minio-$$SVC_NAME"; \
	echo "Deleting MinIO-related secrets and PVCs from namespace $$NAMESPACE..."; \
	kubectl --context "$(CUR_CTX)" delete secret -n "$$NAMESPACE" --all || true; \
	kubectl --context "$(CUR_CTX)" delete pvc -n "$$NAMESPACE" --all || true; \
	echo "Deleting namespace $$NAMESPACE..."; \
	kubectl --context "$(CUR_CTX)" delete namespace "$$NAMESPACE" --ignore-not-found || true

################################################################################
# MinIO Management Targets
# Targets for managing MinIO buckets and policies for services.
# Namespace and environment auto-detected from current kubectl context
################################################################################

export BUCKET_NAME POLICY_FILE

.PHONY: minio-svc-bucket-create
minio-svc-bucket-create: _env-check _svc-name-check ## Create a MinIO bucket for service. Usage: make minio-svc-bucket-create SVC_NAME=<svc-name> BUCKET_NAME=<bucket-name>
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" minio bucket

.PHONY: minio-svc-user-secret-create
minio-svc-user-secret-create: _env-check _svc-name-check ## Create/update MinIO credentials secret for service account. Usage: make minio-svc-user-secret-create SVC_NAME=<svc-name> [COPY_SECRET_NS=<target-namespace>] MINIO_SVC_ACCESS_KEY_FILE=</absolute/mode-0600-file> MINIO_SVC_SECRET_KEY_FILE=</absolute/mode-0600-file>
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" secret minio-service-user

.PHONY: minio-svc-user-with-policy-create
minio-svc-user-with-policy-create: _env-check _svc-name-check ## Create a MinIO service account and apply an S3 policy. Usage: make minio-svc-user-with-policy-create SVC_NAME=<svc-name> POLICY_FILE=<path/to/policy.json>
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" minio user-policy

################################################################################
# MongoDB Deployment Targets
# Targets for deploying and managing MongoDB in local dev and QA cluster
# Namespace and environment auto-detected from current kubectl context
################################################################################

MONGODB_OPERATOR_VERSION := 0.13.0

.PHONY: mongodb-operator-install
mongodb-operator-install: _env-check ## Install MongoDB Community Operator
	@PYTHONDWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" operator mongodb

.PHONY: mongodb-operator-uninstall
mongodb-operator-uninstall: _env-check ## Delete MongoDB Community Operator
	@echo "Uninstalling MongoDB operator from namespace $(DATA_NS)..."
	@helm uninstall mongodb-operator --kube-context "$(CUR_CTX)" -n "$(DATA_NS)" || true
	@echo "🗑 MongoDB operator uninstalled."

.PHONY: mongodb-root-user-secret-create
mongodb-root-user-secret-create: _env-check _data-ns-ensure ## Create/update secret for MongoDB 'root' user. Usage: make mongodb-root-secret-create MONGO_ROOT_USER_PWD_FILE=</absolute/mode-0600-file>
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" secret mongodb-root

.PHONY: mongodb-product-user-secret-create
mongodb-product-user-secret-create: _env-check _data-ns-ensure _svc-ns-ensure ## Create/update secret for MongoDB product-service user. Usage: make mongodb-product-user-secret-create MONGO_PRODUCT_USER_PWD_FILE=</absolute/mode-0600-file>
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" secret mongodb-product

.PHONY: mongodb-deploy
mongodb-deploy: _env-check _data-ns-ensure ## Deploy MongoDB cluster
	@echo "Deploying MongoDB cluster to '$(DATA_NS)' namespace...";
	@kubectl --context "$(CUR_CTX)" apply --server-side -n "$(DATA_NS)" -f "$(OVERLAYS_FILE_PATH)"/infra/mongodb/mongodbcommunity.yaml;
	@echo "✅ MongoDB cluster deployment initiated. Check status with: make mongodb-status"

.PHONY: mongodb-status
mongodb-status: _env-check ## Show MongoDB cluster status
	@echo "Status for namespace: $(DATA_NS)"
	@echo "\nMongoDB Pods:"
	@kubectl --context "$(CUR_CTX)" get pods -n "$(DATA_NS)" -l app="$(ENV_NAME)"-mongodb-svc || true
	@echo "\nMongoDB Services:"
	@kubectl --context "$(CUR_CTX)" get svc -n "$(DATA_NS)" | grep "$(ENV_NAME)"-mongodb || true
	@echo "\nMongoDB PersistentVolumeClaims:"
	@kubectl --context "$(CUR_CTX)" get pvc -n "$(DATA_NS)" | grep "$(ENV_NAME)"-mongodb || true
	@echo "\nStatefulSets:"
	@kubectl --context "$(CUR_CTX)" get statefulset -n "$(DATA_NS)" | grep "$(ENV_NAME)"-mongodb || true

.PHONY: mongodb-delete
mongodb-delete: _env-check ## Delete MongoDB cluster only
	@echo "Deleting MongoDB cluster '$(ENV_NAME)-mongodb' from namespace $(DATA_NS)..."
	@kubectl --context "$(CUR_CTX)" delete mongodbcommunity "$(ENV_NAME)"-mongodb -n "$(DATA_NS)" --ignore-not-found || true
	@echo "🗑 MongoDB cluster deleted."

.PHONY: mongodb-purge
mongodb-purge: mongodb-delete ## Delete MongoDB cluster and permanently remove related secrets and PVCs
	@echo "Deleting MongoDB-related secrets and PVCs from namespace $(DATA_NS)..."
	@kubectl --context "$(CUR_CTX)" delete secret -n "$(DATA_NS)" $(ENV_NAME)-mongodb-root-user-creds || true
	@kubectl --context "$(CUR_CTX)" delete secret -n "$(DATA_NS)" $(ENV_NAME)-mongodb-product-user-creds || true
	@kubectl --context "$(CUR_CTX)" delete pvc -n "$(DATA_NS)" -l app="$(ENV_NAME)"-mongodb-svc || true
	@echo "🗑 MongoDB-related secrets and PVCs deleted."

################################################################################
# JSReport Deployment Targets
# Targets for deploying and managing JSReport in local dev and QA cluster
# Namespace and environment auto-detected from current kubectl context
################################################################################

.PHONY: jsreport-deploy
jsreport-deploy: _env-check ## Deploy JSReport instance
	@kubectl --context "$(CUR_CTX)" get namespace "$(SVC_NS)" >/dev/null 2>&1 || kubectl --context "$(CUR_CTX)" create namespace "$(SVC_NS)"
	@echo "Deploying JSReport cluster into '$(SVC_NS)' namespace..."
	@kubectl --context "$(CUR_CTX)" kustomize "$(OVERLAYS_FILE_PATH)/svc/jsreport" | kubectl --context "$(CUR_CTX)" -n "$(SVC_NS)" apply --server-side -f -
	@echo "✅ JSReport deployment initiated. Check status with: make jsreport-status"

.PHONY: jsreport-status
jsreport-status: _env-check ## Show JSReport status (pod, service, PVC only)
	@echo "Status for JSReport in namespace: $(SVC_NS)"
	@echo "\nJSReport Pod:"
	@kubectl --context "$(CUR_CTX)" get pod -n "$(SVC_NS)" -l app=jsreport || true
	@echo "\nJSReport Service:"
	@kubectl --context "$(CUR_CTX)" get svc "$(ENV_NAME)"-jsreport -n "$(SVC_NS)" || true
	@echo "\nJSReport PVC:"
	@kubectl --context "$(CUR_CTX)" get pvc "$(ENV_NAME)"-jsreport-cache-pvc -n "$(SVC_NS)" || true

.PHONY: jsreport-delete
jsreport-delete: _env-check ## Delete JSReport deployment
	@echo "Deleting JSReport resources from namespace $(SVC_NS)..."
	@kubectl --context "$(CUR_CTX)" delete -k "$(OVERLAYS_FILE_PATH)/svc/jsreport" -n "$(SVC_NS)" --ignore-not-found || true
	@echo "⏳ Waiting 5s for JSReport resources to be cleaned up..."
	@sleep 5

.PHONY: jsreport-purge
jsreport-purge: jsreport-delete _env-check ## Purge JSReport PVC
	@echo "Deleting JSReport-related PVC from namespace $(SVC_NS)..."
	@kubectl --context "$(CUR_CTX)" delete pvc "$(ENV_NAME)"-jsreport-cache-pvc -n "$(SVC_NS)" --ignore-not-found || true
	@echo "🗑 JSReport cluster and its persistent data deleted."

.PHONY: jsreport-ui
jsreport-ui: _env-check ## Access Grafana UI via port-forward
	@echo "Forwarding JSReport UI to http://localhost:5488"
	@echo "Press Ctrl+C to stop."
	@kubectl --context "$(CUR_CTX)" port-forward svc/"$(ENV_NAME)"-jsreport 5488:5488 -n $(SVC_NS)

################################################################################
# Zipkin Deployment Targets
# Targets for deploying and managing Zipkin cluster per app service in QA cluster
# Namespace is auto-detected from the current kubectl context
################################################################################

ZIPKIN_VERSION := 0.4.0
ZIPKIN_ES_ROLE := zipkin_role
ZIPKIN_ES_USER := zipkin_user
export ZIPKIN_ES_USER

.PHONY: zipkin-es-user-secret-create
zipkin-es-user-secret-create: _env-check _monitoring-ns-ensure ## Create/update secret for Elasticsearch's Zipkin user. Requires ZIPKIN_ES_USER_PWD_FILE with an absolute mode-0600 file.
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" secret zipkin

.PHONY: zipkin-es-user-create
zipkin-es-user-create: _env-check _monitoring-ns-ensure ## Create role and user for Zipkin in Elasticsearch in QA cluster.
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" zipkin-user \
		--role "$(ZIPKIN_ES_ROLE)" --user "$(ZIPKIN_ES_USER)"

.PHONY: zipkin-install
zipkin-install: _env-check _monitoring-ns-ensure ## Install Zipkin in QA cluster
	@echo "Installing Zipkin using Helm chart v$(ZIPKIN_VERSION) into $(MONITORING_NS) namespace..."
	@helm repo add zipkin https://zipkin.io/zipkin-helm
	@helm repo update
	@helm upgrade --install $(ENV_NAME)-zipkin zipkin/zipkin \
	   --version $(ZIPKIN_VERSION) \
	   --kube-context "$(CUR_CTX)" \
	   --values "$(OVERLAYS_FILE_PATH)"/infra/zipkin/values.yaml \
	   --namespace $(MONITORING_NS)
	@echo "Disabling Zipkin deployment readiness probe..."
	@kubectl --context "$(CUR_CTX)" patch deployment $(ENV_NAME)-zipkin -n $(MONITORING_NS) --type=json -p='[{"op": "remove", "path": "/spec/template/spec/containers/0/readinessProbe"}]'
	@echo "Waiting for Zipkin rollout after disabling probe..."
	@kubectl --context "$(CUR_CTX)" rollout status deployment/$(ENV_NAME)-zipkin -n $(MONITORING_NS) --timeout=2m
	@echo "✅ Zipkin installation initiated. Check status with: make zipkin-status"

.PHONY: zipkin-uninstall
zipkin-uninstall: _env-check _monitoring-ns-check ## Uninstall Zipkin from QA cluster
	@echo "Uninstalling Zipkin from namespace '$(MONITORING_NS)'..."
	@helm uninstall $(ENV_NAME)-zipkin --kube-context "$(CUR_CTX)" -n $(MONITORING_NS) || true
	@echo "🗑 Zipkin uninstalled."

.PHONY: zipkin-status
zipkin-status: _env-check _monitoring-ns-check ## Show Zipkin deployment status in QA cluster
	@echo "Status for Zipkin in namespace: $(MONITORING_NS)"
	@echo "\nZipkin Deployment:"
	@kubectl --context "$(CUR_CTX)" get deployment $(ENV_NAME)-zipkin -n $(MONITORING_NS) -o wide || true
	@echo "\nZipkin Pods:"
	@kubectl --context "$(CUR_CTX)" get pods -n $(MONITORING_NS) -l app.kubernetes.io/name=zipkin || true
	@echo "\nZipkin Service:"
	@kubectl --context "$(CUR_CTX)" get svc $(ENV_NAME)-zipkin -n $(MONITORING_NS) || true
	@echo "\nZipkin Endpoint Slice:"
	@kubectl --context "$(CUR_CTX)" get endpointslice -l kubernetes.io/service-name=$(ENV_NAME)-zipkin -n $(MONITORING_NS) || true

.PHONY: zipkin-ui
zipkin-ui: ## Access Zipkin UI via port-forward
	@echo "Forwarding Zipkin UI to http://localhost:9411"
	@echo "Press Ctrl+C to stop."
	@kubectl --context "$(CUR_CTX)" port-forward svc/$(ENV_NAME)-zipkin 9411:9411 -n $(MONITORING_NS)

################################################################################
# Services Deployment Targets
# Targets for deploying and managing Insurance Hub services and front-end apps
# in local dev and QA cluster
# Namespace is auto-detected from the current kubectl context
################################################################################

.PHONY: svc-image-load
svc-image-load: _svc-name-check ## Load a locally built Docker image into the target cluster based on ENV_NAME. Usage: make svc-image-load ENV_NAME=<env> SVC_NAME=<svc-name>
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" image load

.PHONY: svc-image-local-dev-load
svc-image-local-dev-load: _svc-name-check ## Loads a locally built Docker image into the local-dev cluster. Usage: make svc-image-local-dev-load SVC_NAME=<svc-name>
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" image local-dev-load

.PHONY: svc-image-local-dev-unload
svc-image-local-dev-unload: _svc-name-check ## Unloads (removes) a Docker image from the local-dev Kind cluster nodes. Usage: make svc-image-local-dev-unload SVC_NAME=<svc-name>
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" image local-dev-unload


.PHONY: svc-image-qa-load
svc-image-qa-load: _svc-name-check ## Loads a locally built Docker image into the QA LXD cluster nodes. Usage: make svc-image-qa-load SVC_NAME=<svc-name>
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" image qa-load

.PHONY: svc-deploy
svc-deploy: _env-check _svc-ns-ensure _svc-name-check ## Deploy a Java service or web app using Kustomize. Usage: make svc-deploy SVC_NAME=<svc-name>
	@SVC_NAME_LABEL="$(SVC_NAME)-api-legacy"; \
	if [ "$(SVC_NAME)" = "agent-portal-gateway" ] || [ "$(SVC_NAME)" = "web-vue" ]; then \
		SVC_NAME_LABEL="$(SVC_NAME)-legacy"; \
	fi; \
	echo "Deploying legacy service '$(SVC_NAME)' to '$(SVC_NS)' namespace..."; \
	kubectl --context "$(CUR_CTX)" apply -k "overlays/$(ENV_NAME)/svc/$(SVC_NAME)/legacy" -n "$(SVC_NS)"; \
	echo "✅ Service '$(SVC_NAME)' deployment initiated."; \
	echo "Check status with: kubectl get deploy,rs,po,svc,endpointslices -n $(SVC_NS) -l app.kubernetes.io/name=$$SVC_NAME_LABEL"

.PHONY: svc-delete
svc-delete: _env-check _svc-name-check ## Delete (purge) a deployed Java service or web app using Kustomize. Usage: make svc-delete SVC_NAME=<svc-name>
	@echo "Deleting legacy service '$(SVC_NAME)' from '$(SVC_NS)' namespace..."
	@kubectl --context "$(CUR_CTX)" delete -k "overlays/$(ENV_NAME)/svc/$(SVC_NAME)/legacy" -n "$(SVC_NS)" --ignore-not-found
	@echo "🗑 Service '$(SVC_NAME)' deleted."

.PHONY: svc-load-delete-deploy
svc-load-delete-deploy: _env-check _svc-ns-check _svc-name-check svc-image-load svc-delete svc-deploy ## Load Docker image, delete and deploy a Java service or web app using Kustomize. Usage: make svc-load-delete-deploy SVC_NAME=<svc-name>

##############################################################################################
# Local Dev Cluster Infra and Services Deployment Targets
# Targets for deploying and managing Insurance Hub infra and service apps in local dev cluster
##############################################################################################

.PHONY: legacy-all-build
legacy-all-build: ## Build all legacy artifacts sequentially (Java → Frontend → Docker). Usage: legacy-all-build
	@echo "🚀 Building all legacy artifacts (Java → Frontend → Docker)..."

	$(MAKE) java-all-build
	$(MAKE) frontend-build
	$(MAKE) docker-frontend-build
	$(MAKE) docker-java-svc-all-build
	@docker images | grep insurance-hub

	@echo "✅ Legacy all legacy artifacts build completed!"
	@echo "📦 Ready for k8s deployment!"


.PHONY: cluster-qa-monitoring-deploy
cluster-qa-monitoring-deploy: _env-check _data-ns-ensure ## Deploy monitoring stack (Prometheus, Grafana, Loki, Tempo, Zipkin, Alloy) into qa cluster. Usage: make cluster-qa-monitoring-deploy
	@echo "🚀 Deploying monitoring stack into '$(ENV_NAME)' cluster..."
	@if [ "$(ENV_NAME)" != "qa" ]; then \
		echo "ERROR: This target is only applicable for the 'qa' environment."; \
		exit 1; \
	fi

	# Elasticsearch
	$(MAKE) es-deploy

	# Prometheus & Grafana stack
	$(MAKE) prometheus-stack-install

    # Monitoring operator
	$(MAKE) minio-operator-deploy

	# MinIO Monitoring Tenants
	$(MAKE) minio-storage-user-secret-create SVC_NAME=loki
	$(MAKE) minio-storage-config-secret-create SVC_NAME=loki
	$(MAKE) minio-storage-user-secret-create SVC_NAME=tempo
	$(MAKE) minio-storage-config-secret-create SVC_NAME=tempo
	$(MAKE) minio-tenant-deploy SVC_NAME=loki & $(MAKE) minio-tenant-deploy SVC_NAME=tempo & wait
	@echo "⏳ Waiting 3m for MinIO monitoring tenants.." && sleep 180

    # MinIO Monitoring Tenant Configuration
	# Loki
	$(MAKE) minio-svc-bucket-create SVC_NAME=loki BUCKET_NAME=loki-logs
	$(MAKE) minio-svc-user-secret-create SVC_NAME=loki COPY_SECRET_NS=$(MONITORING_NS)
	$(MAKE) minio-svc-user-with-policy-create SVC_NAME=loki POLICY_FILE=apps/infra/loki/minio/s3-policy-loki-logs.json

	# Tempo
	$(MAKE) minio-svc-bucket-create SVC_NAME=tempo BUCKET_NAME=tempo-traces
	$(MAKE) minio-svc-user-secret-create SVC_NAME=tempo COPY_SECRET_NS=$(MONITORING_NS)
	$(MAKE) minio-svc-user-with-policy-create SVC_NAME=tempo POLICY_FILE=apps/infra/tempo/minio/s3-policy-tempo-traces.json

	# Zipkin (requires Elasticsearch)
	$(MAKE) zipkin-es-user-secret-create
	$(MAKE) zipkin-es-user-create
	$(MAKE) zipkin-install

	# Grafana Loki,Tempo and Alloy
	$(MAKE) loki-install
	$(MAKE) tempo-install
	$(MAKE) alloy-install

	@echo "✅ QA cluster monitoring stack deployed!"

.PHONY: cluster-infra-deploy
cluster-infra-deploy: _env-check ## Deploy all infra apps (except monitoring stack) into either local-dev or qa cluster. Usage: make cluster-infra-deploy
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" deploy infra

.PHONY: cluster-svc-deploy
cluster-svc-deploy:  _env-check ## Deploy all services into either local-dev or qa cluster. Usage: make cluster-svc-deploy
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(K8S_ADMIN_SCRIPT)" deploy services

ifndef INSURANCE_HUB_PYTHON_TOOLCHAIN_INCLUDED
include $(K8S_REPOSITORY_ROOT)/scripts/python/toolchain.mk
endif
