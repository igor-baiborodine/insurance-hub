### Explanation of Components and Flows
1.  **External Systems & Services**:
    -   **Agent Portal Gateway**: The primary entry point for synchronous API calls to create and manage offers and policies.
    -   **Pricing Service**: An external microservice that provides price calculations for insurance products via a synchronous REST API call.
    -   **Apache Kafka**: The event streaming platform used to publish `PolicyCreated` events asynchronously, decoupling the policy creation from downstream processes.
    -   **RDBMS**: The relational database providing persistent storage for policy and offer domain aggregates.

2.  **Internal Components**:
    -   **Policy API Controller**: A REST controller that handles incoming HTTP requests from the gateway. It acts as a dispatcher, routing requests to the appropriate command or query service based on the operation type (write vs. read).
    -   **Policy Command Service**: Implements the "write" side of the CQRS pattern. It orchestrates all business logic related to state changes, such as creating an offer, calculating its price, and converting it into a policy.
    -   **Policy Query Service**: Implements the "read" side of the CQRS pattern. It provides optimized queries to fetch policy and offer data for display purposes.
    -   **Offer/Policy Aggregates**: The core domain models (JPA Aggregates) that encapsulate the business rules, logic, and state for insurance offers and policies.
    -   **Policy Repository**: An interface defining the data persistence contract for the aggregates, abstracting the database implementation from the domain logic.
    -   **Database Adapter**: The infrastructure-layer implementation of the repository, using JPA to interact with the relational database.
    -   **Pricing Service Client**: A Micronaut HTTP client that handles the synchronous REST communication with the `Pricing Service` to obtain price calculations.
    -   **Kafka Event Publisher**: The infrastructure component responsible for publishing the `PolicyCreated` event to a Kafka topic after a policy has been successfully created and persisted.