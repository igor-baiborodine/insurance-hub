INSERT INTO public.base_price_rules (tariff_id, cover_code, price_formula, position)
SELECT tariff_id, cover_code, price_formula, position + 1
FROM public.base_price_rules WHERE tariff_id = (SELECT id FROM public.tariff WHERE code = 'CAR');
