SELECT cover_code, apply_if_formula,
		price_formula, position FROM public.base_price_rules
		WHERE tariff_id = $1 ORDER BY position NULLS LAST
