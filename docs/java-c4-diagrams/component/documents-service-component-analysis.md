### Explanation of Components and Flows

1. **External Systems & Services**:
    - **Agent Portal Gateway**: The entry point for synchronous API calls, allowing users to request
      and download previously generated PDF documents.
    - **Policy Service**: The upstream service that publishes a `PolicyCreated` event when a new
      insurance policy is finalized, initiating the document generation process.
    - **Apache Kafka**: Acts as the message broker for asynchronous communication, delivering the
      `PolicyCreated` event from the `policy-service`.
    - **JSReport Service**: An external, third-party service responsible for the technical task of
      rendering HTML templates and data into a PDF file.
    - **Document Database**: A PostgreSQL database used for the persistent storage of the generated
      PDF documents as byte arrays (blobs) and their metadata.

2. **Internal Components**:
    - **Document API Controller**: A REST controller that handles incoming HTTP requests from the
      gateway for downloading policy documents.
    - **Policy Event Listener**: A Kafka listener (built with Kotlin) that subscribes to the
      `PolicyCreated` event topic. This is the starting point for the asynchronous generation flow.
    - **Document Generation Service**: The central application service that orchestrates the PDF
      creation process. It is triggered by the event listener and coordinates interactions with the
      PDF client and the document repository.
    - **PDF Generator Client**: An infrastructure client responsible for communicating with the
      external `JSReport Service`, sending it the necessary data and template information to render
      a PDF.
    - **Document Repository**: An infrastructure adapter that handles the persistence of the
      generated documents, saving them to and retrieving them from the **Document Database**.
    - **Kafka Consumer**: The low-level infrastructure component that connects to Kafka, consumes
      the event messages, and forwards them to the `Policy Event Listener`.