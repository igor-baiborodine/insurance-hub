SELECT coalesce(string_agg(version_id || ':' || applied_at, ',' ORDER BY version_id), '')
FROM public.policy_schema_version
