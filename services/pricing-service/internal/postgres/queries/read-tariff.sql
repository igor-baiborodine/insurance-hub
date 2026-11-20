SELECT t.code,
       COALESCE((
           SELECT jsonb_agg(
               jsonb_build_object(
                   'position', b.position,
                   'coverCode', b.cover_code,
                   'condition', b.apply_if_formula,
                   'formula', b.price_formula
               ) ORDER BY b.position NULLS LAST
           )
           FROM public.base_price_rules AS b
           WHERE b.tariff_id = t.id
       ), '[]'::jsonb) AS base_rules,
       COALESCE((
           SELECT jsonb_agg(
               jsonb_build_object(
                   'position', m.position,
                   'kind', m.type,
                   'condition', m.apply_if_formula,
                   'factor', m.param_value::text
               ) ORDER BY m.position NULLS LAST
           )
           FROM public.discount_markup_rule AS m
           WHERE m.tariff_id = t.id
       ), '[]'::jsonb) AS markup_rules
FROM public.tariff AS t
WHERE t.code = $1
ORDER BY t.id;
