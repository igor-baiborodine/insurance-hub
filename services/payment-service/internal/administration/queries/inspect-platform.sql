-- Enrollment is written only by platform administration, never by a Payment login.
-- $1 environment, $2 instance UUID, $3 cluster UID, $4 current user, $5 session user.
SELECT current_user = $4 AND session_user = $5
    AND current_database() = 'payment'
    AND EXISTS (
        SELECT FROM pg_database WHERE datname = current_database() AND datdba = 'payment'::regrole
    )
    AND EXISTS (
        SELECT FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE c.oid = 'payment_admin.target_identity'::regclass
          AND c.relowner = 'postgres'::regrole AND n.nspowner = 'postgres'::regrole
          AND c.relkind = 'r' AND NOT c.relrowsecurity
    )
    AND (SELECT count(*) = 1 FROM payment_admin.target_identity)
    AND EXISTS (
        SELECT FROM payment_admin.target_identity
        WHERE singleton AND environment = $1 AND instance_id = $2::uuid AND cluster_uid = $3
    )
    AND NOT EXISTS (
        SELECT FROM pg_roles
        WHERE rolname IN ('payment', 'go_payment_migrator', 'go_payment_writer')
          AND (
              has_any_column_privilege(oid, 'payment_admin.target_identity', 'INSERT,UPDATE,REFERENCES')
              OR has_table_privilege(oid, 'payment_admin.target_identity', 'DELETE,TRUNCATE,TRIGGER')
          )
    )
    AND NOT EXISTS (
        SELECT FROM pg_attribute
        WHERE attrelid = 'payment_admin.target_identity'::regclass AND attacl IS NOT NULL
    )
