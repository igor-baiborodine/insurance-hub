## Verify MongoDB Connectivity

### Local Dev

To verify connectivity to the MongoDB instance running in your local Kind cluster, you can use
`kubectl` to forward a local port to the MongoDB service and then connect to it using the `mongosh`
command-line tool.

1. **Port-Forward to the MongoDB Service**

   In a terminal window, run the following command. This will create a secure tunnel from your local
   machine's port `27017` to the MongoDB service running in the cluster.

    ```shell
    kubectl port-forward svc/mongodb 27017:27017 -n local-dev
    Forwarding from 127.0.0.1:27017 -> 27017
    Forwarding from [::1]:27017 -> 27017
    ```

2. **Connect Using mongosh**

   With the port-forwarding active, open a **new** terminal window and run the following command to
   connect to the database. The password is `mongodb` as configured in the
   `mongodb-values.yaml` for the local dev environment.

    ```shell
    mongosh "mongodb://root:mongodb@localhost:27017/admin"
    ```

3. **Verify the Connection**

   If the connection is successful, you will see the `mongosh` prompt. You can run a command like
   `show dbs` to list the available databases and confirm that you are connected.

    ```shell
    Current Mongosh Log ID: 68c760a8a9f5e85071ce5f46
    Connecting to:          mongodb://<credentials>@localhost:27017/admin?directConnection=true&serverSelectionTimeoutMS=2000&appName=mongosh+2.5.8
    Using MongoDB:          8.0.13
    Using Mongosh:          2.5.8
    
    For mongosh info see: https://docs.mongodb.com/mongodb-shell/
    
    admin> show dbs
    admin   100.00 KiB
    config   12.00 KiB
    local    72.00 KiB
    admin>
    ```

   To exit the `mongosh` shell, you can type `exit` and press Enter.

### QA

1. **Run the `mongosh` Client in a Temporary Pod**

   Start a temporary pod with the Bitnami MongoDB image that includes the `mongosh` shell and
   connect to the MongoDB service inside the QA namespace. This pod will be removed after you exit
   the shell:

    ```shell
    kubectl run mongosh-test --rm -it --image=bitnami/mongodb --namespace=default -- bash -c "mongosh mongodb://root:mongodb@mongodb.qa-data.svc.cluster.local:27017/admin"
    ```

3. **Verify the Connection**

   When connected successfully, you will see the `mongosh` prompt. Run a command like `show dbs` to
   list the available databases and confirm connectivity:

    ```shell
    admin> show dbs
    admin   100.00 KiB
    config   12.00 KiB
    local    72.00 KiB
    ```

   To exit the `mongosh` shell, type `exit` and press Enter.
