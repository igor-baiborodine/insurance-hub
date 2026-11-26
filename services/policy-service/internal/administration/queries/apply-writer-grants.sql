-- Replays converge on the same writer grants. Identity inserts need no sequence privilege, and
-- the writer role receives no DDL, DELETE or history access.
GRANT SELECT, INSERT, UPDATE ON public.offer, public.offer_answers, public.offer_cover,
    public.policy, public.policy_version, public.cover, public.policy_outbox
    TO go_policy_writer
