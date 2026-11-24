-- Independent-session view of every offer row and the offers carrying one number.
SELECT format('offers=%s;answers=%s;covers=%s;numbered=%s',
    (SELECT count(*) FROM public.offer),
    (SELECT count(*) FROM public.offer_answers),
    (SELECT count(*) FROM public.offer_cover),
    (SELECT count(*) FROM public.offer WHERE number = $1))
