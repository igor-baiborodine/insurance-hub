## Verify JSReport PDF Generation

This guide explains how to test JSReport connectivity and PDF rendering in both **Local Dev** and **QA** Kubernetes environments using a consistent procedure. The process uses Makefile targets and temporary test pods to validate service DNS, HTTP connectivity, and PDF generation.

### Local Dev and QA

1. **Prerequisites**
- Ensure your Kubernetes context is set for your target environment (`local-dev` or `QA`).
- Confirm JSReport is deployed and running by executing:

  ```shell
  make jsreport-status
  ```

- Local Dev: env-name=local-dev, svc-ns=local-dev-all
- QA: env-name=qa, svc-ns=qa-svc

2. **Run Test Pod to Verify Connectivity**

- Start a temporary test pod in the JSReport namespace:
   ```shell
   kubectl run -it jsreport-test --rm --restart=Never --image=curlimages/curl:latest -n <svc-ns> -- sh
   ```

- In the shell, check DNS resolution and HTTP connectivity:
   ```shell
   nslookup <env-name>-jsreport.<svc-ns>.svc.cluster.local
   curl -v http://<env-name>-jsreport:5488/
   ```

3. **Generate Sample PDF via HTTP API**

- Use curl to send a PDF generation request (basic echo template):
   ```shell
   curl -v -X POST http://<env-name>-jsreport:5488/api/report \
     -H "Content-Type: application/json" \
     -d '{"template":{"content":"Hello Insurance Hub!","engine":"none","recipe":"chrome-pdf"}}' \
     --output /tmp/sample.pdf
   ```

- If successful, `/tmp/sample.pdf` will contain the generated PDF. To verify the content of the PDF,
  copy it from the test pod to the host:
    ```shell
    kubectl cp <env-name>-svc/jsreport-test:/tmp/sample.pdf ./sample.pdf
    ```

4. **Exit and Cleanup**

- Upon shell exit (`Ctrl+D`), the test pod will delete itself automatically. To verify, list pods:
   ```shell
   kubectl get pods -n <svc-ns> | grep jsreport
   ```
