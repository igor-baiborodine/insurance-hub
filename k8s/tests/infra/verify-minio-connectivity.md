## Verify MinIO Connectivity

### Local Dev

For local development, connectivity to the MinIO cluster is best tested using port-forwarding from
your local machine. This is a temporary and simple way to access MinIO services running inside your
Kind cluster.

1. **Port-forward MinIO Service**

   Forward local port `9000` to the MinIO tenant's headless service port `9000`:

    ```bash
    kubectl port-forward svc/local-dev-minio-document-hl 9000:9000 -n local-dev-minio-document
    Forwarding from 127.0.0.1:9000 -> 9000
    Forwarding from [::1]:9000 -> 9000
    ```

2. **Test MinIO Connectivity**

   In a new terminal, verify connectivity using MinIO Client or curl:

    ```bash
    mc alias set local http://localhost:9000
    # Use values from the secret created by the target below, for example, qa-minio-document-storage-user-creds
    # make minio-storage-user-secret-create SVC_NAME=<svc-name> MINIO_CONSOLE_ACCESS_KEY=<access-key> MINIO_CONSOLE_SECRET_KEY=<secret-key>
    Enter Access Key: <access-key>
    Enter Secret Key: <secret-key>
    Added `local` successfully.
   
    mc admin info local
    localhost:9000
    Uptime: 1 hour 
    Version: 2025-04-08T15:41:24Z
    Network: 1/1 OK 
    Drives: 1/1 OK 
    Pool: 1

    ┌──────┬────────────────────────┬─────────────────────┬──────────────┐
    │ Pool │ Drives Usage           │ Erasure stripe size │ Erasure sets │
    │ 1st  │ 59.4% (total: 887 GiB) │ 1                   │ 1            │
    └──────┴────────────────────────┴─────────────────────┴──────────────┘

    1 drive online, 0 drives offline, EC:0
    ```

3. **Verify Bucket Creation**

   Crate and list buckets to verify access and data visibility:

    ```bash
    mc mb local/test-bucket
    Bucket created successfully `local/test-bucket`.
    mc ls local
    [2025-10-10 18:12:37 EDT]     0B test-bucket/
    ```

   The output should list the newly created bucket in the MinIO tenant.

4. **Stop Port Forward**

   When done, press `Ctrl+C` in the port-forward terminal to stop forwarding.

### QA

In QA, it's better to test connectivity from within the cluster network using a temporary test pod,
as port-forwarding is less practical for multi-user or production-like environments.

1. **Run a temporary test pod**

   Run a pod in the `default` namespace with MinIO Client installed for connectivity testing:

    ```bash
    kubectl run minio-connect-test --rm -it --image=curlimages/curl -n default -- sh
    curl -O https://dl.min.io/client/mc/release/linux-amd64/mc
    chmod +x mc
    ./mc --version
    
    ```

2. **Inside the temporary pod, test MinIO cluster**

   Use internal DNS and services to connect:

    ```bash
    ./mc alias set local https://qa-minio-document-hl.qa-minio-document.svc.cluster.local:9000
    # Use values from the secret created by the target below, for example, qa-minio-document-storage-user-creds
    # make minio-storage-user-secret-create SVC_NAME=<svc-name> MINIO_CONSOLE_ACCESS_KEY=<access-key> MINIO_CONSOLE_SECRET_KEY=<secret-key>
    Enter Access Key: <access-key>
    Enter Secret Key: <secret-key>
    Added `local` successfully.
    ./mc admin info local
    ●  qa-minio-document-pool-0.qa-minio-document-hl.qa-minio-document.svc.cluster.local:9000
   Uptime: 12 minutes 
   Version: 2025-09-07T16:13:09Z
   Network: 3/3 OK 
   Drives: 2/2 OK 
   Pool: 1

    ●  qa-minio-document-pool-1.qa-minio-document-hl.qa-minio-document.svc.cluster.local:9000
    Uptime: 12 minutes
    Version: 2025-09-07T16:13:09Z
    Network: 3/3 OK
    Drives: 2/2 OK
    Pool: 1
    
    ●  qa-minio-document-pool-2.qa-minio-document-hl.qa-minio-document.svc.cluster.local:9000
    Uptime: 12 minutes
    Version: 2025-09-07T16:13:09Z
    Network: 3/3 OK
    Drives: 2/2 OK
    Pool: 1
    
    ┌──────┬────────────────────────┬─────────────────────┬──────────────┐
    │ Pool │ Drives Usage           │ Erasure stripe size │ Erasure sets │
    │ 1st  │ 43.8% (total: 104 GiB) │ 6                   │ 1            │
    └──────┴────────────────────────┴─────────────────────┴──────────────┘
    
    6 drives online, 0 drives offline, EC:2
    ```

3. **Verify Bucket Creation**

   Crate and list buckets to verify access and data visibility:

    ```bash
    ./mc mb local/test-bucket
    Bucket created successfully `local/test-bucket`.
    ./mc ls local
    [2025-10-10 22:41:10 UTC]     0B test-bucket/
    ```

   The output should list the newly created bucket in the MinIO tenant.

4. **Exit test pod**

   Type `exit` to quit, and the pod will terminate automatically.
