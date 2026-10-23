package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
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

// login runs the interactive login: through the browser (OAuth device
// code) when the homeserver offers it, otherwise with a password. The
// password is returned for verify, empty after a browser login.
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
	methods, err := b.LoginMethods(ctx, hsURL)
	if err != nil {
		return "", fmt.Errorf("asking %s how to log in: %w", hsURL, err)
	}
	switch {
	case methods.OAuthDeviceCode:
		return "", deviceLogin(ctx, b, hsURL, userID)
	case methods.Password:
	case methods.OAuth || methods.SSO:
		return "", errors.New("this homeserver only offers browser login without a device code (SSO), which mtx does not support yet")
	default:
		return "", errors.New("this homeserver offers no login method mtx supports")
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

// deviceLogin logs in through the homeserver's authorization server:
// the user approves the login in any browser, on any device.
func deviceLogin(ctx context.Context, b *matrix.Backend, hsURL string, userID id.UserID) error {
	d, err := b.StartDeviceLogin(ctx, hsURL, userID)
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Println("Approve this login in a browser (on any device):")
	fmt.Println()
	fmt.Println("    " + d.URL)
	fmt.Println()
	if d.CodeInURL {
		fmt.Printf("and check that it shows the code %s.\n", d.Code)
	} else {
		fmt.Printf("and enter the code %s.\n", d.Code)
	}
	fmt.Println("Waiting for approval (Ctrl-C to cancel)...")
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	if err := b.FinishDeviceLogin(ctx, d); err != nil {
		return fmt.Errorf("login failed: %w", err)
	}
	fmt.Println("Logged in.")
	return nil
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
		key, err := bootstrap(ctx, b, p, password)
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

// bootstrap sets up cross-signing and key backup. The server must
// authorize publishing the cross-signing keys: a password session
// proves it with the password, an OAuth session by the user allowing
// it on the authorization server's account page. A brand-new account
// needs neither, so an OAuth session tries without first.
func bootstrap(ctx context.Context, b *matrix.Backend, p *prompter, password string) (string, error) {
	if !b.UsesOAuth() {
		if password == "" {
			var err error
			if password, err = p.secret("Account password (to authorize the setup)"); err != nil {
				return "", err
			}
		}
		return b.Bootstrap(ctx, password)
	}
	key, err := b.Bootstrap(ctx, "")
	if err == nil {
		return key, nil
	}
	resetURL := b.CrossSigningResetURL(ctx)
	if resetURL == "" || !matrix.NeedsApproval(err) {
		return "", err
	}
	fmt.Println()
	fmt.Println("Your homeserver needs you to allow this in the browser first:")
	fmt.Println()
	fmt.Println("    " + resetURL)
	fmt.Println()
	if _, err := p.line("Press Enter once you have allowed it", ""); err != nil {
		return "", err
	}
	return b.Bootstrap(ctx, "")
}
