-- Verifies the grants required by the restricted seed login.
SELECT
    has_table_privilege('go_product_seeder', 'public.product', 'SELECT')
    AND has_table_privilege('go_product_seeder', 'public.product', 'INSERT')
    AND NOT has_table_privilege(
        'go_product_seeder',
        'public.product',
        'UPDATE,DELETE,TRUNCATE'
    )
    AND has_table_privilege('go_product_seeder', 'public.goose_db_version', 'SELECT')
    AND NOT has_table_privilege(
        'go_product_seeder',
        'public.goose_db_version',
        'INSERT,UPDATE,DELETE,TRUNCATE'
    )
    AND NOT has_sequence_privilege(
        'go_product_seeder',
        'public.goose_db_version_id_seq',
        'USAGE,SELECT,UPDATE'
    )
