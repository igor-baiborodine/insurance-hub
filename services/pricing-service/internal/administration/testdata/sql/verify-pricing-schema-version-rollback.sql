SELECT (to_regclass('public.pricing_schema_version') IS NULL
		AND NOT EXISTS (SELECT FROM pg_attribute WHERE attrelid = 'public.base_price_rules'::regclass
			AND attname = 'position' AND NOT attisdropped))::text
