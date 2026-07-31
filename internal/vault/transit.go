package vault

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"github.com/hashicorp/vault/api"
)

type TransitClient struct {
	cfg    ClientConfig
	client *api.Client
}

func NewTransitClient(ctx context.Context, cfg ClientConfig) (*TransitClient, *Renewer, error) {
	if cfg.MountPath == "" {
		cfg.MountPath = "transit"
	}
	v, renewer, err := newClient(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	t := &TransitClient{
		cfg:    cfg,
		client: v,
	}
	return t, renewer, nil
}

func (t *TransitClient) Encrypt(keyName string, plaintext []string) ([]string, error) {
	batch := make([]any, len(plaintext))
	for i, p := range plaintext {
		batch[i] = map[string]string{"plaintext": base64.StdEncoding.EncodeToString([]byte(p))}
	}
	results, err := t.writeBatch([]string{"encrypt", keyName}, batch)
	if err != nil {
		return nil, err
	}
	res := make([]string, len(plaintext))
	for i, result := range results {
		r := result.(map[string]any)
		res[i] = r["ciphertext"].(string)
	}
	return res, nil
}

func (t *TransitClient) Decrypt(keyName string, ciphertext []string) ([]string, error) {
	batch := make([]any, len(ciphertext))
	for i, c := range ciphertext {
		batch[i] = map[string]string{"ciphertext": c}
	}
	results, err := t.writeBatch([]string{"decrypt", keyName}, batch)
	if err != nil {
		return nil, err
	}
	res := make([]string, len(ciphertext))
	for i, result := range results {
		r := result.(map[string]any)
		b, err := base64.StdEncoding.DecodeString(r["plaintext"].(string))
		if err != nil {
			return nil, err
		}
		res[i] = string(b)
	}
	return res, nil
}

// CreateTestKey is for TESTS ONLY
func (t *TransitClient) CreateTestKey(keyName string) error {
	err := t.enable()
	if err != nil {
		return err
	}
	_, err = t.write([]string{"keys", keyName}, map[string]any{})
	if err != nil {
		return err
	}
	_, err = t.write([]string{"keys", keyName, "config"}, map[string]any{
		"deletion_allowed": true,
	})
	return err
}

// DeleteTestKey is for TESTS ONLY
func (t *TransitClient) DeleteTestKey(keyName string) error {
	_, err := t.delete([]string{"keys", keyName})
	return err
}

func (t *TransitClient) enable() error {
	if _, err := t.client.Sys().GetMount(t.cfg.MountPath); err != nil {
		err := t.client.Sys().Mount(t.cfg.MountPath, &api.MountInput{
			Type: "transit",
		})
		return err
	}
	return nil
}

func (t *TransitClient) write(args []string, data map[string]any) (map[string]any, error) {
	secret, err := t.client.Logical().Write(t.path(args), data)
	if err != nil {
		return nil, err
	}
	return secret.Data, nil
}

func (t *TransitClient) writeBatch(args []string, batch []any) ([]any, error) {
	data, err := t.write(args, map[string]any{
		"batch_input": batch,
	})
	if err != nil {
		return nil, err
	}
	return data["batch_results"].([]any), nil
}

func (t *TransitClient) delete(args []string) (*api.Secret, error) {
	return t.client.Logical().Delete(t.path(args))
}

func (t *TransitClient) path(args []string) string {
	escaped := make([]string, len(args))
	for i, a := range args {
		escaped[i] = url.PathEscape(a)
	}
	return fmt.Sprintf("/%s/%s", t.cfg.MountPath, strings.Join(escaped, "/"))
}
