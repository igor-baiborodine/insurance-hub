-- Identity generation for every ID column and all sequences.
SELECT format('%s;sequences=%s',
    (SELECT string_agg(table_name || '.' || column_name || '=' || identity_generation, ','
        ORDER BY table_name)
     FROM information_schema.columns
     WHERE table_schema = 'public' AND column_name = 'id' AND is_identity = 'YES'),
    (SELECT string_agg(relname, ',' ORDER BY relname) FROM pg_class
     WHERE relkind = 'S' AND relnamespace = 'public'::regnamespace))
