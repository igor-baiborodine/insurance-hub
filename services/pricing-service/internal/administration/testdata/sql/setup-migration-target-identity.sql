CREATE ROLE pricing NOLOGIN;
ALTER DATABASE pricing OWNER TO pricing;
CREATE ROLE go_pricing_migrator LOGIN NOINHERIT PASSWORD '%s';
GRANT pricing TO go_pricing_migrator WITH INHERIT FALSE, SET TRUE, ADMIN FALSE;
CREATE SCHEMA pricing_admin AUTHORIZATION postgres;
CREATE TABLE pricing_admin.target_identity (
	singleton boolean PRIMARY KEY CHECK (singleton),
	environment text NOT NULL, instance_id uuid NOT NULL,
	cluster_uid text NOT NULL, seed_allowed boolean NOT NULL
);
INSERT INTO pricing_admin.target_identity
VALUES (true, 'disposable', '%s', '%s', true);
GRANT USAGE ON SCHEMA pricing_admin TO pricing, go_pricing_migrator;
GRANT SELECT ON pricing_admin.target_identity TO pricing, go_pricing_migrator;
