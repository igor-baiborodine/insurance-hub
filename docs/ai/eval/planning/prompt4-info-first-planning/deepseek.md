```shell
ollama run --verbose deepseek-r1:8b “Before planning a database migration, list the key information you need to collect. Then, based only on that information, propose a 4-step high-level migration plan.”
```
Thinking...
Okay, user is asking about database migration planning. This is a pretty standard but crucial question for any dev or DBA working on system upgrades  
or architecture changes.

First, I need to understand what they really need. They're not just asking for a generic migration plan - they want the prerequisites (what info to   
collect first) AND then a structured approach based on that info. Smart. This shows they're thinking systematically, probably not a junior dev        
winging it.

Hmm, the user might be in one of two situations: either they're about to start a migration and want to avoid common pitfalls, or they're just              
preparing for an interview and testing their knowledge. Given the phrasing, I lean toward the former - they sound like a technical professional being      
practical.

Right, so the first part is about what to collect. This is where most migrations fail - people jump straight to execution without proper groundwork.       
Need to emphasize the "why" behind each item. Like, why ask about current architecture? Because schema design affects every migration decision -           
tools, downtime windows, even rollback strategy.

For the high-level plan, it should be generic enough to apply to any migration type (schema change, data format change, platform migration etc.), but      
specific enough to show logical flow. The 4-step structure is good - it's memorable and forces prioritization.

Wait, should I mention different migration approaches (cutover vs. continuous) in the plan? No, better to keep it high-level. The user asked for 4         
steps, not 8. They can dive deeper if needed. The "based only on that information" part is key - I need to show the plan directly derives from the         
collected data.

The challenge is balancing completeness with conciseness. For each step, I should reference the relevant data points, but not get bogged down in           
implementation details. Like in Step 2, I can mention schema mapping but not how to do it technically.

User probably wants this to be actionable - not just theory. So I'll use concrete examples like "SQL Server to AWS Aurora" in the prerequisites.           
Makes it tangible. Should I warn about common pitfalls in each step? Maybe not - that would make it too long. The user can ask follow-ups if they          
need that.

Final thought: The response should feel like a mentor talking to a colleague, not a textbook. That's why I'll use "you'll need to" instead of "it is       
required that".                                                                                                                                            
...done thinking.

Okay, let's break this down.

## Part 1: Key Information for Database Migration Planning

Before starting, you need to understand the current state, the target state, and the journey between them. Here's the critical information to
collect:

1.  **Current Database Details:**
    *   **Type:** What kind of database is it (e.g., MySQL, PostgreSQL, SQL Server, Oracle, MongoDB, Cassandra)?
    *   **Version:** What specific version is currently running?
    *   **Schema:** Complete schema definition (tables, columns, data types, indexes, constraints, triggers, views, stored procedures, functions,
        sequences).
    *   **Data:** Approximate volume (total rows, key tables' sizes), nature (structured, semi-structured, unstructured?), quality (data integrity
        issues, duplicates?), sensitivity (compliance requirements).
    *   **Environment:** Development, Testing, Staging, Production. Access credentials for all environments.

2.  **Target Database Details:**
    *   **Type:** What is the target database (e.g., migrate *from* MySQL *to* PostgreSQL)?
    *   **Version:** What specific version is the target?
    *   **Requirements:** Does the target environment have specific configuration or feature requirements?

3.  **Migration Goals & Requirements:**
    *   **Business Objectives:** Why is the migration happening? (e.g., performance improvement, scalability, cost reduction, new features, replace
        legacy system).
    *   **Functional Requirements:** Must the migrated application/database maintain all current functionality? Are there new features required
        post-migration?
    *   **Performance Requirements:** What are the SLAs (Service Level Agreements) for read/write latency, throughput post-migration?
    *   **Availability Requirements:** What is the maximum acceptable downtime? Is a zero-downtime migration required?
    *   **Security Requirements:** Data encryption (at rest, in transit), authentication, authorization requirements specific to the target platform.
    *   **Compliance Requirements:** Any regulatory standards (e.g., GDPR, HIPAA) that must be met post-migration?

4.  **Application & Dependencies:**
    *   **Application(s) Using the DB:** List all applications connecting to the database. Are they custom-built, third-party?
    *   **Connection Strings:** Current connection strings for all applications.
    *   **Application Compatibility:** Are the applications compatible with the target database? Will connection libraries need updating?
    *   *Optional:* Application code snippets showing database interaction (if available).

5.  **Migration Approach & Tools:**
    *   **Migration Strategy:** Will it be a cutover migration (stop, migrate, restart) or a continuous migration (ongoing data sync + cutover of
        application connection)? What tools are available or required (e.g., native migration tools, ETL tools, data comparison tools, custom scripts)?
    *   **Team & Resources:** Who is involved (DBAs, developers, project managers, application owners)? What are their roles and responsibilities?

## Part 2: Proposed 4-Step High-Level Migration Plan (Based *Only* on Collected Information)

Based *only* on the information gathered above, here is a 4-step high-level migration plan:

1.  **Assessment & Planning:**
    *   **Action:** Analyze the collected information to understand the scope, complexity, risks, and requirements.
    *   **Tasks:** Compare source and target schemas (identify mappings, potential data type changes, deprecated features, incompatible objects).
        Estimate data volume and migration time. Define detailed timelines, resource allocation, and rollback strategy. Create a comprehensive migration
        checklist. Determine the chosen migration approach (cutover vs. continuous) and select/prepare necessary tools.
    *   **Output:** Detailed migration plan, risk assessment report, schema mapping document, timeline, resource list, rollback procedure.

2.  **Data Migration & Schema Conversion:**
    *   **Action:** Prepare the target database environment and execute the migration of data and schema.
    *   **Tasks:** Set up the target database according to requirements. Perform schema conversion (if using a tool, validate the output; if manual,
        design the target schema). Develop and test data migration scripts or use ETL tools. Validate data integrity and consistency post-migration (compare
        row counts, sample data checks, checksums). Migrate indexes, constraints, triggers, and stored procedures.
    *   **Output:** Migrated and populated target database, validation reports confirming data integrity.

3.  **Testing & Validation:**
    *   **Action:** Verify that the migrated database and its dependent applications work correctly and meet all requirements.
    *   **Tasks:** Perform thorough testing, including functional testing (using applications), performance testing (against defined SLAs), and user
        acceptance testing (UAT). Validate data consistency and integrity across all migrated data. Check security configurations and compliance. Test the
        rollback procedure.
    *   **Output:** Test results, sign-off from QA and stakeholders, confirmation that requirements are met.

4.  **Cutover & Post-Migration:**
    *   **Action:** Execute the final, controlled switch from the old database to the new one and perform post-migration activities.
    *   **Tasks:** Coordinate the cutover window (ensuring minimal impact). Update application connection strings and configurations to point to the
        target database. Monitor the target database closely for any issues. Perform post-migration tasks like archiving the old database, updating
        documentation, notifying stakeholders, and conducting a post-mortem review.
    *   **Output:** Successful switch to the target database, operational target database, updated documentation, lessons learned report.

---
**Token Throughput**
- total duration:       39.705610412s
- load duration:        2.300034258s
- prompt eval count:    36 token(s)
- prompt eval duration: 35.403212ms
- prompt eval rate:     1016.86 tokens/s
- eval count:           1672 token(s)
- eval duration:        36.953512924s
- eval rate:            45.25 tokens/s