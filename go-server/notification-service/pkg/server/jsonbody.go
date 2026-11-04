package server

import (
	"encoding/json"
	"net/http"

	"github.com/example/notification-service/pkg/config"
)

// readJSON decodes at most limit bytes of the request body into dst. A
// non-positive limit falls back to config.DefaultMaxBodyBytes.
func readJSON(w http.ResponseWriter, r *http.Request, limit int64, dst any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, bodyLimit(limit))).Decode(dst)
}

// bodyLimit returns limit, or the default when limit is not positive.
func bodyLimit(limit int64) int64 {
	if limit <= 0 {
		return config.DefaultMaxBodyBytes
	}
	return limit
}
