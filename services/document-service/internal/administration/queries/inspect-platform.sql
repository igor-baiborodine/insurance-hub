-- Enrollment is written only by platform administration, never by either service login.
-- $1 environment, $2 instance UUID, $3 cluster UID, $4 current user, $5 session user.
SELECT current_user = $4 AND session_user = $5
    AND current_database() = 'document'
    AND EXISTS (SELECT FROM pg_database WHERE datname=current_database() AND datdba='document'::regrole)
    AND EXISTS (
        SELECT FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
        WHERE c.oid='document_admin.target_identity'::regclass
          AND c.relowner='postgres'::regrole AND n.nspowner='postgres'::regrole
          AND c.relkind='r' AND NOT c.relrowsecurity
    )
    AND (SELECT count(*)=1 FROM document_admin.target_identity)
    AND EXISTS (SELECT FROM document_admin.target_identity
        WHERE singleton AND environment=$1 AND instance_id=$2 AND cluster_uid=$3)
    AND NOT EXISTS (SELECT FROM pg_roles WHERE rolname IN ('document','go_document_migrator','go_document_reader')
        AND (has_any_column_privilege(oid,'document_admin.target_identity','INSERT,UPDATE,REFERENCES')
             OR has_table_privilege(oid,'document_admin.target_identity','DELETE,TRUNCATE,TRIGGER')))
    AND NOT EXISTS (SELECT FROM pg_attribute
        WHERE attrelid='document_admin.target_identity'::regclass AND attacl IS NOT NULL)
