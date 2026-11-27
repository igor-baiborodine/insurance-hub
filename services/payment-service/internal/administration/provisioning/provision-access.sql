-- Idempotent Payment access setup, run by the platform administrator after both logins exist.
-- The session must set payment.environment, payment.instance_id and payment.cluster_uid.
-- An existing enrollment marker is never rewritten.
REVOKE ALL ON DATABASE payment FROM PUBLIC;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT CONNECT ON DATABASE payment TO go_payment_migrator, go_payment_writer;
GRANT USAGE ON SCHEMA public TO go_payment_writer;
GRANT payment TO go_payment_migrator WITH INHERIT FALSE, SET TRUE, ADMIN FALSE;
CREATE SCHEMA IF NOT EXISTS payment_admin AUTHORIZATION postgres;
REVOKE ALL ON SCHEMA payment_admin FROM PUBLIC;
CREATE TABLE IF NOT EXISTS payment_admin.target_identity (
    singleton boolean PRIMARY KEY CHECK (singleton),
    environment text NOT NULL,
    instance_id uuid NOT NULL,
    cluster_uid text NOT NULL
);
REVOKE ALL ON payment_admin.target_identity FROM PUBLIC;
GRANT USAGE ON SCHEMA payment_admin TO payment, go_payment_migrator;
GRANT SELECT ON payment_admin.target_identity TO payment, go_payment_migrator;
INSERT INTO payment_admin.target_identity (singleton, environment, instance_id, cluster_uid)
VALUES (true, current_setting('payment.environment'),
    current_setting('payment.instance_id')::uuid, current_setting('payment.cluster_uid'))
ON CONFLICT (singleton) DO NOTHING;
