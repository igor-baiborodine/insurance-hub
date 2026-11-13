-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
    IF to_regclass('public.policy_document') IS NULL THEN
        CREATE TABLE public.policy_document (
            id bigint NOT NULL PRIMARY KEY,
            bytes bytea,
            policy_number varchar(255)
        );
        CREATE SEQUENCE public.hibernate_sequence AS bigint
            START WITH 1 INCREMENT BY 1 MINVALUE 1 MAXVALUE 9223372036854775807
            CACHE 1 NO CYCLE;
    END IF;
END $$;
-- +goose StatementEnd
GRANT SELECT ON public.policy_document TO go_document_reader;
