SELECT jsonb_build_array(
		(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM public.tariff t),
		(SELECT jsonb_agg(to_jsonb(b) ORDER BY tariff_id, position, cover_code,
			apply_if_formula, price_formula) FROM public.base_price_rules b),
		(SELECT jsonb_agg(to_jsonb(m) ORDER BY id) FROM public.discount_markup_rule m),
		(SELECT jsonb_agg(to_jsonb(v) ORDER BY version_id) FROM public.pricing_schema_version v),
		(SELECT jsonb_build_array(last_value, is_called) FROM public.hibernate_sequence)
	)::text
