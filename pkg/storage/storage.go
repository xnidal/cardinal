package storage

import (
	"cardinal/pkg/permissions"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Profile struct {
	Name        string             `json:"name"`
	APIURL      string             `json:"api_url"`
	APIKey      string             `json:"api_key"`
	Model       string             `json:"model"`
	Permissions permissions.Policy `json:"permissions"`
}

type Settings struct {
	ActiveProfile string    `json:"active_profile"`
	Profiles      []Profile `json:"profiles"`
}

func getConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".cardinal"
	}
	return filepath.Join(home, ".cardinal")
}

func GetConfigDir() string {
	return getConfigPath()
}

func defaultSettings() *Settings {
	return &Settings{
		Profiles: []Profile{
			{
				Name:        "ollama",
				APIURL:      "http://localhost:11434/v1",
				Model:       "llama3.2",
				Permissions: permissions.DefaultPolicy(),
			},
		},
		ActiveProfile: "ollama",
	}
}

func normalizeSettings(settings *Settings) *Settings {
	if settings == nil || len(settings.Profiles) == 0 {
		return defaultSettings()
	}

	for i := range settings.Profiles {
		settings.Profiles[i].Permissions = permissions.Normalize(settings.Profiles[i].Permissions)
	}

	if strings.TrimSpace(settings.ActiveProfile) == "" {
		settings.ActiveProfile = settings.Profiles[0].Name
	}

	for _, profile := range settings.Profiles {
		if profile.Name == settings.ActiveProfile {
			return settings
		}
	}

	settings.ActiveProfile = settings.Profiles[0].Name
	return settings
}

func LoadSettings() (*Settings, error) {
	path := getConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultSettings(), nil
		}
		return nil, err
	}

	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, err
	}

	return normalizeSettings(&settings), nil
}

func SaveSettings(settings *Settings) error {
	path := getConfigPath()
	settings = normalizeSettings(settings)
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func SaveProfile(profile Profile) error {
	profile.Name = strings.TrimSpace(profile.Name)
	profile.APIURL = strings.TrimSpace(profile.APIURL)
	profile.APIKey = strings.TrimSpace(profile.APIKey)
	profile.Model = strings.TrimSpace(profile.Model)
	profile.Permissions = permissions.Normalize(profile.Permissions)

	if profile.Name == "" {
		return fmt.Errorf("profile name is required")
	}

	settings, err := LoadSettings()
	if err != nil {
		return err
	}

	found := false
	for i := range settings.Profiles {
		if settings.Profiles[i].Name == profile.Name {
			settings.Profiles[i] = profile
			found = true
			break
		}
	}

	if !found {
		settings.Profiles = append(settings.Profiles, profile)
	}

	return SaveSettings(settings)
}

func GetActiveProfile() (*Profile, error) {
	settings, err := LoadSettings()
	if err != nil {
		return nil, err
	}

	settings = normalizeSettings(settings)
	for i := range settings.Profiles {
		if settings.Profiles[i].Name == settings.ActiveProfile {
			return &settings.Profiles[i], nil
		}
	}

	if len(settings.Profiles) > 0 {
		return &settings.Profiles[0], nil
	}

	return nil, nil
}

func GetProfile(name string) (*Profile, error) {
	settings, err := LoadSettings()
	if err != nil {
		return nil, err
	}

	for i := range settings.Profiles {
		if settings.Profiles[i].Name == name {
			settings.Profiles[i].Permissions = permissions.Normalize(settings.Profiles[i].Permissions)
			return &settings.Profiles[i], nil
		}
	}

	return nil, nil
}

func SetActiveProfile(name string) error {
	settings, err := LoadSettings()
	if err != nil {
		return err
	}

	for _, profile := range settings.Profiles {
		if profile.Name == name {
			settings.ActiveProfile = name
			return SaveSettings(settings)
		}
	}

	return fmt.Errorf("profile not found: %s", name)
}

func ListProfiles() ([]Profile, error) {
	settings, err := LoadSettings()
	if err != nil {
		return nil, err
	}
	return normalizeSettings(settings).Profiles, nil
}
