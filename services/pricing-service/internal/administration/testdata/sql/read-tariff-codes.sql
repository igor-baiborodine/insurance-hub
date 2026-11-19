SELECT string_agg(code, ',' ORDER BY code) FROM public.tariff;
