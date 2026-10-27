-- Decision prototype only: the validating session locker must accept the schema/history first.
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
