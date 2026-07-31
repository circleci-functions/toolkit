package vault

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/vault/api"

	o11y "github.com/circleci-functions/toolkit/observability"
	"github.com/circleci-functions/toolkit/secret"
)

// EnvSource uses a KVClient to provide the environment.Source interface
type EnvSource struct {
	cfg EnvSourceConfig
	kv  *KVClient

	// CachedSecret is the secret as found in Vault when Cache() is called, where
	// the keys are identical to the Vault secret, and the values have been masked
	// with secret.String. All calls to Lookup use this stored secret, rather than
	// the live secret in Vault.
	CachedSecret map[string]secret.String
}

type EnvSourceConfig struct {
	ClientConfig

	// SecretName is the secret for the service, defaults to k8s/<namespace>/<service-account-name>
	SecretName string

	// ExtraSecretSuffixes are additional vault paths to load, formed by appending
	// each suffix to SecretName (e.g. "_rollbar" loads "<SecretName>_rollbar").
	// Missing secrets are silently skipped.
	ExtraSecretSuffixes []string
}

func NewEnvSource(ctx context.Context, cfg EnvSourceConfig) (*EnvSource, error) {
	// NB: ignores renewer, secrets are cached immediately and never fetched again
	kv, _, err := NewKVClient(ctx, cfg.ClientConfig)
	if err != nil {
		return nil, err
	}
	source := &EnvSource{
		cfg: cfg,
		kv:  kv,
	}
	err = source.cache(ctx)
	return source, err
}

func (s *EnvSource) cache(ctx context.Context) (err error) {
	ctx, endSpan := o11y.StartSpan(ctx, "vault secret cache")
	defer endSpan(&err)
	masked := map[string]secret.String{}

	paths := []string{s.cfg.SecretName}
	for _, suffix := range s.cfg.ExtraSecretSuffixes {
		paths = append(paths, s.cfg.SecretName+suffix)
	}
	for _, path := range paths {
		data, err := s.kv.Get(ctx, path)
		if err != nil {
			if errors.Is(err, api.ErrSecretNotFound) {
				continue
			}
			return fmt.Errorf("failed to cache secret %q: %w", path, err)
		}
		for k, v := range data {
			masked[k] = secret.NewString(fmt.Sprintf("%v", v))
			o11y.SetSpanAttribute(ctx, k, path)
		}
	}
	s.CachedSecret = masked
	return nil
}

func (s *EnvSource) Lookup(path string) (string, bool) {
	if v, has := s.CachedSecret[path]; has {
		return v.Value(), true
	}
	return "", false
}
