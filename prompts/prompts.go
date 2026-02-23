package prompts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const defaultPath = "agents/ovad/prompts.yaml"
const defaultAgentsDir = "agents"

var store map[string]string

// AgentConfig holds metadata and prompts for a single agent.
type AgentConfig struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Prompts map[string]string `json:"prompts"`
}

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
		panic(fmt.Sprintf("prompt %q not found in prompts file", key))
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

// DiscoverAgents scans the agents directory and returns all agent configs.
// Each subdirectory under agentsDir is treated as an agent, with a prompts.yaml inside.
func DiscoverAgents(agentsDir string) ([]AgentConfig, error) {
	if agentsDir == "" {
		agentsDir = os.Getenv("AGENTS_DIR")
	}
	if agentsDir == "" {
		agentsDir = defaultAgentsDir
	}

	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read agents directory %s: %w", agentsDir, err)
	}

	var agents []AgentConfig
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		promptsPath := filepath.Join(agentsDir, entry.Name(), "prompts.yaml")
		data, err := os.ReadFile(promptsPath)
		if err != nil {
			continue // skip dirs without prompts.yaml
		}

		parsed := make(map[string]string)
		if err := yaml.Unmarshal(data, &parsed); err != nil {
			continue
		}

		name := entry.Name()
		// Derive a display name: capitalize first letter.
		displayName := strings.ToUpper(name[:1]) + name[1:]

		agents = append(agents, AgentConfig{
			ID:      name,
			Name:    displayName,
			Prompts: parsed,
		})
	}

	return agents, nil
}
