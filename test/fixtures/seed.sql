-- Seed data for the integration test. Two tables with a foreign key, so the
-- dump exercises constraint ordering on restore, and known row counts so the
-- manifest's table_counts can be asserted exactly.
CREATE TABLE users (
    id    serial PRIMARY KEY,
    email text NOT NULL UNIQUE
);

CREATE TABLE orders (
    id      serial PRIMARY KEY,
    user_id integer NOT NULL REFERENCES users (id),
    total   numeric(10, 2) NOT NULL
);

INSERT INTO users (email)
SELECT 'user' || g || '@example.com' FROM generate_series(1, 25) g;

INSERT INTO orders (user_id, total)
SELECT (g % 25) + 1, (g * 1.5)::numeric FROM generate_series(1, 100) g;
