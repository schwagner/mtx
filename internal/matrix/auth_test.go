package matrix

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"go.mau.fi/gomuks/pkg/hicli/jsoncmd"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/oauth"
)

func TestLoginMethodsFromFlows(t *testing.T) {
	flows := func(types ...mautrix.AuthType) *mautrix.RespLoginFlows {
		r := &mautrix.RespLoginFlows{}
		for _, ty := range types {
			r.Flows = append(r.Flows, mautrix.LoginFlow{Type: ty})
		}
		return r
	}
	mas := &oauth.ServerMetadata{
		DeviceAuthorizationEndpoint: "https://account.example.com/oauth2/device",
		GrantTypesSupported:         []oauth.GrantType{oauth.GrantTypeAuthorizationCode, oauth.GrantTypeDeviceCode},
	}
	noDevice := &oauth.ServerMetadata{GrantTypesSupported: []oauth.GrantType{oauth.GrantTypeAuthorizationCode}}
	cases := []struct {
		name string
		resp *jsoncmd.LoginFlowsResponse
		want LoginMethods
	}{
		{"password only", &jsoncmd.LoginFlowsResponse{RespLoginFlows: flows(mautrix.AuthTypePassword, "m.login.application_service")},
			LoginMethods{Password: true}},
		{"legacy sso", &jsoncmd.LoginFlowsResponse{RespLoginFlows: flows(mautrix.AuthTypeSSO, mautrix.AuthTypeToken)},
			LoginMethods{SSO: true}},
		{"mas with device code", &jsoncmd.LoginFlowsResponse{RespLoginFlows: flows(mautrix.AuthTypePassword, mautrix.AuthTypeSSO, mautrix.AuthTypeOAuth), OAuth: mas},
			LoginMethods{Password: true, SSO: true, OAuth: true, OAuthDeviceCode: true}},
		{"oauth without device grant", &jsoncmd.LoginFlowsResponse{RespLoginFlows: flows(mautrix.AuthTypeOAuth), OAuth: noDevice},
			LoginMethods{OAuth: true}},
		{"device grant but no endpoint", &jsoncmd.LoginFlowsResponse{RespLoginFlows: flows(mautrix.AuthTypeOAuth),
			OAuth: &oauth.ServerMetadata{GrantTypesSupported: []oauth.GrantType{oauth.GrantTypeDeviceCode}}},
			LoginMethods{OAuth: true}},
		{"no flows", &jsoncmd.LoginFlowsResponse{}, LoginMethods{}},
	}
	for _, c := range cases {
		if got := loginMethods(c.resp); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestPollDeviceLogin(t *testing.T) {
	pending := mautrix.HTTPError{RespError: &mautrix.RespError{ErrCode: "authorization_pending", OAuth: true}}
	slow := mautrix.HTTPError{RespError: &mautrix.RespError{ErrCode: "slow_down", OAuth: true}}
	denied := mautrix.HTTPError{RespError: &mautrix.RespError{ErrCode: "access_denied", OAuth: true}}
	expired := mautrix.HTTPError{RespError: &mautrix.RespError{ErrCode: "expired_token", OAuth: true}}

	run := func(results []error, deadline time.Duration) (error, []time.Duration, int) {
		var waits []time.Duration
		calls := 0
		now := time.Unix(1000, 0)
		err := pollDeviceLogin(context.Background(), func(context.Context) error {
			r := results[calls]
			calls++
			return r
		}, 5*time.Second, now.Add(deadline), func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)
			now = now.Add(d)
			return nil
		}, func() time.Time { return now })
		return err, waits, calls
	}

	err, waits, calls := run([]error{pending, slow, pending, nil}, time.Minute)
	if err != nil || calls != 4 {
		t.Fatalf("approved: err=%v calls=%d", err, calls)
	}
	want := []time.Duration{5 * time.Second, 10 * time.Second, 10 * time.Second}
	if len(waits) != len(want) {
		t.Fatalf("waits = %v, want %v", waits, want)
	}
	for i := range want {
		if waits[i] != want[i] {
			t.Fatalf("waits = %v, want %v (slow_down adds 5s for good)", waits, want)
		}
	}

	if err, _, _ := run([]error{pending, denied}, time.Minute); !errors.Is(err, ErrLoginDenied) {
		t.Errorf("denied: err = %v, want ErrLoginDenied", err)
	}
	if err, _, _ := run([]error{expired}, time.Minute); !errors.Is(err, ErrLoginExpired) {
		t.Errorf("server expiry: err = %v, want ErrLoginExpired", err)
	}
	if err, _, calls := run([]error{pending, pending, pending, pending}, 12*time.Second); !errors.Is(err, ErrLoginExpired) || calls != 3 {
		t.Errorf("local deadline: err = %v calls = %d, want ErrLoginExpired after 3", err, calls)
	}
	other := errors.New("network down")
	if err, _, _ := run([]error{other}, time.Minute); !errors.Is(err, other) {
		t.Errorf("other error: err = %v, want it passed through", err)
	}
}

func TestPollDeviceLoginStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pending := mautrix.HTTPError{RespError: &mautrix.RespError{ErrCode: "authorization_pending", OAuth: true}}
	err := pollDeviceLogin(ctx, func(context.Context) error { cancel(); return pending },
		time.Second, time.Now().Add(time.Hour), waitCtx, time.Now)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestCrossSigningResetURL(t *testing.T) {
	cases := []struct {
		meta *oauth.ServerMetadata
		want string
	}{
		{nil, ""},
		{&oauth.ServerMetadata{}, ""},
		{&oauth.ServerMetadata{AccountManagementURI: "https://account.matrix.org/account/"},
			"https://account.matrix.org/account/?action=org.matrix.cross_signing_reset"},
		{&oauth.ServerMetadata{AccountManagementURI: "https://id.example.com/account?lang=en",
			AccountManagementActionsSupported: []oauth.AccountManagementAction{oauth.AccountManagementActionCrossSigningReset}},
			"https://id.example.com/account?action=org.matrix.cross_signing_reset&lang=en"},
		{&oauth.ServerMetadata{AccountManagementURI: "https://id.example.com/account",
			AccountManagementActionsSupported: []oauth.AccountManagementAction{oauth.AccountManagementActionProfile}}, ""},
	}
	for _, c := range cases {
		if got := crossSigningResetURL(c.meta); got != c.want {
			t.Errorf("crossSigningResetURL(%+v) = %q, want %q", c.meta, got, c.want)
		}
	}
}

func TestNeedsApproval(t *testing.T) {
	unauthorized := fmt.Errorf("failed to publish cross-signing keys: %w",
		mautrix.HTTPError{Response: &http.Response{StatusCode: http.StatusUnauthorized}, RespError: &mautrix.RespError{ErrCode: "M_UNAUTHORIZED"}})
	if !NeedsApproval(unauthorized) {
		t.Error("wrapped 401 not recognized")
	}
	forbidden := mautrix.HTTPError{Response: &http.Response{StatusCode: http.StatusForbidden}, RespError: &mautrix.RespError{ErrCode: "M_FORBIDDEN"}}
	if NeedsApproval(forbidden) || NeedsApproval(errors.New("connection refused")) || NeedsApproval(nil) {
		t.Error("non-401 errors must not ask for approval")
	}
}

func TestSessionEnded(t *testing.T) {
	invalidGrant := mautrix.HTTPError{Response: &http.Response{StatusCode: http.StatusBadRequest},
		RespError: &mautrix.RespError{ErrCode: "invalid_grant", OAuth: true}}
	if err := sessionEnded(invalidGrant); err != nil {
		t.Errorf("refresh refused: err = %v, want nil (session is gone)", err)
	}
	if err := sessionEnded(nil); !errors.Is(err, ErrSessionNotEnded) {
		t.Errorf("refresh succeeded: err = %v, want ErrSessionNotEnded", err)
	}
	other := errors.New("connection refused")
	if err := sessionEnded(other); !errors.Is(err, other) {
		t.Errorf("network error: err = %v, want it wrapped", err)
	}
}
