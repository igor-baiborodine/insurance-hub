-- Exact effective access of the two Policy logins. Run read-only as the platform administrator.
-- Business and history checks apply once the managed schema or migration history exists.
WITH login AS (
    SELECT oid, rolname FROM pg_roles WHERE rolname IN ('go_policy_migrator', 'go_policy_runtime')
), managed (relation) AS (
    VALUES ('public.offer'), ('public.offer_answers'), ('public.offer_cover'), ('public.policy'),
        ('public.policy_version'), ('public.cover'), ('public.policy_outbox')
)
SELECT (SELECT count(*) = 2 AND bool_and(
        rolcanlogin AND NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole
        AND NOT rolinherit AND NOT rolreplication AND NOT rolbypassrls)
        FROM pg_roles WHERE oid IN (SELECT oid FROM login))
    -- The migrator may only SET ROLE to the owner; the runtime belongs to no role.
    AND (SELECT count(*) = 1 AND bool_and(
            owner.rolname = 'policy' AND member.rolname = 'go_policy_migrator'
            AND NOT m.inherit_option AND m.set_option AND NOT m.admin_option)
        FROM pg_auth_members m
        JOIN pg_roles owner ON owner.oid = m.roleid
        JOIN pg_roles member ON member.oid = m.member
        WHERE m.member IN (SELECT oid FROM login) OR m.roleid IN (SELECT oid FROM login))
    AND NOT EXISTS (SELECT FROM pg_class WHERE relowner IN (SELECT oid FROM login))
    AND NOT EXISTS (SELECT FROM pg_namespace WHERE nspowner IN (SELECT oid FROM login))
    AND NOT EXISTS (SELECT FROM pg_database WHERE datdba IN (SELECT oid FROM login))
    AND (SELECT bool_and(has_database_privilege(oid, current_database(), 'CONNECT')
            AND NOT has_database_privilege(oid, current_database(), 'CREATE,TEMPORARY'))
        FROM login)
    AND has_schema_privilege('go_policy_runtime', 'public', 'USAGE')
    AND NOT has_schema_privilege('go_policy_runtime', 'public', 'CREATE')
    AND NOT has_schema_privilege('go_policy_migrator', 'public', 'USAGE,CREATE')
    AND NOT has_schema_privilege('go_policy_runtime', 'policy_admin', 'USAGE,CREATE')
    AND has_table_privilege('go_policy_migrator', 'policy_admin.target_identity', 'SELECT')
    AND NOT has_table_privilege('go_policy_migrator', 'policy_admin.target_identity',
        'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER,MAINTAIN')
    AND (to_regclass('public.policy_outbox') IS NULL OR (
        SELECT bool_and(
            has_table_privilege('go_policy_runtime', relation, 'SELECT')
            AND has_table_privilege('go_policy_runtime', relation, 'INSERT')
            AND has_table_privilege('go_policy_runtime', relation, 'UPDATE')
            AND NOT has_table_privilege('go_policy_runtime', relation,
                'DELETE,TRUNCATE,REFERENCES,TRIGGER,MAINTAIN')
            AND NOT has_table_privilege('go_policy_migrator', relation,
                'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER,MAINTAIN'))
        FROM managed))
    AND (to_regclass('public.goose_db_version') IS NULL OR NOT EXISTS (
        SELECT FROM login WHERE has_table_privilege(oid, 'public.goose_db_version',
            'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER,MAINTAIN')))
