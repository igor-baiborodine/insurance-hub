-- $1 seed allowed.
SELECT
    product_table.relkind = 'r'
    AND product_table.relpersistence = 'p'
    AND NOT product_table.relrowsecurity
    AND NOT product_table.relforcerowsecurity
    AND pg_get_userbyid(product_table.relowner) = 'product'
    AND (
        SELECT count(*)
        FROM pg_index
        WHERE indrelid = product_table.oid
    ) = 1
    AND (
        SELECT count(*)
        FROM pg_index AS product_index
        JOIN pg_class AS index_class ON index_class.oid = product_index.indexrelid
        JOIN pg_am AS index_method ON index_method.oid = index_class.relam
        WHERE product_index.indrelid = product_table.oid
          AND product_index.indisprimary
          AND product_index.indisvalid
          AND product_index.indisready
          AND product_index.indisunique
          AND index_method.amname = 'btree'
    ) = 1
    AND NOT EXISTS (
        SELECT
        FROM pg_trigger
        WHERE tgrelid = product_table.oid
          AND NOT tgisinternal
    )
    AND NOT EXISTS (
        SELECT
        FROM pg_inherits
        WHERE inhrelid = product_table.oid
           OR inhparent = product_table.oid
    )
    AND NOT EXISTS (
        SELECT
        FROM pg_attribute
        WHERE attrelid = product_table.oid
          AND attacl IS NOT NULL
    )
    AND NOT EXISTS (
        SELECT
        FROM aclexplode(
            COALESCE(product_table.relacl, acldefault('r', product_table.relowner))
        ) AS access
        LEFT JOIN pg_roles AS granted_role ON granted_role.oid = access.grantee
        WHERE access.grantee <> product_table.relowner
          AND (
              granted_role.rolname IS NULL
              OR (
                  granted_role.rolname = 'go_product_reader'
                  AND (access.privilege_type <> 'SELECT' OR access.is_grantable)
              )
              OR (
                  granted_role.rolname = 'go_product_seeder'
                  AND (
                      NOT $1
                      OR access.privilege_type NOT IN ('SELECT', 'INSERT')
                      OR access.is_grantable
                  )
              )
              OR granted_role.rolname NOT IN ('go_product_reader', 'go_product_seeder')
          )
    )
    AND has_table_privilege('go_product_reader', product_table.oid, 'SELECT')
    AND NOT has_table_privilege(
        'go_product_reader',
        product_table.oid,
        'INSERT,UPDATE,DELETE,TRUNCATE'
    )
FROM pg_class AS product_table
WHERE product_table.oid = 'public.product'::regclass
