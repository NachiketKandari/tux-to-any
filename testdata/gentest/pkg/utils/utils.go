// Package utils is the demo fixture's stand-in for the converted host's
// shared helpers.
//
// It exists so the generated db suites can be executed rather than only
// byte-compared. NewSqlxMockDB is the load-bearing one: it is what
// SetupSuite calls to build the sqlmock-backed *sqlx.DB that every generated
// db test asserts against, so a suite that never got a live handle could not
// run at all.
package utils

import (
	"encoding/json"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

// NewSqlxMockDB returns a sqlmock-backed *sqlx.DB and the mock that drives it.
func NewSqlxMockDB() (*sqlx.DB, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	if err != nil {
		panic(err)
	}
	return sqlx.NewDb(db, "sqlmock"), mock
}

// TypeConverter is the host's generic JSON round-trip helper, used by the
// generated handler suites to unmarshal a response payload into its typed
// shape. The fixture implements it directly so the handler goldens stay
// executable without the host's reflection machinery.
func TypeConverter[T any](v any) (*T, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
