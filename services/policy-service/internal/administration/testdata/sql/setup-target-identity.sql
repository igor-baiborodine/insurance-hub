-- Platform-owned roles and enrollment that resource administration provisions before migration.
CREATE ROLE policy NOLOGIN;
ALTER DATABASE policy OWNER TO policy;
CREATE ROLE go_policy_migrator LOGIN NOINHERIT PASSWORD '%s';
CREATE ROLE go_policy_runtime LOGIN NOINHERIT PASSWORD '%s';
REVOKE ALL ON DATABASE policy FROM PUBLIC;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT CONNECT ON DATABASE policy TO go_policy_migrator, go_policy_runtime;
GRANT USAGE ON SCHEMA public TO go_policy_runtime;
GRANT policy TO go_policy_migrator WITH INHERIT FALSE, SET TRUE, ADMIN FALSE;
CREATE SCHEMA policy_admin AUTHORIZATION postgres;
CREATE TABLE policy_admin.target_identity (
	singleton boolean PRIMARY KEY CHECK (singleton),
	environment text NOT NULL, instance_id uuid NOT NULL, cluster_uid text NOT NULL
);
INSERT INTO policy_admin.target_identity VALUES (true, 'disposable', '%s', '%s');
GRANT USAGE ON SCHEMA policy_admin TO policy, go_policy_migrator;
GRANT SELECT ON policy_admin.target_identity TO policy, go_policy_migrator
