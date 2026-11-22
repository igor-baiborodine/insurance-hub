-- Deployed version 1 tariff relations at c110c417b9362053ab1c26a44f52d2c4c485b152.
-- Preserve the accepted corpus and its foreign keys across the disposable fixture switch.
SET ROLE pricing;
CREATE TEMP TABLE saved_tariffs AS TABLE public.tariff;
CREATE TEMP TABLE saved_base AS TABLE public.base_price_rules;
CREATE TEMP TABLE saved_markup AS TABLE public.discount_markup_rule;
DROP TABLE public.base_price_rules, public.discount_markup_rule, public.tariff;

CREATE TABLE public.tariff (
    id bigint PRIMARY KEY,
    code varchar(255)
);
CREATE TABLE public.base_price_rules (
    tariff_id bigint NOT NULL REFERENCES public.tariff (id),
    cover_code varchar(255),
    apply_if_formula varchar(255),
    price_formula varchar(255)
);
CREATE TABLE public.discount_markup_rule (
    id bigint PRIMARY KEY,
    type varchar(31) NOT NULL,
    tariff_id bigint REFERENCES public.tariff (id),
    apply_if_formula varchar(255),
    param_value numeric(19, 2)
);
ALTER TABLE public.base_price_rules
    ADD COLUMN position integer CHECK (position > 0),
    ADD UNIQUE (tariff_id, position);
ALTER TABLE public.discount_markup_rule
    ADD COLUMN position integer CHECK (position > 0),
    ADD UNIQUE (tariff_id, position);

INSERT INTO public.tariff SELECT * FROM saved_tariffs;
INSERT INTO public.base_price_rules SELECT * FROM saved_base;
INSERT INTO public.discount_markup_rule SELECT * FROM saved_markup;
GRANT SELECT ON public.tariff, public.base_price_rules, public.discount_markup_rule TO go_pricing_reader;
GRANT SELECT, INSERT, MAINTAIN ON public.tariff, public.base_price_rules, public.discount_markup_rule TO go_pricing_seeder;
GRANT UPDATE (position) ON public.base_price_rules, public.discount_markup_rule TO go_pricing_seeder;
RESET ROLE;
