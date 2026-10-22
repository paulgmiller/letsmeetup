package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

//go:embed static
var assets embed.FS

var validID = regexp.MustCompile(`^[a-zA-Z0-9_=-]{1,128}$`)
var errExpired = errors.New("meetup expired")

type Guest struct {
	Id        string `json:"id,omitempty"`
	MeetupId  string
	GuestId   string
	GuestName string
	Lat, Long float64
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	store, err := newGuestStore(startup, os.Getenv("DATABASE_URL"))
	cancel()
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	go store.cleanupLoop(ctx)
	mux := http.NewServeMux()
	mux.Handle("/meetups/", meetupLifetime(store, "/meetups/", &MeetupHandler{store: store}))
	mux.Handle("/meetup-search/", meetupLifetime(store, "/meetup-search/", &MeetupSearchHandler{store: store}))
	files, _ := fs.Sub(assets, "static")
	mux.Handle("/", http.FileServer(http.FS(files)))
	server := &http.Server{Addr: ":8080", Handler: Logging(mux), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			log.Print("HTTP shutdown failed")
		}
	}()
	log.Println("Listening on port 8080")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Print(err)
		stop()
	}
	<-shutdownDone
}

type lifetimeStore interface {
	EnsureMeetup(context.Context, string) (time.Time, error)
}

func meetupLifetime(store lifetimeStore, prefix string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		id := strings.TrimPrefix(r.URL.Path, prefix)
		if !validID.MatchString(id) {
			http.Error(w, "Invalid meetup ID", 400)
			return
		}
		// Rejected methods must not create a meetup or touch storage.
		allowed := r.Method == "GET" || (prefix == "/meetups/" && (r.Method == "POST" || r.Method == "DELETE")) || (prefix == "/meetup-search/" && r.Method == "PUT")
		if !allowed {
			next.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		expiry, err := store.EnsureMeetup(ctx, id)
		if errors.Is(err, errExpired) {
			http.Error(w, "This meetup has expired. Start a new meetup to share your location.", http.StatusGone)
			return
		}
		if err != nil {
			http.Error(w, "Could not load meetup", 500)
			return
		}
		w.Header().Set("X-Meetup-Expires", expiry.UTC().Format(time.RFC3339))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type meetupSearchStore interface {
	GetSearch(context.Context, string) (string, error)
	SaveSearch(context.Context, string, string) error
}
type MeetupSearchHandler struct{ store meetupSearchStore }
type meetupSearch struct {
	Query string `json:"query"`
}

func decodeBody(w http.ResponseWriter, r *http.Request, dest any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	if decoder.Decode(dest) != nil || decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "Invalid JSON request", http.StatusBadRequest)
		return false
	}
	return true
}
func (h *MeetupSearchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	meetup := strings.TrimPrefix(r.URL.Path, "/meetup-search/")
	if !validID.MatchString(meetup) {
		http.Error(w, "Invalid meetup ID", 400)
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
		if !decodeBody(w, r, &search) {
			return
		}
		search.Query = strings.TrimSpace(search.Query)
		if search.Query == "" || utf8.RuneCountInString(search.Query) > 200 {
			http.Error(w, "Search must contain 1 to 200 characters", 400)
			return
		}
		err = h.store.SaveSearch(ctx, meetup, search.Query)
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "Method not allowed", 405)
		return
	}
	if err != nil {
		log.Print("Failed to load or save meetup search")
		http.Error(w, "Could not load or save search", 500)
		return
	}
	writeJSON(w, search)
}

func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		// Invite IDs grant access to locations, so leave them out of logs.
		log.Printf("%s %s", r.Method, time.Since(start))
	})
}

type guestStore interface {
	UpsertAndList(context.Context, Guest) ([]Guest, error)
	List(context.Context, string) ([]Guest, error)
	Leave(context.Context, string, string) error
}
type MeetupHandler struct{ store guestStore }

// The browser owns its participant identity. Other invite holders cannot use an
// ID returned by List to overwrite or delete that participant.
func guestIdentity(w http.ResponseWriter, r *http.Request, create bool) string {
	if cookie, err := r.Cookie("MeetupGuest"); err == nil && len(cookie.Value) == 64 {
		if _, err := hex.DecodeString(cookie.Value); err == nil {
			return cookie.Value
		}
	}
	if !create {
		return ""
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return ""
	}
	id := hex.EncodeToString(token)
	http.SetCookie(w, &http.Cookie{Name: "MeetupGuest", Value: id, Path: "/", HttpOnly: true, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteLaxMode, MaxAge: 86400 * 30})
	return id
}

func (h *MeetupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	meetup := strings.TrimPrefix(r.URL.Path, "/meetups/")
	if !validID.MatchString(meetup) {
		http.Error(w, "Invalid meetup ID", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var guests []Guest
	var err error
	ownID := guestIdentity(w, r, false)
	switch r.Method {
	case http.MethodGet:
		guests, err = h.store.List(ctx, meetup)
	case http.MethodPost:
		var input struct {
			GuestName string
			Lat, Long *float64
		}
		if !decodeBody(w, r, &input) {
			return
		}
		input.GuestName = strings.TrimSpace(input.GuestName)
		if input.Lat == nil || input.Long == nil || math.Abs(*input.Lat) > 90 || math.Abs(*input.Long) > 180 || utf8.RuneCountInString(input.GuestName) > 60 {
			http.Error(w, "Provide valid coordinates and a name of at most 60 characters", 400)
			return
		}
		id := guestIdentity(w, r, true)
		if id == "" {
			http.Error(w, "Could not create participant", 500)
			return
		}
		ownID = id
		guests, err = h.store.UpsertAndList(ctx, Guest{MeetupId: meetup, GuestId: id, GuestName: input.GuestName, Lat: *input.Lat, Long: *input.Long})
	case http.MethodDelete:
		if id := guestIdentity(w, r, false); id != "" {
			err = h.store.Leave(ctx, meetup, id)
		}
		if err == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		http.Error(w, "Method not allowed", 405)
		return
	}
	if err != nil {
		log.Print("Failed to save or query guests")
		http.Error(w, "Could not update meetup", 500)
		return
	}
	// Public participant IDs are labels, never the private cookie credential.
	for i := range guests {
		guests[i].GuestId = publicGuestID(guests[i].GuestId)
	}
	if guests == nil {
		guests = []Guest{}
	}
	w.Header().Set("X-Guest-ID", publicGuestID(ownID))
	writeJSON(w, guests)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Print("Failed to encode response")
	}
}

func publicGuestID(private string) string {
	if private == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(private))
	return hex.EncodeToString(sum[:16])
}
