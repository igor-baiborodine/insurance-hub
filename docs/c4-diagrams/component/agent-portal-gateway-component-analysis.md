### Explanation of Components and Flows

1.  **External Systems & Services**:
    *   **Single Page Application (SPA)**: The user-facing web application (built with Vue.js) that agents interact with. It is the sole client of the gateway.
    *   **Downstream Microservices**: All the "business" services (`Auth`, `Policy`, `Payment`, `Dashboard`, etc.) that implement the core functionalities. The gateway acts as a reverse proxy for these services.
    *   **Consul**: The service discovery tool that holds the network locations (IP and port) of all registered microservices, enabling dynamic routing.

2.  **Internal Components**:
    *   **HTTP Server**: The entry point to the gateway, built on Netty. It listens for incoming HTTP requests and WebSocket connections from the SPA.
    *   **Routing Engine**: The core of the gateway, powered by Micronaut's routing capabilities. It inspects the request's URL and headers to determine which downstream microservice should handle it based on a predefined configuration.
    *   **Authentication Filter**: A Micronaut security filter that intercepts every incoming request. It extracts the JWT token and communicates with the **Auth Service** to validate it, enforcing security and access control before routing the request further.
    *   **HTTP Client**: A declarative, non-blocking Micronaut HTTP client used to forward the request to the target downstream service discovered via the **Service Discovery Client**. It handles the actual communication with the other microservices.
    *   **Service Discovery Client**: Integrates with **Consul** to resolve the actual network address of a target service. This allows the gateway to dynamically route traffic without hardcoded URLs, making the architecture resilient and scalable.

3.  **Flow of a Typical Request**:
    1.  The **SPA** sends an API request to the **Agent Portal Gateway**.
    2.  The **HTTP Server** receives the request.
    3.  The **Routing Engine** identifies the request and passes it through a chain of filters, including the **Authentication Filter**.
    4.  The **Authentication Filter** extracts the JWT token from the request header and makes a call to the **Auth Service** via the **HTTP Client** to validate the token.
    5.  If the token is valid, the **Routing Engine** proceeds.
    6.  The **Service Discovery Client** is used to look up the address of the target microservice (e.g., `policy-service`) from **Consul**.
    7.  The **HTTP Client** forwards the original request to the resolved address of the `policy-service`.
    8.  The `policy-service` processes the request and returns a response.
    9.  The **HTTP Client** receives the response and streams it back through the **HTTP Server** to the **SPA**.