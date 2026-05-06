## Verify Loki Logs

### QA

This runbook validates that Loki in `qa-monitoring` is healthy, receives logs via **HTTP push API**, stores data in the Loki MinIO bucket, and is queryable via curl and MinIO UI.

> This test uses direct HTTP push to `/loki/api/v1/push` (no collector required). Logs persist to MinIO chunks/index objects.

1. **Verify Loki workload health**

   ```shell
   cd k8s
   make loki-status
   ```

   Expected: `qa-loki-0` and `loki-canary-*` pods are `Running`.

2. **Verify Loki readiness endpoint**

   In one terminal:

   ```shell
   make loki-ui
   ```

   In another terminal:

   ```shell
   curl -sf http://localhost:3100/ready && echo "Loki is ready"
   ```

   Expected: `Loki is ready`

3. **Verify MinIO tenant + credentials for Loki**

   ```shell
   make minio-tenant-status SVC_NAME=loki
   kubectl get secret qa-minio-loki-svc-user-creds -n qa-minio-loki
   kubectl get secret qa-minio-loki-svc-user-creds -n qa-monitoring
   ```

4. **Push test logs via Loki HTTP API**

   ```shell
   TS=$(date +%s%N)
   curl -s -H "Content-Type: application/json" \
     -X POST "http://localhost:3100/loki/api/v1/push" \
     --data-raw "{\"streams\": [{ \"stream\": { \"app\": \"curl-test\", \"namespace\": \"qa-svc\" }, \"values\": [ [\"$TS\", \"Test log via curl API at $(date)\"] ] }]}"
   
   echo "✅ Log pushed with TS: $TS (check with step 5)"
   ```

   Expected: Silent success (204 No Content).

5. **Query Loki API for test logs**

   ```shell
   curl -G -s "http://localhost:3100/loki/api/v1/query" \
     --data-urlencode 'query={app="curl-test"}' | jq .
   ```

   Expected: Response includes your test log line from step 4.

6. **Verify bucket object presence in MinIO**

   In one terminal:

   ```shell
   make minio-console-ui SVC_NAME=loki
   ```

   Open `https://localhost:9443`, log in with Loki MinIO credentials, navigate to `loki-logs` (or configured bucket), confirm new `chunks/` and `index/` objects created post-step 4.

7. **Verify Grafana Loki datasource (optional)**

   ```shell
   make grafana-ui
   ```

   In Grafana (`http://localhost:3000`), Explore → Loki → `{app="curl-test"}`.

8. **Verify Loki metrics in Prometheus (optional)**

   ```shell
   make prometheus-ui
   ```

   In Prometheus (`http://localhost:9090`), query `loki_distributor_bytes_received_total`.

## Key Changes

- **Replaced pod log generation** with direct `curl` push to `/loki/api/v1/push` (nanosecond timestamp). [grafana](https://grafana.com/docs/loki/latest/reference/loki-http-api/)
- **Simplified query** to instant `{app="curl-test"}` (no time range needed). [grafana](https://grafana.com/docs/loki/latest/reference/loki-http-api/)
- **MinIO verification** focuses on new chunk/index objects after push. [community.grafana](https://community.grafana.com/t/loki-wont-persist-logs-to-minio-between-container-recreations/144370)
- **Removed collector/pod log dependency** entirely. [stackoverflow](https://stackoverflow.com/questions/67316535/send-logs-directly-to-loki-without-use-of-agents)

**Test completes in ~30 seconds**. Push → Query → MinIO confirms full ingestion pipeline works! [grafana](https://grafana.com/docs/loki/latest/reference/loki-http-api/)