```shell
ollama run --verbose mistral “Before planning a database migration, list the key information you need to collect. Then, based only on that information, propose a 4-step high-level migration plan.”
```
To effectively plan a database migration, it's essential to gather the following key information:

1. Current Database System: Understand the existing database management system (DBMS), version, schema structure, data types, and storage settings.

2. Data Volume and Type: Determine the size of the current database, along with the type and nature of the stored data (structured or unstructured).

3. Application Interfaces: Identify the applications that interface with the existing database, their functionality, and any dependencies they may
   have on the database schema or structure.

4. Performance Requirements: Evaluate the current database performance in terms of read/write operations, response time, and transaction throughput
   to ensure that the new database can meet these requirements.

5. Compatibility with Target DBMS: Verify that the selected target database management system supports the data types, features, and functionalities
   required by the application(s).

6. Migration Tools and Methodologies: Research available migration tools and choose a suitable methodology based on the size of the database,
   complexity of the data, and the chosen target DBMS.

7. Data Security and Compliance: Identify any security measures and compliance requirements for handling sensitive data during the migration process.

8. Recovery Plan: Develop a contingency plan in case of any issues during the migration, including rollback options, disaster recovery procedures,
   and data backup strategies.

Based on this information, here's a high-level 4-step database migration plan:

1. **Assessment and Planning:** Analyze the current database environment and gather all necessary information for making an informed decision about the
   target DBMS, tools, methodology, and recovery plan.

2. **Data Mapping and Preparation:** Create a mapping of the source and target databases to ensure compatibility between them. Prepare data for migration
   by cleaning, transforming, and converting it as needed.

3. **Migration Execution:** Execute the chosen migration methodology using appropriate tools to move data from the source database to the target system
   while minimizing downtime and ensuring data integrity.

4. **Verification and Cutover:** Test the migrated data for accuracy, completeness, and performance. Once satisfied, perform a controlled cutover of
   traffic from the old database to the new one. Monitor both databases during this period for any issues or inconsistencies. After successful
   verification, complete the migration by decommissioning the old database system.

---
**Token Throughput**
- total duration:       12.352822615s
- load duration:        1.700693493s
- prompt eval count:    40 token(s)
- prompt eval duration: 30.788377ms
- prompt eval rate:     1299.19 tokens/s
- eval count:           528 token(s)
- eval duration:        10.519748311s
- eval rate:            50.19 tokens/s