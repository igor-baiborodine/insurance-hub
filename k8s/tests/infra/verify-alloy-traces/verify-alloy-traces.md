## Verify Alloy Trace Routing (Tempo + Zipkin)

### QA

This runbook validates that Alloy in `qa-monitoring` is healthy, receives traces from legacy Java
services (Zipkin protocol), and dual-exports them to both Tempo and Zipkin.

1. **Verify Alloy workload health**

   ```shell
   cd k8s
   make alloy-status
   ```

   **Expected**: `qa-alloy-*` pod is `Running` and `qa-alloy` service exposes `9411`, `4317`, `4318`.

2. **Verify bridge collector health (dual-export path to Zipkin)**

   ```shell
   kubectl get deploy,pod,svc -n qa-monitoring -l app.kubernetes.io/name=zipkin-bridge
   kubectl logs -n qa-monitoring deploy/qa-zipkin-bridge --since=15m
   ```

   **Expected**: bridge pod is `Running`, no fatal startup errors.

3. **Verify Alloy metrics endpoint readiness**

   In one terminal:

   ```shell
   make alloy-ui
   ```

   In another terminal:

   ```shell
   curl -sf http://localhost:12345/-/ready
   ```

   **Expected**: `Alloy is ready.`

4. **Send synthetic trace to Alloy OTLP HTTP endpoint**

   In one terminal:

   ```shell
   kubectl port-forward -n qa-monitoring svc/qa-alloy 4318:4318
   ```

   In another terminal:

   ```shell
   TRACE_ID=$(python3 -c 'import secrets; print(secrets.token_hex(16))')
   SPAN_ID=$(python3 -c 'import secrets; print(secrets.token_hex(8))')
   TS=$(python3 -c 'import time; print(int(time.time()*1_000_000_000))')

   curl -s http://localhost:4318/v1/traces \
     -H 'Content-Type: application/json' \
     -d '{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"alloy-dual-smoke"}}]},"scopeSpans":[{"scope":{"name":"smoke"},"spans":[{"traceId":"'"$TRACE_ID"'","spanId":"'"$SPAN_ID"'","name":"alloy-dual-smoke-span","kind":"SPAN_KIND_INTERNAL","startTimeUnixNano":"'"$TS"'","endTimeUnixNano":"'"$((TS+5000000))"'"}]}]}]}'

   echo "TRACE_ID=$TRACE_ID"
   ```

   **Expected**: API responds with `{"partialSuccess":{}}` or empty success response.

5. **Verify Alloy exporter metrics for traces**

   In another terminal:

   ```shell
   curl -s http://localhost:12345/metrics | \
     grep -E "otelcol_receiver_accepted_spans_total|otelcol_exporter_sent_spans_total|otelcol_exporter_send_failed_spans_total"
   ```

   **Expected**:
   - `otelcol_receiver_accepted_spans_total` increases.
   - `otelcol_exporter_sent_spans_total{component_id="otelcol.exporter.otlp.tempo"...}` increases.
   - No growing permanent failure counter for trace exporters.

6. **Verify trace in Tempo**

   In Grafana:
   - `make grafana-ui`
   - Explore -> Tempo
   - Search by `service.name="alloy-dual-smoke"` or direct trace ID from step 4.

7. **Verify trace in Zipkin**

   In one terminal:

   ```shell
   make zipkin-ui
   ```

   In Zipkin (`http://localhost:9411`):
   - Query recent traces for service `alloy-dual-smoke`.
   - Confirm the trace from step 4 is present.

8. **Verify Java service path (real producer)**

   After redeploying Java services with Alloy Zipkin endpoint:

   ```shell
   kubectl logs -n qa-monitoring deploy/qa-alloy --since=10m | \
     grep -E "receiver_accepted_spans|exporter_sent_spans|Exporting failed"
   ```

   **Expected**: accepted spans from Zipkin receiver and successful export toward Tempo (and bridge path for Zipkin continuity).
