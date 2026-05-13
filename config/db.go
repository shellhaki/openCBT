package config

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

var DB *pgxpool.Pool

func ConnDB() {
	_ = godotenv.Load()

	cstring := os.Getenv("POSTGRES_URI")
	if cstring == "" {
		log.Fatal("POSTGRES_URI is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	config, err := pgxpool.ParseConfig(cstring)
	if err != nil {
		log.Fatalf("failed to parse postgres config: %v", err)
	}

	config.MaxConns = 10
	config.MinConns = 2
	config.MaxConnLifetime = time.Hour
	config.MaxConnIdleTime = 30 * time.Minute
	config.HealthCheckPeriod = time.Minute

	dbpool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		log.Fatalf("failed to connect to postgres: %v", err)
	}

	if err := dbpool.Ping(ctx); err != nil {
		log.Fatalf("failed to ping postgres: %v", err)
	}

	DB = dbpool

	log.Println("Postgres connected successfully")
}
