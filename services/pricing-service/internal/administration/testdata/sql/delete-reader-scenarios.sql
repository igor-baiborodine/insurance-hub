DELETE FROM public.discount_markup_rule WHERE tariff_id BETWEEN 9001 AND 9006;
DELETE FROM public.base_price_rules WHERE tariff_id BETWEEN 9001 AND 9006;
DELETE FROM public.tariff WHERE id BETWEEN 9001 AND 9006;
