SELECT jsonb_build_array(
		(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM public.tariff t),
		(SELECT jsonb_agg(to_jsonb(b) - 'position' ORDER BY tariff_id, cover_code,
			apply_if_formula, price_formula) FROM public.base_price_rules b),
		(SELECT jsonb_agg(to_jsonb(m) - 'position' ORDER BY id) FROM public.discount_markup_rule m)
	)::text
