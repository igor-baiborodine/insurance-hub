## Verify PostgreSQL Connectivity

### Local Dev

To verify connectivity to the PostgreSQL instance running in your local Kind cluster, you can use
`kubectl` to forward a local port to the PostgreSQL service and then connect to it using the `psql`
command-line tool. Below is an example of how to do this for the `authsvc` database in the dedicated
PostgreSQL cluster.

1. **Port-Forward to the PostgreSQL Service**

    In a terminal window, run the following command. This will create a secure tunnel from your local
    machine's port `5432` to the PostgreSQL service running in the cluster.
    
    ```shell
    kubectl port-forward postgres-authsvc-1 -n local-dev-all 5432:5432
    Forwarding from 127.0.0.1:5432 -> 5432
    Forwarding from [::1]:5432 -> 5432
    ```

2. **Connect Using psql**

    With the port-forwarding active, open a **new** terminal window and run the following command to
    connect to the database. 
    
    ```shell
    echo "PGSVCUSERPWD=$(kubectl get secret postgres-authsvc-creds-local-dev -n local-dev-all -o jsonpath='{.data.password}' | base64 --decode)"
    psql --host=localhost -U authsvc -d authsvc -p 5432
    ```

3. **Verify the Connection**

    If the connection is successful, you will see the `psql` prompt. You can run a command like `\dt` to
    list the available tables and confirm that you are connected.
    
    ```shell
    psql (16.10 (Ubuntu 16.10-0ubuntu0.24.04.1), server 17.6 (Debian 17.6-2.pgdg11+1))
    WARNING: psql major version 16, server major version 17.
    Some psql features might not work.
    SSL connection (protocol: TLSv1.3, cipher: TLS_AES_256_GCM_SHA384, compression: off)
    Type "help" for help.
    
    authsvc=> \dt
    Did not find any relations.
    authsvc=> \q
    ```

    To exit the `psql` shell, you can type `\q` and press Enter.

### QA

1. **Retrieve the PostgreSQL Password**

    First, you'll need to get the PostgreSQL password from the Kubernetes secret. You can retrieve it
    and export it as an environment variable with the following command:
    
    ```shell
    export POSTGRES_PASSWORD=$(kubectl get secret qa-postgres-secret --namespace qa-data -o jsonpath="{.data.postgres-password}" | base64 --decode)
    echo "$POSTGRES_PASSWORD"
    ```
    
    This command retrieves the `postgres-password` from the `qa-postgres-secret` in the `qa-data`
    namespace and decodes it from base64.

2. **Run the `psql` Client in a Temporary Pod**

    Next, use the following `kubectl run` command to create a temporary pod with the `postgres` image
    and connect to your database. This command will open a `psql` shell, and the pod will be
    automatically deleted once you exit the shell.
    
    ```shell
    kubectl run psql-test --rm -it --image=postgres --namespace=default -- psql "postgresql://postgres:$POSTGRES_PASSWORD@postgres-postgresql-primary.qa-data.svc.cluster.local:5432/postgres"
    ```
    
    Once connected, the `psql` prompt should be displayed. Then run SQL commands (like `\dt` to list
    tables) to verify the connection:
    
    ```shell
    postgres=# \l
                                                         List of databases
       Name    |  Owner   | Encoding | Locale Provider |   Collate   |    Ctype    | Locale | ICU Rules |   Access privileges   
    -----------+----------+----------+-----------------+-------------+-------------+--------+-----------+-----------------------
     postgres  | postgres | UTF8     | libc            | en_US.UTF-8 | en_US.UTF-8 |        |           | 
     template0 | postgres | UTF8     | libc            | en_US.UTF-8 | en_US.UTF-8 |        |           | =c/postgres          +
               |          |          |                 |             |             |        |           | postgres=CTc/postgres
     template1 | postgres | UTF8     | libc            | en_US.UTF-8 | en_US.UTF-8 |        |           | =c/postgres          +
               |          |          |                 |             |             |        |           | postgres=CTc/postgres
    (3 rows)
    
    postgres=# 
    ```
    
    To exit the `psql` shell, you can type `\q` and press Enter.
