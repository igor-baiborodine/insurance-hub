### Current State

In the current Java-based architecture, the system relies on HashiCorp Consul for service discovery.
Each Micronaut microservice registers itself with the Consul server upon startup, and other
services, particularly the `agent-portal-gateway`, query Consul to resolve the network locations of
downstream services. This is a standard and effective pattern for managing service-to-service
communication in a distributed system that is not orchestrated by a container platform with built-in
discovery capabilities.

### Why Kubernetes-native Service Discovery is the Right Choice

As the project migrates to a Go-based, cloud-native architecture with Kubernetes as the target
deployment environment, replacing Consul with Kubernetes-native solutions is the correct strategic
decision for the following reasons:

1. **Embracing Kubernetes-Native Capabilities**: The target state is "Kubernetes-First," and
   Kubernetes provides robust, built-in service discovery via its own `Service` resources and
   internal DNS. Leveraging the platform's native capabilities is more efficient and idiomatic than
   introducing a separate, external tool for a function that the orchestrator already handles
   excellently.
2. **Reduced Operational Complexity**: Maintaining a separate Consul cluster adds operational
   overhead. It's one more component to deploy, configure, secure, monitor, and manage. By removing
   it, the overall system architecture is simplified, reducing the number of potential failure
   points and lowering the maintenance burden on the team. This aligns perfectly with the migration
   goal of "fundamentally simplifying operations."
3. **Seamless Integration**: Kubernetes-native service discovery is seamlessly integrated with the
   entire Kubernetes ecosystem, including networking, load balancing, health checks (readiness and
   liveness probes), and security policies. Using an external discovery tool can create unnecessary
   friction and complexity at these integration points.
4. **Alignment with Envoy and Service Mesh**: The `agent-portal-gateway` is being replaced with
   Envoy Proxy. In a Kubernetes environment, Envoy is designed to discover endpoints directly from
   the Kubernetes API server, often as part of a service mesh like Istio or Linkerd. Relying on
   Kubernetes for discovery creates a more streamlined and powerful integration with Envoy's
   advanced traffic management features.
5. **Cost and Resource Efficiency**: Eliminating the Consul cluster frees up the compute and memory
   resources it would otherwise consume. While Consul is efficient, removing it entirely contributes
   to the goal of creating a lean, resource-efficient platform.

In summary, while Consul is a powerful tool and appropriate for the current system, its role is made
redundant by the adoption of Kubernetes. Decommissioning it in the target architecture simplifies
the stack, reduces operational overhead, and fully embraces the advantages of the chosen
cloud-native platform.
