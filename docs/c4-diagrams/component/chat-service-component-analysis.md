### Explanation of Components and Flows

1.  **External Systems & Services**:
    *   **Agent's Web Browser**: The frontend application running in the agent's browser that initiates the WebSocket connection and provides the user interface for the chat.
    *   **Agent Portal Gateway**: Acts as a reverse proxy, routing the initial WebSocket upgrade request from the browser to the Chat Service and then proxying the persistent connection.

2.  **Internal Components**:
    *   **Chat WebSocket Server**: The core component of the service, implemented as a Micronaut WebSocket Server. It handles the lifecycle of WebSocket connections, including `@OnOpen`, `@OnMessage`, and `@OnClose` events.
    *   **Message Codec**: A utility component responsible for serializing outgoing message objects into a format suitable for transmission (e.g., JSON) and deserializing incoming messages back into objects that the server can understand and process.
    *   **Session Manager**: A stateful component that maintains a collection of all active WebSocket sessions. It allows the server to iterate over connected clients to broadcast messages to everyone in the chat room.

3.  **Real-time Communication Flow**:
    1.  An **Agent's Web Browser** initiates a WebSocket connection request, which is routed through the **Agent Portal Gateway**.
    2.  The gateway forwards the request to the **Chat WebSocket Server**, which accepts the connection, creating a persistent, full-duplex communication channel. The new session is registered with the **Session Manager**.
    3.  When an agent sends a message, it travels through this connection to the server.
    4.  The **Chat WebSocket Server** receives the raw message and uses the **Message Codec** to deserialize it into a structured message object.
    5.  The server then uses the **Session Manager** to get a list of all active sessions.
    6.  It iterates through the sessions and, for each one, uses the **Message Codec** to serialize the message object back into its wire format.
    7.  Finally, the serialized message is sent to all connected agents' browsers, effectively broadcasting it to the chat room.