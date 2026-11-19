SET ROLE pricing;
DROP TABLE public.pricing_schema_version;
ALTER TABLE public.base_price_rules DROP COLUMN position;
ALTER TABLE public.discount_markup_rule DROP COLUMN position;
ALTER TABLE public.discount_markup_rule ADD COLUMN position text;
INSERT INTO public.tariff (id, code) VALUES (11, 'HSI');
INSERT INTO public.base_price_rules (tariff_id, cover_code, price_formula) VALUES (11, 'C2', '0.25');
INSERT INTO public.discount_markup_rule (id, type, tariff_id, apply_if_formula, param_value)
VALUES (32, 'perc_markup', 11, 'FLOOD == "YES"', 1.50);
RESET ROLE;
