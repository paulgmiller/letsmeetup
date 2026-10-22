package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const meetupsSchema = `CREATE TABLE IF NOT EXISTS meetups (
 meetup_id TEXT PRIMARY KEY,
 expires_at TIMESTAMPTZ NOT NULL DEFAULT (CURRENT_TIMESTAMP + INTERVAL '24 hours')
)`

const guestsSchema = `CREATE TABLE IF NOT EXISTS guests (
 meetup_id TEXT NOT NULL,
 guest_id TEXT NOT NULL,
 guest_name TEXT NOT NULL,
 lat DOUBLE PRECISION NOT NULL,
 long DOUBLE PRECISION NOT NULL,
 PRIMARY KEY (meetup_id, guest_id)
)`

const meetupSearchSchema = `CREATE TABLE IF NOT EXISTS meetup_searches (
 meetup_id TEXT PRIMARY KEY,
 query TEXT NOT NULL
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
	if err := retrySerialization(ctx, func() error { _, err := pool.Exec(ctx, meetupsSchema); return err }); err != nil {
		pool.Close()
		return nil, errors.New("failed to create meetups table")
	}
	if err := retrySerialization(ctx, func() error {
		_, err := pool.Exec(ctx, guestsSchema)
		return err
	}); err != nil {
		pool.Close()
		return nil, errors.New("failed to create guests table; check database schema and permissions")
	}
	if err := retrySerialization(ctx, func() error {
		_, err := pool.Exec(ctx, meetupSearchSchema)
		return err
	}); err != nil {
		pool.Close()
		return nil, errors.New("failed to create meetup searches table; check database schema and permissions")
	}
	if err := retrySerialization(ctx, func() error {
		_, err := pool.Exec(ctx, `INSERT INTO meetups (meetup_id)
 SELECT meetup_id FROM guests UNION SELECT meetup_id FROM meetup_searches
 ON CONFLICT (meetup_id) DO NOTHING`)
		return err
	}); err != nil {
		pool.Close()
		return nil, errors.New("failed to initialize meetup lifetimes")
	}
	return &postgresGuestStore{pool: pool}, nil
}

func (s *postgresGuestStore) Close() { s.pool.Close() }

func (s *postgresGuestStore) GetSearch(ctx context.Context, meetup string) (string, error) {
	query := "food"
	err := retrySerialization(ctx, func() error {
		err := s.pool.QueryRow(ctx, `SELECT query FROM meetup_searches WHERE meetup_id = $1 AND EXISTS (SELECT 1 FROM meetups WHERE meetup_id=$1 AND expires_at>CURRENT_TIMESTAMP)`, meetup).Scan(&query)
		if errors.Is(err, pgx.ErrNoRows) {
			query = "food"
			return nil
		}
		return err
	})
	return query, err
}

func (s *postgresGuestStore) SaveSearch(ctx context.Context, meetup, query string) error {
	return retrySerialization(ctx, func() error {
		_, err := s.pool.Exec(ctx, `INSERT INTO meetup_searches (meetup_id, query) SELECT $1, $2 WHERE EXISTS (SELECT 1 FROM meetups WHERE meetup_id=$1 AND expires_at>CURRENT_TIMESTAMP)
 ON CONFLICT (meetup_id) DO UPDATE SET query = excluded.query`, meetup, query)
		return err
	})
}

func (s *postgresGuestStore) UpsertAndList(ctx context.Context, g Guest) ([]Guest, error) {
	err := retrySerialization(ctx, func() error {
		_, err := s.pool.Exec(ctx, `INSERT INTO guests (meetup_id, guest_id, guest_name, lat, long)
   SELECT $1, $2, $3, $4, $5 WHERE EXISTS (SELECT 1 FROM meetups WHERE meetup_id=$1 AND expires_at>CURRENT_TIMESTAMP)
   ON CONFLICT (meetup_id, guest_id) DO UPDATE
   SET guest_name = excluded.guest_name, lat = excluded.lat, long = excluded.long`,
			g.MeetupId, g.GuestId, g.GuestName, g.Lat, g.Long)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("upsert guest: %w", err)
	}
	return s.List(ctx, g.MeetupId)
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

// Expired IDs are kept as small tombstones so old links cannot resurrect a room.
func (s *postgresGuestStore) EnsureMeetup(ctx context.Context, id string) (time.Time, error) {
	err := retrySerialization(ctx, func() error {
		_, err := s.pool.Exec(ctx, `INSERT INTO meetups (meetup_id) VALUES ($1) ON CONFLICT (meetup_id) DO NOTHING`, id)
		return err
	})
	if err != nil {
		return time.Time{}, err
	}
	var expires time.Time
	var active bool
	err = s.pool.QueryRow(ctx, `SELECT expires_at, expires_at > CURRENT_TIMESTAMP FROM meetups WHERE meetup_id=$1`, id).Scan(&expires, &active)
	if err == nil && !active {
		return expires, errExpired
	}
	return expires, err
}

func (s *postgresGuestStore) List(ctx context.Context, meetup string) ([]Guest, error) {
	guests := []Guest{}
	rows, err := s.pool.Query(ctx, `SELECT meetup_id, guest_id, guest_name, lat, long FROM guests
 WHERE meetup_id=$1 AND EXISTS (SELECT 1 FROM meetups WHERE meetup_id=$1 AND expires_at>CURRENT_TIMESTAMP) ORDER BY guest_id`, meetup)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var g Guest
		if err := rows.Scan(&g.MeetupId, &g.GuestId, &g.GuestName, &g.Lat, &g.Long); err != nil {
			return nil, err
		}
		guests = append(guests, g)
	}
	return guests, rows.Err()
}

func (s *postgresGuestStore) Leave(ctx context.Context, meetup, guest string) error {
	return retrySerialization(ctx, func() error {
		_, err := s.pool.Exec(ctx, `DELETE FROM guests WHERE meetup_id=$1 AND guest_id=$2`, meetup, guest)
		return err
	})
}

func (s *postgresGuestStore) cleanupExpired(ctx context.Context) error {
	return retrySerialization(ctx, func() error {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		for _, table := range []string{"guests", "meetup_searches"} {
			if _, err = tx.Exec(ctx, "DELETE FROM "+table+" WHERE meetup_id IN (SELECT meetup_id FROM meetups WHERE expires_at <= CURRENT_TIMESTAMP)"); err != nil {
				return err
			}
		}
		return tx.Commit(ctx)
	})
}

func (s *postgresGuestStore) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		cleanup, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := s.cleanupExpired(cleanup)
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Print("Could not clean up expired meetup data")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
