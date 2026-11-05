<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Agent Portal Gateway](#agent-portal-gateway)
  - [Dashboard Edge](#dashboard-edge)
  - [Release and QA Ownership](#release-and-qa-ownership)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Agent Portal Gateway

This legacy Micronaut service is the authenticated HTTP edge for the Insurance Hub applications.
It keeps client-facing routes and edge concerns separate from the underlying business services.

## Dashboard Edge

The gateway exposes `POST /api/dashboard/totalsales` and forwards accepted requests to the
Dashboard service. It owns authentication, request binding, bounded downstream retries and safe
error responses at this edge.

## Release and QA Ownership

Changes under this module that reach `main` are selected by the `Legacy Service Release` workflow.
That workflow publishes the `agent-portal-gateway-legacy` image and updates the QA manifest under
`k8s/overlays/qa/svc/agent-portal-gateway/legacy/`. Flux Kustomization `qa-svc` owns reconciliation
of that desired state into the QA cluster.
