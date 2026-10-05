package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
)

// pi: auth/resolve.ts.

// ModelsErrorCode classifies Models failures.
type ModelsErrorCode string

const (
	ModelsErrorModelSource     ModelsErrorCode = "model_source"
	ModelsErrorModelValidation ModelsErrorCode = "model_validation"
	ModelsErrorProvider        ModelsErrorCode = "provider"
	ModelsErrorStream          ModelsErrorCode = "stream"
	ModelsErrorAuth            ModelsErrorCode = "auth"
	ModelsErrorOAuth           ModelsErrorCode = "oauth"
)

// AuthResolutionOverrides adjust one auth resolution.
type AuthResolutionOverrides struct {
	APIKey string
	Env    types.ProviderEnv
	// MinOAuthValidityMs requires this much remaining OAuth-token validity;
	// defaults to five minutes.
	MinOAuthValidityMs int64
}

// ModelsError carries a Models error code. Callers surface Error() only, so
// the underlying reason is folded into the message (pi: withCauseDetail) and
// remains reachable via Unwrap.
type ModelsError struct {
	Code  ModelsErrorCode
	msg   string
	cause error
}

// NewModelsError builds a ModelsError; cause may be nil.
func NewModelsError(code ModelsErrorCode, message string, cause error) *ModelsError {
	return &ModelsError{Code: code, msg: withCauseDetail(message, cause), cause: cause}
}

func (e *ModelsError) Error() string { return e.msg }

func (e *ModelsError) Unwrap() error { return e.cause }

// withCauseDetail keeps the underlying reason in the message because callers
// surface Error() only.
func withCauseDetail(message string, cause error) string {
	if cause == nil {
		return message
	}
	detail := strings.TrimSpace(cause.Error())
	if detail == "" || strings.Contains(message, detail) {
		return message
	}
	return fmt.Sprintf("%s: %s", message, detail)
}

// AuthSource is the structural provider input of ResolveProviderAuth (pi:
// { id, auth }).
type AuthSource interface {
	ID() string
	Auth() ProviderAuth
}

const (
	defaultOAuthMinimumValidityMs = 5 * 60 * 1000
	defaultOAuthRefreshTimeoutMs  = 15_000
)

// ResolveProviderAuth resolves auth shared by the Models and the (deferred)
// image-generation chain
// collections. A stored credential owns the provider: ambient/env is
// consulted only when nothing is stored. No silent env fallback after a
// failed refresh or for a credential type without a matching handler.
func ResolveProviderAuth(ctx context.Context, source AuthSource, credentials CredentialStore, authContext AuthContext, overrides *AuthResolutionOverrides) (*AuthResult, error) {
	result, err := resolveProviderAuthOp(ctx, source, credentials, authContext, overrides)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func resolveProviderAuthOp(ctx context.Context, source AuthSource, credentials CredentialStore, authContext AuthContext, overrides *AuthResolutionOverrides) (*AuthResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	requestAuthContext := authContext
	if overrides != nil && overrides.Env != nil {
		requestAuthContext = overlayEnvAuthContext(authContext, overrides.Env)
	}
	auth := source.Auth()

	if overrides != nil && overrides.APIKey != "" && auth.APIKey != nil {
		return resolveApiKey(ctx, requestAuthContext, auth.APIKey, source.ID(), &ApiKeyCredential{
			Key: overrides.APIKey,
			Env: overrides.Env,
		})
	}

	stored, err := readCredentialForResolve(ctx, credentials, source.ID())
	if err != nil {
		return nil, err
	}
	if stored != nil {
		if storedOAuth, ok := stored.(*OAuthCredential); ok && auth.OAuth != nil {
			return resolveStoredOAuth(ctx, credentials, source.ID(), auth.OAuth, storedOAuth, overrides)
		}
		if storedKey, ok := stored.(*ApiKeyCredential); ok && auth.APIKey != nil {
			if overrides != nil && overrides.Env != nil {
				merged := types.ProviderEnv{}
				for k, v := range storedKey.Env {
					merged[k] = v
				}
				for k, v := range overrides.Env {
					merged[k] = v
				}
				storedKey = &ApiKeyCredential{Key: storedKey.Key, Env: merged}
			}
			return resolveApiKey(ctx, requestAuthContext, auth.APIKey, source.ID(), storedKey)
		}
		return nil, nil
	}

	// Ambient (env vars, AWS profiles, ADC files).
	if auth.APIKey != nil {
		return resolveApiKey(ctx, requestAuthContext, auth.APIKey, source.ID(), nil)
	}
	return nil, nil
}

type overlayEnvContext struct {
	base AuthContext
	env  types.ProviderEnv
}

func (o overlayEnvContext) Env(name string) (string, bool) {
	if v, ok := o.env[name]; ok && v != "" {
		return v, true
	}
	return o.base.Env(name)
}

func (o overlayEnvContext) FileExists(path string) bool {
	return o.base.FileExists(path)
}

func overlayEnvAuthContext(base AuthContext, env types.ProviderEnv) AuthContext {
	return overlayEnvContext{base: base, env: env}
}

// RefreshStoredOAuthCredential refreshes a stored OAuth credential under the
// credential-store lock and persists the result before the lock is released.
// needsRefresh is re-checked under the lock, so concurrent callers and
// processes refresh only once.
//
// ctx cancels only the wait for the lock. Once a refresh starts, the provider
// may already have rotated the refresh token, so the refresh and its
// persistence ignore ctx and are bounded only by the refresh timeout — a
// cancelled caller must not discard the only valid refresh token (pi v1.0.3
// bde882c74). pi arms a dedicated lock-wait signal and disarms it from inside
// fn; pig's CredentialStore.Modify cannot detach the two, so the modify runs
// on a context the caller cannot cancel and ctx is re-checked at fn entry
// instead: an aborted caller waits out the lock acquisition and then errors
// without refreshing (recorded deviation, same observable outcome). Request
// auth and model-list refresh share this path (v1.0.3).
func RefreshStoredOAuthCredential(ctx context.Context, credentials CredentialStore, providerID string, oauth OAuthAuth, needsRefresh func(*OAuthCredential) bool) (*OAuthCredential, error) {
	post, err := credentials.Modify(context.Background(), providerID, func(current Credential) (Credential, error) {
		if err := ctx.Err(); err != nil {
			return nil, err // pi: signal.throwIfAborted() at fn entry
		}
		currentOAuth, ok := current.(*OAuthCredential)
		if !ok || current == nil {
			return nil, nil // logged out meanwhile
		}
		if !needsRefresh(currentOAuth) {
			return nil, nil // another process/request refreshed
		}
		refreshCtx, cancel := context.WithTimeout(context.Background(), defaultOAuthRefreshTimeoutMs*time.Millisecond)
		defer cancel()
		refreshed, err := oauth.Refresh(refreshCtx, currentOAuth)
		if err != nil {
			return nil, NewModelsError(ModelsErrorOAuth, fmt.Sprintf("OAuth refresh failed for %s", providerID), err)
		}
		return refreshed, nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err() // pi: signal.throwIfAborted() on the caller, not the auth wrapper
		}
		var modelsErr *ModelsError
		if errors.As(err, &modelsErr) {
			return nil, err
		}
		return nil, NewModelsError(ModelsErrorAuth, fmt.Sprintf("Credential store modify failed for %s", providerID), err)
	}
	postOAuth, ok := post.(*OAuthCredential)
	if !ok || post == nil {
		return nil, nil // logged out meanwhile
	}
	return postOAuth, nil
}

// resolveStoredOAuth implements OAuth resolution with double-checked locking:
// tokens with less than five minutes remaining lock, re-check expiry under
// the lock, refresh once globally, and persist the rotated credential before
// release.
func resolveStoredOAuth(ctx context.Context, credentials CredentialStore, providerID string, oauth OAuthAuth, stored *OAuthCredential, overrides *AuthResolutionOverrides) (*AuthResult, error) {
	var requestedMin int64
	if overrides != nil {
		requestedMin = overrides.MinOAuthValidityMs
	}
	minimumValidityMs := max64(defaultOAuthMinimumValidityMs, requestedMin)
	expiresSoon := func(credential *OAuthCredential) bool {
		return time.Now().UnixMilli()+minimumValidityMs >= credential.Expires
	}
	credential := stored

	if expiresSoon(credential) {
		// Optimistic check said expired; the authoritative check runs under
		// the lock.
		var minValidityRequested = overrides != nil && overrides.MinOAuthValidityMs > 0
		post, err := RefreshStoredOAuthCredential(ctx, credentials, providerID, oauth, expiresSoon)
		if err != nil {
			return nil, err
		}
		if post == nil {
			return nil, nil // logged out meanwhile
		}
		credential = post
		// The normal five-minute window triggers a refresh but does not
		// impose a provider contract. Explicit callers (such as bearer-token
		// export) do require the requested minimum after the refresh.
		if minValidityRequested && expiresSoon(credential) {
			return nil, NewModelsError(ModelsErrorOAuth, fmt.Sprintf("OAuth refresh returned a token that expires too soon for %s", providerID), nil)
		}
	}

	authResult, err := oauth.ToAuth(credential)
	if err != nil {
		return nil, NewModelsError(ModelsErrorOAuth, fmt.Sprintf("OAuth auth derivation failed for %s", providerID), err)
	}
	return &AuthResult{Auth: *authResult, Source: "OAuth"}, nil
}

func resolveApiKey(ctx context.Context, authContext AuthContext, apiKey ApiKeyAuth, providerID string, credential *ApiKeyCredential) (*AuthResult, error) {
	result, err := apiKey.Resolve(ApiKeyAuthInput{Ctx: authContext, Credential: credential, Context: ctx})
	if err != nil {
		return nil, NewModelsError(ModelsErrorAuth, fmt.Sprintf("API key auth failed for provider %s", providerID), err)
	}
	return result, nil
}

func readCredentialForResolve(ctx context.Context, credentials CredentialStore, providerID string) (Credential, error) {
	credential, err := credentials.Read(ctx, providerID)
	if err != nil {
		return nil, NewModelsError(ModelsErrorAuth, fmt.Sprintf("Credential store read failed for %s", providerID), err)
	}
	return credential, nil
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
