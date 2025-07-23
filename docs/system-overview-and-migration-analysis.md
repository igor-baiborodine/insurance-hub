# Insurance Hub System & Go Migration Analysis

Table of Contents

- [Migration Analysis](#migration-analysis)
    - [Current State](#current-state)
    - [Target State](#target-state)
- [System Context](#system-context)
    - [Migration Notes](#migration-notes)
- [System Containers](#system-containers)
    - [Migration Notes](#migration-notes-1)
        - [Data Stores](#data-stores)
        - [External systems](#external-systems)
        - [External Exposure and Interservice Communication](#external-exposure-and-interservice-communication)
- [System Container Components](#system-container-components)
- [System Observability](#system-observability)
- [Migration Strategy](#migration-strategy)

## Migration Analysis

### Current State

The Insurance Hub system is a production-grade platform built primarily with **Java 14**
using the **Micronaut** framework. It comprises multiple microservices that are responsible for core
insurance functionalities. The system leverages a mix of persistence and messaging technologies,
including **PostgreSQL**, **MongoDB**, **Elasticsearch**, and **Apache Kafka** for asynchronous
event streaming and interservice decoupling. Key characteristics of the current state include:

1. **Technology Stack**
    - The backend services are written in Java, using the Micronaut framework for dependency
      injection, web APIs, data access, and service orchestration.
    - Lombok is extensively used to minimize boilerplate code in Java.
    - Persistence is distributed: PostgreSQL is used for relational data, MongoDB for more
      flexible/no-SQL storage needs, and Elasticsearch for full-text search and analytics.
    - Apache Kafka enables event-driven communication between services.

2. **Architecture**
    - The platform is architected following microservices principles. Services mostly interact
      synchronously over RESTful HTTP APIs, with selective use of event-driven (Kafka-based)
      messaging.
    - Integrations exist with external systems such as a local filesystem (for document and
      statement storage) and JSReports (for PDF generation).

3. **Deployment Environment**
    - The system runs in a production environment that is **not fully cloud-native**—there is no
      Kubernetes or full-feature cloud orchestration in place.
    - For local development and integration testing, **Docker Compose** is used to emulate the
      various services and supporting infrastructure, providing a containerized (but relatively
      simple) developer experience.
    - Production deployments are carried out using containerized images, but orchestration and
      scaling are managed via traditional or bespoke methods rather than a unified cloud-native
      platform.

4. **Observability**
    - Basic observability infrastructure is present: distributed tracing is implemented using
      **Zipkin**.
    - However, the observability stack is **incomplete**. There is no unified or centralized logging
      solution for aggregating and correlating log data across services.
    - Metrics and monitoring are only partially implemented; there is a lack of comprehensive metric
      collection, dashboards, and alerting mechanisms necessary for proactive incident detection and
      diagnosis.
    - This limits the ability to rapidly detect, investigate, and respond to issues in production.

### Target State

The target state for the Insurance Hub system represents a strategic redesign and technology
refresh, with a primary goal of future-proofing the platform by modernizing its technical stack,
leveraging cloud-native best practices, and fundamentally simplifying operations. Key
characteristics of the target state include:

1. **Technology Stack: Go-Powered Cloud-Native Microservices**

   - **Language Migration**: All backend services move from Java (Micronaut) to Go. Go is chosen for
     its simplicity, performance, concurrency model, strong support in the cloud-native ecosystem,
     rapid startup times, modest resource usage, and robust tooling for building distributed systems.
   - **Frameworks**: Use idiomatic Go; favor standard library, proven libraries (e.g., for HTTP/gRPC,
     database), and ecosystem best-practices. Fewer framework-level abstractions than Java—favor clear
     interfaces and explicit composition.

2. **Cloud-Native, Kubernetes-First Deployment**

   - **Containerization**: All components delivered as OCI containers, optimized for fast
     startup/teardown, minimal footprint, and easy orchestration.
   - **Kubernetes as the Primary Platform**: Deployment, scaling, service discovery, configuration, and
     secret management leverage Kubernetes-native constructs (Deployments, Services, ConfigMaps,
     Secrets).
   - **12-Factor Compliance**: Service configuration via environment; stateless business logic; logs to
     stdout/stderr; persistent state only via explicitly managed cloud storage services.

3. **Unified and Simplified Data Storage**

   - **Single Relational Store**: Consolidation onto PostgreSQL—abolish MongoDB, streamline management
     and monitoring, reduce operational and security surface area.
       - Use PostgreSQL’s **JSONB** capabilities for all previous “NoSQL”-style or flexible-schema
         requirements (e.g., insurance products).
       - Continue Elasticsearch for full-text search/analytics use cases.

   - **Benefits**: Improved data governance, reduced cognitive/operational overhead, clear backup, and
     disaster recovery model.

4. **Cloud-Native Object Storage**

   - **File Storage Migration**: All previous direct file system accesses for documents, statements,
     and other artifacts migrate to S3-compatible object storage, such as MinIO or AWS S3.
       - Enables infinite scale, strong durability, and decouples storage lifecycle from application
         lifecycle.
       - Access controls, encryption, versioning, and policies are managed at the storage/service layer.

   - **Integration**: Replace any local file system dependencies in code/config with S3-compatible
     APIs; leverage Go’s strong support for these SDKs.

5. **Replacing Legacy Integrations with Cloud Alternatives**

   - **Tariff Rules Execution**: Legacy file-based rules and script execution replaced by in-memory,
     transactional database-stored procedures (e.g., Tarantool with Lua).
       - Ensures scalability, performance, robust change management/versioning.
   - **PDF Generation**: Centralized, stateless Go service using browser-based rendering (e.g.,
     chromedp/Chrome headless), removing legacy JSReports.

6. **Communication: gRPC Internally, REST at the Edge**

   - **Internal Service Calls**: All interservice communication standardized on gRPC for performance,
     streaming support, strict contracts, and code generation.
   - **External APIs**: Select services exposed via REST/HTTP using gRPC-gateway for compatibility with
     third parties, web frontends, and OpenAPI/Swagger generation.
   - **Benefits**: Strongly typed API interactions, improved developer productivity, easier backward
     compatibility, and API evolution.

7. **Comprehensive Observability**

   - **Distributed Tracing**: End-to-end request tracing using OpenTelemetry, integrated with solutions
     like Jaeger or Tempo.
   - **Centralized Logging**: Structured, JSON-formatted logs to stdout/stderr, ingested by
     cloud-native log aggregators (e.g., Loki, Elasticsearch).
   - **Metrics**: Application-, infrastructure-, and business-level metrics exposed via Prometheus
     endpoints; use Grafana for visualization.
   - **Dashboards & Alerts**: Predefined Grafana dashboards per service and aggregated; robust alerting
     via Grafana Alerting or similar, facilitating rapid detection/triage.

8. **Other Cloud-Native & Go-Centric Enhancements**

   - **CI/CD Pipeline Modernization**: Use GitOps or automated pipelines for deterministic, repeatable
     deployments, with container image scanning, e2e/integration tests, and blue/green rollout
     strategies.
   - **Zero-Trust Security**: Strong authentication and authorization, favoring short-lived
     credentials, service-to-service mTLS, and secrets management tools provided by the cloud or
     Kubernetes ecosystem (e.g., HashiCorp Vault, Kubernetes Secrets).
   - **Documentation & Development Practices**: Strict API contracts via .proto files, self-service API
     documentation via gRPC-gateway OpenAPI integration, and shared libraries for repetitive concerns
     (logging, metrics, error handling).
   - **Resilience & Scalability**: Stateless Go microservices support rapid horizontal scaling;
     leverage Kubernetes' pod autoscaling and distributed message streaming (Kafka) for burst
     workloads.

## System Context

* [Diagram source PlantUML](java-c4-diagrams/context/insurance-hub-system-context-diagram.puml)
* [Generated diagram on www.plantuml.com](https://www.plantuml.com/plantuml/uml/ZPDFSzis4CNl_1HRdzoPsBhqr5CIHwdTsbEDfEIgWS2bY0f_sDsb77txOa69aLcbqni9BDxxOV7sFOiXwOElVdBH-hv6Q4Kw_dKs8_DwltFIzjkUaMoAWb7kRGgppfkeP-vOQk-siuTVxXQwzENbUHOC2r9UsZpeYMzotwbwnNiNEqCcW7SHvTWX998sLPKuyGYh8mi6E1s2sfcT5ZU9u2boJoPQXCT-MrKB94xnrkmopZku-RxxGLTkyj87K6983KYBuCO2DXuPkknhFBos2IoX4MGmiOPWOfRKRU-iGxw_-Rcg1ghRKMAJkt7fgKI3Ot2M4YCTd4M-snh66hYSrZiCwYxk2b7JUm_UDMYFrgDoZWQZ_JjeA0NukqzIZYNep35Wb09R9zkNdtnb92CknGlUpVoRtFx5X7hLJFVxQba-OJNmAN4kGOh6m-BJP_XjL85vp9YbOecnHc9uNByy3wP6J2QLf6T616tKfU_UD7kZBUcCRH7-qDzYU53UbH2Qe3VAYP4cKT2z1QM3g_KX822psI5iAUsHIkyC7wDjASNKCzWKGX-T7MYtAA-84SwDdx1iJTnl6iuE7_DNDZU2XJb_9iesQlxPZV6RFQo-3i_9mpxuT79I41UqYmglmi1dEMYo16l9-4WTfOp3kNQ298PSqmnGw_8DozxZ3ubUliwV_mD9ju4jkKuOf3K2DJOkOWPmLab9nISCTRAyESbbwK_dCFq0uQDFfv1tIEnOjFOQpJcDM6j2XYWVtmQbgfRepxCx7QilZ7g9oQ3AUKulnlJAm2ZXutgzM6KNzPY8Nup3AUt8_oA4NbziL9ejvwlrTRbpoXVzrZjkaGkSJP6p4cV5O5m4F1JLQOWlqd5T_NjkdqhaU1AujraZpBxMULSUOdoz9Phwklnb1hMAtwQbTDnustjjUceQf5A0_3-q9zdII9sQGoZay80yYSdO_L7zQVQdHkKznheF_Xy0)
* [Explanation of system context and flows](java-c4-diagrams/context/insurance-hub-system-context-analysis.md)

### Migration Notes

1. **External System Integration Changes**: While the core external systems (Web Frontend, Mobile
   App, External Insurance Systems) remain unchanged, the internal service communication protocols
   will transition from HTTP/REST to gRPC, with gRPC-gateway providing HTTP compatibility at service
   boundaries.

2. **Cloud-Native Infrastructure**: The system context shifts from traditional deployment to
   Kubernetes-first architecture, requiring updates to service discovery, load balancing, and
   external connectivity patterns.

3. **Object Storage Migration**: Document and file storage interactions move from local filesystem
   to S3-compatible object storage (MinIO), changing how the system handles document persistence and
   retrieval for external integrations.

4. **Observability Integration**: Enhanced observability stack (OpenTelemetry, Prometheus, Grafana)
   will provide better visibility into system-external interactions and performance monitoring
   across service boundaries.

5. **Security Model Changes**: Implementation of cloud-native security patterns including service
   mesh, mTLS for internal communication, and enhanced authentication/authorization for external API
   consumers.

## System Containers

* [Diagram source PlantUML](java-c4-diagrams/container/insurance-hub-container-diagram.puml)
* [Generated diagram on www.plantuml.com](https://www.plantuml.com/plantuml/uml/dLT1LoCv3hxdLsWvDFHx3RdiQKy30QPXcfriGZD7dk9I4WykkzPoGMVtxNzVo-KgAaN1qDnIjgNli_HPKdxXW3xKfPdyJLjbwe9W6qB5_vXEFJwTR7JOrgkQoIjd0zbmebmvhGnQiJdcK1Yzcix-VZoNfUyttwObSY0Vb-vdqGAr9NzIYVy9FsXReSSIJ5mDkuh0-R1raqdGmH1qvw7Gk95pQ-VXodBjqIg2h_KAbZiEL4ucXt2gWlCycPDdPuzmGpPyWOFdqwUoSX2NlZCnX2q19ri83fYC0TqThPpHIXC3sW8UDJt57sXsGIi-EVWycJIWzsUkjWNwtL5dUHzZqu5cYWUVuRyJ0813kFGfPWLyWbynq1FkqaPtpoEkC9h_GIkugoFZgXA-yUV9doo_ReMqjXj3nnLk2B2g8b4Csbcel7lKHTnCLvC4WOvWVesA8ka1qAQ1ZpvJM63kV43JiXAi6wqYMwp3FZ2G3Nu7bOlEKphG63YTNu6dVzV4WM7jNPauhFDj1Jq7OEOyGVBCv1-r8XxmYdZR-xmdvE8dB9jFuN6DZpZTO_OLRM68cmkBDTcGGvAoT_t7BGJtG1Os507Inijmf8JlUi3pjF0sz0rQt123ylICP_IQr4uP0hT-JL5klIRF8ooyAseL-ZIQbVVn25lg8sPJXG6DsuoXHKPsqqThLjv6cw5HjS503EA1M5Jm4fTXXKm5E0i5hIDW0Rusn6_47htQZgL0rj_cD4_Qb-X6CEjyYKR_Xuelq3Z5bQ7c6RjrpiGe4ToLPFV2twoyJu7DFRRE5A94fLnjGygtX4GHCp5aY17mmgawRVNWpzkrFe7VN33QxbFujP5sbbSfUkW87NkIR1NG8S0HdMnEli3y_98_Zp11tguS-gBFf5rxPnuO3N5NDDiwohNa9RsLaGZ4FFLZFukVxqJpXEOuw990VC2wjaem8bZO9NMcPi2H0KAihKkd7gZ9GLkNpZ4WB8Fpmu9qlchrMQmwJQRZ8czXiJW_krdAZyRyFIzTlEPdajqs7tj-RvpTkFEpdkUmVVjLPyzPOzbp-jZtF7SSDfwM__esfBqRw3OR1u-MCSLqz2xvwIR0_5m_mMcN_EOI5mPPFjD-3xZ_OA70W2bZcKvpMiWC9IKy_YTj_-9781zNyoJ0RNEEwqSFk7v0QICLgYt1R_APk3ocLnqyOIa5RsumHDHIzYvHQQD3yvfuPzNME-jg1kNAihPjRrbHU2Aoa0KhVFh8VxAdolaWmbqkqi--c5lIyWOXlrxdavp85kGPljx6qIc5GzlW0ELazuG7O06zNg_lrooTEls6XTJQf6ZSKBgMDfH3DBsvkzXBVIxxFjaWW-m3Aw-hCBpN2kr30tGMVqK032ICNiMQBUzwK68Et1b9gZkLvIePcGmm2wVudfFxVbLy3V7Ck5KFChTzAleHR5__8Ip8D0LfgokMXUzCCcC6yetYuTAufqayrewKN64U8bFrlxsTBwClEP1sdYUh6tmWJhEHGcCOWkiCfjVBt_y9dvwhaR0vRCSl2AuL2yFHybc33Ko7C1YMxj3eGdJR33mCJpfiXnmxunSZpyBLOjlDSS6zRfkBryUCX_t-vrraoNpCk9F01ysxzlWXyq6NctltGvCK5sifwIqRTyh8EasAFCwgW5c_5er8P9ZSbxFU8dDDMmGgJtNYJWRuPwKkBfQt1sDUnsHmIK5jYTiTA2YWDZpkKlwpzGjiKvNbkKcdRghrA6vRrkVrocWMlCEcCSyyoKi_119NcNK-_PhiPivoNRxJHzRTnwmxuNtKlbFUJzXF3hlUFwXrendjpJeBmgB9NEg_Jru7QgLzVNuso-HUY20llEsact0YAQcpZvJRCRTkXw7E2-_WDZzjVOodF7__fIEHgPJlxAqV_WzxsfzO6ZzoFZUpqHmDtsw_tNzp4JMlv0Ok5BgUU3NzFTCVKLzlIEdAWJJ9t3BVg0MZxWPzElTVkPodu3KzKd8_nd0yvS_3nbKfF8VpmwYdoIzaYxeq_mS0)
* [Explanation of system containers and flows](java-c4-diagrams/container/insurance-hub-container-analysis.md)

### Migration Notes

#### Data Stores

| Data Store                  | Java          | Go                 |
|-----------------------------|---------------|--------------------|
| Policy Database             | PostgreSQL    | PostgreSQL         |
| Document Database           | PostgreSQL    | PostgreSQL         |
| Product Database            | MongoDB       | PostgreSQL + JSONB |
| Payment Database            | PostgreSQL    | PostgreSQL         |
| Search & Analytics Database | Elasticsearch | Elasticsearch      |

[Reasoning for using PostgreSQL with JSONB for storing insurance products](migration-reasoning/replace-mongodb.md)

#### External systems

| External System          | Java        | Go                |
|--------------------------|-------------|-------------------|
| Service Discovery        | Consul      | Kubernetes-native |
| Event streaming platform | Kafka       | Kafka             |
| Bank statements storage  | File system | MinIO             |
| Documents storage        | File system | MinIO             |
| Tariff rules storage     | File system | Tarantool         |
| PDF reports generator    | JSReports   | chromedp(library) |

[Reasoning for replacing Consul with Kubernetes-native service discovery](migration-reasoning/replace-consul.md)

[Reasoning for using Tarantool for tariff rule scripts storage and execution](migration-reasoning/replace-old-pricing.md)

[Reasoning for chromedp for generating PDF documents](migration-reasoning/replace-jsreport.md)

#### External Exposure and Interservice Communication

Use gRPC for internal service communication while exposing select services via gRPC-gateway to
provide HTTP/OpenAPI functionality, combining the performance benefits of gRPC with the universal
compatibility of REST APIs.

[Reasoning for using gRPC for internal service communication](migration-reasoning/replace-rest.md)

## System Container Components

1. **agent-portal-gateway**

   * [Diagram source PlantUML](java-c4-diagrams/component/agent-portal-gateway-component-diagram.puml)
   * [Generated diagram on www.plantuml.com](//www.plantuml.com/plantuml/png/ZLN1SkCs3BthAz0ScdIcYIyzxMdT9DjkfzlnnDdTemScOOarHQeaPLVJwRyNfAI4YjmT-KI2UC0py22yzm5TQ6jTVAUCqAqag49e_BlLok7vhbIXQlUj9oUi2MJ2dR3rgj5e8kRM1wdLVdN_q-qccZv_-drLem_asBIxjtLZJSGqCNxXZyeqwB06hGo5hY6mBbIsA88Ack357wJ2CleTh8Dr6IqRzaGDls2WCtP5SGsFVtCQmxPjnmbh_mwUAm8oyZQolVQmT_RC9q0Z8V2Jj6VZWoEEMolXBB-Tb21Vz05s7F36D_WZN6sLAPdF1ak2TTDe9J0eQwxuwKjBTt_wU8hP3YuLHGA-UW6dbx3lG1dVEZI20EDVy7TNF-Ifi0tLRk30uTPyXMr_ZU4_PHIdmCPogYw3Rf9X8HYxcidawDwo32xHLtkBJcO17aRRGWREobPC6FIMfN0bk3qvlBSiWuiAyzJtV5q8jDnE7I7f49q773meB-o9NCU8Wfy3AaDkzxDjZKJNtPIzQ1bnGSiCWdyAuDyr3u3Bfi23FKJXZex9wUNr9avf-YVaEFAlpyyRsAPRfFO7XT35mre8QZWKQWtAyEp6IZdwgoNFLjPl3I4JSjJeD8spROZr9bCg4qlsr1lWCHbUYcPOp-c2GLJannpGOAWy10jSm8CgMqSodyIXrX3Ja9-dJrDoK3gG6uQ4hUEaVKZsd49ifcYT2bts-D7m8PLXP9K8dL0hYO7Wjw_FV3-IyN5i1PUA_rygIpwKSswf0q8hFeyTk4-tAQC74XeTCpvHzlx1kZCwEQKKSsBJECign_3_dwmvb-5b9qSjNjBdHNgzddDcb3gOo1WAP-kEe6rVygJ9UVjw9GsX8hb1q4-aUrYb6Wzd5IfbPf9cfwcOPvBReFSqgHId6A5llQUAIPzlnlQ621t3P24ca6bNrrhRimVhG999WVg-SSPlSCXZp5iD9uKPUE9ygO-VFGt4lTKd6drUcyB1_irNuhJk1T_wnPPNEKxgaUEkDn-Jv5DL1nblLq-FssVu7hxIVcl5aMAnPbJVBB0lmpZvrtbA7Py6lHHejj3Y9sRMxnbiligMuUOxR2bolhgMulATjHoNBwlB8BQ-xT451RtihYSgbG_a-ba4D18ubbFauyo-9oFRMly7)
   * [Explanation of system containers and flows](java-c4-diagrams/component/agent-portal-gateway-component-analysis.md)

2. **auth-service**

   * [Diagram source PlantUML](java-c4-diagrams/component/auth-service-component-diagram.puml)
   * [Generated diagram on www.plantuml.com](//www.plantuml.com/plantuml/png/XLF1Rjim3BtxAxXSrW9hSzbfftPjrwLeWIn9iMF0M8ojLPOqaKuQ3FltIRRZUiw0tIpolSSdwlcA1OFKbSdUQPkRMX6K8fu_JQS1zvU5bhBUr4mXTrR8ocNkggatQ1FdWaKPlPdUVBoOfzBpq-EqGXOAiRI-SPLtDd5yqi_uHLkF0Iimsf8SF84BKhei4os6eCU3qbWatDO5kAwbX2M5dSufoswY2upiiFxiQgim7Cum0jRS0jQRhle19aFYv1n-PG3mDkCClOwWHrTe2qarE6CeJ6BjIUV1sIWBYxlbQjIyU_MEYG7R9bdbdRRIM4rhWcGdwkiSHJjxEJaVpqrsVN0xhIWqDeTuc7UTDEi1TpZjtVHtYPslQ9KXXaW5umgTWzl23erMKSKMhPCya4hAQFZKne-z35qyV5zreySxY9tXv7ko55288Le0reNb1YBkXU9d7UM1wpmdvcrj_hkDP7GToBie_PnCB-8tQt7Xq6nWVZsemCmAXIssJcvfgsstWnJEW7dx384aQDhrUr0e2DlWgWHjJXjaElMYDcjKw8L2KkzWSDsMZcvcbJTKHNXpeLJjAusLTcHuyomETBATtEsrb2ScVcVPyi12rVhkLSuA5DfZazyYQiBS1K43zsrvakhpMNyy7rBL9dRdZgK8jFps2CSh9DA2J6COJL-EvEmz71jSQizPFBqvGAyRvi-QM7Zqb7tWcpYkLlFbDFqGKJHfDDHnqc_9K18_aHBNqKQCOJY-aEGbyKcoOQVnt-nHmcEwE3r6qGMuJMn77KpwAmKp1dryNbAmENJu8QXP-u9GyIXkNMGYvOgigYlp1m00)
   * [Explanation of system containers and flows](java-c4-diagrams/component/auth-service-component-analysis.md)

3. **chat-service**

   * [Diagram source PlantUML](java-c4-diagrams/component/chat-service-component-diagram.puml)
   * [Generated diagram on www.plantuml.com](//www.plantuml.com/plantuml/png/XPD1Rzi-3CNl-XJyvz8M-BUvxBHJsw0RDYn0iQ3OqQ1bpX4gKu98Dos6VVUPiEDaJOlTegVtUwH5vbeKY_PTTFy5zh5l2BQgMJukbmLtLstGRL_tGiKdLcAzygbRveXipALe4qEzN7sul3VfOVrjsQ4ebK6gLgdBYOt9bk_aCN36WXt4mAGlcI0LtIRdD6WacFtG16pDzpCLM6rHOKFbANXoRfLOSQ1BTPjwRh2ydFl1KCbegEf9_HyMn-3Y0duv03ZKEDzHBSa_qiXIsHC_gDwOFh9K5iF5ElYI6FjtRf6n9O5CHO8eyR7D9sRo6X8B83VGaSZWXheaR3oA1cwl5XUlcfjSbKyDUIinKIixMysl-8JBkQq3QNTtRDXOhg7njptIlhP0JReZOiWH0uFIivePH4lljIyqSAXusfQGI4XSTVR5P4-s6HLOZyg_MbjZiFbDRw45_QC0nWZeDJmH-1Z-VhwfebWllvtRl8XILzqzwtcBIZkqGT-qnjodeXZXyoWlJB__CXylZj6wf9rGsQDdOWNXTbID_5HisRc1cvnZy6ZZ6oASEuFl5D6EiWrPt70wf1qwMer35z1aSVFd31b6pCOtTl1E5EiOP3iDC9Vq74ZUt2ZxVqoHfqclL-X1oFgnG4yY4DYdBd0BLYNrsYOxJ9Iy6tko0dFmg9mi_BpenzdkchZfk_W7)
   * [Explanation of system containers and flows](java-c4-diagrams/component/chat-service-component-analysis.md)

4. **dashboard-service**

   * [Diagram source PlantUML](java-c4-diagrams/component/dashboard-service-component-diagram.puml)
   * [Generated diagram on www.plantuml.com](//www.plantuml.com/plantuml/png/RLLDSzis4BtpLw0-nCvCOrrwwYcEx3HkxHcDvKoFcYMu8Z460NONjCpfzByN2yWCH4KdSJzs7zvRB3vp00fZRwjVZDDsR51r8Gpy-sf5SBXkJUZ6UcGaxLr05wwrxrU31ISvdpWqrjIhzM-VDcBw_lYmwe43KZJjrhuVl9ESGUfN_6BS00IziiPXc0PKdaBdgogOO57DyQenq4hStfEw1Uvg3zIeBTAhqLXLb-hkBI0vi6exSS2U5RZPprKsxcBGLGi13p3zgYvkMgcyYHrZsX_PV27spNtvAKMGLJDtRI202bw53jMU4WdDzSN7iidWhT7JZdF_M6IJ3E-0fEoZqUJvn_TchAtX3WMvmbU1TkZGAL0E3oeNL8QL9eI0ouOli7y1mJw0Zh3-aa-fUfSARGCXzCQrAeeI8eUz-BwGVq7Yt8nLZU60wDGH4oywDBMKiy31Q4OWtMLov9_we6uSs4bSwiwrnc5o1a-ObO2s9HJgMJ5Ov4oYg0cIo2QrgzRU1OZPjFlYHzS0JLSpxmMRPnDmyL7zMwdukrIYtWDCIEbxdg0h64oPcUAa7NbhaMP578n1FTrjdnVEkxV1Ct9seMi6RuGqEK4-4Cm4W6iMqvBEbk5bGRx562jwV52tlWVZr8DlZLv0JiBiEeb5ceUeB96n9RNLW-60BbksOysQJ8qiWtgP4zTvSYwpuCaJoBGjKgc9SGs-HMWHymBEVC92YLdsShJ_X5TOpMoMdWrvZSmu3vrmDH0EGAUJqNWzzlZEy1BJaSiD4gT3Qun4thiz0GSQTHW95zXJwLriv8MTrETM-Zn52vlWQRfOZlV2Tujxuv3JhTSo6g130lVFY3H5S7AM6MbnbdCYcte73Gm1wQpdJRQ_iwXElMjhOhuux_l1eX2K0T4CDI88I7lGg8BF7mBqO49tMkmCLjuXE-qTH-ffdgjrDYokIF8LW6AKGvscAlk43k7d0DHmEN8TdavmSJcUq48miLzd1XQRMFBDFH7mEwFKojJ_s2tvikLpIURo0fqDS55go9Dv15hK-4dIOfXUJqTtvAX_KQ2CFb7-dfKvoYZOwwb8AWFJvlsRJCX8XLT2Q5WUd_viR6Mn5OME3yEPjbzFb8brbpb5y78FFUUj8IpzLxwz2_tYBkR9wOwyyoCVHVzg_Q6I9eidCYCity7bsYgGIjDjKJfTvRdo_CBdbVy8ByZ2Yj9WRQAmnR3Oq4BZj-VdpNOb2pvs-8ok6NlxFm00)
   * [Explanation of system containers and flows](java-c4-diagrams/component/dashboard-service-component-analysis.md)

5. **document-service**

   * [Diagram source PlantUML](java-c4-diagrams/component/documents-service-component-diagram.puml)
   * [Generated diagram on www.plantuml.com](//www.plantuml.com/plantuml/png/VLPDSnit3BthLw2-s9bfh4jFFSNnH-B6dcWiPthKKBlGBcikoI6uLZMT_lU2u6f3qPxwv2K-yF3mGEeJHHNYE9Z5BzesPcmH-XWz_RvS1hM_x7Jin-r861fd8zfusRXXwOsoAUOZnTRexVBwju-hTFJZyM4vA8eO-6XpxGRlR8hnAV-2NhJrAgW1ZBOO3nx1XTYxnIBgQ11cVsYrwfBVpWMuSSquy2716iEhRd2nE8VRlxc6LGRM1wus42ZR7ksqo8SRThheLCIzElmAPrTTohpYYXps9H-VfVFL_ScdCMvFa3ecY0vQjxV6gHOu70FxjT0U4LsUVIXhUMTqSzXGXi6vLd9mn9MoF-ecEFhvlHgtHbEFgG70ruHmtwC51HRtu7Ey9cW2fj9LmHUrUr49irTDZ_0jVQQijv9e7JbeqBO3daraAWT1W4IgGzW6zu9165Pqi4qVd7KZPH1rPtzHGEz2vFW_ra_oRzdLF8tLpHryoJnfP-7MTjgAnnFQDX7wzPaLqggeGDleGAM8cS-gw8uuC4zVkd51TPBiJXkS1WzB-6pSjhIkWdlLBH9uBgWffc3APkaszbZCCjNVSJxfU77jR5IQRPlFRhIj2eUBUTZ5NDz8ykm3_BC0_Zk79AS7TS0WtxEaBvJNHQIufMh16ODXbePLOuIds_LpQHI6lIEaR68klTDSFlKp2vG7VQhC2L0MmrTbMyCkxsAJcM-Ca8LX5kuK-Y3d6OT8R3vXzh-vo6cJQJrkgGbwYx8svpd5TTRjUPQLM6BGNOT1cEzchJ3W2fI8UzCuIznIc6jVoq5DsMGpcYEJXRqiDhbWjd7_YQwz2YsLQ7gisYoek_AUcn1yonitA6tXmNMwgP1o5--8erY4SbSoOUN9zy3BIp7faIUAiUbbb2fcq8qoCZX6MdBcmf6o2UoztGN5QSOcZW5XER515LZVxZPp2gvUhAmBHmQBFI-ErcYPkuY3DunLm0dAYSRwPX1r1bbynlzsm2pMJU28ahEKWJGZETIBVjKg7_FOHSj8ijMSMO04Z47ZQ_gSLnfsmGrGto35V9_Go7oervvWhoCpBwrCqnGtTihBM-3zGQBoFAsVMoSjveXwfKuryfnte0muSQY6DDVBpq3gyFt8Tq6IUgr3feVZpOhTdIm81nSnjNFTOTdKl-KRVKep6wxAzoOltvrn-wINwWLPS6Zzh6RKvTjPNpO5UabGiLayPbCb9Aax4ij71FRyl5gcgpOdA9GgRzAVGHz3efjrUh_ypkxSthheRkvhLk7DT5rBuldNINuT7jKBaZmDQUbFVduSOQt_1zSJgbPWYV1hO9_GjkDW_WC0)
   * [Explanation of system containers and flows](java-c4-diagrams/component/documents-service-component-analysis.md)

6. **payment-service**

   * [Diagram source PlantUML](java-c4-diagrams/component/payment-service-component-diagram.puml)
   * [Generated diagram on www.plantuml.com](//www.plantuml.com/plantuml/png/VLLDS-Cs3BtxLx2v99dfnfUUUbhNctIpJQPkdEtL0ucmp0b5gW0LhwVJ_ruIj2APpkHcFtpmwU61_Cm1AFIjdVraN6rxZMeNGiU_pkS4-vl6X5rVzOnKUnVGXPlQj_FEWairdpXeQwhvyfTFgmHzVtoOjy01AKARfMyxxr9Db_hF-CMu3WXQPOt3SEXGUGexFvi54oogjtob3JGfR-j9hU3G9coDz6fgdCsMaGZ41hJvpVTE0nskkfopuPppixeegYwkrRypfTHunXLqPfAtMDshr9YyjKWNCV9eQl8E-g2URjVFHV3sH-SPEOVGwSuR5rZuTjwQ-g2WhdsVC71Q7VdnpSNr6Olk7TQBhejD81ZlthuWXh_1Ayn7NfF8drJla0D1G1vEKxv3aXwPHEqzQUCaHViMZ5FMDwO-9uMlISnTBB98YLR-fDi4gwy2Ymf_mFO5r8FXW2uZwxxYcao5h89NbxbmIGW1zQMIlXAf1L8EzwLUvsnC_47JsIxNVwjx0QSIhAC2khUerJTV9M15laPEKwh0lIWE451qsHeRmItvLeKTob_51uvHvPq2nSTEYKh19PFT7CcEqYmo84DQBTIYQGWR22X60WSDifmb99JVAfX-jxJAssTS4-bQS3L-x9mtpnDs_eo8Ueeec-3fC70Q4NNl0j8MicvVS6lSaLxQSO8wY94rXCWIYMM-DOv--f1DK_fPs3In2PqP-elOS8hSkXewxYs4J2bRLFYGD45A4Wsov2b2EM5eObiExtchuAsh3MZeOjj41m9KmAWM6HgaYsQpqbW-8A5lI2P9exPcq5O5Btz4pxq9EybSUGuDuVgl1wMFfvsB-PBsQbDxntsBzBPeIm7As-aOimQp_sAMmpglMOw1qqd0FP3cSGU5pibAdnD9gx7fyXvvEgxUNGJLQW27Usciajq3OcGv-HMiqH3Erp3rIWV8MFwRpTQoWflR7-4ghSuU3h7HeacfAqy1hFezm-c0T5yFVw-dfNdaKmyACFMOQ7TCE2qMuLFt3keTPlbIHPPf7GYXJVPRMGXHqVQqMbSnTJBWmKwfmnDQCG_O0IwEtdBC4capA5TQy0GDYlkZMeM0dDny6TDTlIFlVC_gplezp-9PeuBvcNk45sHvv6gmDeq_zV_w_BnQpzCR5bkbAaa-VPBk1kCG_jCZYtM6x6bYSHrzP-IFqveHd6QVzZbPptjMPjWxrAew74kcIReIiK7dgq4Sk2SJK7ZdVSwwBNWYd0nOv9EEfOa6NwpwoXhU8R__j8tI70lAHHxLBFEBnF85VYRJD4Y3I4Nux5NAj8RiCboCQWsloAhdQEoipCbBEefIliFbvJ2TOXdRyZ2MSUrbG3c_xFsnJMQVqUc-jVy3)
   * [Explanation of system containers and flows](java-c4-diagrams/component/payment-service-component-analysis.md)

7. **policy-search-service**

   * [Diagram source PlantUML](java-c4-diagrams/component/policy-search-service-component-diagram.puml)
   * [Generated diagram on www.plantuml.com](//www.plantuml.com/plantuml/png/ZLJDSYCr4BxFKpGviAQAn1TEdDO91h9imBNEiaTNUzIsXJMIwDROcQ9uTzII6ModKaKk6VVqzwElU_HU4dBgEzTyPNthUaEmIodAzzCfu_5kQzEkN_T2t0QVoAUxDdJJwD0hvbQISNOzVVZkTg6bpqyVfnrA8iwbrKFeOl2AYShVoDxwY8mTEEif3P4WSDg5faasEOAn7up5hVPj0iCYEDiEi2JaTfV_yS6sr3GBOWb-eiw-XPj7BpsZRmbcsqnmaqipq7SGDf1s17Pi47Ga4BCokhkRRvg7u1DcFpoHY1dtXTRmUv-9ObIQ_7ZtX-ZJWiF16XBbgzpM9-8DjbICncfKYj5BvYqcEkAWhjGUB8ew_5JBIlvaMmuU-sBzEGk8zLj7aBjvoDHPgeYWSp1RF0BJdpr9aakTQc4bDQJCD8PN2g-4dj3ZbWHQ9amsU4shSDXSH6yWzcjdPKS2T21VvPhb88cwrVmbJVQusQCUAsARJV-YFminYMHckEUm9ywWCmn9RfWxb6JR6fOYJlFz6cOUtQ2lOEwtrfCgZYTStOVU6-HX4aLfwe5lAyrjFNT6m5yDvBzneIOOxQXHybD63iuHg_Yd-VBvgZH_YK58eBmYRye8fCmW9yz37iyObQPogTS6Btr7_Dy8vYM_t59Vg4H92pvQIUHhfJuNbHe_XvP4RRohD0ywA3BlwZXo8uUkflxQW_M6NiujF6gX2dt00uwjTUSOlMJTJYet64oe6nu0JpkDQqV0X0QwOCYzLcIAuNgsy2aNnQR0PSz_KDPPgwUwrFyPlN4aPS5LOtfacmX292whAN2qQLU-lKj-zRb68RMZVhAWg_C-sVci-QK0cnp8EBBgNNlFPdMM68hGHH8fd476nYi9mGDLYQ6WCkJlfb54lQSauhY6LCTyPaZxIiTuNUHMR-GKVzqxDQ9nFJylbjCFozz-FG57J5tr7mCVaVydjfw2HBy4GdDQgobyqH7emSPl97yw6QEWrJss-xFi_hq9weA_jR65kQfUNrOZmwBUEMzHr4aqxycRldF_0000)
   * [Explanation of system containers and flows](java-c4-diagrams/component/policy-search-service-component-analysis.md)

8. **policy-service**

   * [Diagram source PlantUML](java-c4-diagrams/component/policy-service-component-diagram.puml)
   * [Generated diagram on www.plantuml.com](//www.plantuml.com/plantuml/png/bLP1RoCt3xtxL-ZB9Gdm9RxqrDDcdM0txGRrsjcpGKkSiM2DD1KvoHf5_tj5oUCPUnPRr0S38ld8fqUHc0_44BbhtEn_rclN6LGxvfP-dSyZlD_NbdVTjYECEdX6p_SwDFFMWHVC7R5nTZjV_7At5DEtbo_p1eWn9jDc4PesUC6qadz6U-jRYD0ePptoeKKL8k_2RCQM7QfJl38MQechGbJBuAm-g3N6DwjnDhjMJzyPemUdrWTYR4Y1DxsVPiMuIK4tDJ2-m-7_wkgXbhJB43d1FXNpbTYNp-FZwmvL6sq3yQ0I8FstmNhEF2HKWtDqVtKxBfBYjVNrXWg1b6LPB3qbIVnYTGmqd1VWTEU0aPJWaN89wwcBuBMG2ARJV5bh3zKUX7OBEb7zNOwIxkbDhhVcYD18vTGCJWcRN1g9e4QrZM6FCTS1EdYzYy67ZXGcxAIGsIRawl7ZorfIh33biq4aVmI6BH2AUSqXoWLAWwG9eQemAfDY9Dzi4Jo3zHWt7qFdJLBrfWJtOcssnIujEk_prQtwQwRIxpeB_mKE6EKyF98RQEq0ao0f5uDp64-IU-XOhPxMhnVEf-zj8AJYGczocqiJT4HW4J5VfZokpDfYLkd8Ql5rjLPtGgV9pyyQlE2dYwk_wy2rnupUns_m1lE1ySZpEKKw90NEgVTe6LLeCUPsa79sZoV2ywurFsD-Jg_l1DHrn1fO-FqXk7clQE-WJ73vC5Z4y1WQi5urmQ0ZnJjWXLv3I_bP9mEgRKVM8oMQeROwqo5EJf5vGknSrgyTniEFHFrJ75D9S_n_5pGYcB6UcTYqroji0raEyJ1zY8EICKKDZ0PCBzyeoxDdZ1NeeYHM8bEMJ9Pi15qsZioHIkJ84gFNPvs_4Uhp_21kfxqrsmqOQ1bZoj-Fh7eeffxFSzCwR59uJYdMawMGYGDX3ieAPw7sRdcN_UQOUAJKiwyY4CTESnTHpTN2MSbtmQ_VcJfxfolp23iVuy-lhykn0_P8Qhp9P9ZBYXQ-ml0YxLIel50tPTjciV9ALMMVBhkjixIxN2SbP23IXv6wBhrVo42YkIwhDPCf6AWOeu9-OrfI16zeHlhrcvPsjgMijFNv9iSNBs4IL9RVTCEiGiT8PL7qRY6GAfmXynXDSNaUn6TnX3f19_KcVVn66QXgvEn4wOVkZfSQiPYccgwZecFPOqqmjhBuqnIJPhv6MzSOCwEsTAdqP9HbMlTyd9_fD896RGy9UOAEHPpWUYKgPBqx-lB4J6iNn7XcfQhjXx6l-_Vus-VyfSpIQVZQoHqkyp5yjKWxarM6Q3sNIKaP1NNPmkddp2TaEZQpYg7vOPOn-VBjW60enqlxoeFC7ChoAR39-ENpluzKUPgp3-XDrxX_0000)
   * [Explanation of system containers and flows](java-c4-diagrams/component/policy-service-component-analysis.md)

9. **pricing-service**

   * [Diagram source PlantUML](java-c4-diagrams/component/pricing-service-component-diagram.puml)
   * [Generated diagram on www.plantuml.com](//www.plantuml.com/plantuml/png/TLHDR-Cs4BthLqnzsIpGZIuzzRJRr46tSB26dItGay5G8siGYcHdHac4el-zf0WvaelwPCt7lCVtEFmiQbY7tbK_aBTkQ10wrIW_rpMRrzipQJSy3O9iWrVqUcj3NqTdVExv9DeuUghlVlgqpw7l3xkwDwB8ANIw2tqCFlV4FB-IP_BHiEd1aKST8q9WxK9LAQb3kDH3G-QSwzh0i6UovCzmH7uXYrNr0RPlYkoDW-CeYhs0ySsSbwe4Jwde9WP7TZn9IVq8c_qKc6itAV90beEyVn_mhm551IAd25ZZxE2CKl0oqI4l0nkVKg5jaULsyt69Q8DlwNng9Oswc_uFF7L33VVa44fjHlhpIrBhuO_jRaf83kqvl5232HnPIBBU81hOd742LyFKji23mmcuIX1go2EVVWc3RmoFDx7ejJpqMi7DH_YxWlJx05_sNs5dHkJy_Qx_ZOcqwCjL6OY3SyWNqRmP50xRu-DLSliMWw2K5Fec1l9QnBkewS-JlkKKCvaO7Tb9hFhNq1loi0jdibVSyep5uUxU3LhQ-hjvCVM5vZBpZMs7ecmK1RHRMGnF8vX4Oqm4IoumezLhuQyeJU4J-ZFvJ6doz91Zi9rYcpcuVJ5kC1fuCjkmOBcx-8Pso7pIfgLwaO84hqogw84yvFx_8XTU9yOOqiJ7mlAGleGIoZWhyTwH8xjWce8Rv_jchCse6fOdXPOoYPP3dyGedo3J_LtOTi2YdNGK4sFIZlnKBaNpN9QBogMwSky-yAlXHbOcS5b1q91Gvkvru_hStmKdxYsgxLPU_U-0jMNpXDc8aWPpDkH5eJ5g5fDMuCk5By9cfuf-OfcY5WMpyDC3yBN-bcRzitp7rXgcn77qjkFWmo1mxyBhvI54MDptAfEvVkBAwZwOPvIyk5bObuzsKHZBvcK-lpq-xezrtjDivsVqpT2xVm40)
   * [Explanation of system containers and flows](java-c4-diagrams/component/pricing-service-component-analysis.md)

10. **product-service**

    * [Diagram source PlantUML](java-c4-diagrams/component/product-service-component-diagram.puml)
    * [Generated diagram on www.plantuml.com](//www.plantuml.com/plantuml/png/TLJ1SXit3BtlLw0wDFQC8rrwwYc9vRQUiJkgvPmr41TQSS8bMG1hHTFflrTOIglDDZaI1F0U7XxvGHHPkpPKF_deGbSJx5Mp_BfOC1xcZTTzj-s4sAMe57NkKhl80QFLl1UjWzykxdzvlxBGv-UdHOkYnAVGvZwrEKMhoTQ_aYy-PcHi8VX8Uim4YNMVgagz1e8X7sgFZUNj4iEAKzqvXJNnctTKLU_WuQiIHmomFefIAu2nljnBLOARKz9DWqe7FDx1x6DZVLU95GFyNi8pYwyUnySJs9klII0NL84Q5K4Jw9vWnxq6zNnsEqR9ANXtt4WXO5twm8MHzNtsZfDSpx-HklqL1chQ-UZLfoXmG_DcVWT_ToHzu1OEUuhWc51zR23jTiGouL1l3IN59Yq_6S2VQVtN4ooJwreRV8cAMvGUUws9IVg1V9IECJeQY3XK3AcnxjLzYee-4cy-fIxMoCURSzferibwPhVmJmK0y0vCsYSy4jlvkjmRp7vKPra6n2a4ua6hY9t2oyFwTNBvy3Kd8IbN5EkSV5JfZQBxxuzHs2nJYpx2px0Y5YzAqT64sBduIcvsqNGGqY8lb9c4eXODHL49qWumJlMqNbu5N7eZbZkpwNMjnksx-1kcd4OAdV2IU4ry7AjJTZgvN9gDphjrfXiwDMsAY_DrSaYd0nQ_TMAkOaAd_eqWeofnb95sZt77ACgTquwdgjNR3TQObTW8d7q67qkew7NkNAHyR7CWqxA7juGXKeZpC1BugCGxT0IQoi46EdZTzvbdlqDzHXrHVg5GnjlxB1QpIB7UHERFOe8bMmmVBvSoknsAlis_ckGP8pRquynlX7aKy9SngORjSLHsJRIYebWjzjkqlMKExBNdUFNZeEbQcR0r4l-EVyR9_8Oo_8KcWh7_GjA_FeSXz0e4Bpe8O5X_lBwkrWjxQhDRwp7zwNxSgY4T_MsAFlo_OVM1OjsruJy0)
    * [Explanation of system containers and flows](java-c4-diagrams/component/product-service-component-analysis.md)

### Migration Notes

#### Component Migration Strategy

| Component                 | Current Stack                                  | Go Migration Approach                                                                                                                                            |
|---------------------------|------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **agent-portal-gateway**  | Micronaut Gateway                              | [Envoy Proxy](https://www.envoyproxy.io/) for routing and load balancing                                                                                         |
| **auth-service**          | Micronaut Security + Micronaut Data JPA        | gRPC service + [grpc-gateway](https://github.com/grpc-ecosystem/grpc-gateway) + [golang-jwt](https://github.com/golang-jwt/jwt) + [GORM](https://gorm.io/)       |
| **chat-service**          | Micronaut + WebSocket + Micronaut Data JPA     | gRPC service + [gorilla/websocket](https://github.com/gorilla/websocket) + [GORM](https://gorm.io/)                                                              |
| **dashboard-service**     | Micronaut + Micronaut Data JPA + Elasticsearch | gRPC service + [grpc-gateway](https://github.com/grpc-ecosystem/grpc-gateway) + [GORM](https://gorm.io/) + [olivere/elastic](https://github.com/olivere/elastic) |
| **document-service**      | Micronaut + Micronaut Data JPA + File Storage  | gRPC service + [grpc-gateway](https://github.com/grpc-ecosystem/grpc-gateway) + [GORM](https://gorm.io/) + [MinIO Go SDK](https://github.com/minio/minio-go)     |
| **policy-service**        | Micronaut + Micronaut Data JPA                 | gRPC service + [grpc-gateway](https://github.com/grpc-ecosystem/grpc-gateway) + [GORM](https://gorm.io/)                                                         |
| **payment-service**       | Micronaut + Micronaut Data JPA                 | gRPC service + [grpc-gateway](https://github.com/grpc-ecosystem/grpc-gateway) + [GORM](https://gorm.io/)                                                         |
| **pricing-service**       | Micronaut + File Scripts                       | gRPC service + [Tarantool Go Connector](https://github.com/tarantool/go-tarantool)                                                                               |
| **product-service**       | Micronaut + MongoDB                            | gRPC service + [grpc-gateway](https://github.com/grpc-ecosystem/grpc-gateway) + [GORM](https://gorm.io/) + PostgreSQL JSONB                                      |
| **policy-search-service** | Micronaut + Elasticsearch                      | gRPC service + [olivere/elastic](https://github.com/olivere/elastic)                                                                                             |

#### Component-Specific Migration Details

1. **agent-portal-gateway**

   - **Current**: Micronaut Gateway for routing and load balancing with custom HTTP client integrations
     for backend service communication.
   - **Migration**: Replace with **Envoy Proxy** - a high-performance, cloud-native L7 proxy designed
     for microservices architectures with advanced routing and observability capabilities.
   - **Key Changes**:
     - **Simplified Architecture**: Transform from custom Micronaut gateway to pure L7 proxy with
       Envoy handling routing, TLS termination, and service discovery integration.
     - **Protocol Translation**: Delegate HTTP-to-gRPC translation to individual backend services
       using gRPC-gateway, eliminating gateway-level protocol conversion complexity.
     - **Cloud-Native Integration**: Deploy using Envoy's Kubernetes-native configuration management
       with enhanced observability, traffic shaping, and fault injection capabilities.

[Reasoning for replacing agent-portal-gateway with Envoy Proxy](migration-reasoning/replace-agent-portal-gateway.md)

2. **auth-service**

   - **Current**: Custom Micronaut Security implementation with basic username/password authentication
     against PostgreSQL, JWT token generation, and user profile management.
   - **Migration**: Replace with **Keycloak** - a mature, open-source Identity and Access Management 
     (IAM) solution deployed as a containerized service in Kubernetes.
   - **Key Changes**:
     - **Complete Service Replacement**: Remove custom authentication logic in favor of Keycloak's
       battle-tested implementation with OAuth 2.0/OIDC support.
     - **Enhanced Security Features**: Gain multifactor authentication (MFA), password policies,
       audit logging, and comprehensive user/role management through Keycloak's admin console.
     - **Cloud-Native Integration**: Deploy using Keycloak's Kubernetes operator with horizontal
       scaling, while Go microservices validate JWT tokens using standard libraries.

[Reasoning for replacing auth-service with Keycloak](migration-reasoning/replace-auth-service.md)

3. **chat-service**

   - **Current**: Micronaut with WebSocket support for real-time client communication and Micronaut
     Data JPA for chat history persistence.
   - **Migration**: Migrate to **Go with gRPC streaming** and gorilla/websocket for hybrid real-time
     communication architecture with GORM for data persistence.
   - **Key Changes**:
     - **Dual Protocol Architecture**: Implement gRPC streaming for high-performance internal service
       communication while maintaining WebSocket gateway for client connections using
       gorilla/websocket.
     - **Data Layer Migration**: Replace Micronaut Data JPA with GORM for PostgreSQL persistence,
       maintaining chat history and user data with Go's idiomatic database patterns.
     - **Cloud-Native Integration**: Deploy as a containerized Go service with Kubernetes-native
       scaling and observability, supporting both streaming protocols within the microservices
       architecture.

4. **dashboard-service**

   - **Current**: Micronaut with Micronaut Data JPA for dashboard configurations and Elasticsearch
     client for analytics data retrieval.
   - **Migration**: Migrate to **Go with gRPC** using GORM for PostgreSQL persistence and
     olivere/elastic client for analytics, exposed via gRPC-gateway.
   - **Key Changes**:
     - **Service Architecture**: Replace Micronaut HTTP controllers with gRPC service definitions and
       Protobuf contracts for type-safe API interactions.
     - **Dual Data Layer**: Use GORM for managing dashboard configurations and user preferences in
       PostgreSQL while integrating olivere/elastic client for Elasticsearch analytics queries.
     - **External API Access**: Expose dashboard functionality via gRPC-gateway for HTTP
       compatibility while maintaining high-performance gRPC for internal service communication.

5. **document-service**

   - **Current**: Micronaut with Micronaut Data JPA for document metadata and local file system storage
     for document content.
   - **Migration**: Migrate to **Go with gRPC** using GORM for metadata persistence and MinIO SDK for
     S3-compatible object storage.
   - **Key Changes**:
     - **Storage Architecture**: Replace a local file system with MinIO Go SDK for scalable,
       cloud-native object storage supporting document persistence and retrieval.
     - **Streaming Performance**: Implement gRPC streaming for efficient handling of large file
       uploads and downloads, improving performance over HTTP-based transfers.
     - **Metadata Management**: Use GORM to manage document metadata in PostgreSQL while leveraging
       MinIO for content storage with gRPC-gateway for external access.

6. **policy-service & payment-service**

   - **Current**: Standard Micronaut services with Micronaut Data JPA repositories for PostgreSQL
     persistence and RESTful HTTP APIs.
   - **Migration**: Migrate to **Go with gRPC** using GORM for PostgreSQL persistence and gRPC-gateway
     for external API compatibility.
   - **Key Changes**:
     - **Protocol Migration**: Convert RESTful controller logic to gRPC service methods with Protobuf
       contracts for strong typing and validation.
     - **Data Layer Modernization**: Replace Micronaut Data JPA repositories with GORM models and
       queries for idiomatic Go database interactions.
     - **API Standardization**: Maintain external HTTP API compatibility via gRPC-gateway while
       enabling high-performance internal gRPC communication.

7. **pricing-service**

   - **Current**: Micronaut service executing pricing logic from file-based scripts with performance
     overhead from file I/O operations.
   - **Migration**: Migrate to **lightweight Go gRPC service** acting as client to Tarantool in-memory
     database with Lua stored procedures for pricing logic.
   - **Key Changes**:
     - **Performance Optimization**: Eliminate file I/O bottlenecks by migrating pricing rules into
       Tarantool in-memory database for ultra-fast calculations.
     - **Architectural Simplification**: Transform Go service into thin wrapper forwarding pricing
       requests to Tarantool via go-tarantool connector.
     - **Internal-Only Access**: Deploy as internal-only gRPC service without external HTTP exposure,
       optimized for high-frequency pricing calculations.

8. **product-service**

   - **Current**: Micronaut service using MongoDB for flexible product data storage with varying
     attributes and semi-structured information.
   - **Migration**: Migrate to **Go with gRPC** leveraging PostgreSQL with JSONB columns for product
     data, managed by GORM and exposed via gRPC-gateway.
   - **Key Changes**:
     - **Database Consolidation**: Migrate product data from MongoDB to PostgreSQL JSONB columns,
       consolidating database technologies while maintaining schema flexibility.
     - **Hybrid Data Model**: Use GORM's native JSONB support for querying semi-structured product
       data, combining document database flexibility with relational database reliability.
     - **Performance Benefits**: Leverage PostgreSQL's advanced JSONB indexing and query capabilities
       for improved product search and retrieval performance.

9. **policy-search-service**

   - **Current**: Micronaut service providing RESTful search capabilities over Elasticsearch with basic
     result pagination.
   - **Migration**: Migrate to **internal-only Go gRPC service** using olivere/elastic client for
     Elasticsearch integration with streaming capabilities.
   - **Key Changes**:
     - **Protocol Optimization**: Convert search APIs from REST to gRPC methods for improved
       performance and type safety in internal service communication.
     - **Streaming Architecture**: Use gRPC streaming to handle large search result sets efficiently,
       reducing memory usage and improving response times.
     - **Internal Service Focus**: Deploy as an internal-only gRPC service accessible exclusively within
       the microservices architecture for optimized search operations.

#### Architecture Pattern Migrations

| Pattern                         | Current (Java/Micronaut)                    | Proposed (Go)                                                                                                                                   |
|---------------------------------|---------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------|
| **Inter-service Communication** | Micronaut HTTP Client (REST, JSON)          | Direct service-to-service gRPC calls with `grpc-go`                                                                                             |
| **External API Exposure**       | Micronaut HTTP Controllers                  | `grpc-gateway` to auto-generate a reverse-proxy server                                                                                          |
| **ORM/Data Access**             | Micronaut Data JPA (Hibernate)              | [GORM](https://gorm.io/) for PostgreSQL interaction                                                                                             |
| **Dependency Injection**        | Micronaut DI (Annotations, JSR-330)         | [Wire](https://github.com/google/wire) for compile-time dependency injection                                                                    |
| **Message Processing**          | Micronaut Kafka with JSON serialization     | [Sarama](https://github.com/Shopify/sarama) or [kafka-go](https://github.com/segmentio/kafka-go) with Avro serialization for schema enforcement |
| **Validation**                  | Micronaut Validation (Annotations, JSR-303) | [protoc-gen-validate](https://github.com/envoyproxy/protoc-gen-validate) for Protobuf-based validation                                          |
| **Configuration**               | Micronaut Configuration (YAML, Properties)  | [Viper](https://github.com/spf13/viper) for handling configuration from files, env vars, etc.                                                   |
| **Command/Query Bus**           | Custom Java implementation                  | Go interfaces to define command/query contracts, with handlers implemented using GORM for persistence                                           |

## System Observability

TODO

## Migration Strategy

TODO

Other Considerations

TODO:

1. Define system architecture: modular monolith etc.
2. Define code source repo type: monorepo etc.
3. Define migration strategy: develop from scratch(green field) or develop along the existing system
4. Define migration sequence for system components
5. Define observability components
6. Define deployment environment: k8s - Kind for running locally
