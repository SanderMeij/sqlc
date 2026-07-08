CREATE TABLE books (
          book_id SERIAL PRIMARY KEY,
          isbn text NOT NULL DEFAULT '' UNIQUE,
          book_type book_type NOT NULL DEFAULT 'FICTION',
          title text NOT NULL DEFAULT '',
          year integer NOT NULL DEFAULT 2000,
          available timestamp with time zone NOT NULL DEFAULT 'NOW()',
          tags varchar[] NOT NULL DEFAULT '{}'
);

CREATE TABLE authors (
          id SERIAL PRIMARY KEY,
          first_name text NOT NULL DEFAULT '',
          last_name text NOT NULL DEFAULT '',
          birth_date date NOT NULL DEFAULT '1970-01-01'
);

CREATE TABLE book_authors (
          book_id integer NOT NULL REFERENCES books(book_id) ON DELETE CASCADE,
          author_id integer NOT NULL REFERENCES authors(id) ON DELETE CASCADE,
          PRIMARY KEY (book_id, author_id)
);
