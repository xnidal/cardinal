package config

import (
	"cardinal/pkg/storage"
	"fmt"
	"os"
	"strings"
)

type Profile = storage.Profile

type Config struct {
	ProfileName  string
	APIURL       string
	APIKey       string
	Model        string
	SystemPrompt string
}

func Load() *Config {
	defaultPrompt := "You are Cardinal, a helpful coding assistant. Be concise and direct."

	profile, err := storage.GetActiveProfile()
	if err != nil || profile == nil {
		return &Config{
			APIURL:       getEnv("CARDINAL_API_URL", "http://localhost:11434/v1"),
			APIKey:       os.Getenv("CARDINAL_API_KEY"),
			Model:        getEnv("CARDINAL_MODEL", "llama3.2"),
			SystemPrompt: getEnv("CARDINAL_SYSTEM_PROMPT", defaultPrompt),
		}
	}

	return &Config{
		ProfileName:  profile.Name,
		APIURL:       getEnv("CARDINAL_API_URL", profile.APIURL),
		APIKey:       getEnv("CARDINAL_API_KEY", profile.APIKey),
		Model:        getEnv("CARDINAL_MODEL", profile.Model),
		SystemPrompt: getEnv("CARDINAL_SYSTEM_PROMPT", defaultPrompt),
	}
}

func (c *Config) ActiveProfileName() string {
	if strings.TrimSpace(c.ProfileName) == "" {
		return "default"
	}
	return c.ProfileName
}

func (c *Config) SetAPIURL(url string) {
	c.APIURL = strings.TrimSpace(url)
	c.saveToProfile()
}

func (c *Config) SetAPIKey(key string) {
	c.APIKey = strings.TrimSpace(key)
	c.saveToProfile()
}

func (c *Config) SetModel(model string) {
	c.Model = strings.TrimSpace(model)
	c.saveToProfile()
}

func (c *Config) ListProfiles() []Profile {
	profiles, err := storage.ListProfiles()
	if err != nil {
		return []Profile{}
	}
	return profiles
}

func (c *Config) SwitchProfile(name string) error {
	name = strings.TrimSpace(name)
	profile, err := storage.GetProfile(name)
	if err != nil {
		return err
	}
	if profile == nil {
		return fmt.Errorf("profile not found: %s", name)
	}
	if err := storage.SetActiveProfile(profile.Name); err != nil {
		return err
	}

	c.ProfileName = profile.Name
	c.APIURL = profile.APIURL
	c.APIKey = profile.APIKey
	c.Model = profile.Model
	return nil
}

func (c *Config) SaveProfile(profile Profile, activate bool) error {
	profile.Name = strings.TrimSpace(profile.Name)
	profile.APIURL = strings.TrimSpace(profile.APIURL)
	profile.APIKey = strings.TrimSpace(profile.APIKey)
	profile.Model = strings.TrimSpace(profile.Model)

	if profile.Name == "" {
		return fmt.Errorf("profile name is required")
	}
	if profile.APIURL == "" {
		return fmt.Errorf("profile API URL is required")
	}
	if profile.Model == "" {
		return fmt.Errorf("profile model is required")
	}
	if err := storage.SaveProfile(profile); err != nil {
		return err
	}
	if activate {
		if err := storage.SetActiveProfile(profile.Name); err != nil {
			return err
		}
		c.ProfileName = profile.Name
		c.APIURL = profile.APIURL
		c.APIKey = profile.APIKey
		c.Model = profile.Model
	}
	return nil
}

func (c *Config) saveToProfile() {
	profileName := strings.TrimSpace(c.ProfileName)
	if profileName == "" {
		activeProfile, err := storage.GetActiveProfile()
		if err != nil || activeProfile == nil {
			return
		}
		profileName = activeProfile.Name
	}

	_ = storage.SaveProfile(storage.Profile{
		Name:   profileName,
		APIURL: c.APIURL,
		APIKey: c.APIKey,
		Model:  c.Model,
	})
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
