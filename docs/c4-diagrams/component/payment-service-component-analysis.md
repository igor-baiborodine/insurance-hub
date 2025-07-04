### Explanation of Components and Flows
1. **External Systems & Services**:
    - **Agent Portal Gateway**: The primary entry point for synchronous API calls to manage payments.
    - **Policy Service**: The upstream service that publishes a `PolicyCreated` event when a new insurance policy is finalized.
    - **Apache Kafka**: Acts as the message broker for asynchronous communication between and . `policy-service``payment-service`
    - **PostgreSQL Database**: Provides persistent storage for policy accounts, payments, and accounting entries.
    - **File System**: Stores the CSV bank statements that need to be imported.

2. **Internal Components**:
    - **Payment API Controller**: A REST controller that handles incoming HTTP requests from the gateway. It's the synchronous entry point to the service.
    - **Payment Application Service**: The central coordinator that orchestrates business logic by interacting with domain models and repositories.
    - **Policy Event Handler**: A Kafka listener that subscribes to the `policy.registered` topic. Upon receiving an event, it initiates the process of creating a corresponding `PolicyAccount`.
    - **CSV Import Service**: A scheduled job (`@Scheduled`) that periodically scans the **File System** for new bank statement files to process.
    - **Policy Account**: The core domain aggregate that encapsulates all logic and data related to a single account, its balance, and its associated payments.
    - **Payment Registration Service**: A domain service that contains the logic for matching payments from an imported file to the correct policy accounts.
    - **Policy Account Repository**: An interface defining the data persistence contract, abstracting the database from the domain logic.
    - **Database Adapter**: The infrastructure-layer implementation of the repository, using JPA to interact with the **PostgreSQL Database**.
    - **Kafka Consumer**: The infrastructure component responsible for the technical details of connecting to Kafka and consuming messages.
    - **CSV File Processor**: An infrastructure component that handles reading and parsing CSV files.
