-- $1 environment, $2 seed allowed.
SELECT
    -- Runtime reader role and grants.
    (
        SELECT count(*) = 1
        FROM pg_roles
        WHERE rolname = 'go_product_reader'
          AND rolcanlogin
          AND NOT (
              rolsuper
              OR rolcreatedb
              OR rolcreaterole
              OR rolinherit
              OR rolreplication
              OR rolbypassrls
          )
    )
    AND NOT EXISTS (
        SELECT
        FROM pg_auth_members
        WHERE member = 'go_product_reader'::regrole
           OR roleid = 'go_product_reader'::regrole
    )
    AND has_database_privilege('go_product_reader', current_database(), 'CONNECT')
    AND NOT has_database_privilege('go_product_reader', current_database(), 'CREATE,TEMP')
    AND has_schema_privilege('go_product_reader', 'public', 'USAGE')
    AND NOT has_schema_privilege('go_product_reader', 'public', 'CREATE')

    -- Migrator and optional seeder role policy.
    AND has_database_privilege('go_product_migrator', current_database(), 'CONNECT')
    AND NOT has_database_privilege('go_product_migrator', current_database(), 'CREATE,TEMP')
    AND (
        $1 <> 'production'
        OR NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'go_product_seeder')
    )
    AND CASE
        WHEN $2 THEN (
            (
                SELECT count(*) = 1
                FROM pg_roles
                WHERE rolname = 'go_product_seeder'
                  AND rolcanlogin
                  AND NOT (
                      rolsuper
                      OR rolcreatedb
                      OR rolcreaterole
                      OR rolinherit
                      OR rolreplication
                      OR rolbypassrls
                  )
            )
            AND NOT EXISTS (
                SELECT
                FROM pg_auth_members
                WHERE member = to_regrole('go_product_seeder')
                   OR roleid = to_regrole('go_product_seeder')
            )
            AND has_database_privilege('go_product_seeder', current_database(), 'CONNECT')
            AND NOT has_database_privilege(
                'go_product_seeder',
                current_database(),
                'CREATE,TEMP'
            )
            AND has_schema_privilege('go_product_seeder', 'public', 'USAGE')
            AND NOT has_schema_privilege('go_product_seeder', 'public', 'CREATE')
        )
        ELSE TRUE
    END

    -- Exact database and schema grant allowlists.
    AND NOT EXISTS (
        SELECT
        FROM pg_database AS database_role,
             LATERAL aclexplode(
                 COALESCE(database_role.datacl, acldefault('d', database_role.datdba))
             ) AS access
        LEFT JOIN pg_roles AS granted_role ON granted_role.oid = access.grantee
        WHERE database_role.datname = current_database()
          AND access.grantee <> database_role.datdba
          AND (
              granted_role.rolname NOT IN (
                  'go_product_reader',
                  'go_product_migrator',
                  'go_product_seeder'
              )
              OR granted_role.rolname IS NULL
              OR access.privilege_type <> 'CONNECT'
              OR access.is_grantable
              OR (granted_role.rolname = 'go_product_seeder' AND NOT $2)
          )
    )
    AND NOT EXISTS (
        SELECT
        FROM pg_namespace AS namespace_role,
             LATERAL aclexplode(
                 COALESCE(namespace_role.nspacl, acldefault('n', namespace_role.nspowner))
             ) AS access
        LEFT JOIN pg_roles AS granted_role ON granted_role.oid = access.grantee
        WHERE namespace_role.nspname = 'public'
          AND access.grantee <> namespace_role.nspowner
          AND (
              granted_role.rolname NOT IN (
                  'product',
                  'go_product_reader',
                  'go_product_seeder'
              )
              OR granted_role.rolname IS NULL
              OR access.privilege_type <> 'USAGE'
              OR access.is_grantable
              OR (granted_role.rolname = 'go_product_seeder' AND NOT $2)
          )
    )
    AND NOT EXISTS (
        SELECT
        FROM pg_namespace AS namespace_role,
             LATERAL aclexplode(
                 COALESCE(namespace_role.nspacl, acldefault('n', namespace_role.nspowner))
             ) AS access
        LEFT JOIN pg_roles AS granted_role ON granted_role.oid = access.grantee
        WHERE namespace_role.nspname = 'product_admin'
          AND access.grantee <> namespace_role.nspowner
          AND (
              granted_role.rolname NOT IN (
                  'product',
                  'go_product_migrator',
                  'go_product_seeder'
              )
              OR granted_role.rolname IS NULL
              OR access.privilege_type <> 'USAGE'
              OR access.is_grantable
              OR (granted_role.rolname = 'go_product_seeder' AND NOT $2)
          )
    )
    AND NOT EXISTS (
        SELECT
        FROM pg_class AS target_table,
             LATERAL aclexplode(
                 COALESCE(target_table.relacl, acldefault('r', target_table.relowner))
             ) AS access
        LEFT JOIN pg_roles AS granted_role ON granted_role.oid = access.grantee
        WHERE target_table.oid = 'product_admin.target_identity'::regclass
          AND access.grantee <> target_table.relowner
          AND (
              granted_role.rolname NOT IN (
                  'product',
                  'go_product_migrator',
                  'go_product_seeder'
              )
              OR granted_role.rolname IS NULL
              OR access.privilege_type <> 'SELECT'
              OR access.is_grantable
              OR (granted_role.rolname = 'go_product_seeder' AND NOT $2)
          )
    )
