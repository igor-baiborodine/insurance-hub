-- Independent fixture-owner row counts of every policy aggregate table and the outbox.
SELECT format('policies=%s;versions=%s;covers=%s;intents=%s',
    (SELECT count(*) FROM public.policy),
    (SELECT count(*) FROM public.policy_version),
    (SELECT count(*) FROM public.cover),
    (SELECT count(*) FROM public.policy_outbox))
