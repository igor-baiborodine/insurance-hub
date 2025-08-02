### Explanation of Containers and Flows

This document explains the containers and data flows shown in the C4 Container Diagram for the Go
implementation of the Insurance Hub system.

#### Containers (Internal Systems)

* **Web Vue App**: The frontend single-page application (SPA) built with Vue.js. It provides the
  user interface for the Insurance Agent.
* **API Gateway**: An Envoy-based API Gateway that acts as the single entry point for all requests
  from the frontend. It routes traffic, handles authentication via delegation, and proxies WebSocket
  connections.
* **Keycloak**: An open-source Identity and Access Management solution. It replaces the original
  `Auth Service` and handles all user authentication and authorization concerns, issuing tokens via
  OpenID Connect.
* **Policy Service**: The core Go service responsible for the business logic of creating and
  managing insurance offers and policies. It acts as a central orchestrator for the policy creation
  flow.
* **Product Service**: A Go service that manages the complete insurance product catalog, including
  information about available products and their coverage options.
* **Pricing Service**: A Go service that calculates the price for an insurance product based on a
  set of rules (tariffs) fetched from Tarantool.
* **Policy Search Service**: A Go service that provides a powerful search capability over all
  policies. It maintains a denormalized read model of policy data in Elasticsearch.
* **Payment Service**: A Go service that handles all financial aspects, including managing
  policyholder accounts, processing payments, and importing bank statements from MinIO.
* **Document Service**: A Go service responsible for generating, storing, and providing access to
  policy-related documents. It uses an embedded `chromedp` library to generate PDFs internally.
* **Dashboard Service**: A Go service that consumes sales data to provide analytics and
  visualizations. It calculates sales statistics and exposes them via an API.
* **Chat Service**: A Go service that enables real-time communication between agents using
  WebSockets.
* **Databases**: Each core service that owns data has its own dedicated database, following the
  database-per-service pattern.
    * **Policy DB (PostgreSQL)**: Stores offers and policies for the `Policy Service`.
    * **Product DB (PostgreSQL)**: Stores the product catalog for the `Product Service`, using JSONB
      for flexible data storage.
    * **Payment DB (PostgreSQL)**: Stores accounts and transactions for the `Payment Service`.
    * **Document Metadata DB (PostgreSQL)**: Stores metadata about generated documents for the
      `Document Service`.
    * **Search & Analytics DB (Elasticsearch)**: A dual-purpose database that stores denormalized
      policy data for the `Policy Search Service` and aggregated sales data for the
      `Dashboard Service`.

#### External Systems & Services

* **Insurance Agent (Person)**: The end-user of the system who interacts with the Web Vue App.
* **Apache Kafka**: An event streaming platform used as a message broker for asynchronous,
  event-driven communication, which decouples the services.
* **MinIO (Object Storage)**: An S3-compatible object storage system used to store all generated
  artifacts, such as policy PDFs and imported bank statements.
* **Tarantool (Tariff Rules Storage)**: An in-memory database used to store pricing rules (tariffs)
  for extremely fast lookups by the `Pricing Service`.

#### Key Architectural Flows

1. **Authentication Flow**:
    * The `Insurance Agent` logs in via the `Web Vue App`.
    * The `API Gateway` redirects the agent to `Keycloak` for authentication.
    * `Keycloak` handles the login process and, upon success, returns a token according to the
      OpenID Connect (OIDC) flow. This token is used for all subsequent API calls.
    * On each request, the `API Gateway` validates the token before routing the request to the
      appropriate upstream service.

2. **Synchronous API Flow (Policy Creation)**:
    * The `API Gateway` routes a request to create an offer to the `Policy Service` via HTTPS/REST.
    * The `Policy Service` calls the `Product Service` to get product details and the
      `Pricing Service` to get a price calculation. These internal, service-to-service calls are
      performed using gRPC for high performance.
    * Once the offer is converted to a policy, the `Policy Service` saves it to its `Policy DB`.

3. **Asynchronous Event Flow (Policy Finalized)**:
    * When a policy is saved, the `Policy Service` publishes a `PolicyCreated` event to
      `Apache Kafka`.
    * This event is consumed independently by several other services:
        * The `Policy Search Service` indexes the new policy data in `Elasticsearch`.
        * The `Payment Service` creates a new policy account in its `Payment DB`.
        * The `Dashboard Service` processes the event and updates sales analytics in
          `Elasticsearch`.
        * The `Document Service` consumes the event, generates the policy PDF using its internal
          `chromedp` library, and stores it in `MinIO`.

4. **Data Analytics Flow**:
    * The agent views a dashboard in the `Web Vue App`.
    * The request goes through the `Gateway` to the `Dashboard Service`.
    * The `Dashboard Service` runs an aggregation query against its sales data in `Elasticsearch`
      and returns the results.

5. **Real-time Chat Flow**:
    * The `Web Vue App` establishes a WebSocket connection that is proxied through the `API Gateway`
      to the `Chat Service`, enabling real-time messaging.