SELECT count(*) = 2
FROM pg_attribute
WHERE attrelid IN (
        to_regclass('public.base_price_rules'),
        to_regclass('public.discount_markup_rule')
    )
    AND attname = 'position' AND atttypid = 'integer'::regtype AND NOT attisdropped
    AND to_regclass('public.tariff') IS NOT NULL
    AND to_regclass('public.hibernate_sequence') IS NOT NULL;
