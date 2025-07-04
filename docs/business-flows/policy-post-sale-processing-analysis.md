### Asynchronous Post-Sale Processing Flow

This diagram shows what happens _after_ a policy is successfully created. The `PolicyCreated` event
published to Kafka triggers a series of independent, parallel processes in different microservices.
This demonstrates a decoupled, event-driven architecture.
