## Verify Loki Logs

### QA

This runbook validates that Loki in `qa-monitoring` is healthy, receives logs, stores data in the
Loki MinIO bucket, and is queryable from Grafana and Prometheus.

Note: pod application logs are visible in Loki only when a collector is deployed (for example,
Promtail, Grafana Alloy, or OpenTelemetry Collector). Loki does not scrape pod logs by itself.

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

4. **Generate test logs**

   Run a short-lived pod that prints JSON logs:

   ```shell
   kubectl run loki-log-generator \
     -n qa-monitoring \
     --image=busybox:1.36 \
     --restart=Never \
     -- sh -c 'i=0; while [ $i -lt 30 ]; do echo "{\"app\":\"loki-log-generator\",\"msg\":\"hello-$i\"}"; i=$((i+1)); sleep 1; done'
   kubectl logs -n qa-monitoring loki-log-generator
   ```

5. **Verify ingestion path exists (required for test pod logs)**

   ```shell
   kubectl get pods -A | grep -Ei 'promtail|alloy|otel|collector'
   ```

   If no collector pods are present, skip step 6 and use step 7.

6. **Query Loki API for recent logs**

   With `make loki-ui` port-forward still active:

   ```shell
   START_NS=$(date -u -d '15 minutes ago' +%s%N)
   END_NS=$(date -u +%s%N)
   curl -G "http://localhost:3100/loki/api/v1/query_range" \
     --data-urlencode 'query={namespace="qa-monitoring"} |= "loki-log-generator"' \
     --data-urlencode "start=${START_NS}" \
     --data-urlencode "end=${END_NS}" \
     --data-urlencode "direction=forward" \
     --data-urlencode "limit=50" | jq .
   ```

   Expected: response includes log lines emitted by `loki-log-generator`.

7. **Fallback: verify Loki with canary logs**

   ```shell
   START_NS=$(date -u -d '15 minutes ago' +%s%N)
   END_NS=$(date -u +%s%N)
   curl -G "http://localhost:3100/loki/api/v1/query_range" \
     --data-urlencode 'query={name="loki-canary"}' \
     --data-urlencode "start=${START_NS}" \
     --data-urlencode "end=${END_NS}" \
     --data-urlencode "limit=50" | jq .
   ```

   If needed, list all labels first:

   ```shell
   curl -s "http://localhost:3100/loki/api/v1/labels" | jq .
   ```

8. **Verify bucket object presence in MinIO**

   In one terminal:

   ```shell
   make minio-console-ui SVC_NAME=loki
   ```

   Then open `http://localhost:9090`, log in with Loki MinIO credentials, and confirm bucket
   `loki-logs` contains objects.

9. **Verify Grafana Loki datasource**

   ```shell
   make grafana-ui
   ```

   In Grafana (`http://localhost:3000`):
   - Open `Explore`
   - Select `Loki` datasource
   - Run query: `{namespace="qa-monitoring"} |= "loki-log-generator"`

10. **Verify Loki metrics in Prometheus**

   ```shell
   make prometheus-ui
   ```

   In Prometheus (`http://localhost:9090`), query:
   - `loki_request_duration_seconds`
   - `loki_ingester_samples_total`

   Expected: series are returned.

11. **Cleanup test pod**

   ```shell
   kubectl delete pod loki-log-generator -n qa-monitoring --ignore-not-found
   ```
