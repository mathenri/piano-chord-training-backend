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

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"github.com/jessevdk/go-flags"
)

var mongoClient *mongo.Client
var authToken string

type StatsRaw struct {
	ChordName                  string    `json:"chord_name" bson:"chord_name"`
	RootNote                   string    `json:"root_note" bson:"root_note"`
	ChordExtension             string    `json:"chord_extension" bson:"chord_extension"`
	AnswerDurationMilliSeconds int       `json:"answer_duration_millis" bson:"answer_duration_millis"`
	CreatedAt                  time.Time `json:"created_at" bson:"created_at"`
}

type StatsCountByDay struct {
	Day   string `json:"day" bson:"_id"`
	Count int    `json:"count" bson:"count"`
}

func main() {
	// parse command line input/env vars
	var options struct {
		MongoUrl  string `short:"u" env:"MONGODB_URL" description:"URL to mongo" required:"true"`
		Port      string `short:"p" env:"PORT" description:"Port that server will be listening on" required:"true"`
		AuthToken string `short:"a" env:"AUTH_TOKEN" description:"Auth token" required:"true"`
	}
	_, err := flags.Parse(&options)
	if err != nil {
		log.Fatalln("Error parsing input:", err)
	}

	authToken = options.AuthToken

	// connect to mongo
	mongoClient = connectToMongo(options.MongoUrl)
	defer mongoClient.Disconnect(context.Background())

	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"https://*", "http://*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"*"},
		AllowCredentials: false,
		MaxAge:           300,
	}))
	r.Use(Authorize)

	r.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	r.Post("/stats", addStatsHandler)
	r.Get("/stats/raw", getStatsRawHandler)
	r.Get("/stats/count_by_day", getCountByDayHandler)

	log.Printf(
		"Starting server!\nPort: %s\n",
		options.Port,
	)
	http.ListenAndServe(fmt.Sprintf(":%s", options.Port), r)
}

// UpdatePost updates settings
func addStatsHandler(w http.ResponseWriter, r *http.Request) {
	var stats StatsRaw
	json.NewDecoder(r.Body).Decode(&stats)

	_, err := mongoClient.Database("main").Collection("statistics").InsertOne(
		context.Background(),
		stats,
	)

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Println("Error:", err)
	} else {
		w.WriteHeader(http.StatusOK)
	}
}

func getStatsRawHandler(w http.ResponseWriter, r *http.Request) {
	stats := []StatsRaw{}
	cursor, err := mongoClient.Database("main").Collection("statistics").Find(
		context.Background(),
		bson.M{},
	)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Println("Error:", err)
		return
	}

	err = cursor.All(context.Background(), &stats)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Println("Error:", err)
		return
	}

	jsonBytes, err := json.Marshal(stats)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Println("Error:", err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(jsonBytes)
}

func getCountByDayHandler(w http.ResponseWriter, r *http.Request) {
	cursor, err := mongoClient.Database("main").Collection("statistics").Aggregate(
		context.Background(),
		mongo.Pipeline{
			bson.D{{
				"$group", bson.D{
					{
						"_id", bson.D{{
							"$dateToString", bson.D{
								{"format", "%Y-%m-%d"},
								{"date", "$created_at"},
							},
						}},
					},
					{
						"count", bson.D{{"$sum", 1}},
					},
				},
			}},
		},
	)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Println("Error:", err)
		return
	}

	var countByDaysFromMongo []StatsCountByDay
	err = cursor.All(
		context.Background(),
		&countByDaysFromMongo,
	)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Println("Error:", err)
		return
	}

	countsMap := make(map[string]StatsCountByDay)
	for _, count := range countByDaysFromMongo {
		countsMap[count.Day] = count
	}

	startTimeDaysAgo := 31
	responseCountByDays := []StatsCountByDay{}
	for i := startTimeDaysAgo; i >= 0; i-- {
		today := time.Now()
		targetDay := today.AddDate(0, 0, -i)
		targetDayStr := targetDay.Format("2006-01-02")
		count, exists := countsMap[targetDayStr]
		if exists {
			responseCountByDays = append(responseCountByDays, count)
		} else {
			responseCountByDays = append(
				responseCountByDays,
				StatsCountByDay{
					Day:   targetDayStr,
					Count: 0,
				},
			)
		}
	}

	// for start_date to end_date:
	// if in map add to result array
	// else add 0 result

	jsonBytes, err := json.Marshal(responseCountByDays)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Println("Error:", err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(jsonBytes)
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

func Authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var token string
		tokens := r.Header["X-Auth-Token"]
		if len(tokens) > 0 {
			token = tokens[0]
		}

		if token != authToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}
