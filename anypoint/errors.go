package anypoint

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// HTTPError is returned for any non-2xx response.
type HTTPError struct {
	Status int
	Method string
	URL    string
	Body   []byte
}

func (e *HTTPError) Error() string {
	msg := strings.TrimSpace(string(e.Body))
	var m map[string]any
	if json.Unmarshal(e.Body, &m) == nil {
		for _, k := range []string{"message", "error_description", "error", "detail"} {
			if s, ok := m[k].(string); ok && s != "" {
				msg = s
				break
			}
		}
	}
	if len(msg) > 2000 {
		msg = msg[:2000] + "…"
	}
	return fmt.Sprintf("%s %s: %d %s: %s", e.Method, e.URL, e.Status, http.StatusText(e.Status), msg)
}

// IsStatus reports whether err is an [*HTTPError] with the given status code.
func IsStatus(err error, code int) bool {
	var he *HTTPError
	return errors.As(err, &he) && he.Status == code
}

var (
	// ErrNotFound matches (with errors.Is) an HTTPError with status 404.
	ErrNotFound = errors.New("not found")
	// ErrConflict matches (with errors.Is) an HTTPError with status 409.
	ErrConflict = errors.New("conflict")
)

// Is lets errors.Is match ErrNotFound and ErrConflict by status code.
func (e *HTTPError) Is(target error) bool {
	switch target {
	case ErrNotFound:
		return e.Status == http.StatusNotFound
	case ErrConflict:
		return e.Status == http.StatusConflict
	}
	return false
}

// IsNotFound reports whether err is a 404 from the API, or a lookup by name that found nothing.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// IsConflict reports whether err is a 409 from the API.
func IsConflict(err error) bool { return errors.Is(err, ErrConflict) }

// notFound is returned by lookups by name.
func notFound(kind, name string) error { return fmt.Errorf("%s %q: %w", kind, name, ErrNotFound) }
