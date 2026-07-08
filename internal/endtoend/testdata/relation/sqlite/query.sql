-- name: GetBookAuthors :many
SELECT authors.* FROM book_authors 
INNER JOIN authors ON book_authors.author_id = authors.id
WHERE book_authors.book_id = ?;

-- name: GetBook :one
SELECT books.*, sqlc.relation('GetBookAuthors') from books WHERE book_id = ?;
