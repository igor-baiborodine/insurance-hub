-- Report whether tariff objects and the Pricing migration history exist.
SELECT
    to_regclass('public.tariff') IS NOT NULL
        OR to_regclass('public.base_price_rules') IS NOT NULL
        OR to_regclass('public.discount_markup_rule') IS NOT NULL
        OR to_regclass('public.hibernate_sequence') IS NOT NULL,
    to_regclass('public.pricing_schema_version') IS NOT NULL;
