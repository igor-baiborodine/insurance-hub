SET default_transaction_read_only = on;

WITH RECURSIVE role_memberships(role_oid, role_name, depth) AS (
    SELECT oid, rolname, 0
    FROM pg_roles
    WHERE rolname = current_user

    UNION

    SELECT parent.oid, parent.rolname, role_memberships.depth + 1
    FROM role_memberships
    JOIN pg_auth_members ON pg_auth_members.member = role_memberships.role_oid
    JOIN pg_roles AS parent ON parent.oid = pg_auth_members.roleid
),
table_metadata AS (
    SELECT
        pg_class.oid AS table_oid,
        pg_get_userbyid(pg_class.relowner) AS table_owner
    FROM pg_class
    JOIN pg_namespace ON pg_namespace.oid = pg_class.relnamespace
    WHERE pg_namespace.nspname = 'public'
      AND pg_class.relname = 'product'
      AND pg_class.relkind IN ('r', 'p')
)
SELECT jsonb_build_object(
    'observedAt', to_char(
        clock_timestamp() AT TIME ZONE 'UTC',
        'YYYY-MM-DD"T"HH24:MI:SS"Z"'
    ),
    'serverVersion', current_setting('server_version'),
    'serverVersionNumber', current_setting('server_version_num')::integer,
    'database', current_database(),
    'schema', current_schema(),
    'currentUser', current_user,
    'sessionReadOnly', current_setting('transaction_read_only')::boolean,
    'table', jsonb_build_object(
        'qualifiedName', 'public.product',
        'owner', (SELECT table_owner FROM table_metadata),
        'columns', COALESCE((
            SELECT jsonb_agg(
                jsonb_build_object(
                    'name', column_name,
                    'ordinal', ordinal_position,
                    'dataType', data_type,
                    'underlyingType', udt_name,
                    'characterMaximumLength', character_maximum_length,
                    'nullable', is_nullable = 'YES',
                    'default', column_default,
                    'identity', is_identity = 'YES',
                    'generated', is_generated
                )
                ORDER BY ordinal_position
            )
            FROM information_schema.columns
            WHERE table_schema = 'public'
              AND table_name = 'product'
        ), '[]'::jsonb),
        'constraints', COALESCE((
            SELECT jsonb_agg(
                jsonb_build_object(
                    'name', pg_constraint.conname,
                    'type', pg_constraint.contype,
                    'definition', pg_get_constraintdef(pg_constraint.oid, true)
                )
                ORDER BY pg_constraint.conname
            )
            FROM pg_constraint
            JOIN table_metadata ON table_metadata.table_oid = pg_constraint.conrelid
        ), '[]'::jsonb),
        'indexes', COALESCE((
            SELECT jsonb_agg(
                jsonb_build_object(
                    'name', indexname,
                    'definition', indexdef
                )
                ORDER BY indexname
            )
            FROM pg_indexes
            WHERE schemaname = 'public'
              AND tablename = 'product'
        ), '[]'::jsonb),
        'triggers', COALESCE((
            SELECT jsonb_agg(
                jsonb_build_object(
                    'name', pg_trigger.tgname,
                    'definition', pg_get_triggerdef(pg_trigger.oid, true),
                    'enabled', pg_trigger.tgenabled
                )
                ORDER BY pg_trigger.tgname
            )
            FROM pg_trigger
            JOIN table_metadata ON table_metadata.table_oid = pg_trigger.tgrelid
            WHERE NOT pg_trigger.tgisinternal
        ), '[]'::jsonb)
    ),
    'role', (
        SELECT jsonb_build_object(
            'name', rolname,
            'canLogin', rolcanlogin,
            'superuser', rolsuper,
            'createDatabase', rolcreatedb,
            'createRole', rolcreaterole,
            'inherits', rolinherit,
            'replication', rolreplication,
            'bypassRowLevelSecurity', rolbypassrls
        )
        FROM pg_roles
        WHERE rolname = current_user
    ),
    'roleMemberships', COALESCE((
        SELECT jsonb_agg(
            jsonb_build_object('role', role_name, 'depth', depth)
            ORDER BY depth, role_name
        )
        FROM role_memberships
    ), '[]'::jsonb),
    'effectivePrivileges', jsonb_build_object(
        'database', jsonb_build_object(
            'connect', has_database_privilege(current_user, current_database(), 'CONNECT'),
            'create', has_database_privilege(current_user, current_database(), 'CREATE'),
            'temporary', has_database_privilege(current_user, current_database(), 'TEMPORARY')
        ),
        'schema', jsonb_build_object(
            'usage', has_schema_privilege(current_user, 'public', 'USAGE'),
            'create', has_schema_privilege(current_user, 'public', 'CREATE')
        ),
        'table', jsonb_build_object(
            'select', has_table_privilege(current_user, 'public.product', 'SELECT'),
            'insert', has_table_privilege(current_user, 'public.product', 'INSERT'),
            'update', has_table_privilege(current_user, 'public.product', 'UPDATE'),
            'delete', has_table_privilege(current_user, 'public.product', 'DELETE'),
            'truncate', has_table_privilege(current_user, 'public.product', 'TRUNCATE'),
            'references', has_table_privilege(current_user, 'public.product', 'REFERENCES'),
            'trigger', has_table_privilege(current_user, 'public.product', 'TRIGGER')
        )
    ),
    'explicitTableGrants', COALESCE((
        SELECT jsonb_agg(
            jsonb_build_object(
                'grantor', grantor,
                'grantee', grantee,
                'privilege', privilege_type,
                'grantable', is_grantable = 'YES'
            )
            ORDER BY grantee, privilege_type
        )
        FROM information_schema.role_table_grants
        WHERE table_schema = 'public'
          AND table_name = 'product'
    ), '[]'::jsonb),
    'relevantObjects', COALESCE((
        SELECT jsonb_agg(
            jsonb_build_object(
                'name', pg_class.relname,
                'kind', CASE pg_class.relkind
                    WHEN 'r' THEN 'table'
                    WHEN 'p' THEN 'partitioned-table'
                    WHEN 'v' THEN 'view'
                    WHEN 'm' THEN 'materialized-view'
                    WHEN 'S' THEN 'sequence'
                    WHEN 'i' THEN 'index'
                    ELSE pg_class.relkind::text
                END,
                'owner', pg_get_userbyid(pg_class.relowner)
            )
            ORDER BY pg_class.relkind, pg_class.relname
        )
        FROM pg_class
        JOIN pg_namespace ON pg_namespace.oid = pg_class.relnamespace
        WHERE pg_namespace.nspname = 'public'
          AND pg_class.relkind IN ('r', 'p', 'v', 'm', 'S', 'i')
    ), '[]'::jsonb),
    'catalogProvenance', jsonb_build_object(
        'rowCount', (SELECT count(*) FROM public.product),
        'codes', COALESCE((
            SELECT jsonb_agg(code ORDER BY code)
            FROM public.product
        ), '[]'::jsonb)
    )
)::text;
