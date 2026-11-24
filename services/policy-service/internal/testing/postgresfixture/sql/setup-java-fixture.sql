-- Independently constructed from the C02 storage matrix: Hibernate DDL with one shared sequence,
-- a still-valid offer, a Java-issued version 1 and the SQL-derived version 2.
SET ROLE policy;
create sequence hibernate_sequence start 1 increment 1;
create table cover (id int8 not null, code varchar(255), price numeric(19, 2),
    policy_version_id int8, primary key (id));
create table offer (id int8 not null, creation_date date, number varchar(255), policy_from date,
    policy_to date, product_code varchar(255), status varchar(255), total_price numeric(19, 2),
    primary key (id));
create table offer_answers (offer_id int8 not null, answer varchar(255),
    question_code varchar(255) not null, primary key (offer_id, question_code));
create table offer_cover (offer_id int8 not null, price numeric(19, 2),
    cover_code varchar(255) not null, primary key (offer_id, cover_code));
create table policy (id int8 not null, agent_login varchar(255), number varchar(255),
    primary key (id));
create table policy_version (id int8 not null, account_number varchar(255), cover_from date,
    cover_to date, first_name varchar(255), last_name varchar(255), pesel varchar(255),
    product_code varchar(255), total_premium_amount numeric(19, 2), version_number int8,
    version_from date, version_to date, policy_id int8, primary key (id));
alter table cover add constraint FKl3ynyrtbuyfqxg7krx3esdq5f
    foreign key (policy_version_id) references policy_version;
alter table offer_answers add constraint FKdx8rk1kn3fmdtk40gb5u5tfjy
    foreign key (offer_id) references offer;
alter table offer_cover add constraint FKsxw8p43ap5f0rbvhkn4vt0hry
    foreign key (offer_id) references offer;
alter table policy_version add constraint FKncrj5ri7llbfpwhdw0hrfn0om
    foreign key (policy_id) references policy;

insert into offer values (1, DATE '2026-11-13', '7e0a1c84-6f43-4c1e-9b7a-2f5d0c3e8a11',
    DATE '2026-11-13', DATE '2027-11-13', 'HSI', 'NEW', 123.45);
insert into offer_answers values (1, 'APARTMENT', 'TYPE'), (1, '0', 'NUM_OF_CLAIM');
insert into offer_cover values (1, 100.00, 'C1'), (1, 23.45, 'C2');
insert into policy values (2, 'synthetic.agent', 'a9c43c58-0f9e-4f68-8c1d-5b6c1e2f7d22');
insert into policy_version values
    (3, '31f5d9a2-8b7c-4e61-a0d4-6c2b9e7f1a33', DATE '2026-11-13', DATE '2027-11-13',
     'Synthetic', 'Holder', '00000000000', 'HSI', 123.45, 1, DATE '2026-11-13', DATE '2027-11-13', 2),
    (6, '31f5d9a2-8b7c-4e61-a0d4-6c2b9e7f1a33', DATE '2026-11-13', DATE '2026-11-23',
     'Synthetic', 'Holder', '00000000000', 'HSI', 3.71, 2, DATE '2026-11-24', DATE '2027-11-13', 2);
insert into cover values (4, 'C1', 100.00, 3), (5, 'C2', 23.45, 3), (7, 'C1', 3.01, 6),
    (8, 'C2', 0.70, 6);
select setval('hibernate_sequence', 8);
RESET ROLE
