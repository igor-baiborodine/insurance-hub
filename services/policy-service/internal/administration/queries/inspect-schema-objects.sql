-- Business table count, outbox and Policy migration history presence.
SELECT
    (SELECT count(*) FROM unnest(ARRAY[
        'public.offer', 'public.offer_answers', 'public.offer_cover',
        'public.policy', 'public.policy_version', 'public.cover'
    ]) AS name WHERE to_regclass(name) IS NOT NULL)::integer,
    to_regclass('public.policy_outbox') IS NOT NULL,
    to_regclass('public.policy_schema_version') IS NOT NULL
