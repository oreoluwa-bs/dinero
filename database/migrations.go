package database

import (
	"database/sql"

	"github.com/pressly/goose/v3"
)

func Up(db *sql.DB, dir string) error {
	if err := goose.SetDialect("sqlite3"); err != nil {
		return err
	}
	return goose.Up(db, dir)
}
