SELECT count(*), coalesce(min(id), 0)
		FROM public.tariff WHERE code = $1
