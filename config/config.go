package config

import (
	"os"
	"strconv"
	"time"
)

type AppConfig struct {
	Port      string
	JWTSecret string
	TokenTTL  time.Duration
}

func Load() AppConfig {
	ttlHours, err := strconv.Atoi(getenv("JWT_TTL_HOURS", "24"))
	if err != nil || ttlHours <= 0 {
		ttlHours = 24
	}

	return AppConfig{
		Port:      getenv("PORT", "8080"),
		JWTSecret: getenv("JWT_SECRET", "change-me-in-production"),
		TokenTTL:  time.Duration(ttlHours) * time.Hour,
	}
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
