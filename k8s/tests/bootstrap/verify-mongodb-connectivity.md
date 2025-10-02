## Verify MongoDB Connectivity

### Local Dev

To verify connectivity to the MongoDB instance running in your local Kind cluster, you can use
`kubectl` to forward a local port to the MongoDB service and then connect to it using the `mongosh`
command-line tool.

1. **Port-Forward to the MongoDB Service**

   In a terminal window, run the following command. This will create a secure tunnel from your local
   machine's port `27017` to the MongoDB service running in the cluster.

    ```shell
    kubectl port-forward svc/local-dev-mongodb 27017:27017 -n local-dev-all
    Forwarding from 127.0.0.1:27017 -> 27017
    Forwarding from [::1]:27017 -> 27017
    ```

2. **Retrieve the Root User Password**

   First, you'll need to get the root user password from the Kubernetes secret. You can retrieve it
   and export it as an environment variable with the following command:

    ```shell
    export MONGOROOTUSERPWD="$(kubectl get secret local-dev-mongodb-root-creds -n local-dev-all -o jsonpath='{.data.password}' | base64 --decode)"
    echo "$MONGOROOTUSERPWD"   
    ```

2. **Connect Using mongosh**

   With the port-forwarding active, open a **new** terminal window and run the following command to
   connect to the database.

    ```shell
    mongosh "mongodb://root:$MONGOROOTUSERPWD@localhost:27017/admin"
    ```

3. **Verify the Connection**

   If the connection is successful, you will see the `mongosh` prompt. You can run a command like
   `show dbs` to list the available databases and confirm that you are connected.

    ```shell
    Current Mongosh Log ID: 68ddc45011ea3fdbbcce5f46
    Connecting to:          mongodb://<credentials>@localhost:27017/admin?directConnection=true&serverSelectionTimeoutMS=2000&appName=mongosh+2.5.8
    Using MongoDB:          8.0.12
    Using Mongosh:          2.5.8    

    local-dev-mongodb [direct: primary] admin> show dbs
    admin   360.00 KiB
    config  176.00 KiB
    local   468.00 KiB
    local-dev-mongodb [direct: primary] admin> exit 
    ```

   To exit the `mongosh` shell, you can type `exit` and press Enter.

### QA

1. **Retrieve the Root User Password**

   First, you'll need to get the root user password from the Kubernetes secret. You can retrieve it
   and export it as an environment variable with the following command:

    ```shell
    export MONGOROOTUSERPWD="$(kubectl get secret local-dev-mongodb-root-creds -n local-dev-all -o jsonpath='{.data.password}' | base64 --decode)"
    echo "$MONGOROOTUSERPWD"   
    ```


2. **Run the `mongosh` Client in a Temporary Pod**

   Start a temporary pod with the Bitnami MongoDB image that includes the `mongosh` shell and
   connect to the MongoDB service inside the QA namespace. This pod will be removed after you exit
   the shell:

    ```shell
    kubectl run mongosh-test --rm -it --image=bitnami/mongodb --namespace=default -- bash \ 
        -c "mongosh mongodb://root:$MONGOROOTUSERPWD@mongodb.qa-data.svc.cluster.local:27017/admin"
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
