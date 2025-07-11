## Current State

The Insurance Hub system currently manages tariff rules through a file-based approach where pricing
logic is stored as script files on disk and executed through interpreted languages or external
script engines. This creates performance bottlenecks due to the chain of
`HTTP Request -> Service Logic -> File I/O -> Script Interpretation` for each pricing calculation.
The current architecture requires service redeployment for rule changes, lacks transactional
consistency when updating multiple related rules, and tightly couples pricing logic to specific
service implementations. Rule management is distributed across multiple services, creating potential
inconsistencies and making it difficult to maintain a single source of truth for pricing
calculations.

## Why Tarantool is the Right Choice

**Extreme Performance Benefits**

- Tarantool operates as an in-memory database and application server, enabling tariff calculations
  to occur at in-memory speeds directly next to the data
- Eliminates the performance overhead of file I/O operations and external script interpretation
- Provides drastically faster pricing calculations compared to the current disk-based script
  execution model
- Supports high-frequency pricing requests without degrading system performance

**Superior Decoupling and Simplicity**

- Transforms pricing logic into a self-contained, language-agnostic component within Tarantool
- Simplifies the Go pricing service to a thin wrapper that receives requests, calls appropriate Lua
  functions, and returns results
- Enables dynamic rule updates without requiring service redeployment or system downtime
- Separates business logic from application deployment cycles, improving operational flexibility

**Centralized and Atomic Rule Management**

- Provides transactional consistency for pricing rule updates, ensuring the system never enters an
  inconsistent state
- Creates a single source of truth for all pricing calculations across the entire system
- Enables atomic updates of complex rule sets that span multiple tariff categories
- Offers superior rule management compared to distributed script files with built-in versioning and
  rollback capabilities

**Future-Proofing and Language Agnostic Access**

- Makes pricing logic accessible to any service regardless of programming language (Go, Python,
  Rust, etc.)
- Eliminates the need to duplicate pricing logic across different service implementations
- Supports the microservices architecture by providing a centralized pricing calculation service
- Enables consistent pricing behavior across all system components without code duplication

**Cloud-Native Integration**

- Tarantool's containerized deployment model aligns with Kubernetes-first architecture
- Supports horizontal scaling and high-availability configurations
- Integrates with modern observability tools for monitoring rule execution performance
- Provides backup and disaster recovery capabilities for critical pricing rule data

This approach transforms tariff rule management from a file-based, tightly-coupled system into a
high-performance, centralized, and language-agnostic pricing engine that supports both current
migration goals and future scalability requirements.