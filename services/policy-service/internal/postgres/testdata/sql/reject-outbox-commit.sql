-- Fails every transaction that inserts an outbox intent at commit time, after all writes ran.
CREATE FUNCTION public.reject_outbox_commit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'commit rejected by the lifecycle smoke';
END $$;
CREATE CONSTRAINT TRIGGER reject_outbox_commit AFTER INSERT ON public.policy_outbox
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.reject_outbox_commit();
