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

func GetConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".cardinal"
	}
	return filepath.Join(home, ".cardinal")
}

func settingsPath() string {
	return filepath.Join(GetConfigDir(), "settings.json")
}

func legacySettingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".cardinal"
	}
	return filepath.Join(home, ".cardinal")
}

func defaultSettings() *Settings {
	return &Settings{
		Profiles: []Profile{{
			Name:        "ollama",
			APIURL:      "http://localhost:11434/v1",
			Model:       "llama3.2",
			Permissions: permissions.DefaultPolicy(),
		}},
		ActiveProfile: "ollama",
	}
}

func normalizeSettings(settings *Settings) *Settings {
	if settings == nil || len(settings.Profiles) == 0 {
		return defaultSettings()
	}
	for i := range settings.Profiles {
		settings.Profiles[i].Name = strings.TrimSpace(settings.Profiles[i].Name)
		settings.Profiles[i].APIURL = strings.TrimSpace(settings.Profiles[i].APIURL)
		settings.Profiles[i].APIKey = strings.TrimSpace(settings.Profiles[i].APIKey)
		settings.Profiles[i].Model = strings.TrimSpace(settings.Profiles[i].Model)
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
	// Migration path for older Cardinal builds that wrote JSON directly to ~/.cardinal.
	// Check this first because ~/.cardinal/settings.json returns ENOTDIR when ~/.cardinal is a file.
	legacy := legacySettingsPath()
	if info, statErr := os.Stat(legacy); statErr == nil && !info.IsDir() {
		data, readErr := os.ReadFile(legacy)
		if readErr != nil {
			return nil, readErr
		}
		var settings Settings
		if jsonErr := json.Unmarshal(data, &settings); jsonErr != nil {
			return nil, jsonErr
		}
		settings = *normalizeSettings(&settings)
		if saveErr := SaveSettings(&settings); saveErr != nil {
			return nil, saveErr
		}
		return &settings, nil
	}

	data, err := os.ReadFile(settingsPath())
	if err == nil {
		var settings Settings
		if err := json.Unmarshal(data, &settings); err != nil {
			return nil, err
		}
		return normalizeSettings(&settings), nil
	}
	if os.IsNotExist(err) {
		return defaultSettings(), nil
	}
	return nil, err
}

func SaveSettings(settings *Settings) error {
	settings = normalizeSettings(settings)
	configDir := GetConfigDir()
	if info, err := os.Stat(configDir); err == nil && !info.IsDir() {
		backup := configDir + ".bak"
		_ = os.Remove(backup)
		if err := os.Rename(configDir, backup); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath(), data, 0600)
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
	for i := range settings.Profiles {
		if settings.Profiles[i].Name == profile.Name {
			settings.Profiles[i] = profile
			return SaveSettings(settings)
		}
	}
	settings.Profiles = append(settings.Profiles, profile)
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
	name = strings.TrimSpace(name)
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
	name = strings.TrimSpace(name)
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
