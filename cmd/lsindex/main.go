// Command lsindex lists the indexes actually present on a collection.
//
// Throwaway: EnsureIndexes says what the service CREATES, which is not the
// same as what a long-lived database HAS. Indexes from earlier versions
// survive, and one of them refusing a write is invisible from the code.
package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/eandstravel/carwash/internal/config"
	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	coll := flag.String("c", "users", "collection")
	flag.Parse()

	_ = godotenv.Load()
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.MongoURI))
	if err != nil {
		panic(err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()

	cur, err := client.Database(cfg.MongoDB).Collection(*coll).Indexes().List(ctx)
	if err != nil {
		panic(err)
	}
	var idx []bson.M
	if err := cur.All(ctx, &idx); err != nil {
		panic(err)
	}
	fmt.Printf("%s:\n", *coll)
	for _, i := range idx {
		fmt.Printf("  %-40v unique=%v sparse=%v keys=%v\n", i["name"], i["unique"], i["sparse"], i["key"])
	}
}
