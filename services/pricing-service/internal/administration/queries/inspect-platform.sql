-- $1 expected current user, $2 expected session user.
SELECT current_user = $1 AND session_user = $2 AND current_database() = 'pricing'
    AND EXISTS (
        SELECT FROM pg_class AS marker
        JOIN pg_namespace AS namespace ON namespace.oid = marker.relnamespace
        WHERE marker.oid = 'pricing_admin.target_identity'::regclass
          AND marker.relowner = 'postgres'::regrole
          AND namespace.nspowner = 'postgres'::regrole
          AND marker.relkind = 'r' AND NOT marker.relrowsecurity
    )
    -- Commands may read the marker, but cannot change their own enrollment.
    AND NOT EXISTS (
        SELECT FROM pg_roles
        WHERE rolname IN ('pricing', 'go_pricing_migrator', 'go_pricing_seeder')
          AND (
              rolsuper OR rolcreaterole OR pg_has_role(oid, 'postgres'::regrole, 'MEMBER')
              OR (rolname = 'go_pricing_seeder' AND pg_has_role(oid, 'pricing'::regrole, 'MEMBER'))
              OR has_schema_privilege(oid, 'pricing_admin', 'CREATE')
              OR has_any_column_privilege(oid, 'pricing_admin.target_identity', 'INSERT,UPDATE,REFERENCES')
              OR has_table_privilege(oid, 'pricing_admin.target_identity', 'DELETE,TRUNCATE,TRIGGER')
          )
    )
    AND NOT EXISTS (
        SELECT FROM pg_attribute
        WHERE attrelid = 'pricing_admin.target_identity'::regclass AND attacl IS NOT NULL
    );
