ALTER TABLE public.accounting_entry DROP CONSTRAINT accounting_entry_policy_account_id_fkey;
ALTER TABLE public.accounting_entry ADD CONSTRAINT accounting_entry_policy_account_id_fkey
    FOREIGN KEY (policy_account_id) REFERENCES public.policy_account (id);
