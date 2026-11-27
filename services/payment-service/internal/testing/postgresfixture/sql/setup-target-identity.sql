-- The CNPG bootstrap owner and the two logins created by resource administration. Shared
-- provisioning SQL then applies the access contract and enrollment marker.
CREATE ROLE payment NOLOGIN;
ALTER DATABASE payment OWNER TO payment;
CREATE ROLE go_payment_migrator LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT
    NOREPLICATION NOBYPASSRLS PASSWORD '%s';
CREATE ROLE go_payment_writer LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT
    NOREPLICATION NOBYPASSRLS PASSWORD '%s';
SET payment.environment = 'disposable';
SET payment.instance_id = '%s';
SET payment.cluster_uid = '%s'
