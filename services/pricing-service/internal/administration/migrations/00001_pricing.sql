DO $$
BEGIN
    IF to_regclass('public.tariff') IS NULL
       AND to_regclass('public.base_price_rules') IS NULL
       AND to_regclass('public.discount_markup_rule') IS NULL
       AND to_regclass('public.hibernate_sequence') IS NULL THEN
        CREATE SEQUENCE public.hibernate_sequence AS bigint;
        CREATE TABLE public.tariff (
            id bigint PRIMARY KEY,
            code varchar(255)
        );
        CREATE TABLE public.base_price_rules (
            tariff_id bigint NOT NULL REFERENCES public.tariff (id),
            cover_code varchar(255),
            apply_if_formula varchar(255),
            price_formula varchar(255)
        );
        CREATE TABLE public.discount_markup_rule (
            id bigint PRIMARY KEY,
            type varchar(31) NOT NULL,
            tariff_id bigint REFERENCES public.tariff (id),
            apply_if_formula varchar(255),
            param_value numeric(19, 2)
        );
    END IF;

    -- Adopt the complete Hibernate layout or fail without modifying it.
    IF (SELECT count(*) FROM pg_class
        WHERE oid IN (to_regclass('public.tariff'), to_regclass('public.base_price_rules'),
                      to_regclass('public.discount_markup_rule'))
          AND relkind = 'r' AND relowner = 'pricing'::regrole AND NOT relrowsecurity) <> 3
       OR NOT EXISTS (SELECT FROM pg_sequence
           WHERE seqrelid = to_regclass('public.hibernate_sequence') AND seqtypid = 'bigint'::regtype)
       OR EXISTS (
           SELECT FROM (VALUES
               ('tariff', 'id', 'bigint'), ('tariff', 'code', 'varchar'),
               ('base_price_rules', 'tariff_id', 'bigint'),
               ('base_price_rules', 'cover_code', 'varchar'),
               ('base_price_rules', 'apply_if_formula', 'varchar'),
               ('base_price_rules', 'price_formula', 'varchar'),
               ('discount_markup_rule', 'id', 'bigint'), ('discount_markup_rule', 'type', 'varchar'),
               ('discount_markup_rule', 'tariff_id', 'bigint'),
               ('discount_markup_rule', 'apply_if_formula', 'varchar'),
               ('discount_markup_rule', 'param_value', 'numeric')
           ) AS expected(table_name, column_name, column_type)
           WHERE NOT EXISTS (
               SELECT FROM pg_attribute
               WHERE attrelid = to_regclass('public.' || expected.table_name)
                 AND attname = expected.column_name AND NOT attisdropped
                 AND atttypid = to_regtype(expected.column_type)
           )
       ) THEN
        RAISE EXCEPTION 'incompatible Pricing schema' USING ERRCODE = '55000';
    END IF;
END;
$$;

-- Java inserts omit position. Seed adoption assigns positions only after matching each full tariff.
ALTER TABLE public.base_price_rules
    ADD COLUMN position integer CHECK (position > 0),
    ADD UNIQUE (tariff_id, position);
ALTER TABLE public.discount_markup_rule
    ADD COLUMN position integer CHECK (position > 0),
    ADD UNIQUE (tariff_id, position);

CREATE TABLE public.pricing_schema_version (
    version_id bigint PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
