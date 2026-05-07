## Verify Tempo Traces

### QA

This runbook validates that Tempo in `qa-monitoring` is healthy, receives traces, stores data in the Tempo MinIO bucket, and is queryable via API and Grafana.

> This test uses direct Zipkin v2 span ingestion to Tempo (`/api/v2/spans`) and then fetches the trace by ID from Tempo (`/api/traces/<trace-id>`).

1. **Verify Tempo workload health**

   ```shell
   cd k8s
   make tempo-status
   ```

   **Expected**: `qa-tempo-0` pod is `Running`.

2. **Verify Tempo readiness endpoint**

   In one terminal:

   ```shell
   make tempo-ui
   ```

   In another terminal:

   ```shell
   curl -sf http://localhost:3200/ready && echo "Tempo is ready"
   ```

   **Expected**: `Tempo is ready`

3. **Verify MinIO tenant + credentials for Tempo**

   ```shell
   make minio-tenant-status SVC_NAME=tempo
   kubectl get secret qa-minio-tempo-svc-user-creds -n qa-minio-tempo
   kubectl get secret qa-minio-tempo-svc-user-creds -n qa-monitoring
   ```

4. **Push and verify a synthetic trace**

   ```shell
   TRACE_ID="$(printf '%032x' "$(date +%s)")"
   SPAN_ID="$(printf '%016x' "$(date +%s)")"
   TS_MICROS="$(($(date -u +%s%N) / 1000))"

   # Push a test span through Zipkin receiver
   curl -s -X POST "http://localhost:3200/api/v2/spans" \
     -H "Content-Type: application/json" \
     -d "[{\"traceId\":\"$TRACE_ID\",\"id\":\"$SPAN_ID\",\"name\":\"tempo-smoke-test\",\"timestamp\":$TS_MICROS,\"duration\":5000,\"localEndpoint\":{\"serviceName\":\"tempo-smoke\"},\"tags\":{\"env\":\"qa\"}}]"

   # Verify trace is retrievable by trace ID
   sleep 10
   curl -s "http://localhost:3200/api/traces/$TRACE_ID" | jq '.batches[0].resource.attributes'
   ```

   **Expected**: JSON output contains resource attributes including `service.name` with value `tempo-smoke`.

5. **Verify bucket object presence in MinIO**

   In one terminal:

   ```shell
   make minio-console-ui SVC_NAME=tempo
   ```

   Open `https://localhost:9443`, log in with Tempo MinIO credentials, navigate to `tempo-traces` bucket, confirm objects are present/updated after step 4.

6. **Verify Grafana Tempo datasource**

   ```shell
   make grafana-ui
   ```

   In Grafana (`http://localhost:3000`):
   - Go to Explore.
   - Select `Tempo` datasource.
   - Search for traces using `service.name="tempo-smoke"` or by recent time range.

   **Expected**: trace appears and opens successfully.

7. **Verify Tempo metrics in Prometheus (optional)**

   ```shell
   make prometheus-ui
   ```

   In Prometheus (`http://localhost:9090`), run:
   - `tempo_distributor_spans_received_total`
   - `tempo_ingester_traces_created_total`

   **Expected**: both queries return time series data.
