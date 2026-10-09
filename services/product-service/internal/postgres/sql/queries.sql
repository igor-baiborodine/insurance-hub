-- name: ListProducts :many
SELECT code, definition
FROM public.product;

-- name: GetProduct :one
SELECT code, definition
FROM public.product
WHERE code = $1;
