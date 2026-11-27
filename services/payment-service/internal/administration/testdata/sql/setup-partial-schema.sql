-- A business table left without the rest of the Payment schema, such as after a partial reset.
SET ROLE payment;
CREATE TABLE public.policy_account (id bigint NOT NULL PRIMARY KEY);
RESET ROLE
