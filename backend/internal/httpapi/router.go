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

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/catalog"
	"downradar.info/backend/internal/detect"
	"downradar.info/backend/internal/store"
)

type handlers struct {
	store           *store.Store
	log             *slog.Logger
	catalog         catalog.Catalog
	detector        *detect.Detector
	readLimiter     *ipLimiter
	suggestLimiter  *ipLimiter
	loginLimiter    *ipLimiter
	passwordLimiter *ipLimiter
	internalToken   string
	sessionCookie   string
	sessionTTL      time.Duration
	sessionSecure   bool
	dummyHash       string
}

type Options struct {
	RateLimitPerMinute  int
	RateLimitBurst      int
	SuggestPerHour      int
	SuggestBurst        int
	LoginPerHour        int
	LoginBurst          int
	PasswordPerHour     int
	PasswordBurst       int
	InternalToken       string
	Catalog             catalog.Catalog
	SessionCookie       string
	SessionTTL          time.Duration
	SessionCookieSecure bool
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

	h := &handlers{
		store:           st,
		log:             log,
		catalog:         opts.Catalog,
		detector:        detect.New(opts.Catalog),
		readLimiter:     newIPLimiter(readInterval, opts.RateLimitBurst),
		suggestLimiter:  newIPLimiter(suggestInterval, opts.SuggestBurst),
		loginLimiter:    newIPLimiter(loginInterval, opts.LoginBurst),
		passwordLimiter: newIPLimiter(passwordInterval, opts.PasswordBurst),
		internalToken:   opts.InternalToken,
		sessionCookie:   cookieName,
		sessionTTL:      sessionTTL,
		sessionSecure:   opts.SessionCookieSecure,
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

	mux.HandleFunc("POST /api/v1/auth/login",
		h.authEndpoint(h.limited(h.loginLimiter, false,
			"too many sign in attempts, please try again later", h.login)))
	mux.HandleFunc("POST /api/v1/auth/logout",
		h.authEndpoint(h.limited(h.readLimiter, true,
			"rate limit exceeded, please try again in a moment", h.logout)))
	mux.HandleFunc("GET /api/v1/auth/me",
		h.authEndpoint(h.limited(h.readLimiter, true,
			"rate limit exceeded, please try again in a moment", h.me)))

	// The account screens are for every role, so they only ask for a session.
	mux.HandleFunc("PATCH /api/v1/auth/profile", h.requireAuth(h.updateProfile))
	mux.HandleFunc("POST /api/v1/auth/password",
		h.requireAuth(h.limited(h.passwordLimiter, false,
			"too many attempts, please try again later", h.changePassword)))

	mux.HandleFunc("GET /api/v1/profiles", h.requireAuth(h.listProfiles))
	mux.HandleFunc("POST /api/v1/profiles/detect",
		h.requireAuth(h.limited(h.readLimiter, true,
			"rate limit exceeded, please try again in a moment", h.detectProfile)))
	mux.HandleFunc("GET /api/v1/categories", h.requireAuth(h.listCategories))

	// The service endpoints ask for a session, not a role: everybody manages
	// their own services, and the handlers decide who may touch the catalogue.
	mux.HandleFunc("GET /api/v1/services", h.requireAuth(h.listServices))
	mux.HandleFunc("GET /api/v1/services/mine", h.requireAuth(h.myServices))
	mux.HandleFunc("POST /api/v1/services", h.requireAuth(h.createService))
	mux.HandleFunc("GET /api/v1/services/{slug}", h.requireAuth(h.getService))
	mux.HandleFunc("PATCH /api/v1/services/{slug}", h.requireAuth(h.updateService))
	mux.HandleFunc("DELETE /api/v1/services/{slug}", h.requireAuth(h.deleteService))
	mux.HandleFunc("GET /api/v1/services/{slug}/status", h.requireAuth(h.serviceStatus))
	mux.HandleFunc("GET /api/v1/services/{slug}/polls", h.requireAuth(h.pollLog))

	// Managing people is admin only, moderators included out.
	admins := []string{auth.RoleAdmin}
	mux.HandleFunc("GET /api/v1/users", h.requireRole(admins, h.listUsers))
	mux.HandleFunc("PATCH /api/v1/users/{id}", h.requireRole(admins, h.updateUser))
	mux.HandleFunc("DELETE /api/v1/users/{id}", h.requireRole(admins, h.deleteUser))

	return recoverer(log, requestLogger(log, securityHeaders(mux)))
}

func (h *handlers) publicRead(next http.HandlerFunc) http.HandlerFunc {
	return h.publicEndpoint(h.readLimiter, true,
		"rate limit exceeded, please try again in a moment", next)
}

func (h *handlers) publicWrite(next http.HandlerFunc) http.HandlerFunc {
	return h.publicEndpoint(h.suggestLimiter, false,
		"you have sent several suggestions already, please try again later", next)
}

func (h *handlers) publicEndpoint(limiter *ipLimiter, skipInternal bool, message string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
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
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "vendor not found"})
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
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
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

	slug, err := h.store.ProposeService(r.Context(), name, pageURL)
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
		return "service name is required"
	}
	if n := utf8.RuneCountInString(name); n < 2 || n > 80 {
		return "service name must be between 2 and 80 characters"
	}
	if len(pageURL) > 300 {
		return "url is too long"
	}
	if pageURL != "" {
		u, err := url.Parse(pageURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return "url must be a full http:// or https:// address"
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
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
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
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
