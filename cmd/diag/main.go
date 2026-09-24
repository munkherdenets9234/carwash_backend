// Command diag is a throwaway read-only look at what is actually in the
// database. It writes nothing.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/eandstravel/carwash/internal/config"
	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	_ = godotenv.Load()
	cfg := config.Load()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.MongoURI))
	if err != nil {
		fmt.Println("connect:", err)
		return
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	db := client.Database(cfg.MongoDB)

	for _, col := range []string{"users", "locations", "wash_services", "reservations", "shifts", "time_entries", "cars"} {
		total, err := db.Collection(col).CountDocuments(ctx, bson.M{})
		if err != nil {
			fmt.Printf("%-14s error: %v\n", col, err)
			continue
		}
		missing, _ := db.Collection(col).CountDocuments(ctx, bson.M{"tenant_id": bson.M{"$exists": false}})
		fmt.Printf("%-14s total=%-4d without tenant_id=%d\n", col, total, missing)
	}

	fmt.Println("\ndistinct tenant_id on users:")
	vals, err := db.Collection("users").Distinct(ctx, "tenant_id", bson.M{})
	if err != nil {
		fmt.Println("  error:", err)
	} else if len(vals) == 0 {
		fmt.Println("  (none)")
	}
	for _, v := range vals {
		fmt.Printf("  %v\n", v)
	}

	fmt.Println("\nusers (email / role / tenant):")
	cur, err := db.Collection("users").Find(ctx, bson.M{}, options.Find().SetLimit(10))
	if err != nil {
		fmt.Println("  error:", err)
		return
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var raw bson.M
		if err := cur.Decode(&raw); err != nil {
			continue
		}
		fmt.Printf("  %-24v %-10v tenant=%v\n", raw["email"], raw["role"], raw["tenant_id"])
	}
}
