package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
	"maunium.net/go/mautrix/id"

	"github.com/schwagner/mtx/internal/matrix"
)

// prompter reads answers from the terminal before the TUI starts.
type prompter struct {
	in *bufio.Reader
}

func newPrompter() *prompter { return &prompter{in: bufio.NewReader(os.Stdin)} }

func (p *prompter) line(prompt, def string) (string, error) {
	if def != "" {
		fmt.Printf("%s [%s]: ", prompt, def)
	} else {
		fmt.Printf("%s: ", prompt)
	}
	s, err := p.in.ReadString('\n')
	if err != nil && s == "" {
		return "", err
	}
	s = strings.TrimSpace(s)
	if s == "" {
		s = def
	}
	return s, nil
}

func (p *prompter) secret(prompt string) (string, error) {
	fmt.Printf("%s: ", prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	return strings.TrimSpace(string(b)), err
}

func (p *prompter) yes(prompt string, def bool) (bool, error) {
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	s, err := p.line(prompt+" ("+hint+")", "")
	if err != nil {
		return false, err
	}
	if s == "" {
		return def, nil
	}
	return strings.HasPrefix(strings.ToLower(s), "y"), nil
}

// login runs the interactive password login.
func login(ctx context.Context, b *matrix.Backend, p *prompter) (password string, err error) {
	fmt.Println("Log in to Matrix.")
	raw, err := p.line("User ID (e.g. @you:example.com)", "")
	if err != nil {
		return "", err
	}
	userID := id.UserID(raw)
	if !strings.HasPrefix(raw, "@") {
		return "", fmt.Errorf("user ID must look like @name:server")
	}
	discovered, err := matrix.DiscoverHomeserver(ctx, userID)
	if err != nil {
		return "", err
	}
	hsURL, err := p.line("Homeserver URL", discovered)
	if err != nil {
		return "", err
	}
	password, err = p.secret("Password")
	if err != nil {
		return "", err
	}
	if err := b.Login(ctx, hsURL, userID, password); err != nil {
		return "", fmt.Errorf("login failed: %w", err)
	}
	fmt.Println("Logged in.")
	return password, nil
}

// verify makes this device trusted: it unlocks secret storage with the
// recovery key, or sets up encryption for an account that has none.
// password is empty when the session was restored rather than logged in.
func verify(ctx context.Context, b *matrix.Backend, p *prompter, password string) error {
	state := b.Verification()
	if state.IsVerified {
		return nil
	}
	if !state.HasSSSS {
		fmt.Println()
		fmt.Println("This account has no encryption backup (secret storage) set up yet.")
		ok, err := p.yes("Set up cross-signing and key backup now", true)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("encryption setup declined; mtx needs a verified device to read encrypted rooms")
		}
		if password == "" {
			if password, err = p.secret("Account password (to authorize the setup)"); err != nil {
				return err
			}
		}
		key, err := b.Bootstrap(ctx, password)
		if err != nil {
			return fmt.Errorf("setting up encryption: %w", err)
		}
		fmt.Println()
		fmt.Println("Encryption is set up. Your recovery key is:")
		fmt.Println()
		fmt.Println("    " + key)
		fmt.Println()
		fmt.Println("Store it somewhere safe (a password manager). You need it to verify")
		fmt.Println("new devices and to restore message history if you lose all devices.")
		_, err = p.line("Press Enter once you have saved it", "")
		return err
	}
	fmt.Println()
	fmt.Println("Verify this device with your recovery key (or security passphrase).")
	fmt.Println("It is the key Element calls \"Recovery key\" under Settings → Encryption.")
	for attempt := 1; attempt <= 3; attempt++ {
		key, err := p.secret("Recovery key")
		if err != nil {
			return err
		}
		if err = b.Verify(ctx, key); err == nil {
			fmt.Println("Device verified.")
			return nil
		}
		fmt.Printf("Verification failed: %v\n", err)
	}
	return errors.New("device verification failed")
}
