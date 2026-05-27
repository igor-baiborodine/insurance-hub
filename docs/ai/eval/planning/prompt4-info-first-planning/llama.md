```shell
ollama run --verbose llama3.1:8b “Before planning a database migration, list the key information you need to collect. Then, based only on that information, propose a 4-step high-level migration plan.”
```
Here's an example of how to plan a database migration:

**Key Information to Collect:**

1. **Current Database Details**
   * Current database type (e.g., MySQL, PostgreSQL)
   * Current database schema
   * Current database size
2. **Target Database Details**
   * Target database type (e.g., MongoDB, Cassandra)
   * Target database schema
   * Target database size
3. **Business Requirements**
   * Data retention policies
   * Data access requirements
   * System performance requirements
4. **Technical Considerations**
   * Network infrastructure and connectivity
   * Security requirements (e.g., encryption, authentication)

**Proposed 4-Step High-Level Migration Plan:**

**Step 1: Assessment and Planning (Weeks 1-2)**

1. Gather and review all relevant information collected during the assessment phase.
2. Analyze current database schema against target database schema to identify differences.
3. Determine data migration strategy based on business requirements, technical considerations, and available resources.

**Step 2: Data Preparation and Transformation (Weeks 3-6)**

1. Develop scripts to transform and convert data between different formats (e.g., from relational to NoSQL).
2. Implement ETL (Extract, Transform, Load) processes for data migration.
3. Perform unit testing of transformed data to ensure accuracy.

**Step 3: Database Configuration and Setup (Weeks 7-8)**

1. Set up target database environment, including configuration and deployment of necessary software and services.
2. Ensure network infrastructure and connectivity meet requirements for database access.
3. Establish security measures, such as encryption and authentication protocols.

**Step 4: Data Migration and Cutover (Weeks 9-10)**

1. Execute data migration scripts to transfer transformed data from source to target database.
2. Verify accuracy of migrated data through extensive testing and quality assurance processes.
3. Plan and execute cutover process, including transitioning traffic between old and new databases.

This high-level plan provides a basic outline for a database migration project. The specifics will vary depending on the complexity of the project,
size of the team, and level of resource availability.

---
**Token Throughput**
- total duration:       11.351312108s
- load duration:        1.64942292s
- prompt eval count:    44 token(s)
- prompt eval duration: 31.274248ms
- prompt eval rate:     1406.91 tokens/s
- eval count:           449 token(s)
- eval duration:        9.383509507s
- eval rate:            47.85 tokens/s