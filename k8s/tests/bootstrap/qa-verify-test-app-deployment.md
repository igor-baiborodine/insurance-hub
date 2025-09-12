## Verify Test Application Deployment

### Step 1: Deploy the Application

First, apply the manifest file to your cluster using `kubectl`. This will create both the
`Deployment` (which manages the pod) and the `Service` (which exposes the pod to network traffic).

```shell
kubectl apply -f k8s/test/hello-world.yaml
```

### Step 2: Verify the Deployment and Pod

Next, check that the deployment was successful and that the pod is running correctly.

```shell
kubectl get deployment hello-world
```

Look for `1/1` in the `READY` column, which indicates the pod is running. You can also check the pod
directly:

```shell
kubectl get pods -l app=hello-world
```

You should see a pod with the `STATUS` of `Running`.

### Step 3: Verify the Service

Now, check that the service has been created and is configured correctly.

```shell
kubectl get service hello-world
```

This command will show you the service's `TYPE`, which is `NodePort`, and the port mapping. It will
look something like this: `80:30080/TCP`. This means that any request sent to port `30080` on *any*
of your cluster nodes will be forwarded to port `80` on the `hello-world` pod.

### Step 4: Test the Application from Your Host Machine

This is the final and most important test. We need to send an HTTP request to the `NodePort` to see
if we get the "Hello, World!" message back.

First, you need the IP address of any of your cluster nodes. Your `qa-master` node is a good choice.
You can get its IP from the `lxc list` command. Let's assume the IP is `10.43.248.193`.

Now, use `curl` from your **host machine's terminal** to send a request to the service:

```shell
curl http://<IP-of-any-cluster-node>:30080
```

For example:

```shell
curl http://10.43.248.193:30080
```

**Expected Output:**
If everything is working correctly, you should see the following output in your terminal:

```
Hello, World!
```

The trailing newline is expected.

This successful response confirms that your deployment is running, the `NodePort` service is
correctly routing traffic, and your cluster's networking is functioning end-to-end.

### Step 5: Clean Up

Once you are done with your test, you can remove the application from your cluster by deleting the
resources you created. The simplest way is to use the same manifest file:

```shell
kubectl delete -f k8s/test/hello-world.yaml
```

This will remove both the deployment and the service.