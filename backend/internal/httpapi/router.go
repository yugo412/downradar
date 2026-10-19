package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"downradar.info/backend/internal/account"
	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/catalog"
	"downradar.info/backend/internal/contact"
	"downradar.info/backend/internal/detect"
	"downradar.info/backend/internal/notify"
	"downradar.info/backend/internal/store"
)

type handlers struct {
	store           *store.Store
	log             *slog.Logger
	catalog         catalog.Catalog
	detector        *detect.Detector
	accountMailer   *account.Mailer
	contactMailer   *contact.Mailer
	notifier        *notify.Notifier
	readLimiter     *ipLimiter
	suggestLimiter  *ipLimiter
	alertLimiter    *ipLimiter
	accountLimiter  *ipLimiter
	contactLimiter  *ipLimiter
	loginLimiter    *ipLimiter
	passwordLimiter *ipLimiter
	internalToken   string
	sessionCookie   string
	sessionTTL      time.Duration
	sessionSecure   bool
	alertsEnabled   bool
	alertLimit      int
	dummyHash       string
}

type Options struct {
	RateLimitPerMinute      int
	RateLimitBurst          int
	SuggestPerHour          int
	SuggestBurst            int
	AlertPerHour            int
	AlertBurst              int
	AlertLimitPerSubscriber int
	AccountPerHour          int
	AccountBurst            int
	ContactPerHour          int
	ContactBurst            int
	LoginPerHour            int
	LoginBurst              int
	PasswordPerHour         int
	PasswordBurst           int
	InternalToken           string
	Catalog                 catalog.Catalog
	AccountMailer           *account.Mailer
	ContactMailer           *contact.Mailer
	Notifier                *notify.Notifier
	SessionCookie           string
	SessionTTL              time.Duration
	SessionCookieSecure     bool
	AlertsEnabled           bool
}

func NewRouter(st *store.Store, log *slog.Logger, opts Options) http.Handler {
	readInterval := time.Minute
	if opts.RateLimitPerMinute > 0 {
		readInterval = time.Minute / time.Duration(opts.RateLimitPerMinute)
	}

	suggestInterval := time.Hour
	if opts.SuggestPerHour > 0 {
		suggestInterval = time.Hour / time.Duration(opts.SuggestPerHour)
	}

	alertInterval := time.Hour
	if opts.AlertPerHour > 0 {
		alertInterval = time.Hour / time.Duration(opts.AlertPerHour)
	}

	accountInterval := time.Hour
	if opts.AccountPerHour > 0 {
		accountInterval = time.Hour / time.Duration(opts.AccountPerHour)
	}

	contactInterval := time.Hour
	if opts.ContactPerHour > 0 {
		contactInterval = time.Hour / time.Duration(opts.ContactPerHour)
	}

	loginInterval := time.Hour
	if opts.LoginPerHour > 0 {
		loginInterval = time.Hour / time.Duration(opts.LoginPerHour)
	}

	passwordInterval := time.Hour
	if opts.PasswordPerHour > 0 {
		passwordInterval = time.Hour / time.Duration(opts.PasswordPerHour)
	}

	cookieName := opts.SessionCookie
	if cookieName == "" {
		cookieName = "dr_session"
	}
	sessionTTL := opts.SessionTTL
	if sessionTTL <= 0 {
		sessionTTL = 30 * 24 * time.Hour
	}

	// A subscriber may keep a handful of alerts; guests get one and staff are not
	// capped. Zero means the default rather than no limit, the same way the rate
	// limits above read it.
	alertLimit := opts.AlertLimitPerSubscriber
	if alertLimit <= 0 {
		alertLimit = 10
	}

	h := &handlers{
		store:           st,
		log:             log,
		catalog:         opts.Catalog,
		detector:        detect.New(opts.Catalog),
		accountMailer:   opts.AccountMailer,
		contactMailer:   opts.ContactMailer,
		notifier:        opts.Notifier,
		readLimiter:     newIPLimiter(readInterval, opts.RateLimitBurst),
		suggestLimiter:  newIPLimiter(suggestInterval, opts.SuggestBurst),
		alertLimiter:    newIPLimiter(alertInterval, opts.AlertBurst),
		accountLimiter:  newIPLimiter(accountInterval, opts.AccountBurst),
		contactLimiter:  newIPLimiter(contactInterval, opts.ContactBurst),
		loginLimiter:    newIPLimiter(loginInterval, opts.LoginBurst),
		passwordLimiter: newIPLimiter(passwordInterval, opts.PasswordBurst),
		internalToken:   opts.InternalToken,
		sessionCookie:   cookieName,
		sessionTTL:      sessionTTL,
		sessionSecure:   opts.SessionCookieSecure,
		alertsEnabled:   opts.AlertsEnabled,
		alertLimit:      alertLimit,
		dummyHash:       auth.DummyHash(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("GET /api/v1/stats", h.stats)
	mux.HandleFunc("GET /api/v1/vendors", h.publicRead(h.listVendors))
	mux.HandleFunc("OPTIONS /api/v1/vendors", h.publicRead(h.listVendors))
	mux.HandleFunc("GET /api/v1/vendors/{slug}", h.publicRead(h.getVendor))
	mux.HandleFunc("OPTIONS /api/v1/vendors/{slug}", h.publicRead(h.getVendor))
	mux.HandleFunc("POST /api/v1/suggest", h.publicWrite(h.suggest))
	mux.HandleFunc("OPTIONS /api/v1/suggest", h.publicWrite(h.suggest))
	mux.HandleFunc("POST /api/v1/contact", h.publicContact(h.contact))
	mux.HandleFunc("OPTIONS /api/v1/contact", h.publicContact(h.contact))

	// Alerts need no account, so they are public like the catalogue. The bell on
	// a vendor page writes one; the two token endpoints are what the unsubscribe
	// link in the email footer reaches.
	mux.HandleFunc("POST /api/v1/vendors/{slug}/alerts", h.publicAlert(h.subscribeAlert))
	mux.HandleFunc("OPTIONS /api/v1/vendors/{slug}/alerts", h.publicAlert(h.subscribeAlert))
	mux.HandleFunc("GET /api/v1/alerts/{token}", h.publicRead(h.getAlert))
	mux.HandleFunc("DELETE /api/v1/alerts/{token}", h.publicRead(h.unsubscribeAlert))
	mux.HandleFunc("OPTIONS /api/v1/alerts/{token}", h.publicRead(h.getAlert))

	// The same path, for somebody signed in: the bell reads whether they already
	// have an alert here, and the modal's button stops it. Both answer for one
	// session only, so neither is cacheable.
	mux.HandleFunc("GET /api/v1/vendors/{slug}/alerts", h.requireAuth(h.getUserAlert))
	mux.HandleFunc("DELETE /api/v1/vendors/{slug}/alerts", h.requireAuth(h.deleteUserAlert))

	// A webhook is a call out to somebody else's end, so the page lets them try
	// it: the call uses what the page is holding rather than what was last saved,
	// and its answer is shown back. It is a call anywhere the subscriber names, so
	// it is counted against the same budget as subscribing.
	mux.HandleFunc("POST /api/v1/vendors/{slug}/alerts/test",
		h.requireAuth(h.limited(h.alertLimiter, false,
			"You have tried that webhook several times already, please try again later",
			h.testWebhook)))

	// Stopping one way without taking away what it was told, which is how a
	// subscriber makes one of them quiet for a while.
	mux.HandleFunc("POST /api/v1/vendors/{slug}/alerts/pause", h.requireAuth(h.setAlertChannelPaused))

	// The account screen lists everything one person has asked to be told about
	// and lets them pause, resume, end or remove any of it. It lives under auth
	// with the rest of the account, and {id} is the row's own id rather than the
	// token, because the session is what proves it is theirs.
	mux.HandleFunc("GET /api/v1/auth/alerts", h.requireAuth(h.listUserAlerts))
	mux.HandleFunc("PATCH /api/v1/auth/alerts/{id}", h.requireAuth(h.setUserAlertState))
	mux.HandleFunc("DELETE /api/v1/auth/alerts/{id}", h.requireAuth(h.deleteUserAlertRow))

	mux.HandleFunc("POST /api/v1/auth/login",
		h.authEndpoint(h.limited(h.loginLimiter, false,
			"Too many sign in attempts, please try again later", h.login)))

	// Accounts made from the site. These send email to an address somebody typed,
	// so they get their own budget rather than sharing the sign in one.
	mux.HandleFunc("POST /api/v1/auth/register",
		h.authEndpoint(h.limited(h.accountLimiter, false,
			"Too many accounts have been asked for, please try again later", h.register)))
	mux.HandleFunc("POST /api/v1/auth/verify",
		h.authEndpoint(h.limited(h.accountLimiter, false,
			"Too many attempts, please try again later", h.verifyEmail)))
	mux.HandleFunc("POST /api/v1/auth/verify/resend",
		h.requireAuth(h.limited(h.accountLimiter, false,
			"Too many attempts, please try again later", h.resendVerification)))
	mux.HandleFunc("POST /api/v1/auth/forgot",
		h.authEndpoint(h.limited(h.accountLimiter, false,
			"Too many attempts, please try again later", h.forgotPassword)))
	mux.HandleFunc("POST /api/v1/auth/reset",
		h.authEndpoint(h.limited(h.accountLimiter, false,
			"Too many attempts, please try again later", h.resetPassword)))
	mux.HandleFunc("POST /api/v1/auth/logout",
		h.authEndpoint(h.limited(h.readLimiter, true,
			"Rate limit exceeded, please try again in a moment", h.logout)))
	mux.HandleFunc("GET /api/v1/auth/me",
		h.authEndpoint(h.limited(h.readLimiter, true,
			"Rate limit exceeded, please try again in a moment", h.me)))

	// The account screens are for every role, so they only ask for a session.
	mux.HandleFunc("PATCH /api/v1/auth/profile", h.requireAuth(h.updateProfile))
	mux.HandleFunc("POST /api/v1/auth/password",
		h.requireAuth(h.limited(h.passwordLimiter, false,
			"Too many attempts, please try again later", h.changePassword)))

	mux.HandleFunc("GET /api/v1/profiles", h.requireAuth(h.listProfiles))
	mux.HandleFunc("POST /api/v1/profiles/detect",
		h.requireAuth(h.limited(h.readLimiter, true,
			"Rate limit exceeded, please try again in a moment", h.detectProfile)))
	mux.HandleFunc("GET /api/v1/categories", h.requireAuth(h.listCategories))

	// The service routes are staff only: a subscriber looks after the alerts on
	// the catalogue, not after the catalogue itself. A service they want added
	// goes through the public suggestion queue.
	mux.HandleFunc("GET /api/v1/services", h.requireStaff(h.listServices))
	mux.HandleFunc("POST /api/v1/services", h.requireStaff(h.createService))
	mux.HandleFunc("GET /api/v1/services/{slug}", h.requireStaff(h.getService))
	mux.HandleFunc("PATCH /api/v1/services/{slug}", h.requireStaff(h.updateService))
	mux.HandleFunc("DELETE /api/v1/services/{slug}", h.requireStaff(h.deleteService))
	mux.HandleFunc("GET /api/v1/services/{slug}/status", h.requireStaff(h.serviceStatus))
	mux.HandleFunc("GET /api/v1/services/{slug}/polls", h.requireStaff(h.pollLog))
	mux.HandleFunc("POST /api/v1/services/{slug}/detect",
		h.requireStaff(h.limited(h.readLimiter, true,
			"Rate limit exceeded, please try again in a moment", h.retryDetection)))

	// The dashboard's own list is a read of the caller's rows, not a management
	// screen, so any session can ask for it.
	mux.HandleFunc("GET /api/v1/services/mine", h.requireAuth(h.myServices))

	// Managing people is admin only, moderators included out.
	admins := []string{auth.RoleAdmin}
	mux.HandleFunc("GET /api/v1/users", h.requireRole(admins, h.listUsers))
	mux.HandleFunc("PATCH /api/v1/users/{id}", h.requireRole(admins, h.updateUser))
	mux.HandleFunc("DELETE /api/v1/users/{id}", h.requireRole(admins, h.deleteUser))

	return recoverer(log, requestLogger(log, securityHeaders(mux)))
}

func (h *handlers) publicRead(next http.HandlerFunc) http.HandlerFunc {
	return h.publicEndpoint(h.readLimiter, true,
		"Rate limit exceeded, please try again in a moment", next)
}

func (h *handlers) publicWrite(next http.HandlerFunc) http.HandlerFunc {
	return h.publicEndpoint(h.suggestLimiter, false,
		"You have sent several suggestions already, please try again later", next)
}

// publicAlert throttles the subscribe endpoint. It is a write that sends email,
// so it gets its own budget rather than sharing the one for suggestions, and the
// site's own proxy is not counted, exactly as the other public endpoints do it.
func (h *handlers) publicAlert(next http.HandlerFunc) http.HandlerFunc {
	return h.publicEndpoint(h.alertLimiter, true,
		"You have subscribed to several alerts already, please try again later", next)
}

// publicContact throttles the contact form. It sends email, so it gets its own
// budget rather than sharing the one for suggestions. Unlike the alert endpoint
// the site's proxy is not skipped: the real client IP travels with the request,
// and a form that mails the owner is worth counting per visitor.
func (h *handlers) publicContact(next http.HandlerFunc) http.HandlerFunc {
	return h.publicEndpoint(h.contactLimiter, false,
		"You have sent several messages already, please try again later", next)
}

func (h *handlers) publicEndpoint(limiter *ipLimiter, skipInternal bool, message string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if skipInternal && h.isInternal(r) {
			next(w, r)
			return
		}

		ip := h.clientIP(r)
		if !limiter.allow(ip) {
			h.log.Warn("rate limit exceeded", "ip", ip, "path", r.URL.Path)
			w.Header().Set("Retry-After", strconv.Itoa(limiter.retryAfterSeconds()))
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": message})
			return
		}

		if r.Method == http.MethodGet {
			w.Header().Set("Cache-Control", "public, max-age=60, stale-while-revalidate=300")
		}
		next(w, r)
	}
}

func (h *handlers) isInternal(r *http.Request) bool {
	if h.internalToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare(
		[]byte(r.Header.Get("X-Internal-Token")),
		[]byte(h.internalToken),
	) == 1
}

func (h *handlers) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *handlers) stats(w http.ResponseWriter, r *http.Request) {
	st, err := h.store.Stats(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (h *handlers) listVendors(w http.ResponseWriter, r *http.Request) {
	vendors, err := h.store.ListVendors(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if vendors == nil {
		vendors = []store.VendorSummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"vendors": vendors,
		"count":   len(vendors),
	})
}

func (h *handlers) getVendor(w http.ResponseWriter, r *http.Request) {
	// The public endpoint always asks as the public, so a private service is not
	// reachable here no matter who is signed in.
	detail, err := h.store.GetVendor(r.Context(), r.PathValue("slug"), store.Public())
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Vendor not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

type suggestRequest struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func (h *handlers) suggest(w http.ResponseWriter, r *http.Request) {
	var body suggestRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}

	name := clean(sanitize(body.Name))
	pageURL := clean(sanitize(body.URL))

	if problem := validateSuggestion(name, pageURL); problem != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}

	// A rejected proposal does not block a new one, but anything else does.
	existing, err := h.store.FindVendor(r.Context(), name)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if existing != "" {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":      name + " has already been suggested or is already monitored",
			"vendorSlug": existing,
		})
		return
	}

	// A signed in visitor who suggests a service is recorded as its owner, so the
	// review queue shows who asked for it. That is attribution, not a visibility
	// choice: approving the suggestion publishes it to the catalogue.
	var ownerID *int64
	if user, err := h.authenticate(r); err == nil {
		ownerID = &user.ID
	}

	slug, err := h.store.ProposeService(r.Context(), name, pageURL, ownerID)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("suggestion recorded", "name", name, "slug", slug)
	writeJSON(w, http.StatusCreated, map[string]any{
		"status": string(store.StatusProposed),
		"slug":   slug,
	})
}

func validateSuggestion(name, pageURL string) string {
	if name == "" {
		return "Service name is required"
	}
	if n := utf8.RuneCountInString(name); n < 2 || n > 80 {
		return "Service name must be between 2 and 80 characters"
	}
	if len(pageURL) > 300 {
		return "Url is too long"
	}
	if pageURL != "" {
		u, err := url.Parse(pageURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return "Url must be a full http:// or https:// address"
		}
	}
	return ""
}

func sanitize(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			if r == '\n' || r == '\r' || r == '\t' {
				return ' '
			}
			return -1
		}
		return r
	}, value)
}

func clean(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func (h *handlers) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.log.Error("handler failed", "path", r.URL.Path, "error", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal error"})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func requestLogger(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Debug("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(start).Round(time.Millisecond))
	})
}

func recoverer(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("panic in handler", "path", r.URL.Path, "panic", rec)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
