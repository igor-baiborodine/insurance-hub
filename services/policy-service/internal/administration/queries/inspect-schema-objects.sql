-- Business table count, outbox, Java allocator and Goose history presence.
SELECT
    (SELECT count(*) FROM unnest(ARRAY[
        'public.offer', 'public.offer_answers', 'public.offer_cover',
        'public.policy', 'public.policy_version', 'public.cover'
    ]) AS name WHERE to_regclass(name) IS NOT NULL)::integer,
    to_regclass('public.policy_outbox') IS NOT NULL,
    to_regclass('public.hibernate_sequence') IS NOT NULL,
    to_regclass('public.goose_db_version') IS NOT NULL
