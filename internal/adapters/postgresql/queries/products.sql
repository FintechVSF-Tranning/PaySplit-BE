-- name: ListProducts :many
SELECT *
FROM products;

-- name: FindProductByID :one
SELECT *
FROM products
WHERE id = $1;

-- name: FindProductsByQuery :many
SELECT *
FROM products
WHERE (sqlc.arg(name)::text = '' OR name ILIKE '%' || sqlc.arg(name) || '%')
  AND (
    sqlc.arg(price_in_centers)::int = 0
    OR price_in_centers = sqlc.arg(price_in_centers)
  );
