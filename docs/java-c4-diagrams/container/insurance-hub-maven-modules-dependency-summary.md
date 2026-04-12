## Maven Module Dependency Summary: Insurance Hub (Legacy)

The legacy portion of the Insurance Hub project is built using **Java 14** and the **Micronaut (
2.4.0)** framework. The architecture follows a modular approach where each business domain is split
into an **API module** (containing DTOs and interfaces) and a **Service module** (containing the
implementation and business logic).

### 1. Core Infrastructure & Shared Modules

These modules provide the foundational communication and abstraction layers used by the business
services.

* **`command-bus-api`**: Defines the base abstractions for the command-driven architecture.
* **`command-bus`**: The implementation of the command dispatching logic.
    * *Depends on*: `command-bus-api`.
* **`policy-service-api`**: A central domain API. Because many services (Payments, Search,
  Documents) are reactive to policy changes, this module is a common dependency across the
  landscape.
    * *Depends on*: `command-bus-api`.

### 2. Business Service Dependencies

Each service implementation typically depends on its own API and the `command-bus` for internal
decoupled processing.

| Service Module              | Implementation Dependencies | API Dependencies                                                             |
|:----------------------------|:----------------------------|:-----------------------------------------------------------------------------|
| **`policy-service`**        | `command-bus`               | `policy-service-api`, `pricing-service-api`                                  |
| **`payment-service`**       | —                           | `payment-service-api`, `policy-service-api`                                  |
| **`pricing-service`**       | —                           | `pricing-service-api`                                                        |
| **`product-service`**       | —                           | `product-service-api`                                                        |
| **`policy-search-service`** | `command-bus`               | `policy-search-service-api`, `policy-service-api`                            |
| **`dashboard-service`**     | `command-bus`               | `dashboard-service-api`, `policy-service-api`                                |
| **`documents-service`**     | `command-bus`               | `documents-service-api`, `policy-service-api`, `policy-service` (test scope) |

### 3. Edge & Communication Layer

* **`agent-portal-gateway`**: Acts as the "Backend for Frontend" (BFF). It does not contain business
  logic but aggregates all service APIs to route requests from the UI.
    * *Depends on*: `policy-service-api`, `payment-service-api`, `product-service-api`,
      `pricing-service-api`, `policy-search-service-api`, `documents-service-api`,
      `dashboard-service-api`.
* **`auth-service`**: Handles JWT-based security. It is largely self-contained at the Maven level,
  relying on Micronaut Security.
* **`chat-service`**: Provides WebSocket-based communication, operating independently of the other
  business domain modules.

### 4. Key Architectural Patterns

* **API/Service Separation**: Prevents circular dependencies between service implementations.
  Services talk to each other via API modules (representing REST or gRPC contracts).
* **Command Pattern**: The use of `command-bus` across `policy`, `search`, `dashboard`, and
  `documents` indicates an asynchronous, event-friendly internal architecture.
* **Shared Domain**: `policy-service-api` is the most "coupled" module in the system, as nearly
  every other business service requires knowledge of the Policy domain model.