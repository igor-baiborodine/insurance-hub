-- +goose Up
GRANT INSERT ON public.policy_document TO go_document_writer;
GRANT USAGE ON SEQUENCE public.hibernate_sequence TO go_document_writer;
