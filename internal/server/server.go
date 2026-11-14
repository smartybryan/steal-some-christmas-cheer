// Package server exposes the duck-tracking HTTP API.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/smartybryan/steal-some-christmas-cheer/internal/auth"
	"github.com/smartybryan/steal-some-christmas-cheer/internal/notify"
	"github.com/smartybryan/steal-some-christmas-cheer/internal/store"
)

const (
	maxUploadBytes = 20 << 20
	maxPhotoSide   = 1600
	ownerTokenTTL  = 30 * 24 * time.Hour
	reviewLinkTTL  = 30 * 24 * time.Hour
)

// Config controls the server's behavior.
type Config struct {
	OwnerPassword  string
	Signer         auth.Signer
	AllowedOrigins []string // origins (e.g. GitHub Pages) allowed to call the API
	PublicURL      string   // externally reachable base URL of this server
	SiteURL        string   // base URL of the website (may equal PublicURL)
	TrustProxy     bool     // use X-Forwarded-For for client IPs
	Static         fs.FS    // optional: serve the website from this server too
	Email          Mailer   // optional; nil when the server can't send email
	EmailFallback  string   // recipient when the owner hasn't set one in settings
	Now            func() time.Time
}

// Mailer sends an email to a specific recipient.
type Mailer interface {
	SendTo(ctx context.Context, to, subject, body string) error
}

// Server handles HTTP requests.
type Server struct {
	cfg        Config
	store      *store.Store
	notifier   notify.Notifier
	registerRL *rateLimiter
	loginRL    *rateLimiter
}

// New builds a Server.
func New(cfg Config, st *store.Store, n notify.Notifier) *Server {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Server{
		cfg:        cfg,
		store:      st,
		notifier:   n,
		registerRL: newRateLimiter(5, time.Hour),
		loginRL:    newRateLimiter(10, 15*time.Minute),
	}
}

// Handler returns the HTTP handler with all routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/public", s.handlePublic)
	mux.HandleFunc("GET /api/photos/{id}", s.handlePublicPhoto)
	mux.HandleFunc("POST /api/register", s.handleRegister)

	mux.HandleFunc("POST /api/owner/login", s.handleLogin)
	mux.HandleFunc("GET /api/owner/registrations", s.owner(s.handleListRegistrations))
	mux.HandleFunc("POST /api/owner/registrations/{id}", s.owner(s.handleReviewAPI))
	mux.HandleFunc("GET /api/owner/photos/{id}", s.owner(s.handleOwnerPhoto))
	mux.HandleFunc("GET /api/owner/settings", s.owner(s.handleGetSettings))
	mux.HandleFunc("PUT /api/owner/settings", s.owner(s.handlePutSettings))
	mux.HandleFunc("POST /api/owner/test-email", s.owner(s.handleTestEmail))
	mux.HandleFunc("PUT /api/owner/home-photo", s.owner(s.handlePutHomePhoto))
	mux.HandleFunc("DELETE /api/owner/home-photo", s.owner(s.handleDeleteHomePhoto))
	mux.HandleFunc("GET /api/home-photo", s.handleHomePhoto)

	mux.HandleFunc("GET /review/{id}", s.handleReviewPage)
	mux.HandleFunc("GET /review/{id}/photo", s.handleReviewPhoto)
	mux.HandleFunc("POST /review/{id}", s.handleReviewSubmit)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	if s.cfg.Static != nil {
		mux.Handle("GET /", http.FileServerFS(s.cfg.Static))
	}
	return s.cors(mux)
}

// ---------- public ----------

type publicStop struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name"`
	Location string    `json:"location"`
	Message  string    `json:"message"`
	Date     time.Time `json:"date"`
	PhotoURL string    `json:"photoUrl,omitempty"`
}

func (s *Server) handlePublic(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	trail, total, err := s.store.PublicTrail(r.Context(), settings.RevealAll)
	if err != nil {
		serverError(w, err)
		return
	}
	stops := make([]publicStop, 0, len(trail))
	for _, reg := range trail {
		stop := publicStop{ID: reg.ID, Name: reg.Name, Location: reg.PublicLocation, Message: reg.Message, Date: reg.CreatedAt}
		if reg.Status == store.StatusApproved && reg.HasPhoto {
			stop.PhotoURL = s.cfg.PublicURL + "/api/photos/" + strconv.FormatInt(reg.ID, 10)
		}
		stops = append(stops, stop)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"settings":     settings.Public(),
		"homePhotoUrl": s.homePhotoURL(r.Context()),
		"stops":        stops,
		"totalSteals":  total,
	})
}

func (s *Server) handlePublicPhoto(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	photo, err := s.store.PublicPhoto(r.Context(), id, settings.RevealAll)
	writePhoto(w, photo, err, "public, max-age=300")
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.registerRL.allow(s.clientIP(r), s.cfg.Now()) {
		writeError(w, http.StatusTooManyRequests, "Whoa there, speedy elf! Please try again later.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "That upload was too big or garbled. Try a smaller photo.")
		return
	}
	defer r.MultipartForm.RemoveAll()

	// Honeypot: real people never see or fill this field.
	if r.FormValue("website") != "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	reg := store.Registration{
		CreatedAt: s.cfg.Now().UTC(),
		Name:      clean(r.FormValue("name")),
		Street:    clean(r.FormValue("street")),
		City:      clean(r.FormValue("city")),
		Message:   clean(r.FormValue("message")),
	}
	if msg := validate(reg); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	reg.PublicLocation = PublicLocation(reg.Street, reg.City)

	photo, ok := readPhoto(w, r, "Please include a photo of the duck in its new home!")
	if !ok {
		return
	}

	id, err := s.store.Create(r.Context(), reg, photo)
	if err != nil {
		serverError(w, err)
		return
	}
	go s.notifyOwner(id, reg)
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "id": id})
}

func (s *Server) notifyOwner(id int64, reg store.Registration) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	subject := "🦆 The duck has a new home! A photo needs vetting"
	body := fmt.Sprintf("%s registered the duck at %s, %s.\n\nApprove or reject: %s\n\nOwner page: %s/owner.html",
		reg.Name, reg.Street, reg.City, s.reviewURL(id), strings.TrimSuffix(s.cfg.SiteURL, "/"))
	_ = s.notifier.Notify(ctx, subject, body)
	if err := s.sendEmail(ctx, subject, body); err != nil && !errors.Is(err, errNoEmail) {
		log.Printf("notify email: %v", err)
	}
}

var errNoEmail = errors.New("email not configured")

// sendEmail emails the owner at the address from the owner settings, falling
// back to the server's default recipient.
func (s *Server) sendEmail(ctx context.Context, subject, body string) error {
	if s.cfg.Email == nil {
		return errNoEmail
	}
	settings, err := s.store.Settings(ctx)
	if err != nil {
		return err
	}
	to := orDefault(settings.NotifyEmail, s.cfg.EmailFallback)
	if to == "" {
		return errNoEmail
	}
	return s.cfg.Email.SendTo(ctx, to, subject, body)
}

func (s *Server) reviewURL(id int64) string {
	exp := s.cfg.Now().Add(reviewLinkTTL).Unix()
	q := url.Values{"exp": {strconv.FormatInt(exp, 10)}, "sig": {s.cfg.Signer.Sign(reviewPurpose(id), exp)}}
	return fmt.Sprintf("%s/review/%d?%s", s.cfg.PublicURL, id, q.Encode())
}

func reviewPurpose(id int64) string { return "review:" + strconv.FormatInt(id, 10) }

// ---------- owner ----------

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.loginRL.allow(s.clientIP(r), s.cfg.Now()) {
		writeError(w, http.StatusTooManyRequests, "Too many attempts. Try again in a few minutes.")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Bad request.")
		return
	}
	if !auth.PasswordMatches(body.Password, s.cfg.OwnerPassword) {
		writeError(w, http.StatusUnauthorized, "That's not the secret password. Naughty list!")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": s.cfg.Signer.OwnerToken(s.cfg.Now(), ownerTokenTTL)})
}

func (s *Server) owner(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" {
			token = r.URL.Query().Get("t") // for <img src> tags
		}
		if !s.cfg.Signer.VerifyOwnerToken(token, s.cfg.Now()) {
			writeError(w, http.StatusUnauthorized, "Please log in again.")
			return
		}
		next(w, r)
	}
}

type ownerRegistration struct {
	store.Registration
	PhotoURL string `json:"photoUrl,omitempty"`
}

func (s *Server) handleListRegistrations(w http.ResponseWriter, r *http.Request) {
	regs, err := s.store.All(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	token := url.QueryEscape(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	out := make([]ownerRegistration, 0, len(regs))
	for _, reg := range regs {
		o := ownerRegistration{Registration: reg}
		if reg.HasPhoto {
			o.PhotoURL = fmt.Sprintf("%s/api/owner/photos/%d?t=%s", s.cfg.PublicURL, reg.ID, token)
		}
		out = append(out, o)
	}
	writeJSON(w, http.StatusOK, map[string]any{"registrations": out})
}

func (s *Server) handleOwnerPhoto(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	photo, err := s.store.Photo(r.Context(), id)
	writePhoto(w, photo, err, "private, max-age=300")
}

type reviewRequest struct {
	Action         string `json:"action"` // approve | reject_photo | pending | delete
	Name           string `json:"name"`
	PublicLocation string `json:"publicLocation"`
}

func (s *Server) handleReviewAPI(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req reviewRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Bad request.")
		return
	}
	if err := s.applyReview(r.Context(), id, req); err != nil {
		reviewError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

var errBadAction = errors.New("unknown action")

func (s *Server) applyReview(ctx context.Context, id int64, req reviewRequest) error {
	if req.Action == "delete" {
		return s.store.Delete(ctx, id)
	}
	var status store.Status
	switch req.Action {
	case "approve":
		status = store.StatusApproved
	case "reject_photo":
		status = store.StatusPhotoRejected
	case "pending":
		status = store.StatusPending
	default:
		return errBadAction
	}
	current, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	name := orDefault(clean(req.Name), current.Name)
	location := orDefault(clean(req.PublicLocation), current.PublicLocation)
	return s.store.Review(ctx, id, status, truncate(name, 60), truncate(location, 120))
}

func reviewError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "That registration no longer exists.")
	case errors.Is(err, errBadAction):
		writeError(w, http.StatusBadRequest, "Unknown action.")
	default:
		serverError(w, err)
	}
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.ownerSettings(r.Context(), settings))
}

type ownerSettings struct {
	store.Settings
	EmailConfigured bool   `json:"emailConfigured"` // the server can send email (SMTP is set up)
	HomePhotoURL    string `json:"homePhotoUrl"`
}

func (s *Server) ownerSettings(ctx context.Context, settings store.Settings) ownerSettings {
	return ownerSettings{
		Settings:        settings,
		EmailConfigured: s.cfg.Email != nil,
		HomePhotoURL:    s.homePhotoURL(ctx),
	}
}

func (s *Server) handleTestEmail(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	err := s.sendEmail(ctx, "🦆 Test from Steal Some Christmas Cheer",
		"Ho ho ho! Email alerts are working. You'll get a message like this whenever someone registers the duck.")
	switch {
	case errors.Is(err, errNoEmail) && s.cfg.Email == nil:
		writeError(w, http.StatusBadRequest, "The server isn't set up to send email yet. Set SMTP_HOST and related settings on the server.")
	case errors.Is(err, errNoEmail):
		writeError(w, http.StatusBadRequest, "Enter an email address and save your settings first.")
	case err != nil:
		log.Printf("test email: %v", err)
		writeError(w, http.StatusBadGateway, "Couldn't send the email: "+err.Error())
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var settings store.Settings
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&settings); err != nil {
		writeError(w, http.StatusBadRequest, "Bad request.")
		return
	}
	settings.OwnerName = truncate(clean(settings.OwnerName), 80)
	settings.HomeLocation = truncate(clean(settings.HomeLocation), 120)
	settings.ReturnAddress = truncate(cleanMultiline(settings.ReturnAddress), 300)
	settings.ReturnBy = truncate(clean(settings.ReturnBy), 80)
	settings.Instructions = truncate(cleanMultiline(settings.Instructions), 2000)
	settings.NotifyEmail = clean(settings.NotifyEmail)
	if settings.NotifyEmail != "" {
		addr, err := mail.ParseAddress(settings.NotifyEmail)
		if err != nil || addr.Name != "" || len(addr.Address) > 254 {
			writeError(w, http.StatusBadRequest, "That email address doesn't look right.")
			return
		}
		settings.NotifyEmail = addr.Address
	}
	if err := s.store.SaveSettings(r.Context(), settings); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.ownerSettings(r.Context(), settings))
}

// ---------- helpers ----------

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && slices.Contains(s.cfg.AllowedOrigins, origin) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Max-Age", "86400")
			h.Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			first, _, _ := strings.Cut(fwd, ",")
			return strings.TrimSpace(first)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

var houseNumber = regexp.MustCompile(`^\s*[0-9]+[A-Za-z]?(-[0-9A-Za-z]+)?\s+`)
var unitSuffix = regexp.MustCompile(`(?i)[\s,]+(apt|apartment|unit|suite|ste|#)\.?\s*\S*\s*$`)

// PublicLocation turns a full street address into something safe to show
// publicly: the street name and city, without the house number or unit.
func PublicLocation(street, city string) string {
	name := houseNumber.ReplaceAllString(street, "")
	name = strings.TrimSpace(unitSuffix.ReplaceAllString(name, ""))
	switch {
	case name == "":
		return city
	case city == "":
		return name
	default:
		return name + ", " + city
	}
}

func validate(r store.Registration) string {
	switch {
	case r.Name == "":
		return "Who are you, mysterious duck thief? Please enter a name."
	case r.Street == "":
		return "Please enter the street address where the duck now lives."
	case r.City == "":
		return "Please enter your city."
	case utf8.RuneCountInString(r.Name) > 60:
		return "That name is a bit long. 60 characters max, please."
	case utf8.RuneCountInString(r.Street) > 120, utf8.RuneCountInString(r.City) > 60:
		return "That address is a bit long."
	case utf8.RuneCountInString(r.Message) > 280:
		return "Your message is a bit long. 280 characters max, please."
	}
	return ""
}

func clean(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s))
}

func cleanMultiline(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r != '\n' && unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s))
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}

func writePhoto(w http.ResponseWriter, photo []byte, err error, cacheControl string) {
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", cacheControl)
	_, _ = w.Write(photo)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func serverError(w http.ResponseWriter, err error) {
	log.Printf("error: %v", err)
	writeError(w, http.StatusInternalServerError, "Something went wrong at the North Pole. Please try again.")
}
