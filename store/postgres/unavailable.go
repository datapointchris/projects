package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/store/postgres/generated"
)

// The codes a server answers with while it stops or starts: a session it cut off
// for a shutdown, one it cut off because another backend crashed, and a connection
// it refused while shutting down, starting up or recovering.
const (
	adminShutdown    = "57P01"
	crashShutdown    = "57P02"
	cannotConnectNow = "57P03"
)

// unavailableError wraps an error in model.ErrUnavailable where the server stopped
// serving the request, or the driver had already closed the connection it ran on.
// It returns any other error unchanged.
func unavailableError(err error) error {
	if err == nil {
		return nil
	}
	var driverErr *pgconn.PgError
	if errors.As(err, &driverErr) {
		switch driverErr.Code {
		case adminShutdown, crashShutdown, cannotConnectNow:
			return fmt.Errorf("%w: %w", model.ErrUnavailable, err)
		}
	}
	if errors.Is(err, pgconn.ErrConnClosed) {
		return fmt.Errorf("%w: %w", model.ErrUnavailable, err)
	}
	return err
}

// unavailableNamingDB is a pool or a transaction whose every query answers
// unavailableError. A read outside a transaction runs on the pool and returns the
// driver's error straight up, so the queries are the one place every error passes.
type unavailableNamingDB struct {
	db generated.DBTX
}

func (d unavailableNamingDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tag, err := d.db.Exec(ctx, sql, args...)
	return tag, unavailableError(err)
}

func (d unavailableNamingDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	rows, err := d.db.Query(ctx, sql, args...)
	if err != nil {
		return rows, unavailableError(err)
	}
	return unavailableNamingRows{rows}, nil
}

func (d unavailableNamingDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return unavailableNamingRow{d.db.QueryRow(ctx, sql, args...)}
}

type unavailableNamingRows struct {
	pgx.Rows
}

func (r unavailableNamingRows) Scan(dest ...any) error { return unavailableError(r.Rows.Scan(dest...)) }

func (r unavailableNamingRows) Err() error { return unavailableError(r.Rows.Err()) }

type unavailableNamingRow struct {
	row pgx.Row
}

func (r unavailableNamingRow) Scan(dest ...any) error { return unavailableError(r.row.Scan(dest...)) }
