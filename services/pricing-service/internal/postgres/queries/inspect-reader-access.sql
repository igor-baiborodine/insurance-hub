SELECT current_database() = 'pricing'
    AND current_user = 'go_pricing_reader' AND session_user = 'go_pricing_reader'
    AND (SELECT count(*) FROM (
        SELECT 1 FROM public.tariff LIMIT 1
    ) AS tariff) >= 0
    AND (SELECT count(*) FROM (
        SELECT 1 FROM public.base_price_rules LIMIT 1
    ) AS base_rules) >= 0
    AND (SELECT count(*) FROM (
        SELECT 1 FROM public.discount_markup_rule LIMIT 1
    ) AS markup_rules) >= 0;
