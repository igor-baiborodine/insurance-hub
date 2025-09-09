## Verify Dynamic PVC Provisioning

Here is a simple, step-by-step process to test it using `kubectl`. We will create a
PersistentVolumeClaim (PVC), see if a PersistentVolume (PV) is automatically created for it, and
then attach it to a pod to confirm we can read and write data.

### Step 1: Check the Available `StorageClass`

First, let's confirm the `StorageClass` that `k3s` provides. You should see one named `local-path`.

```shell script
kubectl get storageclass
```

The `(default)` annotation indicates that any PVC created without specifying a `storageClassName`
will automatically use this provisioner.

### Step 2: Create a PersistentVolumeClaim (PVC)

Now, let's request a small amount of storage. Create a file named `test-pvc.yaml` with the following
content:

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: test-pvc
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 100Mi
```

Apply this manifest to your cluster:

```shell script
kubectl apply -f test-pvc.yaml
```

### Step 3: Verify the PVC is `Bound`

Check the status of your PVC. The `local-path` provisioner should see this request and automatically
create a PersistentVolume to satisfy it.

```shell script
kubectl get pvc test-pvc
```

You should see the `STATUS` change from `Pending` to **`Bound`** within a few seconds. This confirms
that dynamic provisioning worked! You can also see the dynamically created PV by running
`kubectl get pv`.

### Step 4: Create a Test Pod to Use the PVC

Now, let's use this claim in a pod. Create a file named `test-pvc-pod.yaml` with the following content.
This pod will mount your PVC into a `/data` directory.

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: test-pod
spec:
  volumes:
    - name: test-storage
      persistentVolumeClaim:
        claimName: test-pvc
  containers:
    - name: test-container
      image: busybox
      # This command keeps the pod running so we can test it
      command: [ "/bin/sh", "-c", "sleep 3600" ]
      volumeMounts:
        - name: test-storage
          mountPath: /data
```

Apply this manifest to create the pod:

```shell script
kubectl apply -f test-pvc-pod.yaml
```

### Step 5: Test the Mounted Volume

Wait for the `test-pvc-pod` to be in the `Running` state (`kubectl get pods`). Once it is running, let's
write a file to the mounted volume from outside the pod:

```shell script
kubectl exec test-pvc-pod -- touch /data/hello_from_pvc.txt
```

Now, let's list the contents of the directory inside the pod to prove the file was created
successfully:

```shell script
kubectl exec test-pvc-pod -- ls /data
```

You should see the output `hello_from_pvc.txt`. This confirms that your pod can successfully write
data to the dynamically provisioned volume.

### Step 6: Clean Up

Once you are done, you can remove the test resources from your cluster:

```shell script
kubectl delete pod test-pvc-pod
kubectl delete pvc test-pvc
```
