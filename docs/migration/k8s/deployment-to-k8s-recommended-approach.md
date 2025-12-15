## Recommended Strategy: Hybrid Approach (Operators + Kustomize)

### Core Principles

- **Kubernetes operators** manage all production-like, stateful infrastructure components 
  (PostgreSQL, MongoDB, Kafka, Elasticsearch, MinIO, etc.) in both local dev and QA environments.
- For **local dev**, operators are configured for non-HA, single-node, minimal resources, no
  monitoring.
- For **QA**, operators drive HA, production-like architectures.
- **Kustomize** is used for Java/Go microservices deployment via a clear `base` + `overlays` model.
- **Parallels** between dev and QA maximize environment parity and reduce surprises.
- **Namespace isolation** applies only to QA; local dev uses a single namespace for all resources (if possible).

### Directory Structure

```text
insurance-hub/
└── k8s/
    ├── apps/
    │   ├── infra/              # All third-party, operator-managed infra
    │   │   ├── postgres/
    │   │   ├── kafka/
    │   │   └── ...
    │   └── svc/                # Per-service Kustomize bases
    │       ├── auth/
    │       ├── policy/
    │       └── ...
    ├── overlays/
    │   ├── local-dev/          # Environment-level entrypoint (Kind-based dev)
    │   │   ├── infra/
    │   │   │   ├── postgres/
    │   │   │   ├── kafka/
    │   │   │   └── ...
    │   │   └── svc/            # Per-service Kustomize overlays
    │   │       ├── auth/
    │   │       ├── policy/
    │   │       └── ...
    │   └── qa/                 # Environment-level entrypoint (QA cluster)
    │       ├── infra/
    │       │   ├── postgres/
    │       │   ├── kafka/
    │       │   └── ...
    │       └── svc/            # Per-service Kustomize overlays
    │           ├── auth/
    │           ├── policy/
    │           └── ...
    └── tests/
```

- Operator manifests reside in `k8s/apps/infra/<component>/`.
- Service-level Kustomize bases live in `k8s/apps/svc/<service>/`.
- Environment-level Kustomize compositions live in `k8s/overlays/<env>/` (they reference infra and
  service overlays).

### Namespace Isolation

- **QA:**

| Component             | Namespace         |
|-----------------------|-------------------|
| Java/Go microservices | qa-svc            |
| JSReport              | qa-svc            |
| PostgreSQL            | qa-data           |
| MongoDB               | qa-data           |
| Elasticsearch         | qa-data           |
| Kafka                 | qa-data           |
| MinIO                 | qa-minio-<tenant> |
| KeyCloak              | qa-auth           |
| Envoy Proxy           | qa-networking     |
| Zipkin                | qa-monitoring     |
| Prometheus            | qa-monitoring     |
| Grafana               | qa-monitoring     |
| Loki                  | qa-monitoring     |
| Tempo                 | qa-monitoring     |

- **Local dev:** All resources are in the `local-dev-all` namespace except for MinIO.

### PostgreSQL Deployment Example (Operator-based)

**Operator manifest directory**: `k8s/apps/infra/postgres/`

- Contains the operator CRDs, RBAC, and controller manifests.

**Environment manifests** (referenced from `k8s/overlays/<env>/postgres/kustomization.yaml`):

**`k8s/overlays/local-dev/infra/postgresql.yaml`**

```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: postgresql-dev
  namespace: local-dev-all
spec:
  instances: 1
  storage:
    size: 2Gi
    storageClass: standard
  resources:
    requests:
      memory: "256Mi"
      cpu: "250m"
    limits:
      memory: "512Mi"
      cpu: "500m"
  monitoring: false
  backup: false
```

**`k8s/overlays/qa/postgresql.yaml`**

```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: postgresql-qa
  namespace: qa-data
spec:
  instances: 3
  storage:
    size: 10Gi
    storageClass: local-path
  resources:
    requests:
      memory: "512Mi"
      cpu: "500m"
    limits:
      memory: "1Gi"
      cpu: "1000m"
  monitoring: true
  backup: true
```

**Makefile targets for deployment**:

```makefile
.PHONY: deploy-postgresql-local-dev
deploy-postgresql-local-dev: ## Deploy PostgreSQL Operator (single node, dev)
	@echo "Applying PostgreSQL operator and dev instance..."
	@kubectl apply -f k8s/apps/infra/postgres/
	@kubectl apply -f k8s/overlays/local-dev/postgresql.yaml

.PHONY: deploy-postgresql-qa
deploy-postgresql-qa: ## Deploy PostgreSQL Operator (HA, production-like)
	@echo "Applying PostgreSQL operator and QA HA instance..."
	@kubectl apply -f k8s/apps/infra/postgres/
	@kubectl apply -f k8s/overlays/qa/postgresql.yaml

.PHONY: undeploy-postgresql
undeploy-postgresql: ## Remove PostgreSQL resources
	@kubectl delete -f k8s/overlays/local-dev/postgresql.yaml || true
	@kubectl delete -f k8s/overlays/qa/postgresql.yaml || true

.PHONY: postgresql-status
postgresql-status: ## Check PostgreSQL resource status
	@kubectl get pods,svc,pvc -n local-dev-all -l app.kubernetes.io/name=postgresql || true
	@kubectl get pods,svc,pvc -n qa-data -l app.kubernetes.io/name=postgresql || true
```

- Adjust manifests and deletion commands per environment as needed.

### Microservice Deployment (Kustomize)

Use Kustomize bases and overlays for each service, for example `auth-service`:

```text
k8s/apps/svc/auth-service/
├── base/
│   ├── deployment.yaml
│   ├── service.yaml
│   ├── configmap.yaml
│   └── kustomization.yaml
└── overlays
    ├── local-dev/svc/auth-service/
    │   ├── kustomization.yaml
    │   └── patches/
    │       └── replicas.yaml
    └── qa/svc/auth-service/
        ├── kustomization.yaml
        └── patches/
            ├── replicas.yaml
            └── resources.yaml
```

- Environment-level entrypoints (`k8s/overlays/local-dev`, `k8s/overlays/qa`) include the
  appropriate service overlays as resources, alongside infra resources.

### Summary of the Hybrid Operator + Kustomize Model

- **Operators** automate the lifecycle of stateful services for all environments, eliminating manual
  and error-prone custom YAML or Helm for data systems.
- **Parity** between environments: local dev uses single-node, monitoring/HA disabled; QA uses full
  HA with monitoring and backups enabled.
- **Kustomize** is used for stateless Java/Go microservices via a consistent `base` + `overlays`
  layout.
- A **structured directory** (`apps/infra`, `apps/svc`, `overlays/<env>`) yields clarity,
  maintainability, and scalability for future cloud-native growth.

This model aligns with industry best practices for Kubernetes production readiness and developer
efficiency.