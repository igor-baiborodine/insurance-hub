### Current State

The `agent-portal-gateway` is currently a Java-based Micronaut service that serves as the entry
point for agent portal interactions. It handles HTTP requests, routing, and basic authentication
validation within the existing non-cloud-native deployment environment. The current implementation
lacks advanced traffic management capabilities and relies on limited observability infrastructure.

### Why Envoy Proxy is the Right Choice

1. **Cloud-Native Standard**: Envoy is the industry-standard proxy for cloud-native architectures,
serving as the foundation for major service mesh solutions like Istio and Linkerd. This ensures
long-term support and ecosystem compatibility.

2. **Performance**: Built in C++, Envoy significantly outperforms Java-based gateways in memory usage,
CPU efficiency, and request throughput, aligning with the target state's emphasis on resource
efficiency.

3. **Advanced Features**: Envoy provides enterprise-grade capabilities out-of-the-box including load
balancing, circuit breaking, retries, canary deployments, and sophisticated traffic
management—eliminating the need for custom implementation.

4. **Protocol Support**: Native support for both HTTP/REST and gRPC protocols perfectly matches the
target architecture's dual communication strategy (gRPC internally, REST at the edge).

5. **Observability Integration**: Built-in integration with OpenTelemetry, Prometheus, and distributed
tracing systems directly supports the comprehensive observability goals without additional
configuration overhead.

6. **Security and Policy**: Robust security features including TLS termination, JWT validation, rate
limiting, and integration with external auth services support the zero-trust security model.

7. **Dynamic Configuration**: Runtime configuration updates through xDS APIs enable GitOps-style
management and seamless Kubernetes integration without service restarts.

Replacing the custom gateway with Envoy reduces operational complexity, improves performance, and
provides enterprise-grade features that would require significant development effort to implement,
allowing the team to focus on business logic rather than infrastructure concerns.
