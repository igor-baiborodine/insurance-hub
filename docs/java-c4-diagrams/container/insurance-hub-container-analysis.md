### Explanation of Containers and Flows

This document explains the containers and data flows shown in the C4 Container Diagram for the
Java implementation of the Insurance Hub system.

#### Containers (Internal Systems)

* **Web Vue App**: The frontend single-page application (SPA) built with Vue.js. It provides the
  user interface for the Insurance Agent.
* **Agent Portal Gateway**: A Micronaut-based API Gateway that acts as the single entry point for
  all requests from the frontend. It routes traffic, handles authentication, and proxies WebSocket
  connections.
* **Auth Service**: A dedicated service that handles user authentication. It validates credentials
  and issues JWTs for securing API access.
* **Policy Service**: The core service responsible for the business logic of creating and managing
  insurance offers and policies. It acts as a central orchestrator for the policy creation flow.
* **Product Service**: Manages the complete insurance product catalog, including information about
  available products, their coverage options, and the questions required to generate an offer.
* **Pricing Service**: Calculates the price for an insurance product based on a set of rules 
  (tariffs) and the data provided in an offer.
* **Policy Search Service**: Provides a powerful search capability over all policies. It maintains a
  denormalized read model of policy data to enable fast and complex queries.
* **Payment Service**: Handles all financial aspects, including managing policyholder accounts,
  processing payments, and reconciling bank statements.
* **Documents Service**: Responsible for generating, storing, and providing access to policy-related
  documents, such as the final policy PDF.
* **Dashboard Service**: Consumes sales data to provide analytics and visualizations. It calculates
  sales statistics and exposes them via an API for the frontend dashboards.
* **Chat Service**: Enables real-time communication between agents using WebSockets.
* **Databases**: Each core service that owns data has its own dedicated database, following the
  database-per-service pattern.
    * **Policy Database (RDBMS)**: Stores offers and policies for the `Policy Service`.
    * **Product Database (MongoDB)**: Stores the product catalog for the `Product Service`.
    * **Payment Database (PostgreSQL)**: Stores accounts and transactions for the `Payment Service`.
    * **Search & Analytics DB (Elasticsearch)**: A dual-purpose database that stores denormalized
      policy data for the `Policy Search Service` and aggregated sales data for the
      `Dashboard Service`.

#### External Systems & Services

* **Insurance Agent (Person)**: The end-user of the system who interacts with the Web Vue App.
* **Apache Kafka**: An event streaming platform used as a message broker for asynchronous,
  event-driven communication, which decouples the services.
* **JSReport Service**: An external service used by the `Documents Service` to generate PDF
  documents from templates and data.
* **File System / Storage**: Several services interact with externalized storage for specific file
  types:
    * **Tariff Rules Storage**: Stores MVEL rule scripts used by the `Pricing Service`.
    * **Bank Statements Storage**: Stores CSV bank statements that are imported by the
      `Payment Service`.
    * **Document Storage**: Stores the generated PDF documents managed by the `Documents Service`.

#### Key Architectural Flows

1. **Authentication Flow**:
    * The `Insurance Agent` logs in via the `Web Vue App`.
    * The `Gateway` forwards the credentials to the `Auth Service`.
    * The `Auth Service` validates them and returns a JWT, which is used for all subsequent API
      calls.
    * On each request, the `Gateway` communicates with the `Auth Service` to validate the token
      before routing the request.

2. **Synchronous API Flow (Policy Creation)**:
    * The `Gateway` routes a request to create an offer to the `Policy Service`.
    * The `Policy Service` calls the `Product Service` to get product details, then calls the
      `Pricing Service` to get a price calculation.
    * Once the offer is converted to a policy, the `Policy Service` saves it to its
      `Policy Database`.

3. **Asynchronous Event Flow (Policy Finalized)**:
    * When a policy is saved, the `Policy Service` publishes a `PolicyCreated` event to
      `Apache Kafka`.
    * This event is consumed independently by several other services:
        * The `Policy Search Service` indexes the new policy data in `Elasticsearch`.
        * The `Payment Service` creates a new policy account in its `Payment Database`.
        * The `Documents Service` generates a policy PDF using `JSReport Service`.
        * The `Dashboard Service` processes the event and updates sales analytics in
          `Elasticsearch`.

4. **Data Analytics Flow**:
    * The agent views a dashboard in the `Web Vue App`.
    * The request goes through the `Gateway` to the `Dashboard Service`.
    * The `Dashboard Service` runs an aggregation query against its sales data in `Elasticsearch`
      and returns the results.

5. **Real-time Chat Flow**:
    * The `Web Vue App` establishes a WebSocket connection that is proxied through the `Gateway` to
      the `Chat Service`, enabling real-time messaging.