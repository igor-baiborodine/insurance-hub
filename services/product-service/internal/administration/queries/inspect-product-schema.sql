-- Validate the business shape, not a fingerprint of PostgreSQL metadata.
SELECT product.relkind = 'r' AND product.relpersistence = 'p'
    AND product.relowner = 'product'::regrole
    AND NOT product.relrowsecurity AND NOT product.relforcerowsecurity
    AND (
        SELECT count(*) = 2 AND bool_and(
            attnotnull AND NOT atthasdef AND attidentity = '' AND attgenerated = ''
            AND (
                -- varchar typmod includes PostgreSQL's four-byte length header.
                (attname = 'code' AND atttypid = 'varchar'::regtype AND atttypmod = 259)
                OR (attname = 'definition' AND atttypid = 'jsonb'::regtype)
            )
        )
        FROM pg_attribute
        WHERE attrelid = product.oid AND attnum > 0 AND NOT attisdropped
    )
    AND (
        SELECT count(*) = 1 AND bool_and(
            contype = 'p' AND NOT condeferrable AND convalidated
            AND conkey = ARRAY[(SELECT attnum FROM pg_attribute
                WHERE attrelid = product.oid AND attname = 'code' AND NOT attisdropped)]
        )
        FROM pg_constraint WHERE conrelid = product.oid
    )
    -- Additional non-unique indexes are harmless; extra uniqueness can reject valid seeds.
    AND NOT EXISTS (
        SELECT FROM pg_index
        WHERE indrelid = product.oid
          AND (NOT indisvalid OR NOT indisready OR (indisunique AND NOT indisprimary))
    )
    AND NOT EXISTS (SELECT FROM pg_trigger WHERE tgrelid = product.oid AND NOT tgisinternal)
    AND NOT EXISTS (SELECT FROM pg_inherits WHERE inhrelid = product.oid OR inhparent = product.oid)
FROM pg_class AS product
WHERE product.oid = 'public.product'::regclass
