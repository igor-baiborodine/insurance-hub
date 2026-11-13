SELECT to_regclass('public.policy_document') IS NOT NULL,
       to_regclass('public.hibernate_sequence') IS NOT NULL,
       to_regclass('public.goose_db_version') IS NOT NULL,
       NOT EXISTS (
           SELECT FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
           WHERE n.nspname='public' AND c.relname NOT IN
             ('policy_document','policy_document_pkey','hibernate_sequence',
              'goose_db_version','goose_db_version_pkey','goose_db_version_id_seq')
       ) AND NOT EXISTS (SELECT FROM pg_proc WHERE pronamespace='public'::regnamespace)
    AND NOT EXISTS (
        SELECT FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
        WHERE n.nspname='public' AND c.relowner<>'document'::regrole
    )
    AND NOT EXISTS (
        SELECT FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
        WHERE n.nspname='public' AND c.relname IN ('goose_db_version_pkey','goose_db_version_id_seq')
          AND to_regclass('public.goose_db_version') IS NULL
    )
    AND NOT EXISTS (SELECT FROM pg_type WHERE typnamespace='public'::regnamespace AND typtype IN ('d','e','r','m'))
    AND NOT EXISTS (
        SELECT FROM pg_namespace WHERE nspname NOT IN ('public','document_admin','information_schema')
          AND nspname !~ '^pg_'
    )
