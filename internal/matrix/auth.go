package matrix

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"go.mau.fi/gomuks/pkg/hicli/jsoncmd"
	"go.mau.fi/util/random"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/id"
	"maunium.net/go/mautrix/oauth"
)

// LoginMethods is how a homeserver accepts logins.
type LoginMethods struct {
	Password bool
	// SSO is the legacy m.login.sso redirect flow.
	SSO bool
	// OAuth is next-generation auth (Matrix Authentication Service);
	// OAuthDeviceCode means it also offers the device authorization
	// grant, where the user approves the login in any browser.
	OAuth           bool
	OAuthDeviceCode bool
}

// LoginMethods asks the homeserver at homeserverURL how it accepts
// logins.
func (b *Backend) LoginMethods(ctx context.Context, homeserverURL string) (LoginMethods, error) {
	resp, err := b.cli.API.GetLoginFlows(ctx, &jsoncmd.GetLoginFlowsParams{HomeserverURL: strings.TrimRight(homeserverURL, "/")})
	if err != nil {
		return LoginMethods{}, err
	}
	return loginMethods(resp), nil
}

func loginMethods(resp *jsoncmd.LoginFlowsResponse) LoginMethods {
	var m LoginMethods
	if f := resp.RespLoginFlows; f != nil {
		m.Password = f.HasFlow(mautrix.AuthTypePassword)
		m.SSO = f.HasFlow(mautrix.AuthTypeSSO)
		m.OAuth = f.HasFlow(mautrix.AuthTypeOAuth)
	}
	if meta := resp.OAuth; meta != nil {
		m.OAuth = true
		m.OAuthDeviceCode = meta.DeviceAuthorizationEndpoint != "" &&
			slices.Contains(meta.GrantTypesSupported, oauth.GrantTypeDeviceCode)
	}
	return m
}

var (
	ErrLoginDenied  = errors.New("the login was denied in the browser")
	ErrLoginExpired = errors.New("the login code expired before it was approved")
)

// DeviceLogin is an OAuth device-code login waiting for the user to
// approve it in a browser.
type DeviceLogin struct {
	// URL is the page to open. When CodeInURL it carries Code, which
	// the page shows for the user to check; otherwise the user types
	// Code there.
	URL       string
	Code      string
	CodeInURL bool
	Expires   time.Time

	homeserverURL string
	clientID      string
	deviceCode    string
	interval      time.Duration
}

// StartDeviceLogin registers mtx as an OAuth client of the homeserver
// and requests a device code for a new device. userID, if set, is
// passed as a login hint.
func (b *Backend) StartDeviceLogin(ctx context.Context, homeserverURL string, userID id.UserID) (*DeviceLogin, error) {
	homeserverURL = strings.TrimRight(homeserverURL, "/")
	client, err := b.cli.API.OAuthRegisterClient(ctx, &jsoncmd.OAuthRegisterClientParams{
		HomeserverURL: homeserverURL,
		ClientMetadata: oauth.ClientMetadata{
			ApplicationType:         oauth.ApplicationTypeNative,
			ClientName:              "mtx",
			ClientURI:               "https://github.com/schwagner/mtx",
			GrantTypes:              []oauth.GrantType{oauth.GrantTypeDeviceCode, oauth.GrantTypeRefreshToken},
			TokenEndpointAuthMethod: oauth.AuthMethodNone,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("registering with the authorization server: %w", err)
	}
	deviceID := id.DeviceID(random.StringCharset(10, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"))
	resp, err := b.cli.API.OAuthGenerateDeviceCode(ctx, &jsoncmd.OAuthGenerateDeviceCodeParams{
		HomeserverURL: homeserverURL,
		GenerateDeviceCodeParams: oauth.GenerateDeviceCodeParams{
			Scopes:     oauth.ScopeList{oauth.ScopeClientAPI, oauth.ScopeDevice(deviceID)},
			UserIDHint: userID,
			ClientID:   client.ClientID,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("requesting a login code: %w", err)
	}
	d := &DeviceLogin{
		URL:           resp.VerificationURI,
		Code:          resp.UserCode,
		Expires:       time.Now().Add(resp.ExpiresIn.Duration),
		homeserverURL: homeserverURL,
		clientID:      client.ClientID,
		deviceCode:    resp.DeviceCode,
		interval:      resp.Interval.Duration,
	}
	if resp.VerificationURIComplete != "" {
		d.URL, d.CodeInURL = resp.VerificationURIComplete, true
	}
	if d.interval <= 0 {
		d.interval = 5 * time.Second // RFC 8628 §3.2
	}
	return d, nil
}

// FinishDeviceLogin waits until the user approves d in the browser,
// then completes the login like Login does. The access token is
// refreshed automatically from then on.
func (b *Backend) FinishDeviceLogin(ctx context.Context, d *DeviceLogin) error {
	params := &jsoncmd.OAuthPollDeviceCodeParams{
		HomeserverURL: d.homeserverURL,
		PollDeviceCodeParams: oauth.PollDeviceCodeParams{
			DeviceCode: d.deviceCode,
			ClientID:   d.clientID,
		},
	}
	return pollDeviceLogin(ctx, func(ctx context.Context) error {
		return b.cli.API.OAuthPollDeviceCode(ctx, params)
	}, d.interval, d.Expires, waitCtx, time.Now)
}

// pollDeviceLogin polls the token endpoint as RFC 8628 §3.5 asks:
// every interval while authorization is pending, 5s slower for good
// after each slow_down.
func pollDeviceLogin(ctx context.Context, poll func(context.Context) error, interval time.Duration,
	deadline time.Time, wait func(context.Context, time.Duration) error, now func() time.Time) error {
	for {
		err := poll(ctx)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, mautrix.ErrOAuthSlowDown):
			interval += 5 * time.Second
		case errors.Is(err, mautrix.ErrOAuthAuthorizationPending):
		case errors.Is(err, mautrix.ErrOAuthAccessDenied):
			return ErrLoginDenied
		case errors.Is(err, mautrix.ErrOAuthExpiredToken):
			return ErrLoginExpired
		default:
			return err
		}
		if !now().Add(interval).Before(deadline) {
			return ErrLoginExpired
		}
		if err := wait(ctx, interval); err != nil {
			return err
		}
	}
}

func waitCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// UsesOAuth reports whether the session was logged in through OAuth,
// so that account-level actions (cross-signing reset, logout) go
// through the authorization server rather than a password.
func (b *Backend) UsesOAuth() bool {
	return b.cli.Account != nil && b.cli.Account.ClientID != ""
}

// CrossSigningResetURL is the authorization server page where the user
// allows this device to replace the account's cross-signing keys, or
// "" if the server has none.
func (b *Backend) CrossSigningResetURL(ctx context.Context) string {
	meta, err := b.cli.Client.OAuthGetServerMetadata(ctx)
	if err != nil {
		return ""
	}
	return crossSigningResetURL(meta)
}

// NeedsApproval reports whether err is the server refusing an account
// change (such as Bootstrap) the user has not authorized yet.
func NeedsApproval(err error) bool {
	var httpErr mautrix.HTTPError
	return errors.As(err, &httpErr) && httpErr.IsStatus(http.StatusUnauthorized)
}

// AccountManagementURL is the authorization server's account page, or
// "" for a server without one.
func (b *Backend) AccountManagementURL(ctx context.Context) string {
	meta, err := b.cli.Client.OAuthGetServerMetadata(ctx)
	if err != nil || meta == nil {
		return ""
	}
	return meta.AccountManagementURI
}

func crossSigningResetURL(meta *oauth.ServerMetadata) string {
	if meta == nil || meta.AccountManagementURI == "" {
		return ""
	}
	if len(meta.AccountManagementActionsSupported) > 0 &&
		!slices.Contains(meta.AccountManagementActionsSupported, oauth.AccountManagementActionCrossSigningReset) {
		return ""
	}
	u, err := url.Parse(meta.AccountManagementURI)
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("action", string(oauth.AccountManagementActionCrossSigningReset))
	u.RawQuery = q.Encode()
	return u.String()
}

// ErrSessionNotEnded means the authorization server accepted the
// revocation but the session is still alive.
var ErrSessionNotEnded = errors.New("the server did not end the session")

// Logout ends this device's session on the server: an OAuth session by
// revoking its tokens, a password session with /logout.
func (b *Backend) Logout(ctx context.Context) error {
	if !b.UsesOAuth() {
		_, err := b.cli.Client.Logout(ctx)
		return err
	}
	if err := b.cli.Client.OAuthRevokeToken(ctx); err != nil {
		return err
	}
	// A failed revocation looks like success: MAS answers 200 to a
	// token it does not consider valid (RFC 7009 §2.2) and mautrix
	// drops 401s. A forced refresh tells the two apart.
	return sessionEnded(b.cli.Client.OAuthRefreshToken(ctx, forceRefresh))
}

// forceRefresh as OAuthRefreshToken's buffer refreshes however long
// the access token has left.
const forceRefresh = 100 * 365 * 24 * time.Hour

func sessionEnded(refreshErr error) error {
	switch {
	case refreshErr == nil:
		return ErrSessionNotEnded
	case errors.Is(refreshErr, mautrix.ErrOAuthInvalidGrant):
		return nil
	default:
		return fmt.Errorf("checking that the session ended: %w", refreshErr)
	}
}
