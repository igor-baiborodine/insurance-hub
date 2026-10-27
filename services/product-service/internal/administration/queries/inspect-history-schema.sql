-- Goose owns sequence/index/default details; check readable history and its owner only.
SELECT history.relkind = 'r' AND history.relpersistence = 'p'
    AND history.relowner = 'product'::regrole AND NOT history.relrowsecurity
    AND (
        SELECT count(*) = 4 AND bool_and(
            attnotnull AND (attname, atttypid) IN (
                ('id', 'integer'::regtype), ('version_id', 'bigint'::regtype),
                ('is_applied', 'boolean'::regtype), ('tstamp', 'timestamp'::regtype)
            )
        )
        FROM pg_attribute
        WHERE attrelid = history.oid AND attnum > 0 AND NOT attisdropped
    )
    AND NOT EXISTS (SELECT FROM pg_trigger WHERE tgrelid = history.oid AND NOT tgisinternal)
FROM pg_class AS history
WHERE history.oid = 'public.goose_db_version'::regclass
