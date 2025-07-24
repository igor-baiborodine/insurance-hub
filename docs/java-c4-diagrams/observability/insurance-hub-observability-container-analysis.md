### Explanation of Containers and Flows

This document explains the containers and data flows shown in the C4 Container Diagram for the
Insurance Hub system's current observability infrastructure.

#### Containers (Internal Systems)

* **Insurance Hub Microservices**: These are the core application services. From an observability
  perspective, their instrumentation is basic and inconsistent. Some services are instrumented for
  tracing with Zipkin, while others only produce ad-hoc logs or expose partial metrics.
* **Zipkin Server**: The central component for distributed tracing. It is a Java/Spring Boot
  application that collects trace data (spans) from the instrumented microservices and provides a
  basic web UI for developers to visualize and query request flows.

#### External Systems & Services

* **Developer/Operator (Person)**: The end-user of the observability tools, responsible for
  monitoring system health, investigating performance issues, and troubleshooting errors.
* **HashiCorp Consul**: Provides service discovery and includes a basic health-checking mechanism.
  Services register themselves with Consul, allowing it to monitor their status.
* **Apache Kafka**: The event streaming platform. Its observability is limited to basic JMX metrics,
  which are not integrated into a central monitoring system.
* **Scattered Log Files**: Represents the current logging approach. There is no centralized logging
  system; each service instance writes its logs to the local file system. Accessing these logs is a
  manual process.
* **Manual Health Checks**: Represents the use of non-standardized, ad-hoc HTTP endpoints that
  developers can hit to check the basic health of a service.

#### Key Architectural Flows

1. **Distributed Tracing Flow**:
    * Key services (`Gateway`, `Policy Service`, etc.) are instrumented to send trace data to the
      `Zipkin Server` over HTTP.
    * The `Developer/Operator` uses the `Zipkin UI` to manually search for traces, analyze request
      latency, and identify which services were involved in a specific transaction. This flow is
      limited to the services that have been explicitly instrumented.

2. **Logging Flow (Fragmented & Manual)**:
    * Each microservice writes log output to its own local `Scattered Log Files`.
    * To investigate an issue, a `Developer/Operator` must manually access the server (e.g., via
      SSH) and search through individual log files. There is no central aggregation or correlation
      of logs, making cross-service investigation difficult.

3. **Health Check Flow**:
    * Services register their health status with `HashiCorp Consul`. The `Developer/Operator` can
      check the Consul UI to see a high-level overview of which services are up or down.
    * Separately, the `Developer/Operator` can perform `Manual Health Checks` by directly calling
      basic HTTP endpoints on each service.

4. **Metrics Flow (Ad-Hoc)**:
    * The observability of infrastructure like `Apache Kafka` is limited to exposing JMX metrics,
      which require separate, specialized tooling to view and are not part of a unified dashboard.
      Application services have partial or missing metrics.