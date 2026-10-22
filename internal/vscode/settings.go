package vscode

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/tailscale/hujson"
)

const ExtensionID = "Ollama.ollama"

type Profile struct {
	Name                string
	Location            string
	UsesDefaultSettings bool
}

type storedProfile struct {
	Name            string `json:"name"`
	Location        string `json:"location"`
	UseDefaultFlags struct {
		Settings bool `json:"settings"`
	} `json:"useDefaultFlags"`
}

type SettingChange struct {
	Key      string
	Current  any
	Desired  any
	Exists   bool
	Conflict bool
}

func RequiredSettings() map[string]any {
	return map[string]any{
		"chat.agentHost.byokModels.enabled":              true,
		"chat.byokUtilityModelDefault":                   "mainAgent",
		"github.copilot.enable":                          map[string]any{"*": false},
		"github.copilot.nextEditSuggestions.enabled":     false,
		"workbench.settings.enableNaturalLanguageSearch": false,
		"npm.fetchOnlinePackageInfo":                     false,
		"json.schemaDownload.enable":                     false,
		"extensions.ignoreRecommendations":               true,
		"extensions.autoCheckUpdates":                    false,
		"extensions.autoUpdate":                          false,
		"settingsSync.enabled":                           false,
		"telemetry.telemetryLevel":                       "off",
		"telemetry.feedback.enabled":                     false,
		"workbench.enableExperiments":                    false,
		"update.mode":                                    "manual",
		"update.showReleaseNotes":                        false,
	}
}

func ApplicationSettings() map[string]any {
	return map[string]any{
		"telemetry.telemetryLevel":         "off",
		"telemetry.feedback.enabled":       false,
		"workbench.enableExperiments":      false,
		"update.mode":                      "manual",
		"update.showReleaseNotes":          false,
		"extensions.autoCheckUpdates":      false,
		"extensions.autoUpdate":            false,
		"extensions.ignoreRecommendations": true,
		"settingsSync.enabled":             false,
	}
}

func ProfileSettings() map[string]any {
	return map[string]any{
		"chat.agentHost.byokModels.enabled":              true,
		"chat.byokUtilityModelDefault":                   "mainAgent",
		"github.copilot.enable":                          map[string]any{"*": false},
		"github.copilot.nextEditSuggestions.enabled":     false,
		"npm.fetchOnlinePackageInfo":                     false,
		"json.schemaDownload.enable":                     false,
		"workbench.settings.enableNaturalLanguageSearch": false,
	}
}

func Profiles(userDir string) ([]Profile, error) {
	profiles := []Profile{{Name: "Default"}}
	path := filepath.Join(userDir, "globalStorage", "storage.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return profiles, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read VS Code profile metadata %s: %w", path, err)
	}
	var state struct {
		UserDataProfiles []storedProfile `json:"userDataProfiles"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse VS Code profile metadata %s: %w", path, err)
	}
	seen := map[string]bool{"Default": true}
	for _, p := range state.UserDataProfiles {
		if p.Name == "" || p.Location == "" {
			return nil, fmt.Errorf("VS Code profile metadata in %s contains a profile without a name or location", path)
		}
		if seen[p.Name] {
			return nil, fmt.Errorf("VS Code profile metadata in %s contains duplicate profile name %q", path, p.Name)
		}
		profile := Profile{Name: p.Name, Location: p.Location, UsesDefaultSettings: p.UseDefaultFlags.Settings}
		if _, err := SettingsPath(userDir, profile); err != nil {
			return nil, fmt.Errorf("invalid VS Code profile %q in %s: %w", p.Name, path, err)
		}
		seen[p.Name] = true
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func SettingsPath(userDir string, profile Profile) (string, error) {
	if profile.Location == "" || profile.UsesDefaultSettings {
		return filepath.Join(userDir, "settings.json"), nil
	}
	location := filepath.Clean(profile.Location)
	if filepath.IsAbs(location) || location == "." || location == ".." || strings.HasPrefix(location, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("profile location %q is not inside the profiles directory", profile.Location)
	}
	base := filepath.Join(userDir, "profiles")
	path := filepath.Join(base, location, "settings.json")
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("profile location %q is not inside the profiles directory", profile.Location)
	}
	return path, nil
}

func AnalyzeSettings(data []byte, required map[string]any) ([]SettingChange, error) {
	data = append([]byte(nil), data...)
	if len(data) == 0 {
		data = []byte("{}")
	}
	value, err := hujson.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse settings.json as JSONC: %w", err)
	}
	root, ok := value.Value.(*hujson.Object)
	if !ok {
		return nil, errors.New("settings.json must contain a JSON object")
	}
	if _, err := jsonObject(data); err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(required))
	for _, member := range root.Members {
		name, ok := member.Name.Value.(hujson.Literal)
		if !ok {
			continue
		}
		key := name.String()
		if _, relevant := required[key]; relevant {
			if seen[key] {
				return nil, fmt.Errorf("settings.json contains duplicate %q keys; fix the duplicate before continuing", key)
			}
			seen[key] = true
		}
	}
	current, err := jsonObject(data)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(required))
	for key := range required {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	changes := make([]SettingChange, 0, len(required))
	for _, key := range keys {
		desired := required[key]
		got, exists := current[key]
		if exists && reflect.DeepEqual(got, desired) {
			continue
		}
		changes = append(changes, SettingChange{
			Key:      key,
			Current:  got,
			Desired:  desired,
			Exists:   exists,
			Conflict: exists,
		})
	}
	return changes, nil
}

func jsonObject(data []byte) (map[string]any, error) {
	data = append([]byte(nil), data...)
	value, err := hujson.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse settings.json as JSONC: %w", err)
	}
	value.Standardize()
	var object map[string]any
	if err := json.Unmarshal(value.Pack(), &object); err != nil {
		return nil, fmt.Errorf("decode settings.json: %w", err)
	}
	if object == nil {
		return nil, errors.New("settings.json must contain a JSON object")
	}
	return object, nil
}

func ApplySettings(data []byte, changes []SettingChange, replace map[string]bool) ([]byte, error) {
	if len(changes) == 0 {
		return data, nil
	}
	data = append([]byte(nil), data...)
	if len(data) == 0 {
		data = []byte("{}")
	}
	value, err := hujson.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse settings.json as JSONC: %w", err)
	}
	type operation struct {
		Op    string `json:"op"`
		Path  string `json:"path"`
		Value any    `json:"value"`
	}
	var patch []operation
	for _, change := range changes {
		if change.Conflict && !replace[change.Key] {
			continue
		}
		op := "add"
		if change.Exists {
			op = "replace"
		}
		path := "/" + strings.ReplaceAll(strings.ReplaceAll(change.Key, "~", "~0"), "/", "~1")
		patch = append(patch, operation{Op: op, Path: path, Value: change.Desired})
	}
	if len(patch) == 0 {
		return data, nil
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return nil, fmt.Errorf("encode settings update: %w", err)
	}
	if err := value.Patch(patchBytes); err != nil {
		return nil, fmt.Errorf("merge settings.json: %w", err)
	}
	value.Format()
	return value.Pack(), nil
}

func WriteSettings(path string, expected, updated []byte) (string, error) {
	if reflect.DeepEqual(expected, updated) {
		return "", nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create settings directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect settings.json: %w", err)
	}
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("settings.json is a symbolic link; refusing to replace it")
	}
	if err == nil {
		current, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", fmt.Errorf("read settings.json before saving: %w", readErr)
		}
		if !reflect.DeepEqual(current, expected) {
			return "", errors.New("settings.json changed during setup; no settings were written, so run the command again")
		}
	}
	mode := os.FileMode(0o600)
	if err == nil {
		mode = info.Mode().Perm()
	}
	backup := ""
	if err == nil {
		backup, err = backupSettings(path, mode)
		if err != nil {
			return "", err
		}
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.tmp")
	if err != nil {
		return backup, fmt.Errorf("create temporary settings file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return backup, fmt.Errorf("set settings file permissions: %w", err)
	}
	if _, err := temp.Write(updated); err != nil {
		temp.Close()
		return backup, fmt.Errorf("write settings file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return backup, fmt.Errorf("flush settings file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return backup, fmt.Errorf("close settings file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return backup, fmt.Errorf("replace settings.json: %w", err)
	}
	return backup, nil
}

func backupSettings(path string, mode os.FileMode) (string, error) {
	source, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open settings.json for backup: %w", err)
	}
	defer source.Close()
	for i := 0; i < 100; i++ {
		suffix := fmt.Sprintf(".bak.%s", timeStamp())
		if i > 0 {
			suffix += fmt.Sprintf(".%d", i)
		}
		backup := path + suffix
		destination, createErr := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if errors.Is(createErr, os.ErrExist) {
			continue
		}
		if createErr != nil {
			return "", fmt.Errorf("create settings backup: %w", createErr)
		}
		_, copyErr := io.Copy(destination, source)
		if copyErr == nil {
			copyErr = destination.Sync()
		}
		closeErr := destination.Close()
		if copyErr != nil {
			os.Remove(backup)
			return "", fmt.Errorf("write settings backup: %w", copyErr)
		}
		if closeErr != nil {
			os.Remove(backup)
			return "", fmt.Errorf("close settings backup: %w", closeErr)
		}
		return backup, nil
	}
	return "", errors.New("couldn't create a unique settings backup filename")
}

var timeStamp = func() string {
	return time.Now().Format("20060102T150405.000000000")
}
