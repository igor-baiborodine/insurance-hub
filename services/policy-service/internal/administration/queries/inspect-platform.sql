-- Enrollment is written only by platform administration, never by a Policy login.
-- $1 environment, $2 instance UUID, $3 cluster UID, $4 current user, $5 session user.
SELECT current_user = $4 AND session_user = $5
    AND current_database() = 'policy'
    AND EXISTS (
        SELECT FROM pg_database WHERE datname = current_database() AND datdba = 'policy'::regrole
    )
    AND EXISTS (
        SELECT FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE c.oid = 'policy_admin.target_identity'::regclass
          AND c.relowner = 'postgres'::regrole AND n.nspowner = 'postgres'::regrole
          AND c.relkind = 'r' AND NOT c.relrowsecurity
    )
    AND (SELECT count(*) = 1 FROM policy_admin.target_identity)
    AND EXISTS (
        SELECT FROM policy_admin.target_identity
        WHERE singleton AND environment = $1 AND instance_id = $2::uuid AND cluster_uid = $3
    )
    AND NOT EXISTS (
        SELECT FROM pg_roles
        WHERE rolname IN ('policy', 'go_policy_migrator', 'go_policy_runtime')
          AND (
              has_any_column_privilege(oid, 'policy_admin.target_identity', 'INSERT,UPDATE,REFERENCES')
              OR has_table_privilege(oid, 'policy_admin.target_identity', 'DELETE,TRUNCATE,TRIGGER')
          )
    )
    AND NOT EXISTS (
        SELECT FROM pg_attribute
        WHERE attrelid = 'policy_admin.target_identity'::regclass AND attacl IS NOT NULL
    )
