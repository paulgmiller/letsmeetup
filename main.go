package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

type Guest struct {
	Id        string `json:"id,omitempty"`
	MeetupId  string
	GuestId   string
	GuestName string
	Lat, Long float64
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	store, err := newGuestStore(ctx, os.Getenv("DATABASE_URL"))
	cancel()
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	mux := http.NewServeMux()
	mux.Handle("/meetups/", &MeetupHandler{store: store})
	mux.Handle("/meetup-search/", &MeetupSearchHandler{store: store})
	mux.Handle("/", http.FileServer(http.Dir("./static")))
	log.Println("Listening on port 8080")
	if err := http.ListenAndServe(":8080", Logging(mux)); err != nil {
		log.Print(err)
	}
}

type meetupSearchStore interface {
	GetSearch(context.Context, string) (string, error)
	SaveSearch(context.Context, string, string) error
}

type MeetupSearchHandler struct {
	store meetupSearchStore
}

type meetupSearch struct {
	Query string `json:"query"`
}

func (h *MeetupSearchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	meetup := strings.TrimPrefix(r.URL.Path, "/meetup-search/")
	if strings.TrimSpace(meetup) == "" {
		http.Error(w, "Meetup ID required", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var search meetupSearch
	var err error
	switch r.Method {
	case http.MethodGet:
		search.Query, err = h.store.GetSearch(ctx, meetup)
	case http.MethodPut:
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		decoder := json.NewDecoder(r.Body)
		if decoder.Decode(&search) != nil {
			http.Error(w, "Invalid search", http.StatusBadRequest)
			return
		}
		search.Query = strings.TrimSpace(search.Query)
		if search.Query == "" || utf8.RuneCountInString(search.Query) > 200 {
			http.Error(w, "Search must contain 1 to 200 characters", http.StatusBadRequest)
			return
		}
		err = h.store.SaveSearch(ctx, meetup, search.Query)
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err != nil {
		log.Print("Failed to load or save meetup search")
		http.Error(w, "Could not load or save search", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(search); err != nil {
		log.Print("Failed to encode meetup search response")
	}
}

func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, req)
		log.Printf("%s %s %s", req.Method, req.RequestURI, time.Since(start))
	})
}

type guestStore interface {
	UpsertAndList(context.Context, Guest) ([]Guest, error)
}

type MeetupHandler struct {
	store guestStore
}

func (h *MeetupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var g Guest
	if err := json.NewDecoder(r.Body).Decode(&g); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	guests, err := h.store.UpsertAndList(ctx, g)
	if err != nil {
		// Connection errors may contain connection details. Keep them out of logs.
		log.Print("Failed to save or query guests")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(guests); err != nil {
		log.Print("Failed to encode guests response")
	}
}
