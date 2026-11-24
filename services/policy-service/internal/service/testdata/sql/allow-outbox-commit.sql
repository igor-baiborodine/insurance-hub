-- Removes the commit rejection installed by reject-outbox-commit.sql.
DROP TRIGGER reject_outbox_commit ON public.policy_outbox;
DROP FUNCTION public.reject_outbox_commit();
