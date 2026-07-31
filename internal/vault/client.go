package vault

import (
	"context"
	"fmt"

	"github.com/hashicorp/vault/api"
	"github.com/hashicorp/vault/api/auth/kubernetes"
	"golang.org/x/sync/errgroup"

	"github.com/circleci-functions/toolkit/observability"
	"github.com/circleci-functions/toolkit/testing/testrand"
)

// ClientConfig configures a generic Vault api client
type ClientConfig struct {
	// Address of the Vault server, defaults to "http://localhost:8200"
	Address string
	// AuthType is the type of authentication to use, defaults to "token"
	AuthType AuthType
	// K8sRole is the role to use when using role authentication, defaults to "default"
	K8sAuthRole string
	// Token is the root token of the server for development, defaults to "dummyroot"
	RootToken string
	// RenewableTokenTTL manages the TTL of the temporary renewable
	// token used for token auth, default to "5m"
	RenewableTokenTTL string
	// RenewableTokenMaxTTL manages the MaxTTL of the temporary
	// renewable token used for token auth, default to "30m"
	RenewableTokenMaxTTL string
	// MountPath is the location of the relevant mount, currently "secret"
	MountPath string
}

type AuthType string

const (
	// K8sAuth (prod) uses a K8s role and a JWT token to authenticate with Vault
	K8sAuth AuthType = "k8s"
	// TokenAuth (dev) uses a token - typically a root token - to authenticate with Vault
	TokenAuth AuthType = "token"
)

type Renewer struct {
	watcher *api.LifetimeWatcher
}

func (r *Renewer) Run(ctx context.Context) error {
	return watchWatcher(ctx, r.watcher)
}

// newClient creates a Vault api client with either k8s or token auth
func newClient(ctx context.Context, cfg ClientConfig) (*api.Client, *Renewer, error) {
	if cfg.Address == "" {
		cfg.Address = "http://localhost:8200"
	}
	if cfg.AuthType == "" {
		cfg.AuthType = "token"
	}

	apiCfg := api.DefaultConfig()
	apiCfg.Address = cfg.Address
	v, err := api.NewClient(apiCfg)
	if err != nil {
		return nil, nil, err
	}

	var renewer *Renewer
	switch cfg.AuthType {
	case K8sAuth:
		renewer, err = authK8s(ctx, v, cfg.K8sAuthRole)
		if err != nil {
			return nil, nil, err
		}
	case TokenAuth:
		if cfg.RootToken == "" {
			cfg.RootToken = "dummyroot"
		}
		if cfg.RenewableTokenTTL == "" {
			cfg.RenewableTokenTTL = "5m"
		}
		if cfg.RenewableTokenMaxTTL == "" {
			cfg.RenewableTokenMaxTTL = "30m"
		}
		renewer, err = authRenewableToken(v, cfg.RootToken, cfg.RenewableTokenTTL, cfg.RenewableTokenMaxTTL)
		if err != nil {
			return nil, nil, err
		}
	}
	return v, renewer, nil
}

func authK8s(ctx context.Context, client *api.Client, roleName string) (*Renewer, error) {
	auth, err := kubernetes.NewKubernetesAuth(roleName)
	if err != nil {
		return nil, fmt.Errorf("failed to create vault auth: %w", err)
	}
	authSecret, err := client.Auth().Login(ctx, auth)
	if err != nil {
		return nil, fmt.Errorf("failed to log into vault: %w", err)
	}
	return newRenewer(client, authSecret)
}

func authRenewableToken(client *api.Client, rootToken string, ttl, maxttl string) (*Renewer, error) {
	token := "tmpToken" + testrand.Hex(8) // token auth only used in tests
	secret, err := createTmpToken(client, rootToken, token, ttl, maxttl)
	if err != nil {
		return nil, err
	}
	client.SetToken(token)
	return newRenewer(client, secret)
}

func createTmpToken(client *api.Client, rootToken string, tmpToken string, ttl, maxttl string) (*api.Secret, error) {
	client.SetToken(rootToken)
	defer client.ClearToken()
	auth := client.Auth().Token()
	renewable := true
	secret, err := auth.Create(&api.TokenCreateRequest{
		ID:             tmpToken,
		TTL:            ttl,
		Renewable:      &renewable,
		ExplicitMaxTTL: maxttl,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create renewable token: %w", err)
	}
	return secret, nil
}

func newRenewer(client *api.Client, secret *api.Secret) (*Renewer, error) {
	watcher, err := client.NewLifetimeWatcher(&api.LifetimeWatcherInput{
		Secret: secret,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher for renewal: %w", err)
	}
	return &Renewer{watcher}, nil
}

func watchWatcher(ctx context.Context, watcher *api.LifetimeWatcher) error {
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		watcher.Start()
		return nil
	})
	g.Go(func() error {
		defer watcher.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case err := <-watcher.DoneCh():
				return fmt.Errorf("renewer stopped early: %w", err)
			case <-watcher.RenewCh():
				observability.Info(ctx, "renewed watcher")
			}
		}
	})
	return g.Wait()
}
