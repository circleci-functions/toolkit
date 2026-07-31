package vault

import (
	"context"
	"fmt"

	"github.com/circleci-functions/toolkit/secret"
)

// Encryptor uses a TransitClient to provide the crypto.EncryptionProvider interface
type Encryptor struct {
	cfg         EncryptorConfig
	client      *TransitClient
	authRenewer *Renewer
}

type EncryptorConfig struct {
	ClientConfig

	// EncryptionKeyName is the name of the encryption key to use,
	// defaults to the <namespace>_<service-account-name>
	EncryptionKeyName string
}

func NewEncryptor(ctx context.Context, cfg EncryptorConfig) (*Encryptor, error) {
	client, renewer, err := NewTransitClient(ctx, cfg.ClientConfig)
	return &Encryptor{cfg, client, renewer}, err
}

func (e *Encryptor) Run(ctx context.Context) error {
	return e.authRenewer.Run(ctx)
}

func (e *Encryptor) Teardown(context.Context) error {
	return nil
}

func (e *Encryptor) EncryptOne(wrap secret.String) (string, error) {
	ciphertext, err := e.Encrypt([]secret.String{wrap})
	if err != nil {
		return "", err
	}
	return ciphertext[0], nil
}

func (e *Encryptor) Encrypt(wraps []secret.String) ([]string, error) {
	plaintext := make([]string, len(wraps))
	for i, d := range wraps {
		plaintext[i] = d.Value()
	}
	ciphertext, err := e.client.Encrypt(e.cfg.EncryptionKeyName, plaintext)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt with %s", e.cfg.EncryptionKeyName)
	}
	return ciphertext, nil
}

func (e *Encryptor) DecryptOne(ciphertext string) (secret.String, error) {
	plaintext, err := e.Decrypt([]string{ciphertext})
	if err != nil {
		return secret.NewString(""), err
	}
	return plaintext[0], err
}

func (e *Encryptor) Decrypt(ciphertext []string) ([]secret.String, error) {
	plaintext, err := e.client.Decrypt(e.cfg.EncryptionKeyName, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt with %s", e.cfg.EncryptionKeyName)
	}
	wrap := make([]secret.String, len(plaintext))
	for i := range plaintext {
		wrap[i] = secret.NewString(plaintext[i])
	}
	return wrap, nil
}
