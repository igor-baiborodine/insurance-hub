-- Every table-local identity must allocate above its own table's retained maximum.
SELECT bool_and(
    coalesce(pg_sequence_last_value(pg_get_serial_sequence(name, 'id')::regclass), 0) >= maximum
)
FROM (
    VALUES
        ('public.offer', (SELECT coalesce(max(id), 0) FROM public.offer)),
        ('public.policy', (SELECT coalesce(max(id), 0) FROM public.policy)),
        ('public.policy_version', (SELECT coalesce(max(id), 0) FROM public.policy_version)),
        ('public.cover', (SELECT coalesce(max(id), 0) FROM public.cover)),
        ('public.policy_outbox', (SELECT coalesce(max(id), 0) FROM public.policy_outbox))
) AS allocator (name, maximum)
