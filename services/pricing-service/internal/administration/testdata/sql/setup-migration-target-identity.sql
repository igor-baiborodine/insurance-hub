CREATE ROLE pricing NOLOGIN;
ALTER DATABASE pricing OWNER TO pricing;
CREATE ROLE go_pricing_migrator LOGIN NOINHERIT PASSWORD '%s';
CREATE ROLE go_pricing_reader LOGIN NOINHERIT PASSWORD '%s';
CREATE ROLE go_pricing_seeder LOGIN NOINHERIT PASSWORD '%s';
REVOKE ALL ON DATABASE pricing FROM PUBLIC;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT CONNECT ON DATABASE pricing TO go_pricing_reader, go_pricing_migrator, go_pricing_seeder;
GRANT USAGE ON SCHEMA public TO go_pricing_reader, go_pricing_seeder;
GRANT pricing TO go_pricing_migrator WITH INHERIT FALSE, SET TRUE, ADMIN FALSE;
CREATE SCHEMA pricing_admin AUTHORIZATION postgres;
CREATE TABLE pricing_admin.target_identity (
	singleton boolean PRIMARY KEY CHECK (singleton),
	environment text NOT NULL, instance_id uuid NOT NULL,
	cluster_uid text NOT NULL, seed_allowed boolean NOT NULL
);
INSERT INTO pricing_admin.target_identity
VALUES (true, 'disposable', '%s', '%s', true);
GRANT USAGE ON SCHEMA pricing_admin TO pricing, go_pricing_migrator, go_pricing_seeder;
GRANT SELECT ON pricing_admin.target_identity TO pricing, go_pricing_migrator, go_pricing_seeder;
