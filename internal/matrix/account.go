package matrix

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"

	"go.mau.fi/gomuks/pkg/hicli/jsoncmd"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/crypto/ssss"
	"maunium.net/go/mautrix/id"
)

// DiscoverHomeserver resolves the client API base URL for a user ID via
// .well-known, falling back to https://<server name>.
func DiscoverHomeserver(ctx context.Context, userID id.UserID) (string, error) {
	_, server, err := userID.Parse()
	if err != nil {
		return "", fmt.Errorf("invalid user ID %q (expected @name:server): %w", userID, err)
	}
	if wk, err := mautrix.DiscoverClientAPI(ctx, server); err == nil && wk != nil && wk.Homeserver.BaseURL != "" {
		return wk.Homeserver.BaseURL, nil
	}
	return "https://" + server, nil
}

// Login logs in with a password and registers this device's encryption
// keys.
func (b *Backend) Login(ctx context.Context, homeserverURL string, userID id.UserID, password string) error {
	return b.cli.LoginPassword(ctx, strings.TrimRight(homeserverURL, "/"), string(userID), password)
}

// Verification reports the device's cross-signing state.
func (b *Backend) Verification() jsoncmd.VerificationState {
	return b.cli.VerificationState
}

// Verify unlocks secret storage with a recovery key (or passphrase),
// imports the cross-signing keys and key backup key, signs this device,
// and starts syncing.
func (b *Backend) Verify(ctx context.Context, recoveryKey string) error {
	return b.cli.Verify(ctx, strings.TrimSpace(recoveryKey))
}

// Bootstrap sets up cross-signing, secret storage and key backup for an
// account that has none, and returns the new recovery key. password
// authorizes publishing the cross-signing keys.
//
// On an account that already has cross-signing this replaces it, so
// other devices would have to be verified again; callers must only offer
// it when Verification().HasSSSS is false.
func (b *Backend) Bootstrap(ctx context.Context, password string) (string, error) {
	key, err := ssss.NewKey("")
	if err != nil {
		return "", err
	}
	recoveryKey := key.RecoveryKey()
	var seeds crypto.CrossSigningSeeds
	for _, k := range []*[]byte{(*[]byte)(&seeds.MasterKey), (*[]byte)(&seeds.SelfSigningKey), (*[]byte)(&seeds.UserSigningKey)} {
		*k = make([]byte, 32)
		if _, err := rand.Read(*k); err != nil {
			return "", err
		}
	}
	if err := b.cli.ResetEncryption(ctx, recoveryKey, nil, seeds, password); err != nil {
		return "", err
	}
	return recoveryKey, nil
}
