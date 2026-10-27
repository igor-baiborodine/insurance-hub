-- $1 environment, $2 instance UUID, $3 cluster UID, $4 seed allowed,
-- $5 expected current user, $6 expected session user.
-- Platform provisioning owns the broader role and grant configuration.
SELECT current_user = $5 AND session_user = $6 AND current_database() = 'product'
    AND EXISTS (
        SELECT FROM pg_class AS marker
        JOIN pg_namespace AS namespace ON namespace.oid = marker.relnamespace
        WHERE marker.oid = 'product_admin.target_identity'::regclass
          AND marker.relowner = 'postgres'::regrole
          AND namespace.nspowner = 'postgres'::regrole
          AND marker.relkind = 'r' AND NOT marker.relrowsecurity
    )
    AND (SELECT count(*) = 1 FROM product_admin.target_identity)
    AND EXISTS (
        SELECT FROM product_admin.target_identity
        WHERE singleton AND environment = $1 AND instance_id = $2
          AND cluster_uid = $3 AND seed_allowed = $4
    )
    -- Commands may read the marker, but cannot change their own enrollment.
    AND NOT EXISTS (
        SELECT FROM pg_roles
        WHERE rolname IN ('product', 'go_product_migrator', 'go_product_seeder')
          AND (
              has_any_column_privilege(oid, 'product_admin.target_identity', 'INSERT,UPDATE,REFERENCES')
              OR has_table_privilege(oid, 'product_admin.target_identity', 'DELETE,TRUNCATE,TRIGGER')
          )
    )
    AND NOT EXISTS (
        SELECT FROM pg_attribute
        WHERE attrelid = 'product_admin.target_identity'::regclass AND attacl IS NOT NULL
    )
