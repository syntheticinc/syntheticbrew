package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"encode response: %s"}`, err), http.StatusInternalServerError)
	}
}

// writeJSONError writes a JSON error response with the given status code.
func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// readJSON decodes a JSON request body into v.
func readJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return fmt.Errorf("decode request body: %w", err)
	}
	return nil
}

// domainErrorToHTTPStatus maps a DomainError code to an HTTP status code.
// Falls back to 500 for unknown codes or non-domain errors.
func domainErrorToHTTPStatus(err error) int {
	var domainErr *pkgerrors.DomainError
	if !errors.As(err, &domainErr) {
		return http.StatusInternalServerError
	}

	switch domainErr.Code {
	case pkgerrors.CodeNotFound:
		return http.StatusNotFound
	case pkgerrors.CodeAlreadyExists:
		return http.StatusConflict
	case pkgerrors.CodeInvalidInput:
		return http.StatusBadRequest
	case pkgerrors.CodeUnauthorized:
		return http.StatusUnauthorized
	case pkgerrors.CodeForbidden:
		return http.StatusForbidden
	case pkgerrors.CodeUsageLimited:
		return http.StatusPaymentRequired
	default:
		return http.StatusInternalServerError
	}
}

// writeDomainError writes a JSON error response, mapping DomainError codes to HTTP status codes.
// Non-domain errors are mapped to 500.
func writeDomainError(w http.ResponseWriter, err error) {
	writeJSONError(w, domainErrorToHTTPStatus(err), err.Error())
}
