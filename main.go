package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"github.com/jessevdk/go-flags"
)

var mongoClient *mongo.Client

type Stats struct {
	ChordName                  string    `json:"chord_name" bson:"chord_name"`
	RootNote                   string    `json:"root_note" bson:"root_note"`
	ChordExtension             string    `json:"chord_extension" bson:"chord_extension"`
	AnswerDurationMilliSeconds int       `json:"answer_duration_millis" bson:"answer_duration_millis"`
	CreatedAt                  time.Time `json:"created_at" bson:"created_at"`
}

func main() {
	// parse command line input/env vars
	var options struct {
		MongoUrl  string `short:"u" env:"MONGODB_URL" description:"URL to mongo" required:"true"`
		Port      string `short:"p" env:"PORT" description:"Port that server will be listening on" required:"true"`
		ClientUrl string `short:"c" env:"CLIENT_URL" description:"URL of the client calling the server (used for cors settings)" required:"true"`
	}
	_, err := flags.Parse(&options)
	if err != nil {
		log.Fatalln("Error parsing input:", err)
	}

	// connect to mongo
	mongoClient = connectToMongo(options.MongoUrl)
	defer mongoClient.Disconnect(context.Background())

	r := chi.NewRouter()

	r.Use(
		middleware.RequestID,
		middleware.RealIP,
		middleware.Logger,
		middleware.Recoverer,
		middleware.Timeout(60*time.Second),
	)

	r.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	r.Route("/stats", func(r chi.Router) {
		r.Use(cors.Handler(cors.Options{
			AllowedOrigins:   []string{options.ClientUrl},
			AllowedMethods:   []string{"GET", "POST"},
			AllowedHeaders:   []string{"Content-Type"},
			AllowCredentials: false,
			MaxAge:           300,
		}))
		r.Get("/", addStatsHandler)
		r.Post("/", addStatsHandler)
	})

	log.Printf(
		"Starting server!\nPort: %s\nMongoURL: %s\nClientURL: %s\n",
		options.Port,
		options.MongoUrl,
		options.ClientUrl,
	)
	http.ListenAndServe(fmt.Sprintf(":%s", options.Port), r)
}

// UpdatePost updates settings
func addStatsHandler(w http.ResponseWriter, r *http.Request) {
	var stats Stats
	json.NewDecoder(r.Body).Decode(&stats)

	_, err := mongoClient.Database("main").Collection("statistics").InsertOne(
		context.Background(),
		stats,
	)

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
	} else {
		w.WriteHeader(http.StatusOK)
	}
}

func connectToMongo(url string) *mongo.Client {
	client, err := mongo.Connect(context.Background(), options.Client().ApplyURI(url))
	if err != nil {
		log.Fatalln("Failed to connect to Mongo! Error:", err)
	}
	err = client.Ping(context.Background(), readpref.Primary())
	if err != nil {
		log.Fatalln("Failed to ping Mongo! Error:", err)
	}
	return client
}
