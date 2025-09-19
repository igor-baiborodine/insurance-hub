## Current State

The Insurance Hub system currently uses synchronous RESTful HTTP APIs for most interservice
communication, with selective use of event-driven Kafka-based messaging. All external and internal
communications rely on HTTP/JSON protocols, which creates performance bottlenecks and requires
manual API documentation maintenance. The existing Java services use standard HTTP clients and
servers without protocol optimization, leading to increased serialization overhead and network
latency. API contracts are maintained separately from implementation code, creating potential
inconsistencies between documentation and actual service behavior.

## Why gRPC with gRPC-gateway is the Right Choice

1. **Protocol Optimization for Different Use Cases**

- **gRPC for internal communication**: Provides superior performance, type safety, bidirectional
  streaming, and efficient binary serialization between Go services, delivering 7-10x faster
  performance than HTTP/JSON
- **HTTP/REST for external exposure**: Ensures universal compatibility with web frontends, mobile
  apps, and third-party integrations without forcing external clients to adopt gRPC

2. **Single API Definition and Code Generation**

- Define APIs once in `.proto` files, eliminating duplication and ensuring consistency across
  protocols
- Auto-generate gRPC server/client code for Go services
- Auto-generate HTTP/JSON handlers through gRPC-gateway
- Auto-generate OpenAPI/Swagger documentation for external API consumers
- Maintain API contracts as code, ensuring documentation accuracy and version control

3. **Performance Benefits**

- Internal gRPC communication significantly reduces serialization overhead through binary protocol
- HTTP/JSON translation only occurs at the edge where needed, minimizing performance impact
- Streaming capabilities enable efficient real-time data transfer for appropriate use cases
- Connection multiplexing reduces network overhead for high-frequency service interactions

4. **Operational Advantages**

- Strongly typed API contracts prevent runtime errors and improve developer productivity
- Built-in support for API versioning and backward compatibility
- Seamless integration with cloud-native observability tools (OpenTelemetry, Prometheus)
- Kubernetes-native service discovery and load balancing work optimally with gRPC

5. **Architecture Flow**

```
External Client (HTTP/JSON)
    └──> API Gateway (HTTP)
           └──> grpc-gateway (HTTP to gRPC)
                  └──> gRPC Service

Internal Microservices
    └──> Direct gRPC calls between each other
```

This approach combines the performance benefits of gRPC for internal service mesh communication with
the universal compatibility of REST APIs for external consumers, supporting both the migration to Go
and the cloud-native architecture goals.