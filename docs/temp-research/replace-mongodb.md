## Current State

The Insurance Hub system currently uses MongoDB as a separate NoSQL database specifically for
storing insurance product definitions, while PostgreSQL handles policies, payments, and document
metadata. This creates a dual-database architecture that requires maintaining two different database
technologies, separate operational procedures, and distinct skill sets within the team. The product
data stored in MongoDB contains varying attributes and semi-structured information that benefits
from schema flexibility, but the separation from other relational data creates integration
challenges and operational complexity. Managing backups, monitoring, scaling, and disaster recovery
across two different database systems increases the operational burden and potential points of
failure.

## Why PostgreSQL with JSONB is the Right Choice

**Consolidation of Database Technologies**

- Eliminates the need for MongoDB by leveraging PostgreSQL's JSONB capabilities, consolidating all
  data storage to a single, proven technology stack
- Reduces operational complexity by requiring only one database system to deploy, monitor, scale,
  and maintain
- Streamlines tooling, monitoring, and backup procedures across the entire system
- Lowers the learning curve for new team members by focusing expertise on a single database
  technology

**JSONB Capabilities and Flexibility**

- PostgreSQL's JSONB data type provides excellent support for semi-structured insurance product data
  with schema flexibility comparable to MongoDB
- Stores varying product attributes efficiently in a decomposed binary format that enables fast
  indexing and querying
- Supports complex JSON operations, path queries, and advanced indexing strategies (GIN, GiST) for
  optimal performance
- Maintains the flexibility to evolve product schemas without rigid migration requirements

**ACID Compliance and Relational Integration**

- Retains PostgreSQL's strong ACID properties, ensuring data consistency and reliable transactions
  for critical insurance product data
- Enables seamless joins between JSONB product data and other relational tables (policies, payments,
  documents)
- Provides mature transaction support for complex operations that span both product definitions and
  policy data
- Offers the hybrid approach of document-like flexibility with relational database reliability

**Performance and Scalability**

- Delivers comparable or superior performance to MongoDB for many JSON querying scenarios,
  particularly with proper indexing
- Benefits from PostgreSQL's continuous improvements in JSONB handling and query optimization
- Supports horizontal scaling through proven PostgreSQL clustering solutions
- Provides efficient storage and retrieval of complex product hierarchies and attribute
  relationships

**Cloud-Native and Go Ecosystem Integration**

- Integrates seamlessly with the Go ecosystem through robust, mature drivers and ORMs (pgx, GORM)
- Supports containerized deployment with official PostgreSQL Docker images and Kubernetes operators
- Aligns with the target architecture's goal of using proven, well-supported technologies
- Provides excellent observability and monitoring capabilities through standard PostgreSQL tooling

This consolidation approach reduces architectural complexity while maintaining the flexibility
needed for insurance product management, supporting both the migration to Go and the cloud-native
deployment goals.