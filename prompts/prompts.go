package prompts

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

const defaultPath = "prompts.yaml"

var store map[string]string

func Load(path string) error {
	if path == "" {
		path = os.Getenv("PROMPTS_FILE")
	}
	if path == "" {
		path = defaultPath
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read prompts file %s: %w", path, err)
	}

	parsed := make(map[string]string)
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("failed to parse prompts file: %w", err)
	}

	store = parsed
	return nil
}

func Get(key string) string {
	if store == nil {
		return ""
	}
	return store[key]
}

func MustGet(key string) string {
	val := Get(key)
	if val == "" {
		panic(fmt.Sprintf("prompt %q not found in prompts.yaml", key))
	}
	return val
}

// GetAll returns a copy of all loaded prompts.
func GetAll() map[string]string {
	if store == nil {
		return nil
	}
	cp := make(map[string]string, len(store))
	for k, v := range store {
		cp[k] = v
	}
	return cp
}
