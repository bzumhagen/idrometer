// Package sqlite implements the domain ports on top of SQLite using the pure
// Go modernc.org/sqlite driver (no CGO).
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/pressly/goose/v3"
	"modernc.org/sqlite"

	"github.com/bzumhagen/idrometer/internal/domain"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store implements domain.TxManager. SQLite allows one writer at a time, so
// writes use a single connection (serialised) while reads use a pool.
type Store struct {
	w, r *sql.DB
}

var _ domain.TxManager = (*Store)(nil)

// Open opens (creating if needed) the database at path and applies pending
// migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	w, err := sql.Open("sqlite", dsn(path, "_txlock=immediate"))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	if err := w.PingContext(ctx); err != nil {
		w.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := migrate(ctx, w); err != nil {
		w.Close()
		return nil, err
	}
	r, err := sql.Open("sqlite", dsn(path, "_pragma=query_only(1)"))
	if err != nil {
		w.Close()
		return nil, err
	}
	return &Store{w: w, r: r}, nil
}

func dsn(path, extra string) string {
	q := []string{
		"_pragma=busy_timeout(5000)",
		"_pragma=journal_mode(WAL)",
		"_pragma=foreign_keys(1)",
		"_pragma=synchronous(NORMAL)",
		extra,
	}
	// A file: URI needs an absolute path; "./x.db" would parse "." as the authority.
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	u := url.URL{Scheme: "file", Path: path}
	return u.String() + "?" + strings.Join(q, "&")
}

func migrate(ctx context.Context, db *sql.DB) error {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	return nil
}

func (s *Store) Close() error { return errors.Join(s.r.Close(), s.w.Close()) }

// Ping checks the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.r.PingContext(ctx) }

func (s *Store) WithinTx(ctx context.Context, fn func(domain.Repos) error) error {
	return runTx(ctx, s.w, fn)
}

func (s *Store) View(ctx context.Context, fn func(domain.Repos) error) error {
	return runTx(ctx, s.r, fn)
}

func runTx(ctx context.Context, db *sql.DB, fn func(domain.Repos) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	q := queries{tx}
	if err := fn(domain.Repos{
		Devices: devices{q}, Fermentations: fermentations{q},
		Readings: readings{q}, Settings: settings{q},
		Users: users{q}, Sessions: sessions{q},
		Recipes: recipes{q}, Manual: manualReadings{q}, Annotations: annotations{q}, AnnotationTypes: annotationTypes{q},
		Calibrations: calibrations{q},
	}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Snapshot writes a consistent copy of the database to dest (which must not
// exist) using VACUUM INTO. Safe to run while the service is live. It uses the
// writer connection (VACUUM INTO is refused on a read-only one), so writes
// wait for the few moments the copy takes.
func (s *Store) Snapshot(ctx context.Context, dest string) error {
	_, err := s.w.ExecContext(ctx, "VACUUM INTO ?", dest)
	return err
}

// queries is the subset of *sql.DB / *sql.Tx the repos use.
type queries struct {
	db interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
		QueryRowContext(context.Context, string, ...any) *sql.Row
	}
}

const (
	sqliteConstraintPrimaryKey = 1555
	sqliteConstraintUnique     = 2067
)

// mapErr translates driver errors into domain errors.
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sql.ErrNoRows):
		return domain.ErrNotFound
	}
	var se *sqlite.Error
	if errors.As(err, &se) && (se.Code() == sqliteConstraintUnique || se.Code() == sqliteConstraintPrimaryKey) {
		return fmt.Errorf("%w: %w", domain.ErrConflict, err)
	}
	return err
}
