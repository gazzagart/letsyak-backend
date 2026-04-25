package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/garethmaybery/letsyak-vault-api/internal/db"
)

func main() {
	userID := flag.String("user", "", "Matrix user ID to update, for example @alice:maybery.app")
	tier := flag.String("tier", "", "Vault tier to apply: free or plus")
	databaseURL := flag.String("database-url", envOrDefault("DATABASE_URL", "postgres://localhost:5432/vault?sslmode=disable"), "Postgres connection URL")
	flag.Parse()

	if *userID == "" || *tier == "" {
		flag.Usage()
		os.Exit(2)
	}

	database, err := db.New(*databaseURL)
	if err != nil {
		log.Fatalf("connect database: %v", err)
	}
	defer database.Close()

	user, err := database.UpdateUserTier(*userID, *tier)
	if err != nil {
		log.Fatalf("update user tier: %v", err)
	}
	if user == nil {
		log.Fatalf("vault user %q not found; provision the user before setting a tier", *userID)
	}

	tierInfo, _ := db.TierInfoFor(user.Tier)
	fmt.Printf(
		"Updated %s to %s plan (%d bytes quota)\n",
		user.MatrixUserID,
		tierInfo.Label,
		user.QuotaBytes,
	)
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
