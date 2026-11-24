-- Idempotent Policy access setup, run by the platform administrator after both logins exist.
-- The session must set policy.environment, policy.instance_id and policy.cluster_uid.
-- An existing enrollment marker is never rewritten.
REVOKE ALL ON DATABASE policy FROM PUBLIC;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT CONNECT ON DATABASE policy TO go_policy_migrator, go_policy_writer;
GRANT USAGE ON SCHEMA public TO go_policy_writer;
GRANT policy TO go_policy_migrator WITH INHERIT FALSE, SET TRUE, ADMIN FALSE;
CREATE SCHEMA IF NOT EXISTS policy_admin AUTHORIZATION postgres;
REVOKE ALL ON SCHEMA policy_admin FROM PUBLIC;
CREATE TABLE IF NOT EXISTS policy_admin.target_identity (
    singleton boolean PRIMARY KEY CHECK (singleton),
    environment text NOT NULL,
    instance_id uuid NOT NULL,
    cluster_uid text NOT NULL
);
REVOKE ALL ON policy_admin.target_identity FROM PUBLIC;
GRANT USAGE ON SCHEMA policy_admin TO policy, go_policy_migrator;
GRANT SELECT ON policy_admin.target_identity TO policy, go_policy_migrator;
INSERT INTO policy_admin.target_identity (singleton, environment, instance_id, cluster_uid)
VALUES (true, current_setting('policy.environment'),
    current_setting('policy.instance_id')::uuid, current_setting('policy.cluster_uid'))
ON CONFLICT (singleton) DO NOTHING;
