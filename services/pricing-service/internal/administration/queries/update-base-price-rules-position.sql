UPDATE public.base_price_rules SET position = $1
			WHERE ctid = $2::text::tid
