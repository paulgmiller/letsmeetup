package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

type stubGuestStore struct {
	guest  Guest
	called bool
	guests []Guest
	err    error
}

type stubSearchStore struct {
	meetup, query string
	err           error
}

func (s *stubSearchStore) GetSearch(ctx context.Context, meetup string) (string, error) {
	s.meetup = meetup
	return s.query, s.err
}

func (s *stubSearchStore) SaveSearch(ctx context.Context, meetup, query string) error {
	s.meetup, s.query = meetup, query
	return s.err
}

func TestMeetupSearchHandler(t *testing.T) {
	for _, tc := range []struct {
		name, method, body, path, want string
		status                         int
		err                            error
	}{
		{name: "load", method: "GET", want: "coffee", status: 200},
		{name: "save", method: "PUT", body: `{"query":"  tea & cake  "}`, want: "tea & cake", status: 200},
		{name: "empty", method: "PUT", body: `{"query":"  "}`, status: 400},
		{name: "missing query", method: "PUT", body: `{}`, status: 400},
		{name: "invalid JSON", method: "PUT", body: `{`, status: 400},
		{name: "too long", method: "PUT", body: `{"query":"` + strings.Repeat("a", 201) + `"}`, status: 400},
		{name: "missing meetup", method: "GET", path: "/meetup-search/", status: 400},
		{name: "wrong method", method: "POST", status: 405},
		{name: "load failure", method: "GET", err: errors.New("private database details"), status: 500},
		{name: "save failure", method: "PUT", body: `{"query":"tea"}`, err: errors.New("private database details"), status: 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &stubSearchStore{query: "coffee", err: tc.err}
			path := tc.path
			if path == "" {
				path = "/meetup-search/friends"
			}
			w := httptest.NewRecorder()
			(&MeetupSearchHandler{store: store}).ServeHTTP(w, httptest.NewRequest(tc.method, path, strings.NewReader(tc.body)))
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("shared search must not be cached")
			}
			if tc.status == 200 {
				var search meetupSearch
				if err := json.Unmarshal(w.Body.Bytes(), &search); err != nil {
					t.Fatal(err)
				}
				if search.Query != tc.want || store.meetup != "friends" {
					t.Fatalf("search=%+v meetup=%s", search, store.meetup)
				}
			}
			if tc.status == 400 && store.meetup != "" {
				t.Fatal("invalid input reached store")
			}
			if strings.Contains(w.Body.String(), "private database details") {
				t.Fatal("database details leaked")
			}
		})
	}
}

func (s *stubGuestStore) UpsertAndList(ctx context.Context, g Guest) ([]Guest, error) {
	s.called = true
	s.guest = g
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 10*time.Second {
		panic("request has no ten-second timeout")
	}
	return s.guests, s.err
}

func TestMeetupHandlerJSONCompatibility(t *testing.T) {
	guest := Guest{MeetupId: "body-meetup", GuestId: "guest", GuestName: "O'Brien", Lat: 47.5, Long: -122.1}
	store := &stubGuestStore{guests: []Guest{guest}}
	handler := &MeetupHandler{store: store}
	req := httptest.NewRequest(http.MethodPost, "/meetups/url-meetup", strings.NewReader(`{"MeetupID":"body-meetup","GuestId":"guest","GuestName":"O'Brien","Lat":47.5,"Long":-122.1}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status/content type: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	if !reflect.DeepEqual(store.guest, guest) {
		t.Fatalf("stored guest: %+v", store.guest)
	}
	var response []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{{"MeetupId": "body-meetup", "GuestId": "guest", "GuestName": "O'Brien", "Lat": 47.5, "Long": -122.1}}
	if !reflect.DeepEqual(response, want) {
		t.Fatalf("response: %s", w.Body.String())
	}
}

func TestMeetupHandlerErrors(t *testing.T) {
	for _, tc := range []struct {
		name, method, body string
		err                error
		status             int
		called             bool
	}{
		{name: "malformed", method: "POST", body: "{", status: 400},
		{name: "invalid coordinate type", method: "POST", body: `{"Lat":"bad"}`, status: 400},
		{name: "method", method: "GET", status: 400},
		{name: "database", method: "POST", body: `{"MeetupId":"m","GuestId":"g"}`, err: errors.New("database failure"), status: 500, called: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &stubGuestStore{err: tc.err}
			w := httptest.NewRecorder()
			(&MeetupHandler{store: store}).ServeHTTP(w, httptest.NewRequest(tc.method, "/meetups/m", strings.NewReader(tc.body)))
			if w.Code != tc.status || store.called != tc.called {
				t.Fatalf("status=%d store.called=%v", w.Code, store.called)
			}
			if strings.Contains(w.Body.String(), "database failure") {
				t.Fatal("database details leaked")
			}
		})
	}
}
