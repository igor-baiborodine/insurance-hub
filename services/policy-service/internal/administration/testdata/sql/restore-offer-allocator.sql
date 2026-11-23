SELECT setval(pg_get_serial_sequence('public.offer', 'id'), (SELECT max(id) FROM public.offer))
