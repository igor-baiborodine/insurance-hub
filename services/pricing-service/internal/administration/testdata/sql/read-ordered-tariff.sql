SELECT jsonb_build_object(
    'code', t.code,
    'base', (
        SELECT jsonb_agg(jsonb_build_object(
            'cover', cover_code, 'condition', apply_if_formula, 'formula', price_formula
        ) ORDER BY position)
        FROM public.base_price_rules
        WHERE tariff_id = t.id AND position IS NOT NULL
    ),
    'markup', (
        SELECT jsonb_agg(jsonb_build_object(
            'kind', type, 'condition', apply_if_formula, 'factor', param_value::text
        ) ORDER BY position)
        FROM public.discount_markup_rule
        WHERE tariff_id = t.id AND position IS NOT NULL
    )
)
FROM public.tariff t
WHERE code = $1;
