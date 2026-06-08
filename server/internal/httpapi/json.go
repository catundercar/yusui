package httpapi

import (
	"encoding/json"
	"net/http"
)

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr emits a JSON error with a stable machine code derived from the HTTP
// status. The client maps `code` to a localized message and falls back to the
// English `error` text when a code has no specific translation.
func writeErr(w http.ResponseWriter, status int, msg string) {
	writeErrCode(w, status, codeForStatus(status), msg)
}

// writeErrCode is writeErr with an explicit, specific machine code (e.g.
// "invalid_credentials", "approver_eq_requester") for user-facing errors that
// deserve a precise localized message.
func writeErrCode(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"code": code, "error": msg})
}

func codeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusLocked:
		return "locked"
	case http.StatusNotImplemented:
		return "not_implemented"
	case http.StatusInternalServerError:
		return "internal"
	default:
		return "error"
	}
}

// orEmpty returns a non-nil slice so empty collections serialize as [] not null.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
