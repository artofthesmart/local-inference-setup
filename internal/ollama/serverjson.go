package ollama

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const cloudKey = "disable_ollama_cloud"

// readServerJSON loads ~/.ollama/server.json. A missing or empty file returns an empty map.
func readServerJSON(path string) (map[string]json.RawMessage, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	m := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(data)) == 0 {
		return m, true, nil
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, true, fmt.Errorf("%s is not valid JSON, so it was left untouched (fix or delete it, then run again): %w", path, err)
	}
	return m, true, nil
}

// cloudDisabledInServerJSON reports whether server.json already turns off Ollama cloud.
func cloudDisabledInServerJSON(path string) (bool, error) {
	m, _, err := readServerJSON(path)
	if err != nil {
		return false, err
	}
	var v bool
	if raw, ok := m[cloudKey]; ok && json.Unmarshal(raw, &v) == nil {
		return v, nil
	}
	return false, nil
}

// setServerJSONCloud sets or removes disable_ollama_cloud, keeping every other key. It backs up an existing file first.
func setServerJSONCloud(path string, disable bool) error {
	m, existed, err := readServerJSON(path)
	if err != nil {
		return err
	}
	if disable {
		m[cloudKey] = json.RawMessage("true")
	} else {
		delete(m, cloudKey)
	}
	if existed {
		if err := backup(path); err != nil {
			return err
		}
	}
	if !disable && len(m) == 0 && !existed {
		return nil
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(path, append(out, '\n'), 0o644)
}

func backup(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path+".copilot-local.bak", data, 0o600)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), perm); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
