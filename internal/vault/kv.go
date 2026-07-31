package vault

import (
	"context"

	"github.com/hashicorp/vault/api"
)

// KVClient provides a partial implementation of the K/V api
type KVClient struct {
	cfg    ClientConfig
	client *api.Client
}

func NewKVClient(ctx context.Context, cfg ClientConfig) (*KVClient, *Renewer, error) {
	v, r, err := newClient(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	if cfg.MountPath == "" {
		cfg.MountPath = "secret"
	}
	return &KVClient{
		cfg:    cfg,
		client: v,
	}, r, nil
}

func (c *KVClient) Get(ctx context.Context, path string) (map[string]any, error) {
	secret, err := c.client.KVv2(c.cfg.MountPath).Get(ctx, path)
	if err != nil {
		return nil, err
	}
	return secret.Data, nil
}

func (c *KVClient) Put(ctx context.Context, path string, key string, value string) error {
	data := map[string]any{key: value}
	_, err := c.client.KVv2(c.cfg.MountPath).Put(ctx, path, data)
	return err
}

func (c *KVClient) Delete(ctx context.Context, path string) error {
	return c.client.KVv2(c.cfg.MountPath).Delete(ctx, path)
}
