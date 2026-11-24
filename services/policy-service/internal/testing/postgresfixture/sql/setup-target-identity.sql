-- The CNPG bootstrap owner and the two logins created by resource administration. Shared
-- provisioning SQL then applies the access contract and enrollment marker.
CREATE ROLE policy NOLOGIN;
ALTER DATABASE policy OWNER TO policy;
CREATE ROLE go_policy_migrator LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT
    NOREPLICATION NOBYPASSRLS PASSWORD '%s';
CREATE ROLE go_policy_writer LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT
    NOREPLICATION NOBYPASSRLS PASSWORD '%s';
SET policy.environment = 'disposable';
SET policy.instance_id = '%s';
SET policy.cluster_uid = '%s'
