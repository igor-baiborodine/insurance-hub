#!/usr/bin/env bash
set -euo pipefail

fail() {
  echo "ERROR: $*" >&2
  exit 2
}

require_command() {
  local command_name="$1"
  command -v "${command_name}" >/dev/null 2>&1 || \
    fail "Required command '${command_name}' is not available."
}

readonly script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly repo_root="$(cd "${script_dir}/../.." && pwd)"
readonly environment="${BASELINE_ENV:-}"
readonly part="${BASELINE_PART:-}"

[[ -n "${environment}" ]] || fail "BASELINE_ENV is required; use local-dev or qa."
[[ "${part}" == "inventory" ]] || \
  fail "BASELINE_PART must be 'inventory' for the implemented capture capability."

case "${environment}" in
  local-dev)
    readonly context="kind-local-dev-insurance-hub"
    readonly service_namespace="local-dev-all"
    readonly data_namespace="local-dev-all"
    readonly product_deployment="local-dev-product-api-legacy"
    readonly product_service="local-dev-product-api-legacy"
    readonly gateway_deployment="local-dev-agent-portal-gateway-legacy"
    readonly gateway_service="local-dev-agent-portal-gateway-legacy"
    readonly database_cluster="local-dev-postgres-product"
    readonly database_secret="local-dev-postgres-product-user-creds"
    readonly scenario_id="INV-LOCAL-001"
    readonly product_overlay="k8s/overlays/local-dev/svc/product/legacy/deployment-patch.yaml"
    readonly gateway_overlay="k8s/overlays/local-dev/svc/agent-portal-gateway/legacy/deployment-patch.yaml"
    readonly expected_product_image="insurance-hub-product-api-legacy:latest"
    readonly expected_gateway_image="insurance-hub-agent-portal-gateway-legacy:latest"
    readonly expected_pg_host="local-dev-postgres-product-rw.local-dev-all.svc.cluster.local"
    ;;
  qa)
    readonly context="qa-insurance-hub"
    readonly service_namespace="qa-svc"
    readonly data_namespace="qa-data"
    readonly product_deployment="qa-product-api-legacy"
    readonly product_service="qa-product-api-legacy"
    readonly gateway_deployment="qa-agent-portal-gateway-legacy"
    readonly gateway_service="qa-agent-portal-gateway-legacy"
    readonly database_cluster="qa-postgres-product"
    readonly database_secret="qa-postgres-product-user-creds"
    readonly scenario_id="INV-QA-001"
    readonly product_overlay="k8s/overlays/qa/svc/product/legacy/deployment-patch.yaml"
    readonly gateway_overlay="k8s/overlays/qa/svc/agent-portal-gateway/legacy/deployment-patch.yaml"
    readonly expected_product_image="ghcr.io/igor-baiborodine/insurance-hub/product-api-legacy:1.4.0"
    readonly expected_gateway_image="ghcr.io/igor-baiborodine/insurance-hub/agent-portal-gateway-legacy:1.0.0"
    readonly expected_pg_host="qa-postgres-product-rw.qa-data.svc.cluster.local"
    ;;
  *) fail "Unsupported BASELINE_ENV '${environment}'; use local-dev or qa." ;;
esac

for command_name in git jq kubectl; do
  require_command "${command_name}"
done

cd "${repo_root}"
umask 077
readonly temp_dir="$(mktemp -d)"
trap 'rm -rf "${temp_dir}"' EXIT
BASELINE_PREFLIGHT_INVENTORY_OUTPUT="${temp_dir}/postgres.json" \
  "${script_dir}/preflight.sh"

kubectl --context="${context}" -n "${service_namespace}" \
  get deployment "${product_deployment}" -o json >"${temp_dir}/product-deployment.json"
kubectl --context="${context}" -n "${service_namespace}" \
  get deployment "${gateway_deployment}" -o json >"${temp_dir}/gateway-deployment.json"
kubectl --context="${context}" -n "${service_namespace}" \
  get service "${product_service}" -o json >"${temp_dir}/product-service.json"
kubectl --context="${context}" -n "${service_namespace}" \
  get service "${gateway_service}" -o json >"${temp_dir}/gateway-service.json"
kubectl --context="${context}" -n "${service_namespace}" \
  get endpointslices.discovery.k8s.io \
  --selector="kubernetes.io/service-name=${product_service}" -o json \
  >"${temp_dir}/product-endpoints.json"
kubectl --context="${context}" -n "${service_namespace}" \
  get endpointslices.discovery.k8s.io \
  --selector="kubernetes.io/service-name=${gateway_service}" -o json \
  >"${temp_dir}/gateway-endpoints.json"
kubectl --context="${context}" -n "${service_namespace}" \
  get pods -l app.kubernetes.io/name=product-api-legacy -o json \
  >"${temp_dir}/product-pods.json"
kubectl --context="${context}" -n "${service_namespace}" \
  get pods -l app.kubernetes.io/name=agent-portal-gateway-legacy -o json \
  >"${temp_dir}/gateway-pods.json"
kubectl --context="${context}" -n "${data_namespace}" \
  get clusters.postgresql.cnpg.io "${database_cluster}" -o json \
  >"${temp_dir}/database-cluster.json"

product_configmap="$(jq -r '
  .spec.template.spec.containers[]
  | select(.name == "product-api-legacy")
  | .envFrom[]?.configMapRef.name
' "${temp_dir}/product-deployment.json" | head -n 1)"
[[ -n "${product_configmap}" ]] || fail "Product deployment has no ConfigMap reference."
kubectl --context="${context}" -n "${service_namespace}" \
  get configmap "${product_configmap}" -o json >"${temp_dir}/product-configmap.json"

if kubectl --context="${context}" api-resources \
  --api-group=kustomize.toolkit.fluxcd.io -o name 2>/dev/null | \
  grep -Fxq kustomizations.kustomize.toolkit.fluxcd.io; then
  kubectl --context="${context}" -n flux-system \
    get kustomizations.kustomize.toolkit.fluxcd.io qa-svc -o json \
    >"${temp_dir}/flux-kustomization.json" 2>/dev/null || printf 'null\n' \
    >"${temp_dir}/flux-kustomization.json"
else
  printf 'null\n' >"${temp_dir}/flux-kustomization.json"
fi

if kubectl --context="${context}" api-resources \
  --api-group=source.toolkit.fluxcd.io -o name 2>/dev/null | \
  grep -Fxq gitrepositories.source.toolkit.fluxcd.io; then
  kubectl --context="${context}" -n flux-system \
    get gitrepositories.source.toolkit.fluxcd.io insurance-hub -o json \
    >"${temp_dir}/flux-source.json" 2>/dev/null || printf 'null\n' \
    >"${temp_dir}/flux-source.json"
else
  printf 'null\n' >"${temp_dir}/flux-source.json"
fi

readonly database_host="${BASELINE_PRODUCT_DB_HOST:-127.0.0.1}"
readonly database_port="${BASELINE_PRODUCT_DB_PORT:-5492}"
jq empty "${temp_dir}/postgres.json"

readonly captured_at="$(jq -r '.observedAt' "${temp_dir}/postgres.json")"
readonly source_revision="$(git rev-parse HEAD)"
readonly output_dir="legacy/product-service/src/test/resources/product-read-baseline/inventory"
readonly output_file="${output_dir}/${environment}.json"
mkdir -p "${output_dir}"

jq -n \
  --arg schema_version "1" \
  --arg scenario_id "${scenario_id}" \
  --arg captured_at "${captured_at}" \
  --arg environment "${environment}" \
  --arg context "${context}" \
  --arg service_namespace "${service_namespace}" \
  --arg data_namespace "${data_namespace}" \
  --arg source_revision "${source_revision}" \
  --arg capture_command "make product-baseline-capture BASELINE_ENV=${environment} BASELINE_PART=inventory" \
  --arg database_host "${database_host}" \
  --arg database_port "${database_port}" \
  --arg database_secret "${database_secret}" \
  --arg expected_product_image "${expected_product_image}" \
  --arg expected_gateway_image "${expected_gateway_image}" \
  --arg expected_pg_host "${expected_pg_host}" \
  --arg product_overlay "${product_overlay}" \
  --arg gateway_overlay "${gateway_overlay}" \
  --slurpfile product_deployment "${temp_dir}/product-deployment.json" \
  --slurpfile gateway_deployment "${temp_dir}/gateway-deployment.json" \
  --slurpfile product_service "${temp_dir}/product-service.json" \
  --slurpfile gateway_service "${temp_dir}/gateway-service.json" \
  --slurpfile product_endpoints "${temp_dir}/product-endpoints.json" \
  --slurpfile gateway_endpoints "${temp_dir}/gateway-endpoints.json" \
  --slurpfile product_pods "${temp_dir}/product-pods.json" \
  --slurpfile gateway_pods "${temp_dir}/gateway-pods.json" \
  --slurpfile database_cluster "${temp_dir}/database-cluster.json" \
  --slurpfile product_configmap "${temp_dir}/product-configmap.json" \
  --slurpfile postgres "${temp_dir}/postgres.json" \
  --slurpfile flux_kustomization "${temp_dir}/flux-kustomization.json" \
  --slurpfile flux_source "${temp_dir}/flux-source.json" '
    def sensitive_name:
      test("password|secret|token|credential|private|api[_-]?key"; "i");
    def sanitized_env($deployment; $container):
      [
        $deployment.spec.template.spec.containers[]
        | select(.name == $container)
        | .env[]?
        | if has("value") then
            {name, value: (if (.name | sensitive_name) then "<redacted>" else .value end)}
          elif .valueFrom.secretKeyRef then
            {name, secretKeyRef: {
              name: .valueFrom.secretKeyRef.name,
              key: .valueFrom.secretKeyRef.key,
              optional: (.valueFrom.secretKeyRef.optional // false)
            }}
          elif .valueFrom.configMapKeyRef then
            {name, configMapKeyRef: {
              name: .valueFrom.configMapKeyRef.name,
              key: .valueFrom.configMapKeyRef.key,
              optional: (.valueFrom.configMapKeyRef.optional // false)
            }}
          elif .valueFrom.fieldRef then
            {name, fieldRef: .valueFrom.fieldRef.fieldPath}
          else
            {name, valueFrom: "other"}
          end
      ];
    def sanitized_config($config):
      $config.data
      | with_entries(
          if (.key | sensitive_name) then .value = "<redacted>" else . end
        );
    def workload($deployment; $pods; $container): {
      deployment: $deployment.metadata.name,
      generation: $deployment.metadata.generation,
      observedGeneration: $deployment.status.observedGeneration,
      desiredReplicas: ($deployment.spec.replicas // 1),
      readyReplicas: ($deployment.status.readyReplicas // 0),
      availableReplicas: ($deployment.status.availableReplicas // 0),
      deploymentRevision: ($deployment.metadata.annotations["deployment.kubernetes.io/revision"] // null),
      strategy: $deployment.spec.strategy,
      container: (
        $deployment.spec.template.spec.containers[]
        | select(.name == $container)
        | {
            name,
            image,
            imagePullPolicy,
            ports,
            readinessProbe,
            livenessProbe,
            envFrom,
            environment: sanitized_env($deployment; $container)
          }
      ),
      pods: [
        $pods.items[]
        | {
            name: .metadata.name,
            node: .spec.nodeName,
            phase: .status.phase,
            ready: ([.status.conditions[]? | select(.type == "Ready") | .status][0] // "False"),
            image: ([.status.containerStatuses[]? | select(.name == $container) | .image][0] // null),
            imageDigest: ([.status.containerStatuses[]? | select(.name == $container) | .imageID][0] // null)
          }
      ]
    };
    def service($service; $endpoints): {
      name: $service.metadata.name,
      type: $service.spec.type,
      clusterIP: $service.spec.clusterIP,
      ports: $service.spec.ports,
      readyEndpointAddresses: [
        $endpoints.items[].endpoints[]?
        | select(.conditions.ready != false)
        | .addresses[]
      ] | unique
    };
    def flux_record($kustomization; $source): {
      apiAvailable: ($kustomization != null or $source != null),
      qaServiceKustomization: (
        if $kustomization == null then null else {
          name: $kustomization.metadata.name,
          sourceRef: $kustomization.spec.sourceRef,
          path: $kustomization.spec.path,
          interval: $kustomization.spec.interval,
          retryInterval: $kustomization.spec.retryInterval,
          prune: $kustomization.spec.prune,
          lastAppliedRevision: $kustomization.status.lastAppliedRevision,
          lastAttemptedRevision: $kustomization.status.lastAttemptedRevision,
          conditions: $kustomization.status.conditions
        } end
      ),
      source: (
        if $source == null then null else {
          name: $source.metadata.name,
          url: $source.spec.url,
          branch: $source.spec.ref.branch,
          interval: $source.spec.interval,
          artifactRevision: $source.status.artifact.revision
        } end
      )
    };

    ($product_deployment[0]) as $pd
    | ($gateway_deployment[0]) as $gd
    | ($product_service[0]) as $ps
    | ($gateway_service[0]) as $gs
    | ($product_endpoints[0]) as $pe
    | ($gateway_endpoints[0]) as $ge
    | ($product_pods[0]) as $pp
    | ($gateway_pods[0]) as $gp
    | ($database_cluster[0]) as $dc
    | ($product_configmap[0]) as $pc
    | ($postgres[0]) as $pg
    | ($flux_kustomization[0]) as $fk
    | ($flux_source[0]) as $fs
    | (workload($pd; $pp; "product-api-legacy")) as $product_workload
    | (workload($gd; $gp; "agent-portal-gateway-legacy")) as $gateway_workload
    | (sanitized_config($pc)) as $active_config
    | (flux_record($fk; $fs)) as $flux
    | {
        schemaVersion: ($schema_version | tonumber),
        scenarioId: $scenario_id,
        status: "captured",
        captureProfile: "inventory",
        provenance: "captured-observation",
        credentialCategory: "database-capture",
        capturedAt: $captured_at,
        captureClock: "Target PostgreSQL clock at the read-only inventory query",
        captureCommand: $capture_command,
        environment: $environment,
        sourceRevision: $source_revision,
        kubernetes: {
          context: $context,
          namespaces: {service: $service_namespace, data: $data_namespace},
          workloads: {product: $product_workload, gateway: $gateway_workload},
          services: {
            product: service($ps; $pe),
            gateway: service($gs; $ge)
          },
          productConfiguration: {
            configMap: $pc.metadata.name,
            data: $active_config
          },
          postgresCluster: {
            name: $dc.metadata.name,
            generation: $dc.metadata.generation,
            observedGeneration: $dc.status.observedGeneration,
            instances: $dc.spec.instances,
            image: $dc.spec.imageName,
            currentPrimary: $dc.status.currentPrimary,
            readyInstances: $dc.status.readyInstances,
            phase: $dc.status.phase,
            bootstrap: {
              database: $dc.spec.bootstrap.initdb.database,
              owner: $dc.spec.bootstrap.initdb.owner,
              secretReference: $dc.spec.bootstrap.initdb.secret.name
            }
          },
          flux: $flux
        },
        postgres: $pg,
        connection: {
          endpoint: ($database_host + ":" + $database_port),
          forwardVerified: true,
          transactionReadOnly: $pg.sessionReadOnly
        },
        ownership: {
          runtimeManagers: {
            services: ($pd.metadata.labels["app.kubernetes.io/managed-by"] // null),
            databaseOperator: "cloudnative-pg"
          },
          checkedInResources: ([
            "k8s/apps/svc/product/base/legacy/deployment.yaml",
            $product_overlay,
            $gateway_overlay,
            "k8s/apps/infra/postgres/base/cluster.yaml",
            ("k8s/overlays/" + $environment + "/infra/postgres/product/cluster-patch.yaml"),
            "k8s/Makefile"
          ] + (if $environment == "qa" then [
            "k8s/flux/qa/flux-system/kustomization-qa-svc.yaml",
            "k8s/flux/qa/flux-system/gotk-sync.yaml"
          ] else [] end)),
          secretReferences: ([
            $database_secret,
            $product_workload.container.environment[]?.secretKeyRef.name,
            $gateway_workload.container.environment[]?.secretKeyRef.name
          ] | map(select(. != null)) | unique),
          writers: [
            {
              owner: "legacy Product service",
              mechanism: "DataLoader inserts missing CAR, FAI, HSI, and TRI on ServerStartupEvent",
              source: "legacy/product-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/product/service/init/DataLoader.java"
            },
            {
              owner: "legacy Product service Hibernate startup",
              mechanism: "hibernate.hbm2ddl.auto=update",
              source: "legacy/product-service/src/main/resources/application.yml"
            },
            {
              owner: "CloudNativePG bootstrap/admin identity",
              mechanism: "creates the product database and product owner from a Secret reference",
              source: ("k8s/overlays/" + $environment + "/infra/postgres/product/cluster-patch.yaml")
            }
          ]
        },
        checkedInExpectations: {
          productImage: $expected_product_image,
          gatewayImage: $expected_gateway_image,
          productDatabaseHost: $expected_pg_host,
          postgresImage: "ghcr.io/cloudnative-pg/postgresql:17",
          productTable: {
            owner: "product",
            columns: [
              {name: "code", underlyingType: "varchar", characterMaximumLength: 255, nullable: false},
              {name: "definition", underlyingType: "jsonb", characterMaximumLength: null, nullable: false}
            ],
            primaryKey: "PRIMARY KEY (code)"
          }
        },
        differencesFromCheckedInSources: ([
          if $product_workload.container.image != $expected_product_image then {
            field: "productImage",
            checkedIn: $expected_product_image,
            observed: $product_workload.container.image
          } else empty end,
          if $gateway_workload.container.image != $expected_gateway_image then {
            field: "gatewayImage",
            checkedIn: $expected_gateway_image,
            observed: $gateway_workload.container.image
          } else empty end,
          if $active_config.PG_HOST != $expected_pg_host then {
            field: "productDatabaseHost",
            checkedIn: $expected_pg_host,
            observed: ($active_config.PG_HOST // null)
          } else empty end,
          if $dc.spec.imageName != "ghcr.io/cloudnative-pg/postgresql:17" then {
            field: "postgresImage",
            checkedIn: "ghcr.io/cloudnative-pg/postgresql:17",
            observed: $dc.spec.imageName
          } else empty end,
          if ($environment == "qa" and ($flux.apiAvailable | not)) then {
            field: "fluxRuntimeApi",
            checkedIn: "QA reconciliation resources under k8s/flux/qa",
            observed: "Flux APIs unavailable in the active QA cluster"
          } else empty end
        ]),
        sanitizationRecord: {
          secretValuesCaptured: false,
          bearerTokensCaptured: false,
          catalogDefinitionsCaptured: false,
          retainedSensitiveReferences: "Secret names and keys only",
          temporaryCredentialHandling: "Database password held only in process memory and unset after the read-only query"
        }
      }
  ' >"${temp_dir}/inventory.json"

jq empty "${temp_dir}/inventory.json"
mv "${temp_dir}/inventory.json" "${output_file}"
chmod 0644 "${output_file}"

echo "Captured sanitized ${environment} Product inventory at ${output_file}."
