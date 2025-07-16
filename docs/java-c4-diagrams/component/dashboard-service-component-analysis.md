### Explanation of Components and Flows
1. **External Systems & Services**:
    - **Agent Portal Gateway**: The primary entry point for synchronous API calls to fetch sales statistics and data needed for the dashboards.
    - **Policy Service**: The upstream service that publishes a `PolicyCreated` event when a new insurance policy is sold.
    - **Apache Kafka**: Acts as the message broker for asynchronous communication, delivering the `PolicyCreated` event from the `policy-service` to the `dashboard-service`.
    - **Elasticsearch**: Provides persistent storage for sales data and includes a powerful aggregation framework used to compute statistics for the dashboards.

2. **Internal Components**:
    - **Dashboard API Controller**: A REST controller that handles incoming HTTP requests from the gateway. It provides endpoints for querying aggregated sales data (e.g., total sales, sales per agent).
    - **Policy Event Handler**: A Kafka listener that subscribes to the `policy.registered` topic. Upon receiving a `PolicyCreated` event, it initiates the process of indexing the new sales data.
    - **Analytics Service**: Contains the business logic to transform an incoming `PolicyCreated` event into a structured sales data document suitable for storage and analysis in Elasticsearch.
    - **Sales Data Repository**: An interface defining the data contract for all interactions with Elasticsearch, such as saving new sales documents and executing complex aggregation queries.
    - **Elasticsearch Adapter**: The infrastructure-layer implementation of the repository, using the Java client for Elasticsearch to perform indexing and search operations.
    - **Kafka Consumer**: The infrastructure component responsible for the technical details of connecting to Kafka, consuming messages, and passing them to the `Policy Event Handler`.