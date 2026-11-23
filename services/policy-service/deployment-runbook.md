<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Policy foundation deployment](#policy-foundation-deployment)
  - [Candidate checks](#candidate-checks)
  - [Direct local-dev](#direct-local-dev)
  - [Flux-owned QA](#flux-owned-qa)
  - [Private smoke and recovery](#private-smoke-and-recovery)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Policy foundation deployment

Use catalog key `policy`. The Go workload is `<environment>-policy-api`, selected by
`app.kubernetes.io/name=policy-api`; Java Policy (`policy-api-legacy`) and Go Policy Search
(`policy-search-api`) keep their own selectors. The agent portal gateway keeps its
environment-specific `http://<environment>-policy-api-legacy:80` upstream, so agents continue to
use Java Policy. Go Policy serves only the bootstrap Echo. It uses Pod loopback for business HTTP and gRPC, and Pod port 8081 for kubelet health.
Operators use a loopback-bound Kubernetes API port-forward. The foundation has no Kubernetes
Service, ingress route, workload token or business credentials.

| Target | Context | Namespace | Owner |
| --- | --- | --- | --- |
| local-dev | `kind-local-dev-insurance-hub` | `local-dev-all` | Guarded direct deployment into Kind cluster `local-dev-insurance-hub` |
| QA | `qa-insurance-hub` | `qa-svc` | `flux-system/qa-svc` reconciles `./k8s/overlays/qa/svc` from `flux-system/insurance-hub` on `main` |

## Candidate checks

Run from the repository root with the pinned Python, Go, Docker, Kind and kubectl tools.

```bash
make go-svc-deploy-check go-svc-deploy-test-tooling SVC_NAME=policy
make go-svc-deploy-render SVC_NAME=policy DEPLOY_ENV=local-dev
make go-svc-deploy-render SVC_NAME=policy DEPLOY_ENV=qa
```

The initial QA overlay contains an inactive release placeholder. The parent Kustomization
includes only `./policy/legacy` until release preparation adds `./policy`. An activated
placeholder fails the check. Local-dev requires an explicit, source-matched image to perform
operations. For disposable qualification, build a local image as below, then run
`make go-svc-deploy-smoke SVC_NAME=policy` with the selected image and source revision exported.
The smoke scopes its writes to one labeled Kind namespace and removes that namespace afterward.
It runs only the private workload checks (Echo, probes, identity, Pod denial and scale-to-zero).
It has no Java Policy, so the gateway and Java endpoint coexistence check runs only through
`go-svc-deploy-verify` against local-dev and QA.

## Direct local-dev

From the clean checkout of the approved image source, build and inspect its local image.

```bash
export DEPLOY_IMAGE_SOURCE_REVISION="$(git rev-parse --verify 'HEAD^{commit}')"
export DEPLOY_IMAGE_REF="insurance-hub-policy-api:1.0.0-local.${DEPLOY_IMAGE_SOURCE_REVISION:0:12}"
GOWORK=off make go-svc-image-check SVC_NAME=policy
docker image inspect "$DEPLOY_IMAGE_REF" --format \
  '{{.Id}} {{index .Config.Labels "org.opencontainers.image.revision"}} {{index .Config.Labels "org.opencontainers.image.version"}}'
```

The image ID is content-addressed. The OCI revision must equal the full source commit and
the version must equal `git-${DEPLOY_IMAGE_SOURCE_REVISION:0:12}`. With local-dev access
authorized, select the configured context and run the guarded operations.

```bash
export DEPLOY_ENV=local-dev DEPLOY_CONTEXT=kind-local-dev-insurance-hub DEPLOY_SVC_NS=local-dev-all
kubectl config use-context "$DEPLOY_CONTEXT"
make go-svc-deploy-preflight SVC_NAME=policy
make go-svc-image-load SVC_NAME=policy
make go-svc-deploy SVC_NAME=policy
make go-svc-deploy-status SVC_NAME=policy
make go-svc-deploy-verify SVC_NAME=policy
```

## Flux-owned QA

The [release workflow](../../.github/workflows/go-release-svc.yml) builds once, pushes the
semantic and source tags, and resolves the registry digest before calling
`go-svc-release-prepare`. Release preparation records the semantic tag, pins the published
digest beside it (catalog `pinDigest`), sets the source-derived runtime version and adds
`./policy` to the parent QA Kustomization in the same release commit. The QA Pod template
therefore references `policy-api:<version>@sha256:<digest>`, and the render check rejects an
active QA release without a digest. Keep image source and Flux configuration commits separate.

From the checkout of the applied release configuration, set the full image source and applied
Flux commits from the workflow and Flux records. Set `RELEASE_VERSION` to the published
semantic version. Set `DEPLOY_IMAGE_DIGEST` to the digest recorded by the release workflow.

```bash
: "${DEPLOY_IMAGE_SOURCE_REVISION:?set the full image source commit}"
: "${DEPLOY_SOURCE_REVISION:?set the full Flux-applied configuration commit}"
: "${RELEASE_VERSION:?set the published semantic version}"
: "${DEPLOY_IMAGE_DIGEST:?set the immutable digest recorded by the release workflow}"
export DEPLOY_IMAGE_SOURCE_REVISION DEPLOY_SOURCE_REVISION DEPLOY_IMAGE_DIGEST
export DEPLOY_ENV=qa DEPLOY_CONTEXT=qa-insurance-hub DEPLOY_SVC_NS=qa-svc
unset DEPLOY_IMAGE_REF
image=ghcr.io/igor-baiborodine/insurance-hub/policy-api
observed_digest="$(docker buildx imagetools inspect "$image:$RELEASE_VERSION" \
  --format '{{json .Manifest.Digest}}' | tr -d '"')"
test "$observed_digest" = "$DEPLOY_IMAGE_DIGEST"
docker pull "$image@$DEPLOY_IMAGE_DIGEST"
kubectl config use-context "$DEPLOY_CONTEXT"
kubectl --context "$DEPLOY_CONTEXT" -n flux-system get gitrepository insurance-hub
kubectl --context "$DEPLOY_CONTEXT" -n flux-system get kustomization qa-svc
make go-svc-deploy-status SVC_NAME=policy
make go-svc-deploy-verify SVC_NAME=policy
```

Status checks the exact deployment, ready Pod, Pod UID, version and running image ID. Use a
digest-qualified image reference for OCI revision and version checks. Status and verification
inspect that pulled image, require the rendered QA image to carry the same digest, and require it
to match the ready Pod image ID.

## Private smoke and recovery

`go-svc-deploy-verify` resolves the ready Policy Pod and uses private port-forwards for all
three listeners. It checks HTTP and generated-client gRPC Echo of `hello`, management
`200 live\n` and `200 ready\n`, and the six safe startup identity fields, including the Pod
UID. It then confirms that the `<environment>-policy-api-legacy` Service EndpointSlices list only
ready Java Policy Pods. `go-svc-deploy-check` separately confirms that the gateway profile and its
route override keep the Java upstream. For manual troubleshooting, forward the exact Pod selected by status.

```bash
: "${POLICY_POD:?set the ready Go Policy Pod name from status}"
kubectl --context "$DEPLOY_CONTEXT" -n "$DEPLOY_SVC_NS" port-forward --address 127.0.0.1 \
  "pod/$POLICY_POD" 18080:8080 19090:9090 18081:8081
```

With explicit authorization for the temporary probe Pod, run
`make go-svc-deploy-verify SVC_NAME=policy DEPLOY_DENIAL_PROBE=1`. It also checks that
an ordinary workload cannot open any Policy port and removes its own probe Pod.

In another terminal, use bounded requests against the loopback ports.

```bash
curl --fail --max-time 5 -H 'Content-Type: application/json' \
  -d '{"message":"hello"}' http://127.0.0.1:18080/v1/example/echo
make -C services/policy-service _deploy-smoke-rpc POLICY_SMOKE_GRPC_ADDR=127.0.0.1:19090
curl --fail --max-time 5 http://127.0.0.1:18081/livez
curl --fail --max-time 5 http://127.0.0.1:18081/readyz
```

Local-dev permits only TCP and UDP 53 to kube-dns and disables telemetry. QA additionally
permits OTLP/gRPC to the selected Alloy Pods. Inspect the six identity fields in the Policy
startup record and verify corresponding telemetry by `service.name=policy-service`, `service.version` and `service.instance.id` through
Alloy's existing signal paths. Keep request payloads out of operational evidence.

Use the [Dashboard runbook](../dashboard-service/deployment-runbook.md) first for source,
image, Flux and transport troubleshooting. When QA Flux, DNS or registry state looks stale, apply
the [QA network recovery runbook](../../k8s/base-cluster-how-tos.md#qa-network-recovery) before
treating the failure as a Policy defect. In local-dev, `make go-svc-stop SVC_NAME=policy`
with the same coordinates and image selection scales only the Go Deployment to zero.
Redeploy the approved previous Go image by selecting its full source revision and tag and
rerunning the direct deployment steps.

If first activation in QA stalls the shared owner, prepare a reviewed recovery commit that removes
`./policy` from `k8s/overlays/qa/svc/kustomization.yaml` and restores
`newTag: 0.0.0-unpublished` and `SERVICE_VERSION=git-000000000000` in the Go Policy QA overlay.
Keep `./policy/legacy` in the parent. Run `make go-svc-deploy-check SVC_NAME=policy` before
publishing that correction. After Flux applies it, verify the applied revision and pruning.

```bash
kubectl --context qa-insurance-hub -n flux-system get kustomization qa-svc -o yaml
kubectl --context qa-insurance-hub -n qa-svc get deployment qa-policy-api --ignore-not-found
kubectl --context qa-insurance-hub -n qa-svc get endpointslice \
  -l kubernetes.io/service-name=qa-policy-api-legacy \
  -o 'custom-columns=POD:.endpoints[*].targetRef.name,READY:.endpoints[*].conditions.ready'
```

Expect the shared owner to be Ready at the reviewed correction commit, the Go Deployment to be
absent, and the Java Policy endpoint to remain ready.
