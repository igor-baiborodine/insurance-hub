-- Replays converge on the same writer grants: account and entry reads and inserts for the account
-- API and registrations, and receipt updates for statement posting. Identity inserts need no
-- sequence privilege, and the writer role receives no DDL, DELETE or history access.
GRANT SELECT, INSERT ON public.policy_account, public.accounting_entry TO go_payment_writer;
GRANT SELECT, INSERT, UPDATE ON public.statement_receipt TO go_payment_writer
