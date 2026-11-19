CREATE ROLE go_pricing_seeder LOGIN NOINHERIT PASSWORD '%s';
GRANT USAGE ON SCHEMA pricing_admin, public TO go_pricing_seeder;
GRANT SELECT ON pricing_admin.target_identity, public.pricing_schema_version
	TO go_pricing_seeder;
GRANT SELECT, INSERT, MAINTAIN ON public.tariff, public.base_price_rules,
	public.discount_markup_rule TO go_pricing_seeder;
GRANT UPDATE (position) ON public.base_price_rules, public.discount_markup_rule
	TO go_pricing_seeder;
GRANT USAGE ON SEQUENCE public.hibernate_sequence TO go_pricing_seeder;
TRUNCATE public.tariff, public.base_price_rules, public.discount_markup_rule;
