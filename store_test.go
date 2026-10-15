package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestRetrySerialization(t *testing.T) {
	serialization := fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: "40001"})
	other := &pgconn.PgError{Code: "23505"}
	for _, tc := range []struct {
		name     string
		results  []error
		attempts int
		want     error
	}{
		{"success", []error{nil}, 1, nil},
		{"retry succeeds", []error{serialization, serialization, nil}, 3, nil},
		{"retry exhausted", []error{serialization, serialization, serialization}, 3, serialization},
		{"other SQL error", []error{other}, 1, other},
		{"connection error", []error{errors.New("offline")}, 1, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			err := retrySerialization(context.Background(), func() error {
				result := tc.results[calls]
				calls++
				return result
			})
			want := tc.want
			if tc.name == "connection error" {
				want = tc.results[0]
			}
			if calls != tc.attempts || !errors.Is(err, want) {
				t.Fatalf("attempts=%d err=%v", calls, err)
			}
		})
	}
}

func TestRetrySerializationCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := retrySerialization(ctx, func() error {
		calls++
		cancel()
		return &pgconn.PgError{Code: "40001"}
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	calls = 0
	err = retrySerialization(ctx, func() error { calls++; return nil })
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestDatabaseConfiguration(t *testing.T) {
	for _, url := range []string{"", " ", "postgres://user:secret@host:invalid/db"} {
		_, err := newGuestStore(context.Background(), url)
		if err == nil {
			t.Fatal("expected configuration error")
		}
		if strings.Contains(err.Error(), "secret") {
			t.Fatal("credentials leaked")
		}
	}
}

// Set TEST_DATABASE_URL to a disposable PostgreSQL or CockroachDB database.
func TestGuestStoreIntegration(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := newGuestStore(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	meetup := fmt.Sprintf("test-%d", time.Now().UnixNano())
	otherMeetup := meetup + "-other"
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := store.pool.Exec(cleanupCtx, `DELETE FROM guests WHERE meetup_id IN ($1,$2)`, meetup, otherMeetup); err != nil {
			t.Error(err)
		}
		if _, err := store.pool.Exec(cleanupCtx, `DELETE FROM meetup_searches WHERE meetup_id IN ($1,$2)`, meetup, otherMeetup); err != nil {
			t.Error(err)
		}
	}()
	if query, err := store.GetSearch(ctx, meetup); err != nil || query != "food" {
		t.Fatalf("default search: %q %v", query, err)
	}
	for _, query := range []string{"coffee", "O'Brien's tea & cake"} {
		if err := store.SaveSearch(ctx, meetup, query); err != nil {
			t.Fatal(err)
		}
	}
	if query, err := store.GetSearch(ctx, otherMeetup); err != nil || query != "food" {
		t.Fatalf("search isolation: %q %v", query, err)
	}
	first := Guest{MeetupId: meetup, GuestId: "b", GuestName: "O'Brien", Lat: 47.5, Long: -122.1}
	guests, err := store.UpsertAndList(ctx, first)
	if err != nil || !reflect.DeepEqual(guests, []Guest{first}) {
		t.Fatalf("insert: %+v %v", guests, err)
	}
	first.GuestName = "Updated"
	first.Lat = 0
	first.Long = 1
	guests, err = store.UpsertAndList(ctx, first)
	if err != nil || !reflect.DeepEqual(guests, []Guest{first}) {
		t.Fatalf("update: %+v %v", guests, err)
	}
	// Reuse a guest ID in another meetup: it must be a separate row.
	other := first
	other.MeetupId = otherMeetup
	guests, err = store.UpsertAndList(ctx, other)
	if err != nil || !reflect.DeepEqual(guests, []Guest{other}) {
		t.Fatalf("isolation: %+v %v", guests, err)
	}
	// A new pool runs schema initialization again and sees persisted records.
	reopened, err := newGuestStore(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if query, err := reopened.GetSearch(ctx, meetup); err != nil || query != "O'Brien's tea & cake" {
		t.Fatalf("persisted search: %q %v", query, err)
	}
	second := Guest{MeetupId: meetup, GuestId: "a", GuestName: "Second", Lat: -1, Long: 0}
	guests, err = reopened.UpsertAndList(ctx, second)
	if err != nil || !reflect.DeepEqual(guests, []Guest{second, first}) {
		t.Fatalf("restart/order: %+v %v", guests, err)
	}
	var count int
	if err := reopened.pool.QueryRow(ctx, `SELECT count(*) FROM guests WHERE meetup_id = $1`, meetup).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("got %d rows, want 2", count)
	}
	reopened.Close()
	if _, err := reopened.UpsertAndList(ctx, first); err == nil {
		t.Fatal("expected error from closed pool")
	}
}
