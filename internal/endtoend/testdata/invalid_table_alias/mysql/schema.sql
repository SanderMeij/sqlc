-- https://github.com/SanderMeij/sqlc/issues/437
CREATE TABLE authors (
  id   INT PRIMARY KEY,
  name VARCHAR(255) NOT NULL,
  bio  text
);

