-- The platform provisions roles before migration. A disposable schema may have neither role.
DO $grants$
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'go_pricing_reader') THEN
        GRANT SELECT ON public.tariff, public.base_price_rules, public.discount_markup_rule
            TO go_pricing_reader;
    END IF;
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'go_pricing_seeder')
       AND (SELECT seed_allowed FROM pricing_admin.target_identity WHERE singleton) THEN
        GRANT SELECT, INSERT, MAINTAIN
            ON public.tariff, public.base_price_rules, public.discount_markup_rule
            TO go_pricing_seeder;
        GRANT UPDATE (position) ON public.base_price_rules, public.discount_markup_rule
            TO go_pricing_seeder;
        GRANT SELECT ON public.pricing_schema_version TO go_pricing_seeder;
        GRANT USAGE ON SEQUENCE public.hibernate_sequence TO go_pricing_seeder;
    END IF;
END;
$grants$;
