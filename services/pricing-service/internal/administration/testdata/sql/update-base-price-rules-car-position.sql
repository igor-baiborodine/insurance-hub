UPDATE public.base_price_rules SET position = 2 WHERE tariff_id = (SELECT id FROM public.tariff WHERE code = 'CAR')
