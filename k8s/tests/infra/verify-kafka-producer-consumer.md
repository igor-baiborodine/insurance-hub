## Verify Kafka Producer/Consumer

This guide explains how to test Kafka message production and consumption in both **Local Dev** and *
*QA** environments using the same consistent procedure. The process uses your Makefile
targets to deploy temporary Kafka producer and consumer pods to send and receive messages on a
specified Kafka topic.


### Local Dev and QA

1. **Prerequisites**
- Ensure you have the correct Kubernetes context set for your target environment (local-dev or QA).
- Confirm that your Kafka cluster is running and ready by executing:

  ```shell
  make kafka-status
  ```

- Know your Kafka namespace (`DATA_NS`) and environment prefix (`ENV_NAME`). For example:
    - Local Dev: `ENV_NAME=local-dev`, `DATA_NS=local-dev-all`
    - QA: `ENV_NAME=qa`, `DATA_NS=qa-data`

2. **Run Kafka Console Producer**

   Start a temporary Kafka producer pod to send messages to your target topic.

   ```shell
   make kafka-console-producer TOPIC_NAME=test-topic
   Using ENV_NAME=local-dev, CUR_CTX=kind-local-dev-insurance-hub
   Starting Kafka console producer for topic: test-topic
   Type messages and press Enter to send. Press Ctrl+C to exit.
   If you don't see a command prompt, try pressing enter.
   >Hello Kafka/Insurance Hub!
   ```

   Type your test messages and hit Enter after each message to produce them.

2. **Run Kafka Console Consumer**

   In another terminal, start the consumer pod to read messages from the same topic:

   ```shell
   make kafka-console-consumer TOPIC_NAME=test-topic
   Using ENV_NAME=local-dev, CUR_CTX=kind-local-dev-insurance-hub
   Starting Kafka console consumer for topic: test-topic
   Press Ctrl+C to exit.
   If you don't see a command prompt, try pressing enter.
   Hello Kafka/Insurance Hub!
   ```

   The consumer will print received messages to the console, verifying proper message flow.

3. **Exit and Cleanup**

   Both producer and consumer pods run with `--rm=true` and remove themselves automatically upon
   exit (Ctrl+C).  
   You may verify cleanup by listing pods:

   ```shell
   kubectl get pods -n <namespace> | grep kafka
   ```
