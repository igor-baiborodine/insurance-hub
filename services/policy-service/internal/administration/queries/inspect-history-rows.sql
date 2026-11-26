-- $1 is the current schema version. The migrator creates history with that version applied.
SELECT CASE
    WHEN count(*) = 1 AND count(*) FILTER (WHERE version_id = $1) = 1
        THEN 'version=' || $1::bigint::text
    ELSE 'invalid'
END
FROM public.policy_schema_version
