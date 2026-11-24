-- Independent fixture-owner view of one policy's version history, its intent count and the
-- latest intent's topic, key and record value.
SELECT
    coalesce((SELECT string_agg(format('v%s %s..%s valid %s..%s premium %s covers %s',
            v.version_number, v.cover_from, v.cover_to, v.version_from, v.version_to,
            v.total_premium_amount,
            (SELECT string_agg(c.code || '=' || c.price, ',' ORDER BY c.code)
                FROM public.cover c WHERE c.policy_version_id = v.id)),
        ' | ' ORDER BY v.version_number)
        FROM public.policy_version v JOIN public.policy p ON p.id = v.policy_id
        WHERE p.number = $1), ''),
    (SELECT count(*) FROM public.policy_outbox o JOIN public.policy p ON p.id = o.policy_id
        WHERE p.number = $1),
    coalesce((SELECT o.topic || ' ' || o.message_key || ' ' || o.payload::text
        FROM public.policy_outbox o JOIN public.policy p ON p.id = o.policy_id
        WHERE p.number = $1 ORDER BY o.id DESC LIMIT 1), '')
