SELECT c.relkind='r' AND c.relpersistence='p' AND c.relowner='document'::regrole
    AND NOT c.relrowsecurity AND NOT c.relforcerowsecurity
    AND (SELECT count(*)=3 AND bool_and(
        NOT atthasdef AND attidentity='' AND attgenerated='' AND (
          (attname='id' AND atttypid='bigint'::regtype AND attnotnull)
          OR (attname='bytes' AND atttypid='bytea'::regtype AND NOT attnotnull)
          OR (attname='policy_number' AND atttypid='varchar'::regtype
              AND atttypmod=259 AND NOT attnotnull)))
        FROM pg_attribute WHERE attrelid=c.oid AND attnum>0 AND NOT attisdropped)
    AND (SELECT count(*)=1 AND bool_and(contype='p' AND NOT condeferrable
        AND convalidated AND conkey=ARRAY[(SELECT attnum FROM pg_attribute
          WHERE attrelid=c.oid AND attname='id' AND NOT attisdropped)])
        FROM pg_constraint WHERE conrelid=c.oid)
    AND (SELECT count(*)=1 AND bool_and(indisprimary AND indisvalid AND indisready)
        FROM pg_index WHERE indrelid=c.oid)
    AND NOT EXISTS (SELECT FROM pg_trigger WHERE tgrelid=c.oid AND NOT tgisinternal)
    AND NOT EXISTS (SELECT FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid)
    AND NOT EXISTS (SELECT FROM pg_rewrite WHERE ev_class=c.oid)
FROM pg_class c WHERE c.oid='public.policy_document'::regclass
