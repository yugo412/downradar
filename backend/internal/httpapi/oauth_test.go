package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"downradar.info/backend/internal/oauth"
	"downradar.info/backend/internal/store"
)

// fakeProvider stands in for a real one: the exchange is the only part the
// handler cannot do itself, and a test decides what it says.
type fakeProvider struct {
	identity oauth.Identity
	err      error
}

func (p *fakeProvider) Name() string  { return "zz" }
func (p *fakeProvider) Label() string { return "Zed" }

func (p *fakeProvider) AuthorizeURL(state, redirectURI string) string {
	return "https://provider.invalid/authorize?state=" + url.QueryEscape(state) +
		"&redirect_uri=" + url.QueryEscape(redirectURI)
}

func (p *fakeProvider) Exchange(context.Context, string, string) (oauth.Identity, error) {
	if p.err != nil {
		return oauth.Identity{}, p.err
	}
	return p.identity, nil
}

func testProviderRouter(t *testing.T, provider oauth.Provider) (*store.Store, http.Handler) {
	t.Helper()

	st, router, _ := testRouterWithOptions(t, func(opts *Options) {
		opts.Providers = map[string]oauth.Provider{provider.Name(): provider}
		opts.PublicURL = "https://downradar.test"
	})
	return st, router
}

// startSignIn follows the first half of the flow and hands back what the browser
// would be holding: the state in the address and the cookie behind it.
func startSignIn(t *testing.T, router http.Handler, next string) (state, cookie string) {
	t.Helper()

	path := "/api/v1/auth/zz"
	if next != "" {
		path += "?next=" + url.QueryEscape(next)
	}
	rec := call(t, router, http.MethodGet, path, "", "")
	if rec.Code != http.StatusFound {
		t.Fatalf("start = %d, want a redirect: %s", rec.Code, rec.Body.String())
	}

	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse the provider url: %v", err)
	}
	if location.Host != "provider.invalid" {
		t.Errorf("start sent the browser to %s, not the provider", location)
	}
	if got := location.Query().Get("redirect_uri"); got != "https://downradar.test/api/v1/auth/zz/callback" {
		t.Errorf("redirect_uri = %q", got)
	}
	state = location.Query().Get("state")
	if state == "" {
		t.Fatal("no state in the provider url")
	}

	for _, c := range rec.Result().Cookies() {
		if c.Name == oauthStateCookie {
			cookie = c.Name + "=" + c.Value
		}
	}
	if cookie == "" {
		t.Fatal("no state cookie was set")
	}
	return state, cookie
}

// finishSignIn sends what the provider would send back.
func finishSignIn(t *testing.T, router http.Handler, state, code, cookie string) *http.Response {
	t.Helper()

	path := "/api/v1/auth/zz/callback?state=" + url.QueryEscape(state) + "&code=" + url.QueryEscape(code)
	return call(t, router, http.MethodGet, path, "", cookie).Result()
}

// startLink follows the first half of connecting a provider to a signed in
// account, keeping the session and the state the browser would hold.
func startLink(t *testing.T, router http.Handler, session string) (state, cookie string) {
	t.Helper()

	rec := call(t, router, http.MethodGet, "/api/v1/auth/zz/link", "", session)
	if rec.Code != http.StatusFound {
		t.Fatalf("start link = %d, want a redirect: %s", rec.Code, rec.Body.String())
	}

	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse the provider url: %v", err)
	}
	if location.Host != "provider.invalid" {
		t.Errorf("start link sent the browser to %s, not the provider", location)
	}
	state = location.Query().Get("state")
	if state == "" {
		t.Fatal("no state in the provider url")
	}

	for _, c := range rec.Result().Cookies() {
		if c.Name == oauthStateCookie {
			cookie = c.Name + "=" + c.Value
		}
	}
	if cookie == "" {
		t.Fatal("no state cookie was set")
	}
	return state, cookie
}

// finishLink sends what the provider would send back to the link callback.
func finishLink(t *testing.T, router http.Handler, state, code, session, stateCookie string) *http.Response {
	t.Helper()

	path := "/api/v1/auth/zz/callback?state=" + url.QueryEscape(state) + "&code=" + url.QueryEscape(code)
	return call(t, router, http.MethodGet, path, "", session+"; "+stateCookie).Result()
}

func wantLinkSuccess(t *testing.T, res *http.Response) {
	t.Helper()

	location, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse the redirect: %v", err)
	}
	if res.StatusCode != http.StatusFound || location.Path != "/account/logins" ||
		location.Query().Get("linked") != "zz" {
		t.Errorf("link callback = %d %q, want the account logins screen",
			res.StatusCode, res.Header.Get("Location"))
	}
}

func wantLinkFailure(t *testing.T, res *http.Response, reason string) {
	t.Helper()

	location, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse the redirect: %v", err)
	}
	if res.StatusCode != http.StatusFound || location.Path != "/account/logins" {
		t.Errorf("link callback = %d %q, want the account logins screen",
			res.StatusCode, res.Header.Get("Location"))
		return
	}
	if got := location.Query().Get("oauth"); got != reason {
		t.Errorf("reason = %q, want %q", got, reason)
	}
	if got := location.Query().Get("provider"); got != "zz" {
		t.Errorf("provider = %q, want the one that was used", got)
	}
}

// wantSignInFailure checks that the callback sent the browser back to the sign in
// screen with a reason, rather than leaving it on the callback address.
func wantSignInFailure(t *testing.T, res *http.Response, reason string) {
	t.Helper()

	location, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse the redirect: %v", err)
	}
	if res.StatusCode != http.StatusFound || location.Path != "/login" {
		t.Errorf("callback = %d %q, want the sign in screen", res.StatusCode, res.Header.Get("Location"))
		return
	}
	if got := location.Query().Get("oauth"); got != reason {
		t.Errorf("reason = %q, want %q", got, reason)
	}
	if got := location.Query().Get("provider"); got != "zz" {
		t.Errorf("provider = %q, want the one that was used", got)
	}
}

func sessionCookie(res *http.Response) string {
	for _, c := range res.Cookies() {
		if c.Name == "dr_session" && c.Value != "" {
			return c.Name + "=" + c.Value
		}
	}
	return ""
}

func TestSignInWithAProviderMakesAVerifiedSubscriber(t *testing.T) {
	email := fmt.Sprintf("zz-oauth-%d@example.invalid", time.Now().UnixNano())
	subject := fmt.Sprintf("subject-%d", time.Now().UnixNano())
	st, router := testProviderRouter(t, &fakeProvider{identity: oauth.Identity{
		Subject: subject, Email: email, Name: "Provider Person", Username: "provider-person",
	}})
	t.Cleanup(func() { dropUser(t, st, email) })

	state, cookie := startSignIn(t, router, "/alerts")
	res := finishSignIn(t, router, state, "the-code", cookie)

	if res.StatusCode != http.StatusFound {
		t.Fatalf("callback = %d, want a redirect", res.StatusCode)
	}
	if got := res.Header.Get("Location"); got != "/alerts" {
		t.Errorf("after signing in = %q, want the page that was asked for", got)
	}
	for _, c := range res.Cookies() {
		if c.Name == oauthStateCookie && c.MaxAge >= 0 {
			t.Error("the state cookie was not spent")
		}
	}

	session := sessionCookie(res)
	if session == "" {
		t.Fatal("signing in did not set a session")
	}

	// The session is the same one the password form issues, so the account
	// screen answers with the account.
	me := call(t, router, http.MethodGet, "/api/v1/auth/me", "", session)
	var body struct {
		User struct {
			Email      string     `json:"email"`
			Role       string     `json:"role"`
			VerifiedAt *time.Time `json:"verifiedAt"`
		} `json:"user"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.User.Email != email {
		t.Errorf("email = %q, want %q", body.User.Email, email)
	}
	if body.User.Role != "subscriber" {
		t.Errorf("role = %q, want subscriber", body.User.Role)
	}
	if body.User.VerifiedAt == nil {
		t.Error("the provider proved the address, so the account should be confirmed")
	}

	user, err := st.UserByProvider(context.Background(), "zz", subject)
	if err != nil {
		t.Fatalf("UserByProvider: %v", err)
	}
	if user.Email != email {
		t.Errorf("linked account = %q, want %q", user.Email, email)
	}
}

func TestSignInWithAProviderLinksTheAccountThatHasTheAddress(t *testing.T) {
	email := uniqueEmail()
	subject := fmt.Sprintf("subject-%d", time.Now().UnixNano())
	provider := &fakeProvider{identity: oauth.Identity{
		Subject: subject, Email: email, Name: "Existing Person",
	}}
	st, router := testProviderRouter(t, provider)
	t.Cleanup(func() { dropUser(t, st, email) })

	existing := testUser(t, st, "subscriber")
	if err := st.SetUserEmail(context.Background(), existing.ID, email); err != nil {
		t.Fatalf("SetUserEmail: %v", err)
	}

	state, cookie := startSignIn(t, router, "")
	res := finishSignIn(t, router, state, "the-code", cookie)
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/" {
		t.Fatalf("callback = %d %q, want a redirect to the site", res.StatusCode, res.Header.Get("Location"))
	}
	session := sessionCookie(res)
	if session == "" {
		t.Fatal("signing in did not set a session")
	}

	var me struct {
		User struct {
			ID int64 `json:"id"`
		} `json:"user"`
	}
	rec := call(t, router, http.MethodGet, "/api/v1/auth/me", "", session)
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if me.User.ID != existing.ID {
		t.Errorf("signed in as %d, want the account that has the address (%d)", me.User.ID, existing.ID)
	}

	// A second sign in finds the link by the provider's id, even when the
	// address it reports has moved on since.
	provider.identity.Email = "changed@example.invalid"
	state, cookie = startSignIn(t, router, "")
	res = finishSignIn(t, router, state, "the-code", cookie)
	if sessionCookie(res) == "" {
		t.Fatal("the second sign in did not set a session")
	}
	rec = call(t, router, http.MethodGet, "/api/v1/auth/me", "", sessionCookie(res))
	me.User.ID = 0
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if me.User.ID != existing.ID {
		t.Errorf("second sign in = %d, want %d", me.User.ID, existing.ID)
	}
}

func TestProviderCallbackRefusesAnAnswerThisBrowserDidNotAskFor(t *testing.T) {
	_, router := testProviderRouter(t, &fakeProvider{})

	for _, tc := range []struct {
		label string
		state func(string) string
		code  string
	}{
		{label: "another state", state: func(s string) string { return s + "x" }, code: "the-code"},
		{label: "no state at all", state: func(string) string { return "" }, code: "the-code"},
		{label: "no code", state: func(s string) string { return s }, code: ""},
	} {
		t.Run(tc.label, func(t *testing.T) {
			state, cookie := startSignIn(t, router, "")

			path := "/api/v1/auth/zz/callback?code=" + url.QueryEscape(tc.code)
			if sent := tc.state(state); sent != "" {
				path += "&state=" + url.QueryEscape(sent)
			}
			rec := call(t, router, http.MethodGet, path, "", cookie)
			wantSignInFailure(t, rec.Result(), "failed")
			for _, c := range rec.Result().Cookies() {
				if c.Name == "dr_session" && c.Value != "" {
					t.Error("a refused callback still signed somebody in")
				}
			}
		})
	}
}

func TestProviderCallbackRefusesADisabledAccount(t *testing.T) {
	email := uniqueEmail()
	subject := fmt.Sprintf("subject-%d", time.Now().UnixNano())
	st, router := testProviderRouter(t, &fakeProvider{identity: oauth.Identity{
		Subject: subject,
		Email:   email,
		Name:    "Disabled Person",
	}})
	t.Cleanup(func() { dropUser(t, st, email) })

	user := testUser(t, st, "subscriber")
	if err := st.SetUserEmail(context.Background(), user.ID, email); err != nil {
		t.Fatalf("SetUserEmail: %v", err)
	}
	if err := st.SetUserDisabled(context.Background(), user.ID, true); err != nil {
		t.Fatalf("SetUserDisabled: %v", err)
	}

	state, cookie := startSignIn(t, router, "")
	res := finishSignIn(t, router, state, "the-code", cookie)
	wantSignInFailure(t, res, "disabled")
	if sessionCookie(res) != "" {
		t.Error("a disabled account was signed in")
	}

	// Nothing about the account changed on the way to the refusal.
	if _, err := st.UserByProvider(context.Background(), "zz", subject); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a refused sign in linked a provider: %v", err)
	}
}

func TestProviderCallbackRefusesAnAddressOnOurOwnDomain(t *testing.T) {
	email := "someone@downradar.info"
	st, router := testProviderRouter(t, &fakeProvider{identity: oauth.Identity{
		Subject: fmt.Sprintf("subject-%d", time.Now().UnixNano()),
		Email:   email,
		Name:    "Own Domain",
	}})
	t.Cleanup(func() { dropUser(t, st, email) })

	state, cookie := startSignIn(t, router, "")
	res := finishSignIn(t, router, state, "the-code", cookie)
	wantSignInFailure(t, res, "failed")
	if sessionCookie(res) != "" {
		t.Error("an account was created on the site's own domain")
	}
	if _, err := st.UserByEmail(context.Background(), email); err == nil {
		t.Error("an account was created on the site's own domain")
	}
}

func TestProvidersListOffersOnlyWhatIsConfigured(t *testing.T) {
	_, router := testProviderRouter(t, &fakeProvider{})

	rec := call(t, router, http.MethodGet, "/api/v1/auth/providers", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("providers = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	// It decides whether a button exists, so a cached copy would keep offering a
	// provider that was just turned off.
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("cache-control = %q, want the list uncached", got)
	}
	var body struct {
		Providers []struct {
			Name  string `json:"name"`
			Label string `json:"label"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Providers) != 1 || body.Providers[0].Name != "zz" || body.Providers[0].Label != "Zed" {
		t.Errorf("providers = %+v, want the one that is configured", body.Providers)
	}

	// A site with no provider configured offers none, and the route still reads
	// as an empty list rather than a missing answer.
	_, plainRouter := testRouter(t)
	rec = call(t, plainRouter, http.MethodGet, "/api/v1/auth/providers", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("providers = %d, want 200", rec.Code)
	}
	body.Providers = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Providers == nil || len(body.Providers) != 0 {
		t.Errorf("providers = %+v, want an empty list", body.Providers)
	}
}

func TestAnUnconfiguredProviderCannotBeUsed(t *testing.T) {
	_, router := testRouter(t)

	rec := call(t, router, http.MethodGet, "/api/v1/auth/github", "", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("start = %d, want 404", rec.Code)
	}
}

func TestLinkingAProviderAttachesItToTheSignedInAccount(t *testing.T) {
	email := uniqueEmail()
	subject := fmt.Sprintf("subject-%d", time.Now().UnixNano())
	st, router := testProviderRouter(t, &fakeProvider{identity: oauth.Identity{
		Subject: subject, Email: email, Name: "Linked Person", Username: "linked-person",
	}})
	t.Cleanup(func() { dropUser(t, st, email) })

	current := testUser(t, st, "subscriber")
	session := sessionFor(t, st, current)

	var list struct {
		HasPassword bool `json:"hasPassword"`
		Providers   []struct {
			Name      string `json:"name"`
			Label     string `json:"label"`
			Connected bool   `json:"connected"`
			Email     string `json:"email"`
			Username  string `json:"username"`
		} `json:"providers"`
	}
	rec := call(t, router, http.MethodGet, "/api/v1/auth/logins", "", session)
	if rec.Code != http.StatusOK {
		t.Fatalf("logins = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !list.HasPassword || len(list.Providers) != 1 || list.Providers[0].Connected {
		t.Fatalf("logins = %+v, want one provider that is not connected", list)
	}

	state, stateCookie := startLink(t, router, session)
	res := finishLink(t, router, state, "the-code", session, stateCookie)
	wantLinkSuccess(t, res)
	for _, c := range res.Cookies() {
		if c.Name == "dr_session" && c.Value != "" {
			t.Error("linking a provider started a session")
		}
	}

	linked, err := st.UserByProvider(context.Background(), "zz", subject)
	if err != nil {
		t.Fatalf("UserByProvider: %v", err)
	}
	if linked.ID != current.ID {
		t.Errorf("linked account = %d, want the signed in account (%d)", linked.ID, current.ID)
	}
	if linked.Email != current.Email {
		t.Errorf("account address = %q, want it left at %q", linked.Email, current.Email)
	}

	list.Providers = nil
	rec = call(t, router, http.MethodGet, "/api/v1/auth/logins", "", session)
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list.Providers) != 1 {
		t.Fatalf("logins = %+v, want the configured provider", list.Providers)
	}
	got := list.Providers[0]
	if !got.Connected || got.Name != "zz" || got.Label != "Zed" ||
		got.Email != email || got.Username != "linked-person" {
		t.Errorf("provider = %+v, want the identity just linked", got)
	}
}

func TestLinkingRefusesAProviderThatBelongsToAnotherAccount(t *testing.T) {
	subject := fmt.Sprintf("subject-%d", time.Now().UnixNano())
	provider := &fakeProvider{}
	st, router := testProviderRouter(t, provider)
	ctx := context.Background()

	current := testUser(t, st, "subscriber")
	other := testUser(t, st, "subscriber")
	session := sessionFor(t, st, current)

	// The provider proves an address that already belongs to another account.
	// Connecting it here would merge two accounts, so it is refused instead.
	provider.identity = oauth.Identity{
		Subject: subject, Email: other.Email, Name: "Other Person", Username: "other",
	}
	state, stateCookie := startLink(t, router, session)
	res := finishLink(t, router, state, "the-code", session, stateCookie)
	wantLinkFailure(t, res, "taken")
	if _, err := st.UserByProvider(ctx, "zz", subject); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a refused link attached the identity anyway: %v", err)
	}
	if links, err := st.UserProviders(ctx, current.ID); err != nil || len(links) != 0 {
		t.Errorf("links = %+v, err = %v, want the signed in account untouched", links, err)
	}

	// An identity already attached elsewhere is refused for the same reason.
	linkedSubject := fmt.Sprintf("subject-%d", time.Now().UnixNano())
	if err := st.LinkProvider(ctx, other.ID, "zz", linkedSubject,
		"linked@example.com", "other"); err != nil {
		t.Fatalf("LinkProvider other: %v", err)
	}
	provider.identity = oauth.Identity{
		Subject: linkedSubject, Email: uniqueEmail(), Name: "Other Person", Username: "other",
	}
	state, stateCookie = startLink(t, router, session)
	res = finishLink(t, router, state, "the-code", session, stateCookie)
	wantLinkFailure(t, res, "taken")
	if links, err := st.UserProviders(ctx, current.ID); err != nil || len(links) != 0 {
		t.Errorf("links = %+v, err = %v, want no link on the signed in account", links, err)
	}
	linked, err := st.UserByProvider(ctx, "zz", linkedSubject)
	if err != nil {
		t.Fatalf("the other account lost its link: %v", err)
	}
	if linked.ID != other.ID {
		t.Errorf("linked account = %d, want %d", linked.ID, other.ID)
	}
}

func TestTheLastLoginCannotBeRemovedWithoutAPassword(t *testing.T) {
	st, router := testProviderRouter(t, &fakeProvider{})
	ctx := context.Background()

	email := uniqueEmail()
	t.Cleanup(func() { dropUser(t, st, email) })
	account, err := st.CreateUser(ctx, email, "No Password", "subscriber", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.LinkProvider(ctx, account.ID, "zz", "the-subject", "zz@example.com", "login"); err != nil {
		t.Fatalf("LinkProvider: %v", err)
	}
	session := sessionFor(t, st, account)

	for _, tc := range []struct {
		label  string
		method string
		path   string
	}{
		{"list without a session", http.MethodGet, "/api/v1/auth/logins"},
		{"remove without a session", http.MethodDelete, "/api/v1/auth/logins/zz"},
		{"link without a session", http.MethodGet, "/api/v1/auth/zz/link"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			if rec := call(t, router, tc.method, tc.path, "", ""); rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
		})
	}

	var list struct {
		HasPassword bool `json:"hasPassword"`
		Providers   []struct {
			Connected bool `json:"connected"`
		} `json:"providers"`
	}
	rec := call(t, router, http.MethodGet, "/api/v1/auth/logins", "", session)
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if list.HasPassword || len(list.Providers) != 1 || !list.Providers[0].Connected {
		t.Fatalf("logins = %+v, want the only login connected on an account with no password", list)
	}

	rec = call(t, router, http.MethodDelete, "/api/v1/auth/logins/zz", "", session)
	if rec.Code != http.StatusConflict {
		t.Fatalf("remove the only login = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "set a password") {
		t.Errorf("answer = %s, want it to ask for a password", rec.Body.String())
	}
	if links, err := st.UserProviders(ctx, account.ID); err != nil || len(links) != 1 {
		t.Errorf("links = %+v, err = %v, want the login left alone", links, err)
	}

	if err := st.SetUserPassword(ctx, account.ID, "a-hash"); err != nil {
		t.Fatalf("SetUserPassword: %v", err)
	}
	rec = call(t, router, http.MethodDelete, "/api/v1/auth/logins/zz", "", session)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove after setting a password = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, router, http.MethodDelete, "/api/v1/auth/logins/zz", "", session); rec.Code != http.StatusNotFound {
		t.Errorf("remove it twice = %d, want 404", rec.Code)
	}

	list.Providers = nil
	rec = call(t, router, http.MethodGet, "/api/v1/auth/logins", "", session)
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !list.HasPassword || len(list.Providers) != 1 || list.Providers[0].Connected {
		t.Errorf("logins = %+v, want the provider no longer connected", list)
	}
}
