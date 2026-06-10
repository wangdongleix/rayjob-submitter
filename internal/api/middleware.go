package api

import (
	"log"
	"net/http"

	"github.com/verl-project/rayjob-submitter/internal/config"
)

func authMiddleware(cfg config.Config, next http.Handler) http.Handler {
	_ = cfg
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

func audit(r *http.Request, result string, fields map[string]any) {
	base := []any{
		"method", r.Method,
		"path", r.URL.Path,
		"result", result,
	}
	for k, v := range fields {
		base = append(base, k, v)
	}
	log.Println(base...)
}
