package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/config"
	"downradar.info/backend/internal/store"
)

func main() {
	email := flag.String("email", "", "email address of the account")
	name := flag.String("name", "", "display name, only used when the account is created")
	role := flag.String("role", auth.RoleAdmin, "admin, moderator or subscriber")
	password := flag.String("password", "", "password, a random one is generated when this is empty")
	reset := flag.Bool("reset", false, "change the password of an account that already exists")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(*email, *name, *role, *password, *reset, log); err != nil {
		log.Error("user command failed", "error", err)
		os.Exit(1)
	}
}

func run(email, name, role, password string, reset bool, log *slog.Logger) error {
	email = auth.NormalizeEmail(email)
	name = strings.TrimSpace(name)
	role = strings.ToLower(strings.TrimSpace(role))

	if !auth.ValidEmail(email) {
		return fmt.Errorf("%q is not an email address", email)
	}
	if !reset {
		if name == "" {
			return errors.New("a name is required")
		}
		if !auth.ValidRole(role) {
			return fmt.Errorf("role must be one of %s", strings.Join(auth.Roles(), ", "))
		}
	}

	generated := false
	if password == "" {
		password = rand.Text()
		generated = true
	}
	if err := auth.ValidatePassword(password); err != nil {
		return err
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	cfg := config.Load()
	st, err := store.New(ctx, cfg.DatabaseURL, store.Options{
		DefaultLimits: map[string]int{
			store.LimitAlert: cfg.AlertLimitPerSubscriber,
		},
	})
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		return err
	}

	if reset {
		return resetPassword(ctx, st, email, hash, generated, password, log)
	}

	user, err := st.CreateUser(ctx, email, name, role, hash)
	if errors.Is(err, store.ErrEmailTaken) {
		return fmt.Errorf("%s is already registered, use -reset to change its password", email)
	}
	if err != nil {
		return err
	}

	log.Info("user created", "id", user.ID, "email", user.Email, "role", user.Role)
	reportPassword(generated, password, user.Email)
	return nil
}

func resetPassword(ctx context.Context, st *store.Store, email, hash string, generated bool, password string, log *slog.Logger) error {
	user, err := st.UserByEmail(ctx, email)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("no account for %s", email)
	}
	if err != nil {
		return err
	}

	if err := st.SetUserPassword(ctx, user.ID, hash); err != nil {
		return err
	}

	// A password change has to end the sessions the old password opened.
	removed, err := st.DeleteUserSessions(ctx, user.ID)
	if err != nil {
		return err
	}

	log.Info("password reset", "id", user.ID, "email", user.Email, "sessions_ended", removed)
	reportPassword(generated, password, user.Email)
	return nil
}

func reportPassword(generated bool, password, email string) {
	if !generated {
		return
	}
	fmt.Fprintf(os.Stderr, "\npassword for %s: %s\n\n", email, password)
}
