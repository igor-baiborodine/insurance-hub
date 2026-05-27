```shell
ollama run --verbose phi4-mini “Before planning a database migration, list the key information you need to collect. Then, based only on that information, propose a 4-step high-level migration plan.”
```

Key Information Needed for Database Migration:

1. Source and target databases (name, version).
2. Current schema details of both source and destination.
3. Data volume including size estimates before the start date but no data after it ("Cutover").
4. Relationships between tables: foreign keys or any constraints that might be affected by changes in database structure during migration.
5. Hardware specifications for target system (CPU, RAM).
6. Network connectivity details – both source and destination locations' IP addresses/ranges/certificates if applicable to the databases being
   migrated over a network.

Based on this collected information, I propose these steps:

1. **Pre-Migration Analysis:**
    - Analyze current schema: Understand existing database structure in detail.
    - Identify potential risks/implications of data loss due to structural changes between source and target DBs or during migration process itself
      (such as dropping/deleting tables, converting datatypes etc.).
    - Create a backup plan for the migrated databases.

2. **Preparation Phase:**
    - Determine necessary hardware upgrades if needed: Ensure that new server(s) meet requirements.
    - Set up network connectivity checks between source and target servers to confirm uninterrupted transfer can be guaranteed during migration (if
      migrating over networks).
    - Prepare scripts/tools required, such as dump/restore files or specialized ETL tools.

3. **Data Migration:**
    - Perform a trial run/mockup test of the process by transferring subsets/samples/data from DB A to B.
    - Monitor performance metrics and identify potential bottlenecks during migration (disk speed, CPU/RAM usage etc.).
    - Migrate actual data: Using appropriate backup/restore/dump files/tools for migrating entire dataset(s) in one go or batch mode based on volume
      of the transferred databases.

4. **Post-Migration Testing:**
- Validate integrity/sanity checks after transfer to ensure that no loss/data corruption occurred during migration process.
- Reconfigure applications/databases/etc as needed: If schema changes are made, make sure all dependent systems/applications reflect these updates
  properly by updating their configuration files accordingly (e.g., connection strings).
- Conduct comprehensive tests for performance/security issues etc. and resolve any discovered defects/issues before going live.

In summary, this plan emphasizes careful preparation/analysis to minimize risks/data loss/downtime while migrating databases across different
environments/hardware configurations/nets/windows or Linux operating systems with minimal effort by automating as much of the process possible (using
scripts/tools). Furthermore, it also highlights importance and necessity of thorough testing/revalidation after transfer has been completed.

---
**Token Throughput**
- total duration:       8.401152741s
- load duration:        1.74026614s
- prompt eval count:    37 token(s)
- prompt eval duration: 17.462531ms
- prompt eval rate:     2118.82 tokens/s
- eval count:           514 token(s)
- eval duration:        6.198004444s
- eval rate:            82.93 tokens/s