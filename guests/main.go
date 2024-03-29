package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/samber/lo"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

var (
	endpoint     = "https://letsmeetup.documents.azure.com:443/"
	databaseName = "meetupdb"
)

type Guest struct {
	Id        string `json:"id,omitempty"`
	MeetupId  string
	GuestId   string
	GuestName string
	Lat, Long float64
}

func main() {

	key, ok := os.LookupEnv("AZURE_COSMOS_DB_KEY")
	if !ok {
		log.Fatal("AZURE_COSMOS_DB_KEY not set")
	}

	cred, err := azcosmos.NewKeyCredential(key)
	if err != nil {
		log.Fatal("Failed to create a credential: ", err)
	}

	// Create a CosmosDB client
	client, err := azcosmos.NewClientWithKey(endpoint, cred, nil)
	if err != nil {
		log.Fatal("Failed to create Azure Cosmos DB client: ", err)
	}

	containerClient, err := client.NewContainer(databaseName, "guests")
	if err != nil {
		log.Fatalf("failed to create a container client: %s", err)
	}

	meetupHandler := &MeetupHandler{containerClient}

	mux := http.NewServeMux()

	mux.Handle("/meetups/", meetupHandler)

	fs := http.FileServer(http.Dir("./static"))
	mux.Handle("/", fs)

	log.Println("Listening on port 8080")
	if err := http.ListenAndServe(":8080", Logging(mux)); err != nil {
		log.Printf(err.Error())
	}
}

func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, req)
		log.Printf("%s %s %s", req.Method, req.RequestURI, time.Since(start))
	})
}

type MeetupHandler struct {
	client *azcosmos.ContainerClient
}

func (h *MeetupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	log.Printf("%s %s %s\n", r.RemoteAddr, r.Method, r.URL)
	if r.Method != http.MethodPost {
		log.Printf("Method %s not allowed\n", r.Method)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var g Guest

	err := json.NewDecoder(r.Body).Decode(&g)
	if err != nil {
		log.Printf("Failed to decode body item: %s\n", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	g.Id = g.MeetupId + "/" + g.GuestId //guest id is used as the id for the document (rowkey)
	bytes, err := json.Marshal(g)
	if err != nil {
		log.Printf("Failed to encode guest: %s\n", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	pk := azcosmos.NewPartitionKeyString(g.MeetupId)

	resp, err := h.client.UpsertItem(ctx, pk, bytes, &azcosmos.ItemOptions{
		ConsistencyLevel: azcosmos.ConsistencyLevelSession.ToPtr(),
	})
	if err != nil {
		log.Printf("Failed to create item: %s\n", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	log.Printf("Status %d. Item %v created. ActivityId %s. Consuming %v Request Units.\n", resp.RawResponse.StatusCode, pk, resp.ActivityID, resp.RequestCharge)
	if resp.RawResponse.StatusCode != http.StatusAccepted && resp.RawResponse.StatusCode != http.StatusCreated && resp.RawResponse.StatusCode != http.StatusOK {
		log.Printf("unaccapetable status code %d", resp.RawResponse.StatusCode)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	pager := h.client.NewQueryItemsPager("SELECT * FROM c", pk, &azcosmos.QueryOptions{})
	guests := make([]Guest, 0)
	for pager.More() {
		resp, err := pager.NextPage(ctx)
		if err != nil {
			log.Printf("Failed to query items: %s\n", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		pageOfGuests := lo.FilterMap(resp.Items, func(bytes []byte, _ int) (Guest, bool) {
			var g Guest
			if err := json.Unmarshal(bytes, &g); err != nil {
				log.Printf("could not unmarshal item: %s, %s\n", bytes, err)
				return g, false
			}
			g.Id = "" //blank this out since we're not sure if we might change it
			return g, true

		})
		guests = append(guests, pageOfGuests...)
	}
	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(guests)
	if err != nil {
		log.Printf("Failed to query items: %s\n", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	log.Printf("returning :%d guests", len(guests))
}
