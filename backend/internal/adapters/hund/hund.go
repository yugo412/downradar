// Package hund reads status pages hosted by Hund.io. The page is rendered on
// the server, so its state bar, component groups and incident notices are read
// from the html.
package hund

import (
	"context"
	"fmt"
	"hash/fnv"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"downradar.info/backend/internal/adapters"
)

const KindHund = "hund"

// Hund names a component's state in the class of its icon: operational,
// degraded, outage or maintenance. The same words appear on the page's overall
// state bar. A scheduled maintenance counts as maintenance even before it
// starts.
var hundState = map[string]adapters.Health{
	"operational": adapters.HealthOK,
	"degraded":    adapters.HealthDegraded,
	"partial":     adapters.HealthPartial,
	"outage":      adapters.HealthMajor,
	"maintenance": adapters.HealthMaintenance,
	"pending":     adapters.HealthMaintenance,
}

// The label is only a fallback for a state class this adapter does not know.
var hundLabel = map[string]adapters.Health{
	"operating normally":    adapters.HealthOK,
	"operational":           adapters.HealthOK,
	"degraded performance":  adapters.HealthDegraded,
	"partial outage":        adapters.HealthPartial,
	"outage":                adapters.HealthMajor,
	"major outage":          adapters.HealthMajor,
	"experiencing downtime": adapters.HealthMajor,
	"under maintenance":     adapters.HealthMaintenance,
	"maintenance":           adapters.HealthMaintenance,
}

var (
	stateBarStart = regexp.MustCompile(`<aside[^>]*class=['"]([^'"]*\bstate-bar\b[^'"]*)['"][^>]*>`)
	groupsMarker  = regexp.MustCompile(`<section[^>]*class=['"][^'"]*\bgroups\b[^'"]*['"]`)
	groupStart    = regexp.MustCompile(`<section[^>]*class=['"]([^'"]*\bgroup\b[^'"]*)['"][^>]*>`)
	groupName     = regexp.MustCompile(`(?s)<header[^>]*>.*?<h2[^>]*>(.*?)</h2>`)
	itemStart     = regexp.MustCompile(`<div[^>]*class=['"][^'"]*\bitem\b[^'"]*['"][^>]*>`)

	componentLink = regexp.MustCompile(
		`(?s)<span[^>]*class=['"][^'"]*\bname\b[^'"]*['"][^>]*>\s*<a[^>]*href=['"]([^'"]*/components/([^'"/#?]+))['"][^>]*>(.*?)</a>`)
	spanTag = regexp.MustCompile(`<span[^>]*>`)

	historyStart = regexp.MustCompile(`<section[^>]*class=['"][^'"]*\bhistory\b[^'"]*['"]`)
	historyEvent = regexp.MustCompile(`<div[^>]*class=['"]([^'"]*)['"][^>]*>`)
	issueTitle   = regexp.MustCompile(
		`(?s)<span[^>]*class=['"][^'"]*\btitle\b[^'"]*['"][^>]*>\s*<a[^>]*href=['"]([^'"]+)['"][^>]*>(.*?)</a>`)
	plainTitle = regexp.MustCompile(
		`(?s)<span[^>]*class=['"][^'"]*\btitle\b[^'"]*['"][^>]*>(.*?)</span>`)

	noticeStart = regexp.MustCompile(
		`<div[^>]*class=['"][^'"]*\bissue-notice--wrapper\b[^'"]*['"][^>]*>`)
	noticeHeader = regexp.MustCompile(`<div[^>]*class=['"]([^'"]*\bissue-notice--header\b[^'"]*)['"]`)
	noticeTitle  = regexp.MustCompile(
		`(?s)<div[^>]*class=['"][^'"]*issue-notice--header__title[^'"]*['"][^>]*>\s*<a[^>]*href=['"]([^'"]+)['"][^>]*>(.*?)</a>`)
	noticeLabel = regexp.MustCompile(
		`(?s)<div[^>]*class=['"][^'"]*issue-notice--header__label[^'"]*['"][^>]*>(.*?)</div>`)
	noticeDate = regexp.MustCompile(
		`(?s)<div[^>]*class=['"][^'"]*issue-notice--content__date[^'"]*['"][^>]*>\s*(<time[^>]*>)`)

	barLabel = regexp.MustCompile(`(?s)<span[^>]*class=['"][^'"]*\bstatus\b[^'"]*['"][^>]*>(.*?)</span>`)

	classAttr    = regexp.MustCompile(`class=['"]([^'"]*)['"]`)
	titleAttr    = regexp.MustCompile(`title=['"]([^'"]*)['"]`)
	datetimeAttr = regexp.MustCompile(`datetime=['"]([^'"]*)['"]`)
	toTimeAttr   = regexp.MustCompile(`to_time=['"]([^'"]*)['"]`)
	timeTag      = regexp.MustCompile(`<time[^>]*>`)
	tagPattern   = regexp.MustCompile(`<[^>]+>`)
)

// statusTokens is the order an issue's state is read in. An issue can carry
// several of them, and the more specific state wins over the kind of issue.
var statusTokens = []string{
	"investigating", "identified", "monitoring", "resolved",
	"scheduled", "upcoming", "assessed", "informational", "update",
	"maintenance", "outage", "degraded",
}

type Hund struct {
	client *http.Client
}

func NewHund(client *http.Client) *Hund {
	if client == nil {
		client = adapters.DefaultClient()
	}
	return &Hund{client: client}
}

func (h *Hund) Kind() string { return KindHund }

func (h *Hund) Fetch(ctx context.Context, t adapters.Target) (adapters.Report, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.URL, nil)
	if err != nil {
		return adapters.Report{}, fmt.Errorf("hund: build request: %w", err)
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", adapters.UserAgent)

	resp, err := h.client.Do(req)
	if err != nil {
		return adapters.Report{}, fmt.Errorf("hund: request %s: %w", t.URL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return adapters.Report{}, fmt.Errorf("hund: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return adapters.Report{HTTPStatus: resp.StatusCode, RetryAfter: adapters.RetryAfter(resp), Raw: body},
			fmt.Errorf("hund: %s returned HTTP %d", t.URL, resp.StatusCode)
	}

	// The state bar and the groups together are what make this page a Hund page.
	// A page that is not one has neither, and the detect queue moves on.
	bar := stateBarStart.FindSubmatch(body)
	if bar == nil || !groupsMarker.Match(body) {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("hund: %s is not a Hund.io page", t.URL)
	}

	rep := adapters.Report{
		FetchedAt:  time.Now().UTC(),
		HTTPStatus: resp.StatusCode,
		Raw:        body,
		PageID:     pageID(t.URL),
	}
	if updated := firstTime(body); updated != "" {
		if at, err := time.Parse(time.RFC3339, updated); err == nil {
			rep.SourceUpdatedAt = at.UTC()
		}
	}

	include := adapters.LowerSet(t.Include)
	position := 0
	var statuses []adapters.Health

	for _, group := range blocks(body, groupStart) {
		class := classOf(group)
		if !hasToken(class, "group") {
			continue
		}
		name := groupName.FindSubmatch(group)
		groupNameText := ""
		if name != nil {
			groupNameText = cleanText(string(name[1]))
		}

		for _, item := range blocks(group, itemStart) {
			link := componentLink.FindSubmatch(item)
			if link == nil {
				continue
			}
			id := string(link[2])
			componentName := cleanText(string(link[3]))
			if id == "" || componentName == "" {
				continue
			}

			health := componentHealth(item)
			statuses = append(statuses, health)
			position++
			if len(include) > 0 && !include[strings.ToLower(componentName)] {
				continue
			}

			rep.Components = append(rep.Components, adapters.ComponentStatus{
				SourceID: id,
				Name:     componentName,
				Group:    groupNameText,
				Position: position,
				Status:   health,
			})
		}
	}

	if len(rep.Components) == 0 && position == 0 {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("hund: %s lists no components", t.URL)
	}

	// The state bar is what the page itself shows at the top, so it is the
	// overall state. Its class is the reliable part; the label is a fallback.
	global, ok := healthFromClass(string(bar[1]))
	if !ok {
		if label := barLabel.FindSubmatch(body); label != nil {
			global, ok = hundLabel[strings.ToLower(cleanText(string(label[1])))]
		}
	}
	if !ok {
		global = adapters.Worst(statuses...)
	}
	rep.Global = global

	rep.Incidents = incidents(body, t.URL)
	return rep, nil
}

func incidents(body []byte, rawURL string) []adapters.Incident {
	base, _ := url.Parse(rawURL)
	var out []adapters.Incident

	// An open issue sits in its own notice at the top of the page, above the
	// groups. It carries the current state and the time of its latest update.
	for _, notice := range blocks(body, noticeStart) {
		title := noticeTitle.FindSubmatch(notice)
		if title == nil {
			continue
		}
		id := issueID(string(title[1]))
		if id == "" {
			continue
		}

		status := statusOf(classOfNotice(notice))
		if status == "" {
			if label := noticeLabel.FindSubmatch(notice); label != nil {
				status = strings.ToLower(cleanText(string(label[1])))
			}
		}
		if status == "" {
			status = "investigating"
		}

		item := adapters.Incident{
			SourceID: id,
			Name:     cleanText(string(title[2])),
			Status:   status,
			Impact:   impactOf(classOfNotice(notice)),
			URL:      absoluteURL(base, string(title[1])),
		}
		if tag := noticeDate.FindSubmatch(notice); tag != nil {
			item.StartedAt = parseTime(attr(datetimeAttr, tag[1]))
		}
		out = append(out, item)
	}

	// What has already closed is in the history block, with both its start and
	// its end on the time element. An outage the platform recorded itself has no
	// issue link, while a written issue does; both are read.
	for _, history := range blocks(body, historyStart) {
		for _, issue := range blocks(history, historyEvent) {
			class := classOf(issue)
			if !isHistoryEvent(class) {
				continue
			}

			title := issueTitle.FindSubmatch(issue)
			name := ""
			href := ""
			if title != nil {
				href = string(title[1])
				name = cleanText(string(title[2]))
			} else if plain := plainTitle.FindSubmatch(issue); plain != nil {
				name = cleanText(string(plain[1]))
			}
			if name == "" {
				continue
			}

			tag := firstTimeTag(issue)
			var started, ended time.Time
			if tag != nil {
				started = parseTime(attr(datetimeAttr, tag))
				ended = parseTime(attr(toTimeAttr, tag))
			}

			id := issueID(href)
			if id == "" {
				id = historyID(name, started)
			}

			item := adapters.Incident{
				SourceID: id,
				Name:     name,
				Status:   historyStatus(class, !ended.IsZero()),
				Impact:   impactOf(class),
			}
			if href != "" {
				item.URL = absoluteURL(base, href)
			}
			item.StartedAt = started
			if !ended.IsZero() {
				item.ResolvedAt = &ended
			}
			out = append(out, item)
		}
	}

	return out
}

func isHistoryEvent(class string) bool {
	return hasToken(class, "issue") || hasToken(class, "outage") ||
		hasToken(class, "maintenance") || hasToken(class, "informational") ||
		hasToken(class, "upcoming")
}

func historyStatus(class string, hasEnd bool) string {
	switch {
	case hasToken(class, "resolved"):
		return "resolved"
	case hasToken(class, "scheduled"), hasToken(class, "upcoming"):
		return "scheduled"
	case hasToken(class, "informational"):
		return "informational"
	case hasToken(class, "assessed"):
		return "assessed"
	case hasEnd:
		return "resolved"
	default:
		return statusOf(class)
	}
}

func componentHealth(item []byte) adapters.Health {
	for _, tag := range spanTag.FindAll(item, -1) {
		class := attr(classAttr, tag)
		if !hasToken(class, "state") {
			continue
		}
		if health, ok := healthFromClass(class); ok {
			return health
		}
		if health, ok := hundLabel[strings.ToLower(cleanText(attr(titleAttr, tag)))]; ok {
			return health
		}
		return adapters.HealthUnknown
	}
	return adapters.HealthUnknown
}

func healthFromClass(class string) (adapters.Health, bool) {
	for _, token := range strings.Fields(class) {
		if health, ok := hundState[token]; ok {
			return health, true
		}
	}
	return adapters.HealthUnknown, false
}

func statusOf(class string) string {
	for _, wanted := range statusTokens {
		for _, token := range strings.Fields(class) {
			if token == wanted {
				return wanted
			}
		}
	}
	return ""
}

func impactOf(class string) string {
	switch {
	case hasToken(class, "severity-outage"):
		return "major"
	case hasToken(class, "severity-degraded"):
		return "degraded"
	case hasToken(class, "severity-pending"):
		return "maintenance"
	case hasToken(class, "maintenance"):
		return "maintenance"
	case hasToken(class, "outage"):
		return "major"
	case hasToken(class, "degraded"):
		return "degraded"
	case hasToken(class, "informational"):
		return "informational"
	default:
		return ""
	}
}

func classOfNotice(notice []byte) string {
	if header := noticeHeader.FindSubmatch(notice); header != nil {
		return string(header[1])
	}
	return ""
}

// blocks slices a document at every start marker, so a block runs until the
// next thing of the same kind. Nested sections are common on these pages, so
// matching a closing tag would stop at the wrong one.
func blocks(body []byte, start *regexp.Regexp) [][]byte {
	locs := start.FindAllIndex(body, -1)
	if len(locs) == 0 {
		return nil
	}
	out := make([][]byte, 0, len(locs))
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		out = append(out, body[loc[0]:end])
	}
	return out
}

func classOf(block []byte) string {
	return attr(classAttr, block)
}

func attr(pattern *regexp.Regexp, block []byte) string {
	match := pattern.FindSubmatch(block)
	if match == nil {
		return ""
	}
	return string(match[1])
}

func hasToken(class, token string) bool {
	for _, field := range strings.Fields(class) {
		if field == token {
			return true
		}
	}
	return false
}

func firstTime(body []byte) string {
	tag := firstTimeTag(body)
	if tag == nil {
		return ""
	}
	return attr(datetimeAttr, tag)
}

func firstTimeTag(body []byte) []byte {
	return timeTag.Find(body)
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return at.UTC()
}

func cleanText(value string) string {
	return strings.TrimSpace(html.UnescapeString(tagPattern.ReplaceAllString(value, "")))
}

func historyID(name string, started time.Time) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	_, _ = h.Write([]byte(started.UTC().Format(time.RFC3339)))
	return fmt.Sprintf("history-%08x", h.Sum32())
}

func issueID(href string) string {
	const marker = "/issues/"
	i := strings.LastIndex(href, marker)
	if i < 0 {
		return ""
	}
	id := href[i+len(marker):]
	if j := strings.IndexAny(id, "/?#"); j >= 0 {
		id = id[:j]
	}
	return id
}

func absoluteURL(base *url.URL, href string) string {
	if base == nil {
		return href
	}
	ref, err := url.Parse(href)
	if err != nil {
		return href
	}
	return base.ResolveReference(ref).String()
}

func pageID(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Host
}

func init() {
	adapters.Register(NewHund(nil))
}
