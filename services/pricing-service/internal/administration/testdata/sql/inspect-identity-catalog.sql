SELECT (
    (SELECT count(*) = 2 FROM pg_attribute
     WHERE attrelid IN ('public.tariff'::regclass, 'public.discount_markup_rule'::regclass)
       AND attname = 'id' AND attidentity = 'a' AND atttypid = 'bigint'::regtype)
    AND (SELECT count(*) = 2 FROM pg_class
         WHERE relnamespace = 'public'::regnamespace AND relkind = 'S')
)::text;
