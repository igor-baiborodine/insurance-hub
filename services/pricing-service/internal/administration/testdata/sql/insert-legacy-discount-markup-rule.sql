INSERT INTO public.discount_markup_rule
				(id, type, tariff_id, apply_if_formula, param_value)
				VALUES ($1,$2,$3,$4,$5::text::numeric)
