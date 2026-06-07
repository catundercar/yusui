-- name: HealthCheck :one
-- Liveness of the DB round-trip; also proves the sqlc+pgx pipeline.
SELECT 1 AS ok;
