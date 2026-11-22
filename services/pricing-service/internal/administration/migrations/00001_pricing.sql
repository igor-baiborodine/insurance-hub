CREATE TABLE public.tariff (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code varchar(255)
);
CREATE TABLE public.base_price_rules (
    tariff_id bigint NOT NULL REFERENCES public.tariff (id),
    cover_code varchar(255),
    apply_if_formula varchar(255),
    price_formula varchar(255),
    position integer NOT NULL CHECK (position > 0),
    UNIQUE (tariff_id, position)
);
CREATE TABLE public.discount_markup_rule (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    type varchar(31) NOT NULL,
    tariff_id bigint REFERENCES public.tariff (id),
    apply_if_formula varchar(255),
    param_value numeric(19, 2),
    position integer NOT NULL CHECK (position > 0),
    UNIQUE (tariff_id, position)
);

CREATE TABLE public.pricing_schema_version (
    version_id bigint PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
