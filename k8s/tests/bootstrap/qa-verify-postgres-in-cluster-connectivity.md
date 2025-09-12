## Verify PostgreSQL in Cluster Connectivity

### Step 1: Retrieve the PostgreSQL Password

First, you'll need to get the PostgreSQL password from the Kubernetes secret. You can retrieve it
and export it as an environment variable with the following command:

```shell
export POSTGRES_PASSWORD=$(kubectl get secret qa-postgres-secret --namespace qa-data -o jsonpath="{.data.postgres-password}" | base64 --decode)
echo "$POSTGRES_PASSWORD"
```

This command retrieves the `postgres-password` from the `qa-postgres-secret` in the `qa-data`
namespace and decodes it from base64.

### Step 2: Run the `psql` Client in a Temporary Pod

Next, use the following `kubectl run` command to create a temporary pod with the `postgres` image
and connect to your database. This command will open a `psql` shell, and the pod will be
automatically deleted once you exit the shell.

```shell
kubectl run psql-test --rm -it --image=postgres --namespace=default -- psql "postgresql://postgres:$POSTGRES_PASSWORD@postgres-postgresql-primary.qa-data.svc.cluster.local:5432/postgres"
```

Once connected, the `psql` prompt should be displayed. Then run SQL commands (like `\l` to list
databases) to verify the connection:

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
