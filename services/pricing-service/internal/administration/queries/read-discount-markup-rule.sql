SELECT id::text, type, apply_if_formula,
		trim_scale(param_value)::text, position FROM public.discount_markup_rule
		WHERE tariff_id = $1 ORDER BY position NULLS LAST
