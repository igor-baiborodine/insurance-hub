-- Replays converge on the same writer grants: account and entry reads and inserts for the account
-- API and registrations, and receipt updates for statement posting. Identity inserts need no
-- sequence privilege, and the writer role receives no DDL, DELETE or history access.
GRANT SELECT, INSERT ON public.policy_account, public.accounting_entry TO go_payment_writer;
GRANT SELECT, INSERT ON public.statement_receipt TO go_payment_writer;
REVOKE UPDATE ON public.statement_receipt FROM go_payment_writer;
REVOKE UPDATE (id, source_name, sha256, state, created_at, updated_at)
    ON public.statement_receipt FROM go_payment_writer;
GRANT UPDATE (state, updated_at) ON public.statement_receipt TO go_payment_writer
