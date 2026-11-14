// Command duckd runs the "Steal Some Christmas Cheer" duck-tracking server.
//
// Configuration is read from environment variables; see README.md.
package main

import (
	"context"
	"crypto/rand"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/smartybryan/steal-some-christmas-cheer/internal/auth"
	"github.com/smartybryan/steal-some-christmas-cheer/internal/notify"
	"github.com/smartybryan/steal-some-christmas-cheer/internal/server"
	"github.com/smartybryan/steal-some-christmas-cheer/internal/store"
)

//go:embed all:docs
var site embed.FS

func main() {
	port := env("PORT", "8080")
	publicURL := strings.TrimSuffix(env("PUBLIC_URL", "http://localhost:"+port), "/")
	siteURL := strings.TrimSuffix(env("SITE_URL", publicURL), "/")

	password := os.Getenv("OWNER_PASSWORD")
	if password == "" {
		log.Fatal("OWNER_PASSWORD must be set")
	}
	secret := []byte(os.Getenv("SECRET"))
	if len(secret) == 0 {
		log.Print("WARNING: SECRET is not set; generating a random one. Owner logins and review links will stop working when the server restarts.")
		secret = make([]byte, 32)
		_, _ = rand.Read(secret)
	}

	dataDir := env("DATA_DIR", "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		log.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dataDir, "duck.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	static, err := fs.Sub(site, "docs")
	if err != nil {
		log.Fatal(err)
	}

	cfg := server.Config{
		OwnerPassword:  password,
		Signer:         auth.NewSigner(secret),
		AllowedOrigins: splitList(os.Getenv("ALLOWED_ORIGINS")),
		PublicURL:      publicURL,
		SiteURL:        siteURL,
		TrustProxy:     os.Getenv("TRUST_PROXY") == "true",
		Static:         static,
		EmailFallback:  os.Getenv("NOTIFY_EMAIL"),
	}
	if sender := emailSender(); sender != nil {
		cfg.Email = sender
	}
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           server.New(cfg, st, notifiers()).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      2 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		log.Printf("🦆 duck server listening on :%s (public URL %s)", port, publicURL)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
}

// emailSender returns the SMTP sender, or nil if SMTP isn't configured. The
// recipient comes from the owner settings page, falling back to NOTIFY_EMAIL.
// (Returned as a pointer and assigned only when non-nil so the interface
// field stays truly nil when email is off.)
func emailSender() *notify.Email {
	host := os.Getenv("SMTP_HOST")
	if host == "" {
		log.Print("email notifications disabled (SMTP_HOST not set)")
		return nil
	}
	user := os.Getenv("SMTP_USERNAME")
	log.Printf("email notifications enabled via %s", host)
	return &notify.Email{
		Host:     host,
		Port:     env("SMTP_PORT", "587"),
		Username: user,
		Password: os.Getenv("SMTP_PASSWORD"),
		From:     env("SMTP_FROM", user),
	}
}

func notifiers() notify.Notifier {
	all := notify.Multi{notify.Log{}}
	if to := os.Getenv("NOTIFY_SMS"); to != "" {
		all = append(all, notify.SMS{
			AccountSID: os.Getenv("TWILIO_ACCOUNT_SID"),
			AuthToken:  os.Getenv("TWILIO_AUTH_TOKEN"),
			From:       os.Getenv("TWILIO_FROM"),
			To:         to,
		})
		log.Printf("SMS notifications enabled")
	}
	return all
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func splitList(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSuffix(strings.TrimSpace(part), "/"); part != "" {
			out = append(out, part)
		}
	}
	return out
}
