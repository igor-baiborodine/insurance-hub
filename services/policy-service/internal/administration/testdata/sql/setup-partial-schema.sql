-- A business table left without the rest of the Policy schema, such as after a partial reset.
SET ROLE policy;
CREATE TABLE public.offer (id bigint NOT NULL PRIMARY KEY);
RESET ROLE
