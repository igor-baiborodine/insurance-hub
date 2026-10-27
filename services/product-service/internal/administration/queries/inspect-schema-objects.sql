-- Reports whether the managed Product and Goose history tables exist.
SELECT
    to_regclass('public.product') IS NOT NULL,
    to_regclass('public.goose_db_version') IS NOT NULL
