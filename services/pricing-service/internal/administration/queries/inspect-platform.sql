SELECT current_database() = 'pricing'
    AND current_user = 'pricing' AND session_user = 'go_pricing_migrator'
    AND EXISTS (
        SELECT FROM pg_class AS marker
        JOIN pg_namespace AS namespace ON namespace.oid = marker.relnamespace
        WHERE marker.oid = 'pricing_admin.target_identity'::regclass
          AND marker.relowner = 'postgres'::regrole
          AND namespace.nspowner = 'postgres'::regrole
          AND marker.relkind = 'r' AND NOT marker.relrowsecurity
    )
    AND NOT EXISTS (
        SELECT FROM pg_roles
        WHERE rolname IN ('pricing', 'go_pricing_migrator')
          AND (
              rolsuper OR rolcreaterole OR pg_has_role(oid, 'postgres'::regrole, 'MEMBER')
              OR has_schema_privilege(oid, 'pricing_admin', 'CREATE')
              OR has_any_column_privilege(oid, 'pricing_admin.target_identity', 'INSERT,UPDATE,REFERENCES')
              OR has_table_privilege(oid, 'pricing_admin.target_identity', 'DELETE,TRUNCATE,TRIGGER')
          )
    )
