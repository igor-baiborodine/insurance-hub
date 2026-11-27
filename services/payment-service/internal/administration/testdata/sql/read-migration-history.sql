SELECT coalesce(string_agg(version_id || ':' || applied_at, ',' ORDER BY version_id), '')
FROM public.payment_schema_version
