ALTER TABLE public.cover DROP CONSTRAINT FKl3ynyrtbuyfqxg7krx3esdq5f;
ALTER TABLE public.cover ADD CONSTRAINT FKl3ynyrtbuyfqxg7krx3esdq5f
    FOREIGN KEY (policy_version_id) REFERENCES public.policy_version (id) ON DELETE CASCADE;
