-- Exact decimals cross the boundary as PostgreSQL numeric text, never as floating point.

-- name: GetOffer :one
SELECT id, number, product_code, status, creation_date, policy_from, policy_to,
    total_price
FROM public.offer
WHERE number = $1;

-- name: ListOfferAnswers :many
SELECT question_code, answer
FROM public.offer_answers
WHERE offer_id = $1
ORDER BY question_code;

-- name: ListOfferCovers :many
SELECT cover_code, price
FROM public.offer_cover
WHERE offer_id = $1
ORDER BY cover_code;

-- name: GetPolicy :one
SELECT id, number, agent_login
FROM public.policy
WHERE number = $1;

-- name: ListPolicyVersions :many
SELECT id, version_number, product_code, first_name, last_name, pesel, account_number,
    cover_from, cover_to, version_from, version_to,
    total_premium_amount
FROM public.policy_version
WHERE policy_id = $1
ORDER BY version_number, id;

-- name: ListPolicyCovers :many
SELECT c.policy_version_id, c.code, c.price
FROM public.cover AS c
JOIN public.policy_version AS v ON v.id = c.policy_version_id
WHERE v.policy_id = $1
ORDER BY c.policy_version_id, c.id;

-- name: InsertPolicy :one
INSERT INTO public.policy (number, agent_login)
VALUES (sqlc.arg(number), sqlc.narg(agent_login))
RETURNING id;

-- name: InsertPolicyVersion :one
INSERT INTO public.policy_version (
    policy_id, version_number, product_code, first_name, last_name, pesel, account_number,
    cover_from, cover_to, version_from, version_to, total_premium_amount
) VALUES (
    sqlc.arg(policy_id), sqlc.arg(version_number), sqlc.arg(product_code),
    sqlc.arg(first_name), sqlc.arg(last_name), sqlc.arg(pesel), sqlc.arg(account_number),
    sqlc.arg(cover_from), sqlc.arg(cover_to), sqlc.arg(version_from), sqlc.arg(version_to),
    sqlc.narg(total_premium_amount)::text::numeric
)
RETURNING id;

-- name: InsertCover :exec
INSERT INTO public.cover (policy_version_id, code, price)
VALUES (sqlc.arg(policy_version_id), sqlc.arg(code), sqlc.narg(price)::text::numeric);

-- name: InsertOutboxIntent :one
INSERT INTO public.policy_outbox (
    intent_id, policy_id, topic, message_key, payload, trace_parent, trace_state
)
VALUES (sqlc.arg(intent_id), sqlc.arg(policy_id), sqlc.arg(topic), sqlc.arg(message_key),
    sqlc.arg(payload), sqlc.narg(trace_parent), sqlc.narg(trace_state))
RETURNING id;

-- name: LockNextOutboxIntent :one
SELECT o.id, o.intent_id, o.topic, o.message_key, o.payload, o.trace_parent, o.trace_state
FROM public.policy_outbox AS o
WHERE o.published_at IS NULL
  AND NOT EXISTS (
      SELECT 1
      FROM public.policy_outbox AS earlier
      WHERE earlier.policy_id = o.policy_id
        AND earlier.id < o.id
        AND earlier.published_at IS NULL
  )
ORDER BY o.id
LIMIT 1
FOR UPDATE OF o SKIP LOCKED;

-- name: MarkOutboxIntentPublished :execrows
UPDATE public.policy_outbox
SET published_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id) AND published_at IS NULL;

-- name: InsertOffer :one
INSERT INTO public.offer (
    number, product_code, status, creation_date, policy_from, policy_to, total_price
) VALUES (
    sqlc.arg(number), sqlc.arg(product_code), sqlc.arg(status), sqlc.arg(creation_date),
    sqlc.arg(policy_from), sqlc.arg(policy_to), sqlc.arg(total_price)::text::numeric
)
RETURNING id;

-- name: InsertOfferAnswer :exec
INSERT INTO public.offer_answers (offer_id, question_code, answer)
VALUES (sqlc.arg(offer_id), sqlc.arg(question_code), sqlc.arg(answer));

-- name: InsertOfferCover :exec
INSERT INTO public.offer_cover (offer_id, cover_code, price)
VALUES (sqlc.arg(offer_id), sqlc.arg(cover_code), sqlc.arg(price)::text::numeric);
