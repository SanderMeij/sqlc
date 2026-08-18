-- Example queries for sqlc
CREATE TABLE authors (
  id   BIGINT PRIMARY KEY,
  name TEXT      NOT NULL,
  bio  text
);

-- https://github.com/SanderMeij/sqlc/issues/1290
CREATE TABLE users (
  sub TEXT PRIMARY KEY
);
