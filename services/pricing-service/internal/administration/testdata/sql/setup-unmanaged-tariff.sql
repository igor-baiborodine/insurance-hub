-- A tariff relation left without Pricing migration history, such as after a partial reset.
SET ROLE pricing;
CREATE TABLE public.tariff (
    id bigint PRIMARY KEY,
    code varchar(255)
);
RESET ROLE;
