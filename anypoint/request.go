package anypoint

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// rawJSON is embedded in models to keep the JSON they were decoded from, so
// that fields the model does not declare are not lost (see [RawJSONer]).
type rawJSON struct{ raw json.RawMessage }

// RawJSON returns the JSON the value was decoded from, or nil if it was built in code.
func (r rawJSON) RawJSON() json.RawMessage { return r.raw }

func (r *rawJSON) setRawJSON(b json.RawMessage) { r.raw = b }

// RawJSONer is implemented by every model returned from the API.
type RawJSONer interface{ RawJSON() json.RawMessage }

type rawSetter interface{ setRawJSON(json.RawMessage) }

// decode unmarshals data into out and, for models, keeps a copy of data.
func decode(data []byte, out any) error {
	if err := json.Unmarshal(data, out); err != nil {
		return err
	}
	if rs, ok := out.(rawSetter); ok {
		rs.setRawJSON(append(json.RawMessage(nil), data...))
	}
	return nil
}

// do sends body (nil, []byte or a JSON-marshalable value) and decodes the
// response into a new T. An empty response body yields a zero T.
func do[T any](ctx context.Context, c *Client, method, path string, body any) (*T, error) {
	var out T
	if err := c.JSON(ctx, method, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func get[T any](ctx context.Context, c *Client, path string) (*T, error) {
	return do[T](ctx, c, http.MethodGet, path, nil)
}

// list decodes a JSON array, found at the first present key of an envelope
// object, or the body itself when it is an array.
func list[T any](ctx context.Context, c *Client, path string, keys ...string) ([]T, error) {
	var raw json.RawMessage
	if err := c.JSON(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	return decodeList[T](raw, keys...)
}

func decodeList[T any](raw json.RawMessage, keys ...string) ([]T, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] == '{' && len(keys) > 0 {
		var env map[string]json.RawMessage
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, err
		}
		raw = nil
		for _, k := range keys {
			if v, ok := env[k]; ok {
				raw = v
				break
			}
		}
		if raw == nil || string(raw) == "null" {
			return nil, nil
		}
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("expected a JSON array: %w", err)
	}
	out := make([]T, len(items))
	for i, it := range items {
		var v T
		if err := decode(it, any(&v)); err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// withQuery appends q to path, if q has any values.
func withQuery(path string, q url.Values) string {
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}

// esc escapes one path segment.
func esc(s string) string { return url.PathEscape(s) }

// pageSize is the page size used by the paged list helpers.
const pageSize = 100

// paged follows limit/offset pagination until a short page, or the reported
// total, is reached. path must not already have a query string other than q.
func paged[T any](ctx context.Context, c *Client, path string, q url.Values, keys ...string) ([]T, error) {
	if q == nil {
		q = url.Values{}
	}
	var all []T
	for offset := 0; ; offset += pageSize {
		q.Set("limit", fmt.Sprint(pageSize))
		q.Set("offset", fmt.Sprint(offset))
		var raw json.RawMessage
		if err := c.JSON(ctx, http.MethodGet, withQuery(path, q), nil, &raw); err != nil {
			return nil, err
		}
		page, err := decodeList[T](raw, keys...)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		var env struct {
			Total *int `json:"total"`
		}
		_ = json.Unmarshal(raw, &env)
		if len(page) < pageSize || (env.Total != nil && len(all) >= *env.Total) {
			return all, nil
		}
	}
}
