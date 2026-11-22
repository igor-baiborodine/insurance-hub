INSERT INTO public.tariff (id, code) OVERRIDING SYSTEM VALUE VALUES
    (9001, 'ORDERED'),
    (9002, 'AMBIGUOUS'),
    (9003, 'AMBIGUOUS'),
    (9004, 'INCOMPLETE'),
    (9005, 'UNUSABLE'),
    (9006, 'PAIR');

-- Physical insertion order deliberately differs from persisted rule order.
INSERT INTO public.base_price_rules
    (tariff_id, cover_code, apply_if_formula, price_formula, position) VALUES
    (9001, 'C2', 'NUM_OF_CLAIM > 1', '20.00', 2),
    (9001, 'C1', NULL, '10.00', 1),
    (9002, 'C1', NULL, '10.00', 1),
    (9003, 'C1', NULL, '20.00', 1),
    (9005, 'C1', NULL, '10.00', 2),
    (9006, 'C1', NULL, '10.00', 1);

INSERT INTO public.discount_markup_rule
    (id, type, tariff_id, apply_if_formula, param_value, position) OVERRIDING SYSTEM VALUE VALUES
    (9902, 'perc_markup', 9001, 'NUM_OF_CLAIM > 2', 1.25, 2),
    (9901, 'perc_markup', 9001, NULL, 1.50, 1),
    (9903, 'perc_markup', 9002, NULL, 1.00, 1),
    (9904, 'perc_markup', 9003, NULL, 1.00, 1),
    (9905, 'perc_markup', 9005, NULL, 1.00, 1),
    (9906, 'perc_markup', 9006, NULL, 2.00, 1);
