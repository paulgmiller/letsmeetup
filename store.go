package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const guestsSchema = `CREATE TABLE IF NOT EXISTS guests (
 meetup_id TEXT NOT NULL,
 guest_id TEXT NOT NULL,
 guest_name TEXT NOT NULL,
 lat DOUBLE PRECISION NOT NULL,
 long DOUBLE PRECISION NOT NULL,
 PRIMARY KEY (meetup_id, guest_id)
)`

type postgresGuestStore struct {
	pool *pgxpool.Pool
}

func newGuestStore(ctx context.Context, databaseURL string) (*postgresGuestStore, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("DATABASE_URL not set")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("invalid DATABASE_URL")
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("failed to initialize database pool")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("failed to connect to database; check DATABASE_URL and connectivity")
	}
	if err := retrySerialization(ctx, func() error {
		_, err := pool.Exec(ctx, guestsSchema)
		return err
	}); err != nil {
		pool.Close()
		return nil, errors.New("failed to create guests table; check database schema and permissions")
	}
	return &postgresGuestStore{pool: pool}, nil
}

func (s *postgresGuestStore) Close() { s.pool.Close() }

func (s *postgresGuestStore) UpsertAndList(ctx context.Context, g Guest) ([]Guest, error) {
	err := retrySerialization(ctx, func() error {
		_, err := s.pool.Exec(ctx, `INSERT INTO guests (meetup_id, guest_id, guest_name, lat, long)
   VALUES ($1, $2, $3, $4, $5)
   ON CONFLICT (meetup_id, guest_id) DO UPDATE
   SET guest_name = excluded.guest_name, lat = excluded.lat, long = excluded.long`,
			g.MeetupId, g.GuestId, g.GuestName, g.Lat, g.Long)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("upsert guest: %w", err)
	}
	var guests []Guest
	err = retrySerialization(ctx, func() error {
		rows, err := s.pool.Query(ctx, `SELECT meetup_id, guest_id, guest_name, lat, long
   FROM guests WHERE meetup_id = $1 ORDER BY guest_id`, g.MeetupId)
		if err != nil {
			return err
		}
		defer rows.Close()
		guests = make([]Guest, 0)
		for rows.Next() {
			var guest Guest
			if err := rows.Scan(&guest.MeetupId, &guest.GuestId, &guest.GuestName, &guest.Lat, &guest.Long); err != nil {
				return err
			}
			guests = append(guests, guest)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("list guests: %w", err)
	}
	return guests, nil
}

// Each operation is a single implicit transaction. Retry only serialization
// failures, with at most three attempts and within the caller's deadline.
func retrySerialization(ctx context.Context, operation func() error) error {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := operation()
		var pgErr *pgconn.PgError
		if err == nil || attempt == 2 || !errors.As(err, &pgErr) || pgErr.Code != "40001" {
			return err
		}
		timer := time.NewTimer(time.Duration(25*(1<<attempt)) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
