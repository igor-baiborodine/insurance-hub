## Verify Zipkin Deployment and Tracing in QA

This guide explains how to test Zipkin tracing functionality in the QA Kubernetes cluster using a consistent procedure. The process uses Makefile targets and temporary test pods to send test traces and verify them in the Zipkin UI.

### QA 

1. **Prerequisites**

- Ensure your Kubernetes context is set `QA` environment.
- Elasticsearch cluster is deployed and accessible with Zipkin user credentials configured.
    - Port-forward to the Elasticsearch service:
        ```shell
        kubectl port-forward svc/qa-elasticsearch-es-http 9200:9200 -n qa-data
        Forwarding from 127.0.0.1:9200 -> 9200
        Forwarding from [::1]:9200 -> 9200
        ```
    - Get the password for the `elastic` user from the Kubernetes secret and export it as an
      environment variable with:
        ```shell
        export ELASTIC_USER_PWD="$(kubectl get secret qa-elasticsearch-es-elastic-user -n qa-data -o go-template='{{.data.elastic | base64decode}}')"
        echo "$ELASTIC_USER_PWD"
        ```
    - Get Zipkin role:
        ```shell
        curl -u elastic:$ELASTIC_USER_PWD -k https://localhost:9200/_security/role/zipkin_role
        ```
    - Get Zipkin user:
        ```shell
        curl -u elastic:$ELASTIC_USER_PWD -k https://localhost:9200/_security/user/zipkin_user
        ```
- Confirm Zipkin is deployed and running by executing:
  ```shell
  make zipkin-status
  ```

2. **Send Traces Using a Test Pod**

- Start a temporary test pod in `default` namespace:
    ```shell
    kubectl run zipkin-test -n default --rm -it --image=curlimages/curl -- sh
    ```
- Inside the test pod shell, send test traces with different service names to test variety:
    ```shell
    curl -X POST -H "Content-Type: application/json" \
      --data '[{
        "traceId": "abc123def456",
        "id": "abc123def456",
        "name": "test-operation",
        "timestamp": 1762130823000000,
        "duration": 150000,
        "localEndpoint": {
          "serviceName": "test-service",
          "ipv4": "10.42.0.1"
        },
        "tags": {
          "http.method": "GET",
          "http.path": "/api/test"
        }
      }]' \
      http://qa-zipkin.qa-monitoring.svc.cluster.local:9411/api/v2/spans
    ```

    ```shell
    curl -X POST -H "Content-Type: application/json" \
      --data '[{
        "traceId": "abc456def789",
        "id": "abc456def789",
        "name": "database-query",
        "timestamp": 1762130824000000,
        "duration": 250000,
        "localEndpoint": {
          "serviceName": "user-service"
        },
        "tags": {
          "db.type": "postgresql",
          "db.statement": "SELECT * FROM users"
        }
      }]' \
      http://qa-zipkin.qa-monitoring.svc.cluster.local:9411/api/v2/spans
    ```

    ```shell
    curl -X POST -H "Content-Type: application/json" \
      --data '[{
        "traceId": "abc789def123",
        "id": "abc789def123",
        "name": "http-request",
        "timestamp": 1762130824000000,
        "duration": 180000,
        "localEndpoint": {
          "serviceName": "api-gateway"
        },
        "tags": {
          "http.method": "POST",
          "http.status_code": "200"
        }
      }]' \
      http://qa-zipkin.qa-monitoring.svc.cluster.local:9411/api/v2/spans
    ```

- Type `exit` to leave the test pod shell. The pod will automatically terminate.

3. **Verify Traces in the UI**

- Use the Makefile target to forward the Zipkin UI to your local machine:
    ```shell
    make zipkin-ui
    Forwarding Zipkin UI to http://localhost:9411
    Press Ctrl+C to stop.
    Forwarding from 127.0.0.1:9411 -> 9411
    Forwarding from [::1]:9411 -> 9411
    ```

- Open your browser and navigate to [http://localhost:9411](http://localhost:9411).
- On the Zipkin UI home page, search traces by trace ID (`abc123def456`, `abc456def789`, `abc789def123`).
- Confirm the test traces appear with correct service names (`test-service`, `user-service`, `api-gateway`).
