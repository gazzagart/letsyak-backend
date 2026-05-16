package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/garethmaybery/letsyak-control-plane/internal/workspace"
)

func main() {
	port := envOrDefault("PORT", "8085")
	configPath := envOrDefault("TENANT_CONFIG_PATH", "./config/tenants.sample.json")
	corsOrigins := envOrDefault("CORS_ALLOWED_ORIGINS", "*")

	store, err := workspace.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load tenant config: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthz)
	workspace.NewHandler(store).Register(mux)

	log.Printf("LetsYak control-plane listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, withCORS(mux, strings.Split(corsOrigins, ","))))
}

func healthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func withCORS(next http.Handler, allowedOrigins []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && originAllowed(origin, allowedOrigins) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		} else if origin == "" && originAllowed("*", allowedOrigins) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if originAllowed("*", allowedOrigins) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type")
		w.Header().Set("Access-Control-Max-Age", "300")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func originAllowed(origin string, allowedOrigins []string) bool {
	for _, allowed := range allowedOrigins {
		allowed = strings.TrimSpace(allowed)
		if allowed == "*" || allowed == origin {
			return true
		}
	}
	return false
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
