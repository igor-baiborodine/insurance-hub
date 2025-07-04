### Explanation of Components and Flows

1.  **External Systems & Services**:
    *   **Agent Portal Gateway**: The only client of the Auth Service. It forwards login requests from the frontend and uses the returned JWT to authorize subsequent API calls to other microservices.
    *   **PostgreSQL Database**: Provides persistent storage for agent (user) data, including usernames, hashed passwords, and roles.

2.  **Internal Components**:
    *   **Login Controller**: A Micronaut REST controller that exposes the primary `/login` endpoint. It serves as the single synchronous entry point to the service.
    *   **Authentication Provider**: A service bean that contains the core business logic. It orchestrates the process of authenticating a user by interacting with the repository and the JWT provider.
    *   **JWT Provider**: A component leveraging Micronaut's built-in JWT support. It is responsible for creating a cryptographically signed JSON Web Token containing the user's identity and claims (like roles) after successful credential validation.
    *   **User Repository**: An interface (specifically, a Micronaut Data JPA repository) that defines the contract for data access operations related to user entities, abstracting the database from the domain logic.
    *   **Database Adapter**: The concrete, framework-generated implementation of the repository interface. It handles the low-level JDBC/JPA interactions with the **PostgreSQL Database** to fetch user records.

3.  **Authentication Flow**:
    1.  The **Agent Portal Gateway** receives a login request from the frontend and forwards it to the **Login Controller**.
    2.  The **Login Controller** passes the credentials (username and password) to the **Authentication Provider**.
    3.  The **Authentication Provider** uses the **User Repository** to look up the user by their username in the **PostgreSQL Database**.
    4.  If the user is found, the provider securely compares the submitted password with the stored password hash.
    5.  Upon successful validation, the provider calls the **JWT Provider** to generate a signed JWT for the user.
    6.  The **Login Controller** returns this JWT in the response body.
    7.  The **Agent Portal Gateway** receives the JWT and forwards it back to the frontend. The JWT is then included in the headers of all subsequent requests to access protected resources.