UPDATE public.discount_markup_rule SET position = $1
			WHERE id = $2::text::bigint
