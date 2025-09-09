## QA Snapshots

### Make Targets k8s/bootstrap/qa/Makefile

- Create a new snapshot: `make qa-nodes-snapshot SNAPSHOT_NAME=your_new_snapshot`
- Restore from existing snapshot: `make qa-nodes-restore SNAPSHOT_NAME=your_existing_snapshot`
- List snapshots: `make qa-nodes-snapshots-list`

### Current Snapshots

| Name                                       | Description                                           |
|--------------------------------------------|-------------------------------------------------------|
| **qa-cluster-create-2025-09-03**           | Base cluster image with CoreDNS and storage installed |
| **prometheus-operator-install-2025-09-09** | Cluster image with Prometheus operator installed      |
