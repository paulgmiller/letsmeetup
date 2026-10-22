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

func (s *stubGuestStore) List(ctx context.Context, meetup string) ([]Guest, error) {
	s.called = true
	return s.guests, s.err
}
func (s *stubGuestStore) Leave(ctx context.Context, meetup, guest string) error {
	s.called = true
	s.guest = Guest{MeetupId: meetup, GuestId: guest}
	return s.err
}

func TestMeetupHandlerJSONCompatibility(t *testing.T) {
	guest := Guest{MeetupId: "url-meetup", GuestId: strings.Repeat("a", 64), GuestName: "O'Brien", Lat: 47.5, Long: -122.1}
	store := &stubGuestStore{guests: []Guest{guest}}
	handler := &MeetupHandler{store: store}
	req := httptest.NewRequest(http.MethodPost, "/meetups/url-meetup", strings.NewReader(`{"MeetupID":"body-meetup","GuestId":"guest","GuestName":"O'Brien","Lat":47.5,"Long":-122.1}`))
	req.AddCookie(&http.Cookie{Name: "MeetupGuest", Value: strings.Repeat("a", 64)})
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
	want := []map[string]any{{"MeetupId": "url-meetup", "GuestId": publicGuestID(strings.Repeat("a", 64)), "GuestName": "O'Brien", "Lat": 47.5, "Long": -122.1}}
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
		{name: "missing coordinates", method: "POST", body: `{"Lat":1}`, status: 400},
		{name: "latitude out of bounds", method: "POST", body: `{"Lat":91,"Long":0}`, status: 400},
		{name: "longitude out of bounds", method: "POST", body: `{"Lat":0,"Long":181}`, status: 400},
		{name: "trailing JSON", method: "POST", body: `{"Lat":0,"Long":0} {}`, status: 400},
		{name: "large payload", method: "POST", body: `{"GuestName":"` + strings.Repeat("x", 5000) + `","Lat":0,"Long":0}`, status: 400},
		{name: "malformed", method: "POST", body: "{", status: 400},
		{name: "invalid coordinate type", method: "POST", body: `{"Lat":"bad"}`, status: 400},
		{name: "method", method: "PATCH", status: 405},
		{name: "database", method: "POST", body: `{"MeetupId":"m","GuestId":"g","Lat":0,"Long":0}`, err: errors.New("database failure"), status: 500, called: true},
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

type stubLifetime struct {
	calls int
	err   error
}

func (s *stubLifetime) EnsureMeetup(ctx context.Context, id string) (time.Time, error) {
	s.calls++
	return time.Now().Add(time.Hour), s.err
}
func TestMeetupLifetime(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		err        error
		status     int
	}{
		{"active", "/meetups/friends", nil, 200},
		{"expired", "/meetups/friends", errExpired, 410},
		{"invalid", "/meetups/bad/id", nil, 400},
		{"database", "/meetups/friends", errors.New("private details"), 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &stubLifetime{err: tc.err}
			called := false
			h := meetupLifetime(store, "/meetups/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
			if w.Code != tc.status || called != (tc.status == 200) {
				t.Fatalf("status=%d called=%v", w.Code, called)
			}
			if tc.status == 400 && store.calls != 0 {
				t.Fatal("invalid ID reached database")
			}
			if tc.status == 200 && w.Header().Get("X-Meetup-Expires") == "" {
				t.Fatal("missing expiry")
			}
		})
	}
}

func TestParticipantIdentity(t *testing.T) {
	store := &stubGuestStore{}
	handler := &MeetupHandler{store: store}
	req := httptest.NewRequest("POST", "/meetups/friends", strings.NewReader(`{"GuestId":"someone-else","Lat":0,"Long":0}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 || len(w.Result().Cookies()) != 1 {
		t.Fatalf("status=%d cookies=%v", w.Code, w.Result().Cookies())
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || len(cookie.Value) != 64 {
		t.Fatal("incorrect identity cookie")
	}
	if store.guest.GuestId != cookie.Value || w.Header().Get("X-Guest-ID") != publicGuestID(cookie.Value) {
		t.Fatal("identity mismatch")
	}
	if strings.Contains(w.Body.String(), cookie.Value) {
		t.Fatal("private credential exposed")
	}
	// Supplying another participant's public ID cannot delete that participant.
	req = httptest.NewRequest("DELETE", "/meetups/friends?GuestId=victim", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 204 || store.guest.GuestId != cookie.Value {
		t.Fatal("leave did not use cookie identity")
	}
	// A viewer cannot delete any participant.
	store.called = false
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("DELETE", "/meetups/friends", nil))
	if w.Code != 204 || store.called {
		t.Fatal("anonymous delete reached store")
	}
}

func TestMeetupViewerDoesNotJoin(t *testing.T) {
	store := &stubGuestStore{}
	w := httptest.NewRecorder()
	(&MeetupHandler{store: store}).ServeHTTP(w, httptest.NewRequest("GET", "/meetups/friends", nil))
	if w.Code != 200 || w.Body.String() != "[]\n" || len(w.Result().Cookies()) != 0 || store.guest.GuestId != "" {
		t.Fatalf("viewer response: %s", w.Body.String())
	}
}
