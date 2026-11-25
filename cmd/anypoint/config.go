package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/muratcanozdemir/anypoint-go-client/internal/atomicfile"
)

// Profile holds the connection settings for one Anypoint account.
type Profile struct {
	Host         string `json:"host,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	Username     string `json:"username,omitempty"`
	Password     string `json:"password,omitempty"`
	Org          string `json:"org,omitempty"`
	Env          string `json:"env,omitempty"`
}

var profileKeys = []string{"host", "client_id", "client_secret", "username", "password", "org", "env"}

func isSecretKey(k string) bool { return k == "client_secret" || k == "password" }

func (p *Profile) field(key string) (*string, error) {
	switch key {
	case "host":
		return &p.Host, nil
	case "client_id":
		return &p.ClientID, nil
	case "client_secret":
		return &p.ClientSecret, nil
	case "username":
		return &p.Username, nil
	case "password":
		return &p.Password, nil
	case "org":
		return &p.Org, nil
	case "env":
		return &p.Env, nil
	}
	return nil, fmt.Errorf("unknown key %q (valid: %s)", key, strings.Join(profileKeys, ", "))
}

// overlayEnv applies ANYPOINT_* environment variables on top of the profile.
// Names match anypoint-cli-v4 so existing CI setups keep working.
func (p Profile) overlayEnv() Profile {
	set := func(dst *string, names ...string) {
		for _, n := range names {
			if v := os.Getenv(n); v != "" {
				*dst = v
				return
			}
		}
	}
	set(&p.Host, "ANYPOINT_HOST")
	set(&p.ClientID, "ANYPOINT_CLIENT_ID")
	set(&p.ClientSecret, "ANYPOINT_CLIENT_SECRET")
	set(&p.Username, "ANYPOINT_USERNAME")
	set(&p.Password, "ANYPOINT_PASSWORD")
	set(&p.Org, "ANYPOINT_ORG")
	set(&p.Env, "ANYPOINT_ENV")
	return p
}

// Config is the on-disk configuration file.
type Config struct {
	Current  string              `json:"current"`
	Profiles map[string]*Profile `json:"profiles"`
	path     string
}

func configDir() string {
	if d := os.Getenv("ANYPOINT_CONFIG_DIR"); d != "" {
		return d
	}
	d, err := os.UserConfigDir()
	if err != nil {
		d = "."
	}
	return filepath.Join(d, "anypoint")
}

func loadConfig() (*Config, error) {
	c := &Config{Current: "default", Profiles: map[string]*Profile{}, path: filepath.Join(configDir(), "config.json")}
	b, err := os.ReadFile(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", c.path, err)
	}
	if c.Profiles == nil {
		c.Profiles = map[string]*Profile{}
	}
	if c.Current == "" {
		c.Current = "default"
	}
	return c, nil
}

func (c *Config) save() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return atomicfile.Write(c.path, append(b, '\n'), 0o600)
}

// profile returns the named profile (or the current one), creating it in memory if absent.
func (c *Config) profile(name string) *Profile {
	if name == "" {
		name = c.Current
	}
	p, ok := c.Profiles[name]
	if !ok {
		p = &Profile{}
		c.Profiles[name] = p
	}
	return p
}

func (c *Config) names() []string {
	n := make([]string, 0, len(c.Profiles))
	for k := range c.Profiles {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}
