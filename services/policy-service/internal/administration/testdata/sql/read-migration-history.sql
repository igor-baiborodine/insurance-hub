SELECT coalesce(string_agg(version_id || ':' || is_applied || ':' || tstamp, ',' ORDER BY id), '')
FROM public.goose_db_version
