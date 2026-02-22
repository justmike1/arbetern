package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const modelsAPIURL = "https://models.github.ai/inference/chat/completions"

// azureAPIVersion is the Azure OpenAI REST API version to use.
const azureAPIVersion = "2024-10-21"

type ModelsClient struct {
	token      string
	model      string
	httpClient *http.Client

	// Azure OpenAI fields (empty when using GitHub Models).
	azureEndpoint string
	azureAPIKey   string
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Tools    []Tool        `json:"tools,omitempty"`
}

type ChatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type ChatResponse struct {
	Choices []struct {
		Message struct {
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func NewModelsClient(token, model string) *ModelsClient {
	return &ModelsClient{
		token:      token,
		model:      model,
		httpClient: &http.Client{},
	}
}

// NewAzureModelsClient creates a ModelsClient backed by Azure OpenAI.
// The deployment parameter is used as the model/deployment name in the URL.
func NewAzureModelsClient(endpoint, apiKey, deployment string) *ModelsClient {
	endpoint = strings.TrimRight(endpoint, "/")
	return &ModelsClient{
		model:         deployment,
		httpClient:    &http.Client{},
		azureEndpoint: endpoint,
		azureAPIKey:   apiKey,
	}
}

// useAzure returns true when the client is configured for Azure OpenAI.
func (m *ModelsClient) useAzure() bool {
	return m.azureEndpoint != "" && m.azureAPIKey != ""
}

// Model returns the model/deployment name this client is using.
func (m *ModelsClient) Model() string {
	return m.model
}

func (m *ModelsClient) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}
	resp, err := m.doChat(ctx, messages, nil)
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("GitHub Models returned no choices")
	}
	return resp.Choices[0].Message.Content, nil
}

func (m *ModelsClient) CompleteWithTools(ctx context.Context, messages []ChatMessage, tools []Tool) (*ChatResponse, error) {
	return m.doChat(ctx, messages, tools)
}

func (m *ModelsClient) doChat(ctx context.Context, messages []ChatMessage, tools []Tool) (*ChatResponse, error) {
	reqBody := chatRequest{
		Model:    m.model,
		Messages: messages,
		Tools:    tools,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	var apiURL string
	if m.useAzure() {
		apiURL = fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s",
			m.azureEndpoint, m.model, azureAPIVersion)
	} else {
		apiURL = modelsAPIURL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if m.useAzure() {
		req.Header.Set("api-key", m.azureAPIKey)
	} else {
		req.Header.Set("Authorization", "Bearer "+m.token)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("LLM API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LLM API returned %d: %s", resp.StatusCode, string(body))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if chatResp.Error != nil {
		return nil, fmt.Errorf("LLM API error: %s", chatResp.Error.Message)
	}

	return &chatResp, nil
}

func NewChatMessage(role, content string) ChatMessage {
	return ChatMessage{Role: role, Content: content}
}

func NewToolResultMessage(toolCallID, content string) ChatMessage {
	return ChatMessage{Role: "tool", Content: content, ToolCallID: toolCallID}
}
