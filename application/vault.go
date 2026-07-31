package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/circleci-functions/toolkit/environment"
	"github.com/circleci-functions/toolkit/internal/vault"
)

type vaultEncryptionEnv struct {
	Keys map[string]string `env:"VAULT_ENCRYPTION_KEYS"`
	Key  string            `env:"VAULT_ENCRYPTION_KEY"`
}

func loadVaultAuthConfig(env environment.Environment) (vault.ClientConfig, error) {
	cfg := vault.ClientConfig{
		Address:     env.Get("VAULT_ADDR"),
		K8sAuthRole: env.Get("VAULT_K8S_ROLE"),
	}

	if cfg.Address == "" {
		return cfg, errors.New("cannot configure vault without VAULT_ADDR")
	}

	authType := env.Get("VAULT_AUTH_TYPE")
	switch authType {
	case "k8s", "":
		cfg.AuthType = vault.K8sAuth
	case "token":
		cfg.AuthType = vault.TokenAuth
		cfg.RootToken = env.Get("VAULT_DEV_ROOT_TOKEN_ID")
		if cfg.RootToken == "" {
			return cfg, errors.New("must provide VAULT_DEV_ROOT_TOKEN_ID when VAULT_AUTH_TYPE is token")
		}
	default:
		return cfg, fmt.Errorf("unexpected VAULT_AUTH_TYPE: %s", authType)
	}

	if cfg.K8sAuthRole == "" {
		cfg.K8sAuthRole = "default"
	}

	return cfg, nil
}

func loadVaultEnvSourceConfig(env environment.Environment) (vault.EnvSourceConfig, error) {
	clientCfg, err := loadVaultAuthConfig(env)
	cfg := vault.EnvSourceConfig{ClientConfig: clientCfg}
	if err != nil {
		return cfg, err
	}
	cfg.SecretName = env.Get("VAULT_SECRET_NAME")

	if cfg.SecretName == "" {
		serviceAccountName := env.GetBaseMetadata().K8sServiceAccountName
		if serviceAccountName == "" {
			return cfg, errors.New("vault secret engine needs K8S_SERVICE_ACCOUNT_NAME or VAULT_SECRET_NAME")
		}
		namespace := env.GetBaseMetadata().K8sPodNamespace
		if namespace == "" {
			return cfg, errors.New("vault secret engine needs K8S_POD_NAMESPACE or VAULT_SECRET_NAME")
		}

		cfg.SecretName = fmt.Sprintf("k8s/%s/%s", namespace, serviceAccountName)
	}

	cfg.ExtraSecretSuffixes = []string{"_rollbar"}

	return cfg, nil
}

func loadVaultEncryptorConfig(ctx context.Context, env environment.Environment, suffix string) (vault.EncryptorConfig, error) {
	clientCfg, err := loadVaultAuthConfig(env)
	cfg := vault.EncryptorConfig{ClientConfig: clientCfg}
	if err != nil {
		return cfg, err
	}

	var ec vaultEncryptionEnv
	if err := env.Process(ctx, &ec); err != nil {
		return cfg, fmt.Errorf("failed to load vault encryption env: %w", err)
	}
	if name, ok := ec.Keys[suffix]; ok {
		cfg.EncryptionKeyName = name
		return cfg, nil
	}
	if suffix == "" && ec.Key != "" {
		cfg.EncryptionKeyName = ec.Key
		return cfg, nil
	}

	serviceAccountName := env.GetBaseMetadata().K8sServiceAccountName
	if serviceAccountName == "" {
		return cfg, errors.New("vault transit engine needs K8S_SERVICE_ACCOUNT_NAME or VAULT_ENCRYPTION_KEYS")
	}
	namespace := env.GetBaseMetadata().K8sPodNamespace
	if namespace == "" {
		return cfg, errors.New("vault transit engine needs K8S_POD_NAMESPACE or VAULT_ENCRYPTION_KEYS")
	}
	if suffix == "" {
		cfg.EncryptionKeyName = fmt.Sprintf("%s_%s", namespace, serviceAccountName)
	} else {
		cfg.EncryptionKeyName = fmt.Sprintf("%s_%s_%s", namespace, serviceAccountName, suffix)
	}

	return cfg, nil
}
