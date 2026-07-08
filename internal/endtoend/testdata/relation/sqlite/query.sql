-- name: GetBookAuthors :many
SELECT authors.* FROM book_authors 
INNER JOIN authors ON book_authors.author_id = authors.id
WHERE book_authors.book_id = ?;

-- name: GetBook :one
SELECT books.*, sqlc.relation('GetBookAuthors') from books WHERE book_id = ?;

-- name: GetBooksAuthors :many
SELECT book_authors.book_id, authors.first_name FROM book_authors 
INNER JOIN authors ON book_authors.author_id = authors.id
WHERE book_authors.book_id IN (sqlc.slice(book_ids));

-- name: GetBooks :many
SELECT books.*, sqlc.relation('GetBooksAuthors') from books LIMIT 10;
