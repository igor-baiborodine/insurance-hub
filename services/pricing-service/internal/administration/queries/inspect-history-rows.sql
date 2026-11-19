-- $1 is the reviewed schema version. Pricing creates history with the first version already applied.
SELECT CASE
    WHEN count(*) = 1 AND count(*) FILTER (WHERE version_id = $1) = 1
        THEN 'version=' || $1::bigint::text
    ELSE 'invalid'
END
FROM public.pricing_schema_version;
