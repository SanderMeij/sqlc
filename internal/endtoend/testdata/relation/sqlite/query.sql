-- name: GetBookAuthors :many
SELECT
  authors.*
FROM
  book_authors
  INNER JOIN authors ON book_authors.author_id = authors.id
WHERE
  book_authors.book_id = ?;

-- name: GetBook :one
SELECT
  books.*,
  sqlc.relation ('GetBookAuthors')
FROM
  books
WHERE
  book_id = ?;

-- name: GetBooksAuthors :many
SELECT
  authors.id AS source_key,
  authors.first_name,
  authors.last_name,
  book_authors.book_id as target_key
FROM
  book_authors
  INNER JOIN authors ON book_authors.author_id = authors.id
WHERE
  book_authors.book_id IN (sqlc.slice (book_ids));

-- name: GetBooks :many
SELECT
  books.*,
  sqlc.relation ('GetBooksAuthors')
FROM
  books
LIMIT
  10;

