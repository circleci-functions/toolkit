// Package crypto provides apis for cryptography
package crypto

import (
	"github.com/circleci-functions/toolkit/secret"
)

// EncryptionProvider provides methods relating to encrypting and decrypting
type EncryptionProvider interface {
	// EncryptOne is Encrypt for a single secret
	EncryptOne(secret.String) (ciphertext string, err error)
	// Encrypt returns ciphertext for each secret value in a batch
	Encrypt([]secret.String) (ciphertext []string, err error)
	// DecryptOne is Decrypt for a single ciphertext
	DecryptOne(ciphertext string) (secret.String, error)
	// Decrypt returns a batch of secrets containing the plaintext of each decrypted ciphertext
	Decrypt(ciphertexts []string) ([]secret.String, error)
}
