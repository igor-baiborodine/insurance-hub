-- Independent fixture-owner row counts of every offer table.
SELECT format('offers=%s;answers=%s;covers=%s',
    (SELECT count(*) FROM public.offer),
    (SELECT count(*) FROM public.offer_answers),
    (SELECT count(*) FROM public.offer_cover))
