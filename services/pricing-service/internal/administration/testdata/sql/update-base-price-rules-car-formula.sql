UPDATE public.base_price_rules SET price_formula = '101B' WHERE tariff_id = (SELECT id FROM public.tariff WHERE code = 'CAR')
