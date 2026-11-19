WITH definition AS (
    SELECT $1::jsonb AS data
), tariff AS (
    INSERT INTO public.tariff (id, code)
    SELECT nextval('public.hibernate_sequence'), data->>'code' FROM definition
    RETURNING id
), base_rules AS (
    INSERT INTO public.base_price_rules
        (tariff_id, cover_code, apply_if_formula, price_formula, position)
    SELECT tariff.id, rule->>'cover', rule->>'condition', rule->>'formula', position
    FROM tariff CROSS JOIN definition
    CROSS JOIN LATERAL jsonb_array_elements(data->'base') WITH ORDINALITY AS rules(rule, position)
)
INSERT INTO public.discount_markup_rule
    (id, tariff_id, type, apply_if_formula, param_value, position)
SELECT nextval('public.hibernate_sequence'), tariff.id, rule->>'kind', rule->>'condition',
    (rule->>'factor')::numeric, position
FROM tariff CROSS JOIN definition
CROSS JOIN LATERAL jsonb_array_elements(data->'markup') WITH ORDINALITY AS rules(rule, position)
ORDER BY position;
