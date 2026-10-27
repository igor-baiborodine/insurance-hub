-- $1 indicates that the Product table exists.
SELECT CASE
    WHEN count(*) = 1
         AND count(*) FILTER (WHERE version_id = 0 AND is_applied) = 1
        THEN 'pending'
    WHEN count(*) = 2
         AND count(*) FILTER (WHERE version_id = 0 AND is_applied) = 1
         AND count(*) FILTER (WHERE version_id = 1 AND is_applied) = 1
         AND $1
        THEN 'version=1'
    ELSE 'invalid'
END
FROM public.goose_db_version
