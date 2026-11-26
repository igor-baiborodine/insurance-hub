-- Aggregate facts compared with the independently stated fixture expectations.
SELECT format('counts=%s/%s/%s/%s/%s/%s;quote=%s;offerCovers=%s;versions=%s;orphans=%s;answers=%s',
    (SELECT count(*) FROM public.offer), (SELECT count(*) FROM public.offer_answers),
    (SELECT count(*) FROM public.offer_cover), (SELECT count(*) FROM public.policy),
    (SELECT count(*) FROM public.policy_version), (SELECT count(*) FROM public.cover),
    (SELECT string_agg(total_price::text, ',' ORDER BY id) FROM public.offer),
    (SELECT sum(price) FROM public.offer_cover),
    (SELECT string_agg(format('%s:%s:%s:%s:%s', v.version_number, v.total_premium_amount,
        (SELECT sum(c.price) FROM public.cover c WHERE c.policy_version_id = v.id),
        v.cover_to, v.version_from), ',' ORDER BY v.version_number)
     FROM public.policy_version v JOIN public.policy p ON p.id = v.policy_id),
    (SELECT count(*) FROM public.offer_answers a
        WHERE NOT EXISTS (SELECT FROM public.offer o WHERE o.id = a.offer_id))
    + (SELECT count(*) FROM public.offer_cover a
        WHERE NOT EXISTS (SELECT FROM public.offer o WHERE o.id = a.offer_id))
    + (SELECT count(*) FROM public.policy_version v
        WHERE NOT EXISTS (SELECT FROM public.policy p WHERE p.id = v.policy_id))
    + (SELECT count(*) FROM public.cover c
        WHERE NOT EXISTS (SELECT FROM public.policy_version v WHERE v.id = c.policy_version_id)),
    (SELECT string_agg(question_code || '=' || answer, ',' ORDER BY question_code)
     FROM public.offer_answers))
