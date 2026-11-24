-- Independent fixture-owner view of one issued policy, its only version and its intents.
SELECT format('agent=%s;versions=%s;version=%s;holder=%s %s/%s;product=%s;cover=%s..%s;' ||
        'valid=%s..%s;premium=%s;covers=%s;intents=%s/%s',
    p.agent_login,
    (SELECT count(*) FROM public.policy_version WHERE policy_id = p.id),
    v.version_number, v.first_name, v.last_name, v.pesel, v.product_code,
    v.cover_from, v.cover_to, v.version_from, v.version_to, v.total_premium_amount,
    (SELECT string_agg(c.code || '=' || c.price, ',' ORDER BY c.code)
        FROM public.cover c WHERE c.policy_version_id = v.id),
    (SELECT count(*) FROM public.policy_outbox o
        WHERE o.policy_id = p.id AND o.topic = 'policy-registered' AND o.message_key = p.number
            AND o.published_at IS NULL),
    (SELECT count(*) FROM public.policy_outbox o WHERE o.policy_id = p.id)),
    v.account_number,
    (SELECT o.payload::text FROM public.policy_outbox o WHERE o.policy_id = p.id)
FROM public.policy p
JOIN public.policy_version v ON v.policy_id = p.id
WHERE p.number = $1
