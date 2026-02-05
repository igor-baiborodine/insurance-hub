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

   Get the password for the `elastic` user from the Kubernetes secret and export it as an
   environment variable with:

    ```shell
    export ELASTIC_USER_PWD="$(kubectl get secret local-dev-elasticsearch-es-elastic-user -n local-dev-all -o go-template='{{.data.elastic | base64decode}}')"
    echo "$ELASTIC_USER_PWD"
    ```

3. **Verify Elasticsearch Cluster Health**

   With port-forward active, open a **new** terminal and run:

    ```shell
    curl -u elastic:$ELASTIC_USER_PWD http://localhost:9200/_cluster/health | jq .
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
    kubectl run es-connect-test --rm -it --image=curlimages/curl -n default \
        --env ELASTIC_USER_PWD="$ELASTIC_USER_PWD" -- sh
    ``` 

3. **Verify the Connection**

   - **Verify cluster health**
   
     In the pod shell, run the following command to verify the connection:
     ```bash
     curl -u elastic:$ELASTIC_USER_PWD -k https://qa-elasticsearch-es-http.qa-data.svc.cluster.local:9200/_cluster/health
     # response
     {
      "cluster_name": "qa-elasticsearch",
      "status": "green",
      ...
     }
     ```

   - **List all indexes**
   
     In the pod shell, run the following command:
     ```bash
     curl -u elastic:$ELASTIC_USER_PWD -k https://qa-elasticsearch-es-http.qa-data.svc.cluster.local:9200/_cat/indices?v
     # response
     health status index        uuid                   pri rep docs.count docs.deleted store.size pri.store.size dataset.size
     green  open   policy-views eOCw92oiQYWC12vG8baVeQ   1   1          6            0     74.4kb         37.2kb       37.2kb
     green  open   policy_stats U2GsS62aRqK50r-lH-10mg   1   1       1066            0    615.3kb        307.6kb      307.6kb
     ```

   - **List content for an index**
    
     In the pod shell, run the following command:
     ```bash
     curl -u elastic:$ELASTIC_USER_PWD -k "https://qa-elasticsearch-es-http.qa-data.svc.cluster.local:9200/policy-views/_search?pretty" \
     -H 'Content-Type: application/json' \
       -d '{
         "query": { "match_all": {} },
         "size": 10
       }'
     ```

   Successful responses confirms connectivity. To exit the pod shell, the pod will automatically
   terminate as it runs the command and exits.
