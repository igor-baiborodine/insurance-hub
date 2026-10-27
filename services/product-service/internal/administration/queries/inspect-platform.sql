-- $1 environment, $2 instance UUID, $3 cluster UID, $4 seed allowed,
-- $5 expected current user, $6 expected session user.
SELECT
    -- Connected database and effective migration identity.
    current_user = $5
    AND session_user = $6
    AND current_database() = 'product'
    AND (
        SELECT pg_get_userbyid(datdba) = 'product'
        FROM pg_database
        WHERE datname = current_database()
    )
    AND (
        SELECT pg_get_userbyid(nspowner) IN ('product', 'pg_database_owner')
        FROM pg_namespace
        WHERE nspname = 'public'
    )

    -- Owner and migrator role policy.
    AND (
        SELECT count(*) = 2
               AND bool_and(rolname IN ('product', 'go_product_migrator'))
        FROM pg_roles
        WHERE rolname IN ('product', 'go_product_migrator')
    )
    AND NOT EXISTS (
        SELECT
        FROM pg_roles
        WHERE rolname IN ('product', 'go_product_migrator')
          AND (
              NOT rolcanlogin
              OR rolsuper
              OR rolcreatedb
              OR rolcreaterole
              OR rolreplication
              OR rolbypassrls
          )
    )
    AND (
        SELECT NOT rolinherit
        FROM pg_roles
        WHERE rolname = 'go_product_migrator'
    )
    AND (
        SELECT count(*) = 1
               AND bool_and(NOT inherit_option AND set_option AND NOT admin_option)
        FROM pg_auth_members
        WHERE member = 'go_product_migrator'::regrole
    )
    AND NOT EXISTS (
        SELECT
        FROM pg_auth_members
        WHERE roleid = 'go_product_migrator'::regrole
    )

    -- PUBLIC and owner-default privilege policy.
    AND NOT EXISTS (
        SELECT
        FROM pg_database AS database_role,
             LATERAL aclexplode(
                 COALESCE(database_role.datacl, acldefault('d', database_role.datdba))
             ) AS access
        WHERE database_role.datname = current_database()
          AND access.grantee = 0
    )
    AND NOT EXISTS (
        SELECT
        FROM pg_namespace AS namespace_role,
             LATERAL aclexplode(
                 COALESCE(namespace_role.nspacl, acldefault('n', namespace_role.nspowner))
             ) AS access
        WHERE namespace_role.nspname = 'public'
          AND access.grantee = 0
    )
    AND NOT EXISTS (
        SELECT
        FROM pg_default_acl AS default_access,
             LATERAL aclexplode(default_access.defaclacl) AS access
        WHERE default_access.defaclrole = 'product'::regrole
          AND access.grantee <> default_access.defaclrole
    )

    -- Protected target marker ownership, shape, grants and enrolled value.
    AND (
        SELECT pg_get_userbyid(nspowner) = 'postgres'
        FROM pg_namespace
        WHERE nspname = 'product_admin'
    )
    AND NOT EXISTS (
        SELECT
        FROM pg_namespace AS namespace_role,
             LATERAL aclexplode(
                 COALESCE(namespace_role.nspacl, acldefault('n', namespace_role.nspowner))
             ) AS access
        WHERE namespace_role.nspname = 'product_admin'
          AND access.grantee = 0
    )
    AND NOT has_schema_privilege('go_product_reader', 'product_admin', 'USAGE')
    AND (
        SELECT pg_get_userbyid(relowner) = 'postgres'
               AND relkind = 'r'
               AND NOT relrowsecurity
        FROM pg_class
        WHERE oid = 'product_admin.target_identity'::regclass
    )
    AND NOT EXISTS (
        SELECT
        FROM pg_class AS target_table,
             LATERAL aclexplode(
                 COALESCE(target_table.relacl, acldefault('r', target_table.relowner))
             ) AS access
        WHERE target_table.oid = 'product_admin.target_identity'::regclass
          AND access.grantee = 0
    )
    AND NOT has_table_privilege(
        'go_product_reader',
        'product_admin.target_identity',
        'SELECT'
    )
    AND NOT has_table_privilege(
        'go_product_migrator',
        'product_admin.target_identity',
        'UPDATE'
    )
    AND NOT has_table_privilege('product', 'product_admin.target_identity', 'UPDATE')
    AND has_table_privilege('product', 'product_admin.target_identity', 'SELECT')
    AND (
        SELECT count(*) = 5
        FROM information_schema.columns
        WHERE table_schema = 'product_admin'
          AND table_name = 'target_identity'
    )
    AND (
        SELECT count(*) = 5
               AND bool_and(
                   (column_name, data_type, is_nullable) IN (
                       ('singleton', 'boolean', 'NO'),
                       ('environment', 'text', 'NO'),
                       ('instance_id', 'uuid', 'NO'),
                       ('cluster_uid', 'text', 'NO'),
                       ('seed_allowed', 'boolean', 'NO')
                   )
               )
        FROM information_schema.columns
        WHERE table_schema = 'product_admin'
          AND table_name = 'target_identity'
    )
    AND (
        SELECT count(*) = 1
        FROM pg_constraint
        WHERE conrelid = 'product_admin.target_identity'::regclass
          AND contype = 'p'
          AND pg_get_constraintdef(oid, true) = 'PRIMARY KEY (singleton)'
    )
    AND (
        SELECT count(*) = 1
        FROM pg_index AS target_index
        JOIN pg_class AS index_class ON index_class.oid = target_index.indexrelid
        JOIN pg_am AS index_method ON index_method.oid = index_class.relam
        WHERE target_index.indrelid = 'product_admin.target_identity'::regclass
          AND target_index.indisprimary
          AND target_index.indisvalid
          AND target_index.indisready
          AND target_index.indisunique
          AND index_method.amname = 'btree'
    )
    AND (
        SELECT count(*) = 1
        FROM pg_index
        WHERE indrelid = 'product_admin.target_identity'::regclass
    )
    AND (
        SELECT count(*) = 1
        FROM pg_constraint
        WHERE conrelid = 'product_admin.target_identity'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid, true) = 'CHECK (singleton)'
    )
    AND (SELECT count(*) = 1 FROM product_admin.target_identity)
    AND (
        SELECT count(*) = 1
        FROM product_admin.target_identity
        WHERE singleton
          AND environment = $1
          AND instance_id = $2
          AND cluster_uid = $3
          AND seed_allowed = $4
    )
