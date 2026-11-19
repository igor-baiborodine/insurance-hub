SELECT coalesce(string_agg(c.relname || ':' || c.relkind::text,
			',' ORDER BY c.relname), '') FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public'
