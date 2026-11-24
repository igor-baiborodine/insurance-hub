-- Independent-session view of one policy number's aggregate rows and event intents.
SELECT format('policies=%s;versions=%s;covers=%s;premium=%s;intents=%s;allIntents=%s',
    (SELECT count(*) FROM public.policy WHERE number = $1),
    (SELECT count(*) FROM public.policy_version v
        JOIN public.policy p ON p.id = v.policy_id WHERE p.number = $1),
    (SELECT count(*) FROM public.cover c
        JOIN public.policy_version v ON v.id = c.policy_version_id
        JOIN public.policy p ON p.id = v.policy_id WHERE p.number = $1),
    (SELECT coalesce(sum(v.total_premium_amount), 0) FROM public.policy_version v
        JOIN public.policy p ON p.id = v.policy_id WHERE p.number = $1),
    (SELECT count(*) FROM public.policy_outbox o
        JOIN public.policy p ON p.id = o.policy_id WHERE p.number = $1),
    (SELECT count(*) FROM public.policy_outbox))
