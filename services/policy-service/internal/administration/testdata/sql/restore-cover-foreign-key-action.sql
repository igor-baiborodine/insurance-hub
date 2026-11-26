ALTER TABLE public.cover DROP CONSTRAINT cover_policy_version_id_fkey;
ALTER TABLE public.cover ADD CONSTRAINT cover_policy_version_id_fkey
    FOREIGN KEY (policy_version_id) REFERENCES public.policy_version (id);
