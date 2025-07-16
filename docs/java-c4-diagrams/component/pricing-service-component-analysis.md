### Explanation of Components and Flows
1. **External Systems & Services**:
    - **Policy Service**: The only client of this service. It makes synchronous, blocking REST API calls to request a price calculation for a specific insurance offer and set of criteria.
    - **Configuration / File System**: Stores the business rules for pricing (tariffs) as plain text files, written using MVEL (MVFLEX Expression Language).

2. **Internal Components**:
    - **Pricing API Controller**: A Micronaut REST controller that handles incoming HTTP requests from the `policy-service`. It serves as the single synchronous entry point to the service.
    - **Calculation Service**: The core application service that orchestrates the price calculation. It receives the request data from the API, uses the `Tariff Repository` to load the appropriate rules for the given product, and then uses the `MVEL Rules Engine` to execute those rules against the data to produce a final price.
    - **MVEL Rules Engine**: A specialized component that encapsulates the logic for evaluating MVEL scripts. It takes the tariff rules and the input data (e.g., customer's answers to questions) and executes the script to return a calculated value.
    - **Tariff Repository**: An infrastructure-layer component responsible for finding and loading the correct MVEL tariff scripts from the file system based on the product being priced.