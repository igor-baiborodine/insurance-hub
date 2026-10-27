package administration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const productSchemaIdentity = "76fac669b254931c0709c236fdd7804b"

const productSchemaQuery = `WITH schema_parts AS (
 SELECT format('column|%s|%s|%s|%s|%s|%s', ordinal_position, column_name, udt_name,
 COALESCE(character_maximum_length::text, ''), is_nullable, COALESCE(column_default, '')) value
 FROM information_schema.columns WHERE table_schema='public' AND table_name='product'
 UNION ALL
 SELECT format('constraint|%s|%s|%s', conname, contype, pg_get_constraintdef(oid, true))
 FROM pg_constraint WHERE conrelid=to_regclass('public.product'))
 SELECT md5(string_agg(value, E'\n' ORDER BY value)) FROM schema_parts`

// inspect verifies the protected target and complete initial version-1 boundary. The same
// function runs under Goose's session lock and inside read-only status transactions.
func inspect(ctx context.Context, db queryRower, target Target) (string, error) {
	if err := inspectPlatform(ctx, db, target); err != nil {
		return "", err
	}
	var product, history bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('public.product') IS NOT NULL,
 to_regclass('public.goose_db_version') IS NOT NULL`).Scan(&product, &history); err != nil {
		return "", fmt.Errorf("inspect schema objects: %w", err)
	}
	if product {
		if err := inspectProduct(ctx, db, target); err != nil {
			return "", err
		}
	}
	if !history {
		if product {
			return "unmanaged", nil
		}
		return "uninitialized", nil
	}
	version, err := inspectHistory(ctx, db, product, target)
	if err != nil {
		return "", err
	}
	if version == "version=1" && target.SeedAllowed {
		if err := inspectManagedSeedGrants(ctx, db); err != nil {
			return "", err
		}
	}
	return version, nil
}

func inspectManagedSeedGrants(ctx context.Context, db queryRower) error {
	var accepted bool
	err := db.QueryRowContext(ctx, `SELECT
 has_table_privilege('go_product_seeder','public.product','SELECT')
 AND has_table_privilege('go_product_seeder','public.product','INSERT')
 AND NOT has_table_privilege('go_product_seeder','public.product','UPDATE,DELETE,TRUNCATE')
 AND has_table_privilege('go_product_seeder','public.goose_db_version','SELECT')
 AND NOT has_table_privilege('go_product_seeder','public.goose_db_version','INSERT,UPDATE,DELETE,TRUNCATE')
 AND NOT has_sequence_privilege('go_product_seeder','public.goose_db_version_id_seq','USAGE,SELECT,UPDATE')`).
		Scan(&accepted)
	if err != nil {
		return fmt.Errorf("inspect managed seed grants: %w", err)
	}
	if !accepted {
		return errors.New("managed seed grants differ from enrollment")
	}
	return nil
}

func inspectPlatform(ctx context.Context, db queryRower, target Target) error {
	if target.Environment == "production" && target.SeedAllowed {
		return errors.New("production target cannot allow seeding")
	}
	var accepted bool
	err := db.QueryRowContext(ctx, `SELECT current_user='product'
 AND session_user='go_product_migrator'
 AND current_database()='product'
 AND (SELECT pg_get_userbyid(datdba)='product' FROM pg_database WHERE datname=current_database())
 AND (SELECT pg_get_userbyid(nspowner) IN ('product','pg_database_owner')
      FROM pg_namespace WHERE nspname='public')
 AND (SELECT count(*)=2 AND bool_and(rolname IN ('product','go_product_migrator'))
      FROM pg_roles WHERE rolname IN ('product','go_product_migrator'))
 AND NOT EXISTS(SELECT FROM pg_roles WHERE rolname IN ('product','go_product_migrator')
      AND (NOT rolcanlogin OR rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls))
 AND (SELECT NOT rolinherit FROM pg_roles WHERE rolname='go_product_migrator')
 AND (SELECT count(*)=1 AND bool_and(NOT inherit_option AND set_option AND NOT admin_option)
      FROM pg_auth_members WHERE member='go_product_migrator'::regrole)
 AND NOT EXISTS(SELECT FROM pg_auth_members WHERE roleid='go_product_migrator'::regrole)
 AND NOT EXISTS(SELECT FROM pg_database d,
      LATERAL aclexplode(COALESCE(d.datacl,acldefault('d',d.datdba))) a
      WHERE d.datname=current_database() AND a.grantee=0)
 AND NOT EXISTS(SELECT FROM pg_namespace n,
      LATERAL aclexplode(COALESCE(n.nspacl,acldefault('n',n.nspowner))) a
      WHERE n.nspname='public' AND a.grantee=0)
 AND NOT EXISTS(SELECT FROM pg_default_acl d, LATERAL aclexplode(d.defaclacl) a
                WHERE d.defaclrole='product'::regrole AND a.grantee<>d.defaclrole)
 AND (SELECT pg_get_userbyid(nspowner)='postgres' FROM pg_namespace
      WHERE nspname='product_admin')
 AND NOT EXISTS(SELECT FROM pg_namespace n,
      LATERAL aclexplode(COALESCE(n.nspacl,acldefault('n',n.nspowner))) a
      WHERE n.nspname='product_admin' AND a.grantee=0)
 AND NOT has_schema_privilege('go_product_reader','product_admin','USAGE')
 AND (SELECT pg_get_userbyid(relowner)='postgres' AND relkind='r' AND NOT relrowsecurity
      FROM pg_class WHERE oid='product_admin.target_identity'::regclass)
 AND NOT EXISTS(SELECT FROM pg_class c,
      LATERAL aclexplode(COALESCE(c.relacl,acldefault('r',c.relowner))) a
      WHERE c.oid='product_admin.target_identity'::regclass AND a.grantee=0)
 AND NOT has_table_privilege('go_product_reader','product_admin.target_identity','SELECT')
 AND NOT has_table_privilege('go_product_migrator','product_admin.target_identity','UPDATE')
 AND NOT has_table_privilege('product','product_admin.target_identity','UPDATE')
 AND has_table_privilege('product','product_admin.target_identity','SELECT')
 AND (SELECT count(*)=5 FROM information_schema.columns WHERE table_schema='product_admin'
      AND table_name='target_identity')
 AND (SELECT count(*)=5 AND bool_and((column_name,data_type,is_nullable) IN
      (('singleton','boolean','NO'),('environment','text','NO'),
       ('instance_id','uuid','NO'),('cluster_uid','text','NO'),
       ('seed_allowed','boolean','NO')))
      FROM information_schema.columns WHERE table_schema='product_admin'
      AND table_name='target_identity')
 AND (SELECT count(*)=1 FROM pg_constraint WHERE conrelid='product_admin.target_identity'::regclass
      AND contype='p' AND pg_get_constraintdef(oid,true)='PRIMARY KEY (singleton)')
 AND (SELECT count(*)=1 FROM pg_index i JOIN pg_class x ON x.oid=i.indexrelid
      JOIN pg_am a ON a.oid=x.relam
      WHERE i.indrelid='product_admin.target_identity'::regclass
      AND i.indisprimary AND i.indisvalid AND i.indisready AND i.indisunique AND a.amname='btree')
 AND (SELECT count(*)=1 FROM pg_index
      WHERE indrelid='product_admin.target_identity'::regclass)
 AND (SELECT count(*)=1 FROM pg_constraint WHERE conrelid='product_admin.target_identity'::regclass
      AND contype='c' AND pg_get_constraintdef(oid,true)='CHECK (singleton)')
 AND (SELECT count(*)=1 FROM product_admin.target_identity)
 AND (SELECT count(*)=1 FROM product_admin.target_identity
      WHERE singleton AND environment=$1 AND instance_id=$2 AND cluster_uid=$3
      AND seed_allowed=$4)`, target.Environment, target.InstanceID, target.ClusterUID,
		target.SeedAllowed).Scan(&accepted)
	if err != nil {
		return fmt.Errorf("inspect protected target: %w", err)
	}
	if !accepted {
		return errors.New("protected target, role or grant policy differs from enrollment")
	}
	return inspectRoles(ctx, db, target)
}

func inspectRoles(ctx context.Context, db queryRower, target Target) error {
	var accepted bool
	err := db.QueryRowContext(ctx, `SELECT
 (SELECT count(*)=1 FROM pg_roles WHERE rolname='go_product_reader' AND rolcanlogin
  AND NOT (rolsuper OR rolcreatedb OR rolcreaterole OR rolinherit OR rolreplication OR rolbypassrls))
 AND NOT EXISTS(SELECT FROM pg_auth_members WHERE member='go_product_reader'::regrole
                OR roleid='go_product_reader'::regrole)
 AND has_database_privilege('go_product_reader',current_database(),'CONNECT')
 AND NOT has_database_privilege('go_product_reader',current_database(),'CREATE,TEMP')
 AND has_schema_privilege('go_product_reader','public','USAGE')
 AND NOT has_schema_privilege('go_product_reader','public','CREATE')
 AND has_database_privilege('go_product_migrator',current_database(),'CONNECT')
 AND NOT has_database_privilege('go_product_migrator',current_database(),'CREATE,TEMP')
 AND ($1 <> 'production' OR NOT EXISTS(SELECT FROM pg_roles WHERE rolname='go_product_seeder'))
 AND (CASE WHEN $2 THEN ((SELECT count(*)=1 FROM pg_roles WHERE rolname='go_product_seeder'
       AND rolcanlogin AND NOT (rolsuper OR rolcreatedb OR rolcreaterole OR rolinherit
       OR rolreplication OR rolbypassrls))
      AND NOT EXISTS(SELECT FROM pg_auth_members WHERE member=to_regrole('go_product_seeder')
                     OR roleid=to_regrole('go_product_seeder'))
      AND has_database_privilege('go_product_seeder',current_database(),'CONNECT')
      AND NOT has_database_privilege('go_product_seeder',current_database(),'CREATE,TEMP')
      AND has_schema_privilege('go_product_seeder','public','USAGE')
      AND NOT has_schema_privilege('go_product_seeder','public','CREATE')) ELSE TRUE END)
 AND NOT EXISTS(SELECT FROM pg_database d,
       LATERAL aclexplode(COALESCE(d.datacl,acldefault('d',d.datdba))) a
       LEFT JOIN pg_roles r ON r.oid=a.grantee
       WHERE d.datname=current_database() AND a.grantee<>d.datdba
       AND (r.rolname NOT IN ('go_product_reader','go_product_migrator','go_product_seeder')
            OR r.rolname IS NULL OR a.privilege_type<>'CONNECT' OR a.is_grantable
            OR (r.rolname='go_product_seeder' AND NOT $2)))
 AND NOT EXISTS(SELECT FROM pg_namespace n,
       LATERAL aclexplode(COALESCE(n.nspacl,acldefault('n',n.nspowner))) a
       LEFT JOIN pg_roles r ON r.oid=a.grantee
       WHERE n.nspname='public' AND a.grantee<>n.nspowner
       AND (r.rolname NOT IN ('product','go_product_reader','go_product_seeder')
            OR r.rolname IS NULL OR a.privilege_type<>'USAGE' OR a.is_grantable
            OR (r.rolname='go_product_seeder' AND NOT $2)))
 AND NOT EXISTS(SELECT FROM pg_namespace n,
       LATERAL aclexplode(COALESCE(n.nspacl,acldefault('n',n.nspowner))) a
       LEFT JOIN pg_roles r ON r.oid=a.grantee
       WHERE n.nspname='product_admin' AND a.grantee<>n.nspowner
       AND (r.rolname NOT IN ('product','go_product_migrator','go_product_seeder')
            OR r.rolname IS NULL OR a.privilege_type<>'USAGE' OR a.is_grantable
            OR (r.rolname='go_product_seeder' AND NOT $2)))
 AND NOT EXISTS(SELECT FROM pg_class c,
       LATERAL aclexplode(COALESCE(c.relacl,acldefault('r',c.relowner))) a
       LEFT JOIN pg_roles r ON r.oid=a.grantee
       WHERE c.oid='product_admin.target_identity'::regclass
       AND a.grantee<>c.relowner
       AND (r.rolname NOT IN ('product','go_product_migrator','go_product_seeder')
            OR r.rolname IS NULL OR a.privilege_type<>'SELECT' OR a.is_grantable
            OR (r.rolname='go_product_seeder' AND NOT $2)))`,
		target.Environment, target.SeedAllowed).Scan(&accepted)
	if err != nil {
		return fmt.Errorf("inspect administration roles: %w", err)
	}
	if !accepted {
		return errors.New("administration role or grant policy differs from enrollment")
	}
	return nil
}

func inspectProduct(ctx context.Context, db queryRower, target Target) error {
	var identity string
	if err := db.QueryRowContext(ctx, productSchemaQuery).Scan(&identity); err != nil {
		return fmt.Errorf("inspect Product schema: %w", err)
	}
	if identity != productSchemaIdentity {
		return errors.New("incompatible Product columns or constraints")
	}
	var accepted bool
	err := db.QueryRowContext(ctx, `SELECT c.relkind='r' AND c.relpersistence='p'
 AND NOT c.relrowsecurity AND NOT c.relforcerowsecurity
 AND pg_get_userbyid(c.relowner)='product'
 AND (SELECT count(*) FROM pg_index WHERE indrelid=c.oid)=1
 AND (SELECT count(*) FROM pg_index i JOIN pg_class x ON x.oid=i.indexrelid
      JOIN pg_am a ON a.oid=x.relam WHERE i.indrelid=c.oid AND i.indisprimary
      AND i.indisvalid AND i.indisready AND i.indisunique AND a.amname='btree')=1
 AND NOT EXISTS(SELECT FROM pg_trigger WHERE tgrelid=c.oid AND NOT tgisinternal)
 AND NOT EXISTS(SELECT FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid)
 AND NOT EXISTS(SELECT FROM pg_attribute WHERE attrelid=c.oid AND attacl IS NOT NULL)
 AND NOT EXISTS(SELECT FROM aclexplode(COALESCE(c.relacl,acldefault('r',c.relowner))) a
      LEFT JOIN pg_roles r ON r.oid=a.grantee
      WHERE a.grantee<>c.relowner AND
      (r.rolname IS NULL OR
       (r.rolname='go_product_reader' AND (a.privilege_type<>'SELECT' OR a.is_grantable)) OR
       (r.rolname='go_product_seeder' AND
        (NOT $1 OR a.privilege_type NOT IN ('SELECT','INSERT') OR a.is_grantable)) OR
       r.rolname NOT IN ('go_product_reader','go_product_seeder')))
 AND has_table_privilege('go_product_reader',c.oid,'SELECT')
 AND NOT has_table_privilege('go_product_reader',c.oid,'INSERT,UPDATE,DELETE,TRUNCATE')
 FROM pg_class c WHERE c.oid='public.product'::regclass`, target.SeedAllowed).Scan(&accepted)
	if err != nil {
		return fmt.Errorf("inspect Product metadata: %w", err)
	}
	if !accepted {
		return errors.New("incompatible Product ownership, index, security or grants")
	}
	return nil
}

func inspectHistory(
	ctx context.Context,
	db queryRower,
	product bool,
	target Target,
) (string, error) {
	var shape bool
	err := db.QueryRowContext(ctx, `SELECT c.relkind='r' AND c.relpersistence='p'
 AND pg_get_userbyid(c.relowner)='product' AND NOT c.relrowsecurity AND NOT c.relforcerowsecurity
 AND (SELECT count(*)=4 AND bool_and((column_name,data_type,is_nullable) IN
     (('id','integer','NO'),('version_id','bigint','NO'),('is_applied','boolean','NO'),
      ('tstamp','timestamp without time zone','NO')))
      FROM information_schema.columns WHERE table_schema='public' AND table_name='goose_db_version')
 AND (SELECT count(*)=1 FROM pg_constraint WHERE conrelid=c.oid AND contype='p'
      AND pg_get_constraintdef(oid,true)='PRIMARY KEY (id)')
 AND (SELECT count(*)=1 FROM pg_index WHERE indrelid=c.oid)
 AND (SELECT count(*)=1 FROM pg_index i JOIN pg_class x ON x.oid=i.indexrelid
      JOIN pg_am a ON a.oid=x.relam WHERE i.indrelid=c.oid AND i.indisprimary
      AND i.indisvalid AND i.indisready AND i.indisunique AND a.amname='btree')
 AND (SELECT count(*)=1 FROM pg_class s JOIN pg_depend d ON d.objid=s.oid
      WHERE s.relkind='S' AND s.relname='goose_db_version_id_seq'
      AND pg_get_userbyid(s.relowner)='product' AND d.refobjid=c.oid)
 AND (SELECT attidentity IN ('a','d') FROM pg_attribute
      WHERE attrelid=c.oid AND attname='id' AND NOT attisdropped)
 AND (SELECT pg_get_expr(d.adbin,d.adrelid)='now()' FROM pg_attrdef d
      JOIN pg_attribute a ON a.attrelid=d.adrelid AND a.attnum=d.adnum
      WHERE d.adrelid=c.oid AND a.attname='tstamp')
 AND NOT EXISTS(SELECT FROM pg_trigger WHERE tgrelid=c.oid AND NOT tgisinternal)
 AND NOT EXISTS(SELECT FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid)
 AND NOT EXISTS(SELECT FROM pg_attribute WHERE attrelid=c.oid AND attacl IS NOT NULL)
 AND NOT EXISTS(SELECT FROM pg_class s,
      LATERAL aclexplode(COALESCE(s.relacl,acldefault('S',s.relowner))) a
      WHERE s.oid='public.goose_db_version_id_seq'::regclass AND a.grantee<>s.relowner)
 AND NOT EXISTS(SELECT FROM aclexplode(COALESCE(c.relacl,acldefault('r',c.relowner))) a
      LEFT JOIN pg_roles r ON r.oid=a.grantee WHERE a.grantee<>c.relowner
      AND (r.rolname IS DISTINCT FROM 'go_product_seeder' OR NOT $1 OR
           a.privilege_type<>'SELECT' OR a.is_grantable))
 AND NOT has_table_privilege('go_product_reader',c.oid,'SELECT')
 FROM pg_class c WHERE c.oid='public.goose_db_version'::regclass`, target.SeedAllowed).
		Scan(&shape)
	if err != nil {
		return "", fmt.Errorf("inspect migration history shape: %w", err)
	}
	if !shape {
		return "", errors.New("incompatible migration history metadata")
	}
	var version string
	err = db.QueryRowContext(ctx, `SELECT CASE WHEN count(*)=1 AND
 count(*) FILTER(WHERE version_id=0 AND is_applied)=1 THEN 'pending'
 WHEN count(*)=2 AND count(*) FILTER(WHERE version_id=0 AND is_applied)=1 AND
 count(*) FILTER(WHERE version_id=1 AND is_applied)=1 AND $1 THEN 'version=1'
 ELSE 'invalid' END FROM public.goose_db_version`, product).Scan(&version)
	if err != nil {
		return "", fmt.Errorf("inspect migration history rows: %w", err)
	}
	if version == "invalid" {
		return "", errors.New("inconsistent migration history")
	}
	return version, nil
}
