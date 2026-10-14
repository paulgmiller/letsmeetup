package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
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
	mux.Handle("/", http.FileServer(http.Dir("./static")))
	log.Println("Listening on port 8080")
	if err := http.ListenAndServe(":8080", Logging(mux)); err != nil {
		log.Print(err)
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
