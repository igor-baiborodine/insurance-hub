## Current State

The Insurance Hub system currently relies on **Zipkin** for distributed tracing, providing basic
request tracking across microservices but offering limited correlation with logs and metrics.
Context propagation between services is inconsistent, requiring custom instrumentation per service.
The tracing pipeline lacks standardization, leading to fragmented analysis and the limited ability to
perform unified root cause investigations. The Zipkin-based approach operates in isolation from
other observability components, increasing operational overhead and preventing holistic visibility
across the distributed system.

## Why OpenTelemetry + Collector + Tempo is the Right Choice

1. **Vendor-Neutral, Unified Telemetry Pipeline**

- **OpenTelemetry** provides an open, vendor-neutral observability framework that standardizes
  trace, metric, and log data collection across all services.
- The **OpenTelemetry Collector** acts as a centralized telemetry gateway, capable of receiving data
  in multiple protocols (Zipkin, Jaeger, OTLP, Prometheus), transforming it into a common
  OpenTelemetry format, and exporting it to multiple backends such as **Grafana Tempo**.
- This design enables consistent data ingestion while reducing the instrumentation complexity in
  each application.

2. **Seamless Integration with Grafana Ecosystem**

- Combined with **Grafana Tempo**, the Collector provides an end-to-end, cloud-native observability
  stack that unifies traces, logs (via Loki), and metrics (via Prometheus).
- Tempo’s integration with the Collector ensures all telemetry data flows through a single pipeline,
  allowing for automatic contextual linking (trace IDs embedded in logs, metrics, and spans) for
  cross-domain correlation in Grafana.
- The Collector’s batching and retry mechanisms improve data reliability and throughput for
  large-scale trace ingestion.

3. **Cloud-Native Architecture and Scalability**

- Tempo leverages **MinIO** as its S3-compatible backend, offering horizontally scalable and
  cost-effective object storage from the initial deployment onward—no reconfiguration required later
  phases.
- The Collector offloads telemetry processing (sampling, filtering, enrichment) from the
  microservices, reducing performance impact and maintaining low latency under production load.
- As part of the Kubernetes-native ecosystem, both Tempo and the Collector scale independently while
  maintaining unified observability pipelines.

4. **Consistent Developer Experience and Automation**

- OpenTelemetry SDKs in **Go** and **Java** offer automatic correlation across gRPC, HTTP, and
  database operations—providing complete trace context with minimal configuration.
- The Collector centralizes telemetry configurations, eliminating duplication across applications
  and simplifying rollout in development, QA, and production.
- With this model, new or migrated services can immediately send standardized telemetry data without
  individual backend configuration, ensuring repeatable, automated observability rollout.

By introducing the **OpenTelemetry Collector** as an intermediary between application
instrumentation and **Grafana Tempo**, the Insurance Hub platform gains a declarative, extensible,
and future-proof observability layer. It transforms tracing from a fragmented, Zipkin-based workflow
into a cohesive, scalable telemetry architecture that equally supports traces, logs, and metrics
within a unified pipeline. This configuration provides deep system visibility while simplifying
operations, governance, and cross-team diagnostics.
