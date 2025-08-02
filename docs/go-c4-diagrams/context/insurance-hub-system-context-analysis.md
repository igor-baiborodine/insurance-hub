### Explanation of System Context and Flows

This diagram provides a high-level overview of the modernized **Insurance Hub** system, its primary
user, and its key external dependencies after its migration to a Go-based, cloud-native
architecture.

#### Actors and Systems

*   **Insurance Agent (Person)**: The main user of the system. This individual is responsible for
    creating insurance quotes, converting them into active policies, and managing them through the
    system's user interface.

*   **Insurance Hub (Software System)**: This is the core system being described. It is a
    comprehensive, cloud-native microservices platform built with Go that handles all aspects of the
    insurance sales lifecycle. Its responsibilities include managing products, calculating prices,
    creating and storing policies, processing payments, and generating the necessary documents. gRPC
    is used for high-performance internal communication.

*   **Object Storage (External System)**: A single, unified S3-compatible storage system (such as
    MinIO or AWS S3). It provides a scalable and durable location for all system artifacts, including
    policy documents and bank statements, replacing the previous fragmented file system storage.

*   **Apache Kafka (External System)**: An event streaming platform that acts as the central message
    bus for asynchronous communication within the Insurance Hub. It decouples the Go microservices,
    enabling event-driven workflows and enhancing resilience for handling burst workloads.

*   **Tarantool (External System)**: An in-memory database that stores pricing rules (tariffs) to
    enable extremely fast lookups, which are critical for real-time price calculations.

#### Flows

*   **Agent to Hub**: The `Insurance Agent` interacts with the `Insurance Hub` via its web interface.
    External-facing APIs are exposed over HTTPS using a `gRPC-gateway`, which translates RESTful JSON
    requests into gRPC for internal processing.
*   **Hub and Kafka**: The `Insurance Hub` is both a producer and consumer of events in
    `Apache Kafka`. It publishes events when significant business state changes occur (e.g.,
    `PolicyCreated`), and other services within the hub listen for these events to trigger subsequent
    asynchronous processes.
*   **Hub to Object Storage**: The `Insurance Hub` interacts with `Object Storage` via a standard
    `S3 API`. It writes files (like generated policy documents) to storage for persistence and reads
    files (like bank statements) for processing.
*   **Hub to Tarantool**: The `Insurance Hub` connects to `Tarantool` to load the tariff rules
    required for its price calculation engine.