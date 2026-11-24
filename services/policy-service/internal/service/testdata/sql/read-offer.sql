-- Independent fixture-owner view of one committed offer with its answers and cover prices.
SELECT format('%s|%s|%s|%s|%s|%s|%s|%s',
    o.product_code, o.status, o.creation_date, o.policy_from, o.policy_to, o.total_price,
    (SELECT string_agg(a.question_code || '=' || a.answer, ',' ORDER BY a.question_code)
        FROM public.offer_answers AS a WHERE a.offer_id = o.id),
    (SELECT string_agg(c.cover_code || '=' || c.price, ',' ORDER BY c.cover_code)
        FROM public.offer_cover AS c WHERE c.offer_id = o.id))
FROM public.offer AS o
WHERE o.number = $1
