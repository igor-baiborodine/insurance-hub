### Explanation of Components and Flows
1. **External Systems & Services**:
    - **Agent Portal Gateway**: The primary entry point for synchronous search queries. The gateway forwards search requests from the frontend to this service.
    - **Policy Service**: The upstream service that publishes a `PolicyCreated` event when a new insurance policy is finalized.
    - **Apache Kafka**: Acts as the message broker for asynchronous communication, delivering `PolicyCreated` events to this service.
    - **Elasticsearch**: The search engine used as a NoSQL database for storing, indexing, and querying denormalized policy data (read models).

2. **Internal Components**:
    - **Search API Controller**: A REST controller that handles incoming HTTP search requests from the gateway. It serves as the synchronous entry point for querying policy data.
    - **Policy Event Listener**: A Kafka listener that subscribes to the `PolicyCreated` event topic. Upon receiving an event, it triggers the indexing process for the new policy.
    - **Policy Indexer Service**: An application service responsible for transforming the incoming event data into a `PolicyView` document—a denormalized read model optimized for searching.
    - **Elasticsearch Repository**: An interface defining the contract for data operations against Elasticsearch, abstracting the search engine from the application logic.
    - **Elasticsearch Adapter**: The infrastructure-layer implementation of the repository, handling the specific details of connecting to and interacting with the Elasticsearch cluster.
    - **Kafka Consumer**: The infrastructure component that manages the connection to Kafka and forwards incoming event messages to the `Policy Event Listener`.