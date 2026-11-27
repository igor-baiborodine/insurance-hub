-- Exact effective access of the two Payment logins. Run read-only as the platform administrator.
-- Business and history checks apply once the managed schema or migration history exists.
WITH login AS (
    SELECT oid, rolname FROM pg_roles WHERE rolname IN ('go_payment_migrator', 'go_payment_writer')
), managed (relation, granted) AS (
    VALUES ('public.policy_account', ARRAY['SELECT', 'INSERT']),
        ('public.accounting_entry', ARRAY['SELECT', 'INSERT']),
        ('public.statement_receipt', ARRAY['SELECT', 'INSERT'])
), privilege (name) AS (
    SELECT unnest(ARRAY['SELECT', 'INSERT', 'UPDATE', 'DELETE', 'TRUNCATE', 'REFERENCES',
        'TRIGGER', 'MAINTAIN'])
), column_privilege (name) AS (
    SELECT unnest(ARRAY['SELECT', 'INSERT', 'UPDATE', 'REFERENCES'])
), identity_sequence (name) AS (
    SELECT pg_get_serial_sequence(relation, 'id')
    FROM managed
    WHERE to_regclass(relation) IS NOT NULL
)
SELECT (SELECT count(*) = 2 AND bool_and(
        rolcanlogin AND NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole
        AND NOT rolinherit AND NOT rolreplication AND NOT rolbypassrls)
        FROM pg_roles WHERE oid IN (SELECT oid FROM login))
    -- The migrator may only SET ROLE to the owner; the writer belongs to no role.
    AND (SELECT count(*) = 1 AND bool_and(
            owner.rolname = 'payment' AND member.rolname = 'go_payment_migrator'
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
    AND has_schema_privilege('go_payment_writer', 'public', 'USAGE')
    AND NOT has_schema_privilege('go_payment_writer', 'public', 'CREATE')
    AND NOT has_schema_privilege('go_payment_migrator', 'public', 'USAGE,CREATE')
    AND NOT has_schema_privilege('go_payment_writer', 'payment_admin', 'USAGE,CREATE')
    AND has_table_privilege('go_payment_migrator', 'payment_admin.target_identity', 'SELECT')
    AND NOT has_table_privilege('go_payment_migrator', 'payment_admin.target_identity',
        'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER,MAINTAIN')
    -- Each privilege is held exactly when it is in the writer's granted list.
    AND (to_regclass('public.statement_receipt') IS NULL OR (
        SELECT bool_and(
            has_table_privilege('go_payment_writer', relation, name) = (name = ANY (granted))
            AND NOT has_table_privilege('go_payment_migrator', relation, name))
        FROM managed CROSS JOIN privilege))
    -- Compare every grantable column privilege. Table rights supply SELECT/INSERT on every column;
    -- only receipt state and its timestamp add UPDATE.
    AND (to_regclass('public.statement_receipt') IS NULL OR (
        SELECT bool_and(
            has_column_privilege(
                'go_payment_writer', relation, attname, column_privilege.name
            ) = (column_privilege.name = ANY (granted) OR (
                relation = 'public.statement_receipt'
                AND attname = ANY (ARRAY['state', 'updated_at'])
                AND column_privilege.name = 'UPDATE'
            ))
            AND NOT has_column_privilege(
                'go_payment_migrator', relation, attname, column_privilege.name
            )
        )
        FROM managed
        JOIN pg_attribute ON attrelid = to_regclass(relation)
            AND attnum > 0 AND NOT attisdropped
        CROSS JOIN column_privilege))
    AND (to_regclass('public.payment_schema_version') IS NULL OR NOT EXISTS (
        SELECT FROM login WHERE has_table_privilege(oid, 'public.payment_schema_version',
            'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER,MAINTAIN')))
    AND NOT EXISTS (
        SELECT FROM identity_sequence
        WHERE name IS NOT NULL AND (
            has_sequence_privilege('go_payment_writer', name, 'USAGE')
            OR has_sequence_privilege('go_payment_writer', name, 'SELECT')
            OR has_sequence_privilege('go_payment_writer', name, 'UPDATE')
        ))
