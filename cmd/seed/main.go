// Command seed creates (or reuses) a user and issues a fresh API key, printing
// the plaintext key exactly once. It is optional since the web UI has a
// sign-up page (/login); use it for scripts/CI, or with -password to give an
// existing account (e.g. data created before sign-in existed) a password.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"golang.org/x/crypto/bcrypt"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/config"
)

func main() {
	cfgPath := flag.String("config", "configs/config.yaml", "path to config file")
	email := flag.String("email", "dev@bepaylot.local", "user email")
	name := flag.String("name", "Local Dev", "user display name")
	keyName := flag.String("key-name", "local", "api key label")
	password := flag.String("password", "", "set this password so the user can sign in on the login page")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}

	ctx := context.Background()
	st, err := postgres.Open(ctx, cfg.DB)
	if err != nil {
		fmt.Fprintln(os.Stderr, "db:", err)
		os.Exit(1)
	}
	defer st.Close()

	user, err := st.Users.Create(ctx, *email, *name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create user:", err)
		os.Exit(1)
	}
	if *password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(*password), bcrypt.DefaultCost)
		if err != nil {
			fmt.Fprintln(os.Stderr, "hash password:", err)
			os.Exit(1)
		}
		if err := st.Users.SetPassword(ctx, user.ID, string(hash)); err != nil {
			fmt.Fprintln(os.Stderr, "set password:", err)
			os.Exit(1)
		}
		fmt.Println("password set: sign in at /login with this email")
	}
	plaintext, key, err := st.APIKeys.Issue(ctx, user.ID, *keyName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "issue key:", err)
		os.Exit(1)
	}

	fmt.Printf("user_id:  %s\n", user.ID)
	fmt.Printf("email:    %s\n", user.Email)
	fmt.Printf("key_id:   %s\n", key.ID)
	fmt.Printf("api_key:  %s\n", plaintext)
	fmt.Println("\nExport it for the curl examples:")
	fmt.Printf("  export BEPAYLOT_API_KEY=%s\n", plaintext)
}
