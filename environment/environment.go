/*
Package environment provides an abstraction for the application environment. In
production, your application gets the information it needs from OS environment
of the pod your application runs on - information that is often not readily
available in local development.

This package provides two kinds of environments: FromOS, when you want to use
the OS environment (probably production), and FromMap, when you want to define
the environment using a map (probably local development and testing). It also
provides FromSource to load variables from anything that implements the Source
interface.
*/
package environment

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/sethvargo/go-envconfig"

	"github.com/circleci-functions/toolkit/secret"
)

type (
	// Environment is a mapping from paths to values
	Environment interface {
		GetBaseMetadata() BaseMetadata
		Lookup(path string) (string, bool)
		LookupConcat(path ...string) (string, bool)
		LookupSecret(path string) (secret.String, bool)
		LookupSecretConcat(path ...string) (secret.String, bool)
		Get(path ...string) string
		MustGet(path ...string) string
		Process(ctx context.Context, i any) error
	}

	// Backend implements the Environment interface by pulling data from a Source
	Backend struct {
		source Source
	}

	// Source is the where the Environment values are coming from
	Source interface {
		Lookup(path string) (string, bool)
	}

	// Map is for defining bindings, see FromMap
	Map map[string]string

	// BaseMetadata is a set of standard information about the environment, such
	// as k8s variables and service name
	BaseMetadata struct {
		DeployEnvironment     string
		K8sNodeName           string
		K8sPodName            string
		K8sPodNamespace       string
		K8sReplicaSet         string
		K8sServiceAccountName string
		Revision              string
		ServiceName           string
	}
)

// GetBaseMetadata returns the BaseMetadata for the environment
func (env *Backend) GetBaseMetadata() BaseMetadata {
	podName, hasPodName := env.Lookup("K8S_POD_NAME")

	deployEnv, hasDeployEnv := env.Lookup("DEPLOY_ENVIRONMENT")
	if !hasDeployEnv && hasPodName {
		deployEnv = "production"
	} else if !hasDeployEnv {
		deployEnv = "local"
	}

	var revision string
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				revision = setting.Value
				break
			}
		}
	}

	meta := BaseMetadata{
		DeployEnvironment:     deployEnv,
		K8sNodeName:           env.Get("K8S_NODE_NAME"),
		K8sPodName:            podName,
		K8sPodNamespace:       env.Get("K8S_POD_NAMESPACE"),
		K8sServiceAccountName: env.Get("K8S_SERVICE_ACCOUNT_NAME"),
		Revision:              revision,
		ServiceName:           env.Get("SERVICE_NAME"),
	}

	if hasPodName {
		split := strings.Split(podName, "-")
		meta.K8sReplicaSet = strings.Join(split[:len(split)-1], "-")
	}

	return meta
}

// FromSource returns an Environment that retrieves a value from the given
// source, for example the OS, a map, or Vault
func FromSource(s Source) *Backend {
	return &Backend{s}
}

type osSource struct{}

func (*osSource) Lookup(path string) (string, bool) {
	return os.LookupEnv(path)
}

// FromOS returns an Environment that is backed by the OS environment, for
// production services
func FromOS() *Backend {
	return FromSource(&osSource{})
}

type mapSource struct {
	bindings Map
}

func (m *mapSource) Lookup(path string) (string, bool) {
	val, found := m.bindings[path]
	return val, found
}

// FromMap returns an Environment that is backed by a map of keys to their
// string values, for testing purposes
func FromMap(bindings Map) *Backend {
	return FromSource(&mapSource{bindings})
}

type anySource struct {
	sources []Source
}

func (a *anySource) Lookup(path string) (string, bool) {
	for _, s := range a.sources {
		if val, found := s.Lookup(path); found {
			return val, true
		}
	}
	return "", false
}

// FromAny returns an Environment that looks in multiple backends, in order, for
// the requested path
func FromAny(s ...Source) *Backend {
	return FromSource(&anySource{s})
}

// Lookup returns the string found at path in the environment, and a boolean
// indicating whether value was present. **NEVER** use this function directly to
// get the value of sensitive data, use LookupSecret instead.
//
// This function is the main entrypoint for go-envconfig as well, so it must
// support loading values that are eventually stored in the secret.String type.
func (env *Backend) Lookup(path string) (string, bool) {
	if val, found := env.source.Lookup(path); found {
		return val, true
	}
	return "", false
}

// LookupSecret returns the a secret found at path in the environment, and a
// boolean indicating whether the secret was present
func (env *Backend) LookupSecret(path string) (secret.String, bool) {
	if val, found := env.source.Lookup(path); found {
		return secret.NewString(val), true
	}
	return secret.String{}, false
}

func concatPath(path ...string) string {
	return strings.Join(path, "_")
}

// LookupConcat returns the string found at path in the environment, and a
// boolean indicating whether value was present. All parts of the path are
// concatenated with an underscore.
func (env *Backend) LookupConcat(path ...string) (string, bool) {
	return env.Lookup(concatPath(path...))
}

// LookupSecretConcat returns the a secret found at a concatenated path in the
// environment, and a boolean indicating whether the secret was present. All
// parts of the path are concatenated with an underscore.
func (env *Backend) LookupSecretConcat(path ...string) (secret.String, bool) {
	return env.LookupSecret(concatPath(path...))
}

// Get returns the value at path in the environment
func (env *Backend) Get(path ...string) string {
	if val, found := env.LookupConcat(path...); found {
		return val
	}
	return ""
}

// MustGet returns the value at path in the environment, and panics if the
// value was not present
func (env *Backend) MustGet(path ...string) string {
	val, found := env.LookupConcat(path...)
	if !found {
		panic(fmt.Errorf("config not found at path: %s", path))
	}
	return val
}

// Process is a wrapper for envconfig.ProcessWith
func (env *Backend) Process(ctx context.Context, i any) error {
	return envconfig.ProcessWith(ctx, &envconfig.Config{
		Target:   i,
		Lookuper: env,
	})
}
