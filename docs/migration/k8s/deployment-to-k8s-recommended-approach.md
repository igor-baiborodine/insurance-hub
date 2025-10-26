## Recommended Strategy: Hybrid Approach (Operators + Kustomize)

### Core Principles

- **Kubernetes operators** should manage all production-like, stateful infrastructure components 
  (PostgreSQL, MongoDB, Kafka, Elasticsearch, etc.) in both local dev and QA environments.
- For **local dev**, operators are configured for non-HA, single-node, minimal resources, no
  monitoring.
- For **QA**, operators drive HA production-like architectures.
- **Kustomize** will be used for Go microservices deployment.
- **Parallels** between dev and QA maximize environment parity and reduce surprises.
- **Namespace isolation** applies only to QA.

### Directory Structure

```text
insurance-hub/
└── k8s/
    ├── apps/
    │   ├── infra/      # All third-party, operator-managed infra
    │   │   ├── postgres/
    │   │   ├── mongodb/
    │   │   ├── kafka/
    │   │   ├── elasticsearch/
    │   │   └── minio/
    │   └── svc/
    │       ├── auth/
    │       ├── policy/
    │       └── ...
    ├── env/
    │   ├── local-dev/
    │   │   ├── postgres.yaml         # Operator manifest, minimal dev config
    │   │   ├── kafka.yaml
    │   │   ├── elasticsearch.yaml
    │   │   └── kustomization.yaml
    │   └── qa/
    │       ├── postgres.yaml         # Operator manifest, HA config
    │       ├── kafka.yaml
    │       ├── elasticsearch.yaml
    │       └── kustomization.yaml
    └── tests/
```

- Operator manifests reside in `apps/infra/<component>/`.
- Environment-specific Kubernetes manifests and Kustomize overlays are in `env/local-dev/`, `env/qa/`.

### Namespace Isolation

- **QA:** 

| Component             | Namespace     | 
|-----------------------|---------------|
| Java/Go microservices | qa-apps       |
| JSReport              | qa-apps       |
| PostgreSQL            | qa-data       |
| MongoDB               | qa-data       |
| Elasticsearch         | qa-data       |
| Kafka                 | qa-data       |
| MinIO                 | qa-data       |
| TarantoolDB           | qa-data       |
| KeyCloak              | qa-auth       |
| Envoy Proxy           | qa-networking |
| Zipkin                | qa-monitoring |
| Prometheus            | qa-monitoring |
| Grafana               | qa-monitoring |
| Loki                  | qa-monitoring |
| Tempo                 | qa-monitoring |

- **Local dev:** All resources are in the `local-dev-all` namespace.

### PostgreSQL Deployment Example (Operator-based)

**Operator manifest directory**: `k8s/apps/infra/postgres/`

- Contains the Operator CRDs, RBAC, and controller manifests.

**Environment manifests**: 

**`k8s/env/local-dev/postgresql.yaml`**

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

**`k8s/env/qa/postgresql.yaml`**

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
	@kubectl apply -f apps/infra/postgres/
	@kubectl apply -f env/local-dev/postgres.yaml

.PHONY: deploy-postgresql-qa
deploy-postgresql-qa: ## Deploy PostgreSQL Operator (HA, production-like)
	@echo "Applying PostgreSQL operator and QA HA instance..."
	@kubectl apply -f apps/infra/postgres/
	@kubectl apply -f env/qa/postgres.yaml

.PHONY: undeploy-postgresql
undeploy-postgresql: ## Remove PostgreSQL resources
	@kubectl delete -f env/local-dev/postgres.yaml || true
	@kubectl delete -f env/qa/postgres.yaml || true

.PHONY: postgresql-status
postgresql-status: ## Check PostgreSQL resource status
	@kubectl get pods,svc,pvc -n local-dev -l app.kubernetes.io/name=postgresql || true
	@kubectl get pods,svc,pvc -n qa-data -l app.kubernetes.io/name=postgresql || true
```

- Adjust manifests/deletion commands per environment as needed.

### Go Microservice Deployment (Kustomize)

Use Kustomize overlays for Go services, for example, structure for `auth-service`:
```text
k8s/apps/svc/auth-service/
├── base/
│   ├── deployment.yaml
│   ├── service.yaml
│   ├── configmap.yaml
│   └── kustomization.yaml
└── overlays/
    ├── local-dev/
    │   ├── kustomization.yaml
    │   └── patches/
    │       └── replicas.yaml
    └── qa/
        ├── kustomization.yaml
        └── patches/
            ├── replicas.yaml
            └── resources.yaml
```

### Summary of the Hybrid Operator + Kustomize Model

- **Operators** automate the lifecycle of stateful services for all environments, eliminating manual
  and error-prone custom YAML or Helm for data systems.
- **Parity** between environments: dev uses single-node, HA/monitoring disabled; QA uses full HA,
  with monitoring/backups enabled.
- **Kustomize** is used for stateless Go microservices.
- **Structured directory** yields clarity, maintainability, and scalability for future cloud-native
  growth.

This model is considered industry best practice for Kubernetes production readiness and developer
efficiency.
