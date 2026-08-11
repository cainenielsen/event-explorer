// Package config centralizes the small set of environment variables shared
// by the server and seeder binaries so connection settings stay in one
// place.
package config

import "os"

// ClickHouse holds the connection settings for the native protocol client.
type ClickHouse struct {
	Addr     string
	Database string
	Username string
	Password string
}

// ClickHouseFromEnv reads connection settings from the environment, falling
// back to sane defaults for local/docker-compose use.
func ClickHouseFromEnv() ClickHouse {
	return ClickHouse{
		Addr:     getEnv("CLICKHOUSE_ADDR", "localhost:9000"),
		Database: getEnv("CLICKHOUSE_DATABASE", "eventsdb"),
		Username: getEnv("CLICKHOUSE_USER", "default"),
		Password: getEnv("CLICKHOUSE_PASSWORD", ""),
	}
}

// HTTPAddr is the address the API server listens on.
func HTTPAddr() string {
	return getEnv("HTTP_ADDR", ":8080")
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
