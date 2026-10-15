package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const KindFlashDuty = "flashduty"

var flashdutyStatus = map[string]Health{
	"operational":    HealthOK,
	"degraded":       HealthDegraded,
	"partial_outage": HealthPartial,
	"full_outage":    HealthMajor,
	"maintenance":    HealthMaintenance,
}

var rscChunkPattern = regexp.MustCompile(`self\.__next_f\.push\(\[1,"((?:[^"\\]|\\.)*)"\]\)`)

type FlashDuty struct {
	client *http.Client
}

func NewFlashDuty(client *http.Client) *FlashDuty {
	if client == nil {
		client = DefaultClient()
	}
	return &FlashDuty{client: client}
}

func (f *FlashDuty) Kind() string { return KindFlashDuty }

type fdPage struct {
	PageID     int64         `json:"page_id"`
	Name       string        `json:"name"`
	URLName    string        `json:"url_name"`
	Components []fdComponent `json:"components"`
	Sections   []fdSection   `json:"sections"`
}

type fdComponent struct {
	ComponentID string `json:"component_id"`
	SectionID   string `json:"section_id"`
	Name        string `json:"name"`
	OrderID     int    `json:"order_id"`
}

type fdSection struct {
	SectionID string `json:"section_id"`
	Name      string `json:"name"`
	OrderID   int    `json:"order_id"`
}

type fdChange struct {
	ChangeID           int64        `json:"change_id"`
	Type               string       `json:"type"`
	Title              string       `json:"title"`
	Status             string       `json:"status"`
	AffectedComponents []fdAffected `json:"affected_components"`
	StartAtSeconds     int64        `json:"start_at_seconds"`
	CloseAtSeconds     *int64       `json:"close_at_seconds"`
	Updates            []fdUpdate   `json:"updates"`
}

type fdAffected struct {
	ComponentID string `json:"component_id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
}

type fdUpdate struct {
	UpdateID         string              `json:"update_id"`
	AtSeconds        int64               `json:"at_seconds"`
	Status           string              `json:"status"`
	ComponentChanges []fdComponentChange `json:"component_changes"`
}

type fdComponentChange struct {
	ComponentID   string `json:"component_id"`
	ComponentName string `json:"component_name"`
	Status        string `json:"status"`
}

func (f *FlashDuty) Fetch(ctx context.Context, t Target) (Report, error) {
	url := strings.TrimRight(t.URL, "/")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Report{}, fmt.Errorf("flashduty: build request: %w", err)
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", UserAgent)

	resp, err := f.client.Do(req)
	if err != nil {
		return Report{}, fmt.Errorf("flashduty: request %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return Report{}, fmt.Errorf("flashduty: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("flashduty: %s returned HTTP %d", url, resp.StatusCode)
	}

	payload, err := rscPayload(body)
	if err != nil {
		return Report{HTTPStatus: resp.StatusCode, Raw: body}, fmt.Errorf("flashduty: %s: %w", url, err)
	}

	configRaw, ok := extractJSONValue(payload, `"initialPageConfig":`)
	if !ok {
		return Report{HTTPStatus: resp.StatusCode, Raw: []byte(payload)},
			fmt.Errorf("flashduty: %s: initialPageConfig not found", url)
	}
	var page fdPage
	if err := json.Unmarshal([]byte(configRaw), &page); err != nil {
		return Report{HTTPStatus: resp.StatusCode, Raw: []byte(payload)},
			fmt.Errorf("flashduty: %s: decode page config: %w", url, err)
	}
	if page.PageID == 0 {
		return Report{HTTPStatus: resp.StatusCode, Raw: []byte(payload)},
			fmt.Errorf("flashduty: %s: page_id is empty", url)
	}
	if len(page.Components) == 0 {
		return Report{HTTPStatus: resp.StatusCode, Raw: []byte(payload)},
			fmt.Errorf("flashduty: %s: page reports no components", url)
	}

	changesRaw, ok := extractJSONValue(payload, `"active_changes":`)
	if !ok {
		return Report{HTTPStatus: resp.StatusCode, Raw: []byte(payload)},
			fmt.Errorf("flashduty: %s: active_changes not found", url)
	}
	var changes []fdChange
	if err := json.Unmarshal([]byte(changesRaw), &changes); err != nil {
		return Report{HTTPStatus: resp.StatusCode, Raw: []byte(payload)},
			fmt.Errorf("flashduty: %s: decode active_changes: %w", url, err)
	}

	rep := Report{
		FetchedAt:  time.Now().UTC(),
		HTTPStatus: resp.StatusCode,
		Raw:        []byte(payload),
	}

	sections := make(map[string]string, len(page.Sections))
	sectionOrder := make(map[string]int, len(page.Sections))
	for _, s := range page.Sections {
		sections[s.SectionID] = s.Name
		sectionOrder[s.SectionID] = s.OrderID
	}

	current := make(map[string]Health)
	for _, ch := range changes {
		if len(ch.Updates) == 0 {
			continue
		}
		last := ch.Updates[len(ch.Updates)-1]
		for _, cc := range last.ComponentChanges {
			current[cc.ComponentID] = flashdutyHealth(cc.Status)
		}
	}
	for _, ch := range changes {
		for _, a := range ch.AffectedComponents {
			current[a.ComponentID] = flashdutyHealth(a.Status)
		}
	}

	statuses := make([]Health, 0, len(page.Components))
	for _, c := range page.Components {
		health := HealthOK
		if h, ok := current[c.ComponentID]; ok {
			health = h
		}
		rep.Components = append(rep.Components, ComponentStatus{
			SourceID: c.ComponentID,
			Name:     c.Name,
			Group:    sections[c.SectionID],
			Position: compositePosition(sectionOrder[c.SectionID], c.OrderID),
			Status:   health,
		})
		statuses = append(statuses, health)
	}
	rep.Global = Worst(statuses...)

	for _, ch := range changes {
		inc := Incident{
			SourceID:  strconv.FormatInt(ch.ChangeID, 10),
			Name:      ch.Title,
			Status:    ch.Status,
			URL:       url,
			StartedAt: time.Unix(ch.StartAtSeconds, 0).UTC(),
		}
		if ch.CloseAtSeconds != nil && *ch.CloseAtSeconds > 0 {
			resolved := time.Unix(*ch.CloseAtSeconds, 0).UTC()
			inc.ResolvedAt = &resolved
		}

		impacts := make([]Health, 0, len(ch.AffectedComponents))
		for _, a := range ch.AffectedComponents {
			impacts = append(impacts, flashdutyHealth(a.Status))
		}
		if len(impacts) > 0 {
			inc.Impact = string(Worst(impacts...))
		} else if ch.Type != "" {
			inc.Impact = ch.Type
		}

		rep.Incidents = append(rep.Incidents, inc)
	}

	return rep, nil
}

func flashdutyHealth(status string) Health {
	if h, ok := flashdutyStatus[status]; ok {
		return h
	}
	return HealthUnknown
}

func compositePosition(sectionOrder, componentOrder int) int {
	if sectionOrder <= 0 {
		return componentOrder
	}
	return sectionOrder*1000 + componentOrder
}

func rscPayload(html []byte) (string, error) {
	matches := rscChunkPattern.FindAllSubmatch(html, -1)
	if len(matches) == 0 {
		return "", fmt.Errorf("no next.js rsc payload found")
	}
	var sb strings.Builder
	for _, m := range matches {
		chunk, err := unescapeJSString(string(m[1]))
		if err != nil {
			return "", fmt.Errorf("decode rsc chunk: %w", err)
		}
		sb.WriteString(chunk)
	}
	return sb.String(), nil
}

func unescapeJSString(s string) (string, error) {
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			sb.WriteByte(s[i])
			continue
		}
		i++
		if i >= len(s) {
			return "", fmt.Errorf("trailing backslash")
		}
		switch s[i] {
		case 'n':
			sb.WriteByte('\n')
		case 'r':
			sb.WriteByte('\r')
		case 't':
			sb.WriteByte('\t')
		case 'b':
			sb.WriteByte('\b')
		case 'f':
			sb.WriteByte('\f')
		case '"':
			sb.WriteByte('"')
		case '\\':
			sb.WriteByte('\\')
		case '/':
			sb.WriteByte('/')
		case 'u':
			if i+4 >= len(s) {
				return "", fmt.Errorf("truncated unicode escape")
			}
			n, err := strconv.ParseUint(s[i+1:i+5], 16, 32)
			if err != nil {
				return "", fmt.Errorf("invalid unicode escape: %w", err)
			}
			sb.WriteRune(rune(n))
			i += 4
		default:
			return "", fmt.Errorf("unsupported escape \\%c", s[i])
		}
	}
	return sb.String(), nil
}

func extractJSONValue(raw, key string) (string, bool) {
	idx := strings.Index(raw, key)
	if idx < 0 {
		return "", false
	}
	start := idx + len(key)
	for start < len(raw) && (raw[start] == ' ' || raw[start] == '\t' || raw[start] == '\n' || raw[start] == '\r') {
		start++
	}
	if start >= len(raw) {
		return "", false
	}

	var close byte
	switch raw[start] {
	case '{':
		close = '}'
	case '[':
		close = ']'
	default:
		return "", false
	}
	open := raw[start]

	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(raw); i++ {
		c := raw[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch c {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return raw[start : i+1], true
			}
		}
	}
	return "", false
}

func init() {
	Register(NewFlashDuty(nil))
}
