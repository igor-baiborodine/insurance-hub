## Verify Elasticsearch Connectivity

### Local Dev

To verify connectivity to the Elasticsearch instance running in your local Kind cluster, you can use
`kubectl` to forward a local port to the Elasticsearch service and then connect to it using `curl`.

1. **Port-Forward to the Elasticsearch Service**

   In a terminal window, run the following command. This establishes a connection from your local
   machine’s port `9200` to the Elasticsearch service running in the cluster.

    ```shell
    kubectl port-forward svc/local-dev-elasticsearch-es-http 9200:9200 -n local-dev-all
    Forwarding from 127.0.0.1:9200 -> 9200
    Forwarding from [::1]:9200 -> 9200
    ```

2. **Retrieve the Elastic User Password**

   Obtain the password for the `elastic` user from the Kubernetes secret and export it as an
   environment variable with:

    ```shell
    export ELASTIC_USER_PWD="$(kubectl get secret local-dev-elasticsearch-es-elastic-user -n local-dev-all -o go-template='{{.data.elastic | base64decode}}')"
    echo "$ELASTIC_USER_PWD"
    ```

3. **Verify Elasticsearch Cluster Health**

   With port-forward active, open a **new** terminal and run:

    ```shell
    curl -u elastic:$ELASTIC_USER_PWD -k https://localhost:9200/_cluster/health
    ```

   You should receive a JSON response indicating cluster health status:

    ```json
    {
      "cluster_name": "local-dev-elasticsearch",
      "status": "green",
      ...
    }
    ```

   The `-k` flag disables TLS certificate verification and is for testing only.

### QA

1. **Retrieve the Elastic User Password**

   Get the password for the `elastic` user from the secret in the QA namespace:

    ```shell
    export ELASTIC_USER_PWD="$(kubectl get secret qa-elasticsearch-es-elastic-user -n qa-data -o go-template='{{.data.elastic | base64decode}}')"
    echo "$ELASTIC_USER_PWD"
    ```

2. **Run a Temporary Pod with Curl for Connectivity Testing**

   Start a temporary pod in the `default` namespace to test connection with the `curl` command:
    ```bash
    kubectl run es-curl-test --rm -it --image=curlimages/curl -n default \
        --env ELASTIC_USER_PWD="$ELASTIC_USER_PWD" -- sh
    ``` 

3. **Verify the Connection**

   In the pod shell, run the following command to verify the connection:
    ```bash
    curl -u elastic:$ELASTIC_USER_PWD -k https://qa-elasticsearch-es-http.qa-data.svc.cluster.local:9200/_cluster/health
    ```
   
   The output will be JSON with the cluster health status like this:
    ```json
    {
      "cluster_name": "qa-elasticsearch",
      "status": "green",
      ...
    }
    ```

   Successful response confirms connectivity. To exit the pod shell, the pod will automatically
   terminate as it runs the command and exits.
