-- $1 seed allowed.
SELECT
    history_table.relkind = 'r'
    AND history_table.relpersistence = 'p'
    AND pg_get_userbyid(history_table.relowner) = 'product'
    AND NOT history_table.relrowsecurity
    AND NOT history_table.relforcerowsecurity
    AND (
        SELECT count(*) = 4
               AND bool_and(
                   (column_name, data_type, is_nullable) IN (
                       ('id', 'integer', 'NO'),
                       ('version_id', 'bigint', 'NO'),
                       ('is_applied', 'boolean', 'NO'),
                       ('tstamp', 'timestamp without time zone', 'NO')
                   )
               )
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'goose_db_version'
    )
    AND (
        SELECT count(*) = 1
        FROM pg_constraint
        WHERE conrelid = history_table.oid
          AND contype = 'p'
          AND pg_get_constraintdef(oid, true) = 'PRIMARY KEY (id)'
    )
    AND (
        SELECT count(*) = 1
        FROM pg_index
        WHERE indrelid = history_table.oid
    )
    AND (
        SELECT count(*) = 1
        FROM pg_index AS history_index
        JOIN pg_class AS index_class ON index_class.oid = history_index.indexrelid
        JOIN pg_am AS index_method ON index_method.oid = index_class.relam
        WHERE history_index.indrelid = history_table.oid
          AND history_index.indisprimary
          AND history_index.indisvalid
          AND history_index.indisready
          AND history_index.indisunique
          AND index_method.amname = 'btree'
    )
    AND (
        SELECT count(*) = 1
        FROM pg_class AS history_sequence
        JOIN pg_depend AS dependency ON dependency.objid = history_sequence.oid
        WHERE history_sequence.relkind = 'S'
          AND history_sequence.relname = 'goose_db_version_id_seq'
          AND pg_get_userbyid(history_sequence.relowner) = 'product'
          AND dependency.refobjid = history_table.oid
    )
    AND (
        SELECT attidentity IN ('a', 'd')
        FROM pg_attribute
        WHERE attrelid = history_table.oid
          AND attname = 'id'
          AND NOT attisdropped
    )
    AND (
        SELECT pg_get_expr(attribute_default.adbin, attribute_default.adrelid) = 'now()'
        FROM pg_attrdef AS attribute_default
        JOIN pg_attribute AS attribute
          ON attribute.attrelid = attribute_default.adrelid
         AND attribute.attnum = attribute_default.adnum
        WHERE attribute_default.adrelid = history_table.oid
          AND attribute.attname = 'tstamp'
    )
    AND NOT EXISTS (
        SELECT
        FROM pg_trigger
        WHERE tgrelid = history_table.oid
          AND NOT tgisinternal
    )
    AND NOT EXISTS (
        SELECT
        FROM pg_inherits
        WHERE inhrelid = history_table.oid
           OR inhparent = history_table.oid
    )
    AND NOT EXISTS (
        SELECT
        FROM pg_attribute
        WHERE attrelid = history_table.oid
          AND attacl IS NOT NULL
    )
    AND NOT EXISTS (
        SELECT
        FROM pg_class AS history_sequence,
             LATERAL aclexplode(
                 COALESCE(
                     history_sequence.relacl,
                     acldefault('S', history_sequence.relowner)
                 )
             ) AS access
        WHERE history_sequence.oid = 'public.goose_db_version_id_seq'::regclass
          AND access.grantee <> history_sequence.relowner
    )
    AND NOT EXISTS (
        SELECT
        FROM aclexplode(
            COALESCE(history_table.relacl, acldefault('r', history_table.relowner))
        ) AS access
        LEFT JOIN pg_roles AS granted_role ON granted_role.oid = access.grantee
        WHERE access.grantee <> history_table.relowner
          AND (
              granted_role.rolname IS DISTINCT FROM 'go_product_seeder'
              OR NOT $1
              OR access.privilege_type <> 'SELECT'
              OR access.is_grantable
          )
    )
    AND NOT has_table_privilege('go_product_reader', history_table.oid, 'SELECT')
FROM pg_class AS history_table
WHERE history_table.oid = 'public.goose_db_version'::regclass
