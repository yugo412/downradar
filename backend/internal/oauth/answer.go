package oauth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// fetchJSON runs one request and decodes the JSON answer. The address is part of
// an error, so a refusal in the log says which end refused it and what it said.
func fetchJSON(client *http.Client, req *http.Request, out any) error {
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", req.URL.Host, err)
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%s: read the answer: %w", req.URL.Host, err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("%s: %s answered %d: %s",
			req.URL.Host, req.URL.Path, res.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s: decode the answer: %w", req.URL.Host, err)
	}
	return nil
}
