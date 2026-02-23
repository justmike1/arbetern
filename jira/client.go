package jira

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// Client provides access to the Jira Cloud REST API v3.
type Client struct {
	baseURL    string // e.g. "https://yourorg.atlassian.net"
	email      string
	apiToken   string
	projectKey string // default project key
	httpClient *http.Client
}

// NewClient creates a new Jira API client.
// baseURL should be the Atlassian instance URL (e.g. "https://yourorg.atlassian.net").
func NewClient(baseURL, email, apiToken, defaultProject string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		email:      email,
		apiToken:   apiToken,
		projectKey: defaultProject,
		httpClient: &http.Client{},
	}
}

// DefaultProject returns the configured default project key.
func (c *Client) DefaultProject() string {
	return c.projectKey
}

// Issue represents a created Jira issue.
type Issue struct {
	Key    string `json:"key"`
	ID     string `json:"id"`
	Self   string `json:"self"`
	Browse string `json:"-"` // human-friendly URL
}

// CreateIssueInput holds parameters for creating a Jira issue.
type CreateIssueInput struct {
	Project     string // project key, e.g. "ENG"
	Summary     string
	Description string
	IssueType   string // e.g. "Task", "Bug", "Story"
	Labels      []string
}

// createIssuePayload is the JSON body sent to the Jira API.
type createIssuePayload struct {
	Fields createIssueFields `json:"fields"`
}

type createIssueFields struct {
	Project   projectRef `json:"project"`
	Summary   string     `json:"summary"`
	IssueType issueType  `json:"issuetype"`
	// Description uses Atlassian Document Format (ADF).
	Description *adfDoc  `json:"description,omitempty"`
	Labels      []string `json:"labels,omitempty"`
}

type projectRef struct {
	Key string `json:"key"`
}

type issueType struct {
	Name string `json:"name"`
}

// --- Atlassian Document Format (ADF) helpers ---

type adfDoc struct {
	Type    string    `json:"type"`
	Version int       `json:"version"`
	Content []adfNode `json:"content"`
}

type adfNode struct {
	Type    string          `json:"type"`
	Attrs   *adfAttrs       `json:"attrs,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
}

type adfAttrs struct {
	Level int    `json:"level,omitempty"`
	Order int    `json:"order,omitempty"`
	URL   string `json:"url,omitempty"`
	Href  string `json:"href,omitempty"`
}

type adfInline struct {
	Type  string    `json:"type"`
	Text  string    `json:"text,omitempty"`
	Marks []adfMark `json:"marks,omitempty"`
	Attrs *adfAttrs `json:"attrs,omitempty"`
}

type adfMark struct {
	Type string `json:"type"`
}

// marshalInlines converts inline elements to json.RawMessage.
func marshalInlines(inlines []adfInline) json.RawMessage {
	if len(inlines) == 0 {
		return nil
	}
	b, _ := json.Marshal(inlines)
	return b
}

// marshalNodes converts node elements to json.RawMessage.
func marshalNodes(nodes []adfNode) json.RawMessage {
	if len(nodes) == 0 {
		return nil
	}
	b, _ := json.Marshal(nodes)
	return b
}

// textToADF converts markdown-like text into a proper Atlassian Document Format document.
// Supports: # headings, - bullet lists, 1) ordered lists, **bold**, `code`, and plain paragraphs.
func textToADF(text string) *adfDoc {
	if text == "" {
		return nil
	}

	lines := strings.Split(text, "\n")
	var nodes []adfNode
	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Skip empty lines.
		if trimmed == "" {
			i++
			continue
		}

		// Horizontal rule: ---
		if trimmed == "---" || trimmed == "***" || trimmed == "___" {
			nodes = append(nodes, adfNode{Type: "rule"})
			i++
			continue
		}

		// Headings: # ## ### etc.
		if strings.HasPrefix(trimmed, "#") {
			level := 0
			for _, c := range trimmed {
				if c == '#' {
					level++
				} else {
					break
				}
			}
			if level > 6 {
				level = 6
			}
			headingText := strings.TrimSpace(trimmed[level:])
			if headingText != "" {
				nodes = append(nodes, adfNode{
					Type:    "heading",
					Attrs:   &adfAttrs{Level: level},
					Content: marshalInlines(parseInlineMarkdown(headingText)),
				})
			}
			i++
			continue
		}

		// Bullet list: lines starting with - or *
		if isBulletLine(trimmed) {
			var items []adfNode
			for i < len(lines) {
				lt := strings.TrimSpace(lines[i])
				if !isBulletLine(lt) {
					break
				}
				itemText := strings.TrimSpace(lt[1:]) // strip - or *
				items = append(items, adfNode{
					Type: "listItem",
					Content: marshalNodes([]adfNode{
						{Type: "paragraph", Content: marshalInlines(parseInlineMarkdown(itemText))},
					}),
				})
				i++
			}
			nodes = append(nodes, adfNode{
				Type:    "bulletList",
				Content: marshalNodes(items),
			})
			continue
		}

		// Ordered list: lines starting with number) or number.
		if isOrderedLine(trimmed) {
			var items []adfNode
			for i < len(lines) {
				lt := strings.TrimSpace(lines[i])
				if !isOrderedLine(lt) {
					break
				}
				itemText := stripOrderedPrefix(lt)
				items = append(items, adfNode{
					Type: "listItem",
					Content: marshalNodes([]adfNode{
						{Type: "paragraph", Content: marshalInlines(parseInlineMarkdown(itemText))},
					}),
				})
				i++
			}
			nodes = append(nodes, adfNode{
				Type:    "orderedList",
				Content: marshalNodes(items),
			})
			continue
		}

		// Regular paragraph — collect consecutive non-special lines.
		var paraLines []string
		for i < len(lines) {
			lt := strings.TrimSpace(lines[i])
			if lt == "" || strings.HasPrefix(lt, "#") || isBulletLine(lt) || isOrderedLine(lt) {
				break
			}
			paraLines = append(paraLines, lt)
			i++
		}
		paraText := strings.Join(paraLines, " ")
		nodes = append(nodes, adfNode{
			Type:    "paragraph",
			Content: marshalInlines(parseInlineMarkdown(paraText)),
		})
	}

	if len(nodes) == 0 {
		return nil
	}
	return &adfDoc{
		Type:    "doc",
		Version: 1,
		Content: nodes,
	}
}

func isBulletLine(s string) bool {
	return (strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ")) && len(s) > 2
}

var orderedLineRe = regexp.MustCompile(`^\d+[.)]\s+`)

func isOrderedLine(s string) bool {
	return orderedLineRe.MatchString(s)
}

func stripOrderedPrefix(s string) string {
	loc := orderedLineRe.FindStringIndex(s)
	if loc == nil {
		return s
	}
	return strings.TrimSpace(s[loc[1]:])
}

// parseInlineMarkdown converts simple inline markdown (**bold**, `code`) to ADF inlines.
func parseInlineMarkdown(text string) []adfInline {
	var inlines []adfInline
	remaining := text
	for len(remaining) > 0 {
		// Find the next special marker.
		boldIdx := strings.Index(remaining, "**")
		codeIdx := strings.Index(remaining, "`")

		// No more markers.
		if boldIdx < 0 && codeIdx < 0 {
			if remaining != "" {
				inlines = append(inlines, adfInline{Type: "text", Text: remaining})
			}
			break
		}

		// Determine which comes first.
		var nextIdx int
		var marker string
		if boldIdx >= 0 && (codeIdx < 0 || boldIdx < codeIdx) {
			nextIdx = boldIdx
			marker = "**"
		} else {
			nextIdx = codeIdx
			marker = "`"
		}

		// Add plain text before the marker.
		if nextIdx > 0 {
			inlines = append(inlines, adfInline{Type: "text", Text: remaining[:nextIdx]})
		}

		// Find the closing marker.
		rest := remaining[nextIdx+len(marker):]
		closeIdx := strings.Index(rest, marker)
		if closeIdx < 0 {
			// No closing marker — treat as plain text.
			inlines = append(inlines, adfInline{Type: "text", Text: remaining[nextIdx:]})
			break
		}

		inner := rest[:closeIdx]
		if marker == "**" {
			inlines = append(inlines, adfInline{
				Type:  "text",
				Text:  inner,
				Marks: []adfMark{{Type: "strong"}},
			})
		} else {
			inlines = append(inlines, adfInline{
				Type:  "text",
				Text:  inner,
				Marks: []adfMark{{Type: "code"}},
			})
		}

		remaining = rest[closeIdx+len(marker):]
	}
	return inlines
}

// CreateIssue creates a new issue in Jira and returns its details.
func (c *Client) CreateIssue(input CreateIssueInput) (*Issue, error) {
	if input.Project == "" {
		input.Project = c.projectKey
	}
	if input.Project == "" {
		return nil, fmt.Errorf("project key is required (no default configured)")
	}
	if input.IssueType == "" {
		input.IssueType = "Task"
	}

	payload := createIssuePayload{
		Fields: createIssueFields{
			Project:     projectRef{Key: input.Project},
			Summary:     input.Summary,
			IssueType:   issueType{Name: input.IssueType},
			Description: textToADF(input.Description),
			Labels:      input.Labels,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	url := fmt.Sprintf("%s/rest/api/3/issue", c.baseURL)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.email, c.apiToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("jira API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var issue Issue
	if err := json.Unmarshal(respBody, &issue); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	issue.Browse = fmt.Sprintf("%s/browse/%s", c.baseURL, issue.Key)
	return &issue, nil
}

// ListProjects returns the keys of all projects visible to the authenticated user.
func (c *Client) ListProjects() ([]string, error) {
	url := fmt.Sprintf("%s/rest/api/3/project/search?maxResults=100&status=live", c.baseURL)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.email, c.apiToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("jira API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Values []struct {
			Key  string `json:"key"`
			Name string `json:"name"`
		} `json:"values"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	keys := make([]string, 0, len(result.Values))
	for _, p := range result.Values {
		keys = append(keys, fmt.Sprintf("%s (%s)", p.Key, p.Name))
	}
	return keys, nil
}
