-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
    IF to_regclass('public.product') IS NULL THEN
        CREATE TABLE public.product (
            code varchar(255) PRIMARY KEY,
            definition jsonb NOT NULL
        );
    END IF;
END $$;
-- +goose StatementEnd
GRANT SELECT ON public.product TO go_product_reader;
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'go_product_seeder')
       AND (SELECT seed_allowed FROM product_admin.target_identity WHERE singleton) THEN
        GRANT SELECT, INSERT ON public.product TO go_product_seeder;
        GRANT SELECT ON public.goose_db_version TO go_product_seeder;
    END IF;
END $$;
-- +goose StatementEnd
