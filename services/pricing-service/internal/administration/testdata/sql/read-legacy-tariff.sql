SELECT concat_ws('|', t.id, t.code, b.cover_code,
		b.price_formula, b.apply_if_formula IS NULL, m.id, m.type, m.apply_if_formula, m.param_value)
		FROM public.tariff t JOIN public.base_price_rules b ON b.tariff_id = t.id
		JOIN public.discount_markup_rule m ON m.tariff_id = t.id
		WHERE b.position IS NULL AND m.position IS NULL
