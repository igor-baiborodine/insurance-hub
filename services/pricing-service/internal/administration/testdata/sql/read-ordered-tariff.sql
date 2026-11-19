SELECT
			(SELECT jsonb_agg(jsonb_build_object('key', cover_code, 'condition', apply_if_formula,
				'value', price_formula) ORDER BY position) FROM public.base_price_rules
				WHERE tariff_id = t.id AND position IS NOT NULL)::text,
			(SELECT jsonb_agg(jsonb_build_object('key', type, 'condition', apply_if_formula,
				'value', param_value::text) ORDER BY position) FROM public.discount_markup_rule
				WHERE tariff_id = t.id AND position IS NOT NULL)::text
			FROM public.tariff t WHERE code = $1
