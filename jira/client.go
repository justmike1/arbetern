package jira

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
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
	AssigneeID  string // Jira account ID of the assignee (optional)
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
	Description *adfDoc     `json:"description,omitempty"`
	Labels      []string    `json:"labels,omitempty"`
	Assignee    *accountRef `json:"assignee,omitempty"`
}

type accountRef struct {
	AccountID string `json:"accountId"`
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
	Type  string        `json:"type"`
	Attrs *adfMarkAttrs `json:"attrs,omitempty"`
}

type adfMarkAttrs struct {
	Href string `json:"href,omitempty"`
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

// parseInlineMarkdown converts simple inline markdown (**bold**, `code`, [text](url)) to ADF inlines.
func parseInlineMarkdown(text string) []adfInline {
	var inlines []adfInline
	remaining := text
	for len(remaining) > 0 {
		// Find the next special marker.
		boldIdx := strings.Index(remaining, "**")
		codeIdx := strings.Index(remaining, "`")
		linkIdx := strings.Index(remaining, "[")

		// No more markers.
		if boldIdx < 0 && codeIdx < 0 && linkIdx < 0 {
			if remaining != "" {
				inlines = append(inlines, adfInline{Type: "text", Text: remaining})
			}
			break
		}

		// Determine which comes first.
		type candidate struct {
			idx    int
			marker string
		}
		candidates := []candidate{}
		if boldIdx >= 0 {
			candidates = append(candidates, candidate{boldIdx, "**"})
		}
		if codeIdx >= 0 {
			candidates = append(candidates, candidate{codeIdx, "`"})
		}
		if linkIdx >= 0 {
			candidates = append(candidates, candidate{linkIdx, "["})
		}
		// Pick the earliest.
		best := candidates[0]
		for _, c := range candidates[1:] {
			if c.idx < best.idx {
				best = c
			}
		}
		nextIdx := best.idx
		marker := best.marker

		// Add plain text before the marker.
		if nextIdx > 0 {
			inlines = append(inlines, adfInline{Type: "text", Text: remaining[:nextIdx]})
		}

		// Handle markdown link: [text](url)
		if marker == "[" {
			rest := remaining[nextIdx+1:]
			closeB := strings.Index(rest, "](")
			if closeB >= 0 {
				linkText := rest[:closeB]
				urlStart := rest[closeB+2:]
				closeP := strings.Index(urlStart, ")")
				if closeP >= 0 {
					href := urlStart[:closeP]
					inlines = append(inlines, adfInline{
						Type:  "text",
						Text:  linkText,
						Marks: []adfMark{{Type: "link", Attrs: &adfMarkAttrs{Href: href}}},
					})
					remaining = urlStart[closeP+1:]
					continue
				}
			}
			// Not a valid link — treat [ as plain text.
			inlines = append(inlines, adfInline{Type: "text", Text: "["})
			remaining = rest
			continue
		}

		// Find the closing marker for bold/code.
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
	if input.AssigneeID != "" {
		payload.Fields.Assignee = &accountRef{AccountID: input.AssigneeID}
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

// JiraUser represents a user returned by the Jira user search API.
type JiraUser struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
	Active      bool   `json:"active"`
}

// SearchUsers searches for Jira users matching the given query string.
// It first tries the project-scoped assignable-user endpoint (better results for
// teams/service-accounts), then falls back to the general user search.
// Results are ranked by how well the display name matches the query.
func (c *Client) SearchUsers(query string) ([]JiraUser, error) {
	return c.SearchAssignableUsers(query, c.projectKey)
}

// searchUsersRaw performs a single user search API call and returns active users.
func (c *Client) searchUsersRaw(query, project string) ([]JiraUser, error) {
	var searchURL string
	if project != "" {
		searchURL = fmt.Sprintf("%s/rest/api/3/user/assignable/search?query=%s&project=%s&maxResults=50",
			c.baseURL, url.QueryEscape(query), url.QueryEscape(project))
	} else {
		searchURL = fmt.Sprintf("%s/rest/api/3/user/search?query=%s&maxResults=50",
			c.baseURL, url.QueryEscape(query))
	}

	req, err := http.NewRequest(http.MethodGet, searchURL, nil)
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

	var users []JiraUser
	if err := json.Unmarshal(respBody, &users); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	active := make([]JiraUser, 0, len(users))
	for _, u := range users {
		if u.Active {
			active = append(active, u)
		}
	}
	return active, nil
}

// coreQuery strips common suffixes like "team" or "group" from the query.
func coreQuery(query string) string {
	q := strings.ToLower(strings.TrimSpace(query))
	for _, suffix := range []string{" team", " group", " squad"} {
		q = strings.TrimSuffix(q, suffix)
	}
	return strings.TrimSpace(q)
}

// SearchAssignableUsers searches for users assignable to the given project.
// It searches with both the original query and the core term (e.g. "application"
// from "application team") to avoid Jira's fuzzy matching on common words like "team".
// Results are deduplicated, ranked by display-name match quality, and returned.
func (c *Client) SearchAssignableUsers(query, project string) ([]JiraUser, error) {
	core := coreQuery(query)

	// Always search with the core term first (most likely to find the right match).
	users, err := c.searchUsersRaw(core, project)
	if err != nil {
		return nil, err
	}

	// If the core differs from the original query, also search with the full query
	// and merge results (deduplicating by account ID).
	if core != strings.ToLower(strings.TrimSpace(query)) {
		extra, err := c.searchUsersRaw(query, project)
		if err == nil && len(extra) > 0 {
			seen := make(map[string]bool, len(users))
			for _, u := range users {
				seen[u.AccountID] = true
			}
			for _, u := range extra {
				if !seen[u.AccountID] {
					users = append(users, u)
				}
			}
		}
	}

	// Rank by display-name match quality.
	ranked := rankUsersByMatch(users, query)
	return ranked, nil
}

// rankUsersByMatch sorts users so that the best display-name match for query
// comes first. Priority: exact match > starts-with > contains > rest.
func rankUsersByMatch(users []JiraUser, query string) []JiraUser {
	if len(users) <= 1 {
		return users
	}

	q := strings.ToLower(strings.TrimSpace(query))
	// Strip common suffixes like "team" for matching, e.g. "application team" → "application"
	qCore := q
	for _, suffix := range []string{" team", " group"} {
		qCore = strings.TrimSuffix(qCore, suffix)
	}
	qCore = strings.TrimSpace(qCore)

	type scored struct {
		user  JiraUser
		score int // lower is better
	}
	scored_users := make([]scored, len(users))
	for i, u := range users {
		dn := strings.ToLower(u.DisplayName)

		score := 100 // default: no match bonus
		switch {
		case dn == q || dn == qCore:
			score = 0 // exact match
		case strings.HasPrefix(dn, qCore):
			score = 10 // starts with core query
		case strings.Contains(dn, qCore):
			score = 20 // contains core query
		case strings.HasPrefix(dn, q):
			score = 30
		case strings.Contains(dn, q):
			score = 40
		}
		scored_users[i] = scored{user: u, score: score}
	}

	// Sort by score ascending (best match first).
	for i := 1; i < len(scored_users); i++ {
		for j := i; j > 0 && scored_users[j].score < scored_users[j-1].score; j-- {
			scored_users[j], scored_users[j-1] = scored_users[j-1], scored_users[j]
		}
	}

	result := make([]JiraUser, len(scored_users))
	for i, s := range scored_users {
		result[i] = s.user
	}
	return result
}

// BestUserMatch returns the best-matching user and whether the match is good.
// A match is considered good if the user's display name contains the core query term.
func BestUserMatch(users []JiraUser, query string) (JiraUser, bool) {
	if len(users) == 0 {
		return JiraUser{}, false
	}
	ranked := rankUsersByMatch(users, query)
	core := coreQuery(query)
	dn := strings.ToLower(ranked[0].DisplayName)
	q := strings.ToLower(strings.TrimSpace(query))
	return ranked[0], strings.Contains(dn, core) || strings.Contains(dn, q)
}

// teamFieldInfo stores discovered team field metadata.
type teamFieldInfo struct {
	ID      string // e.g. "customfield_10001"
	JQLName string // e.g. "Team" or "cf[10001]"
}

// FindTeamFields discovers all "Team"-like custom fields from Jira field metadata.
// Results are sorted so that the actual Jira Teams field (clause "Team[Team]") comes first,
// followed by dropdown/select variants.
func (c *Client) FindTeamFields() ([]teamFieldInfo, error) {
	reqURL := fmt.Sprintf("%s/rest/api/3/field", c.baseURL)
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
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

	var fields []struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		ClauseNames []string `json:"clauseNames"`
		Schema      *struct {
			Type   string `json:"type"`
			Custom string `json:"custom"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(respBody, &fields); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	// Collect all team-like fields, partition by priority.
	var preferred []teamFieldInfo // fields with [Team] clause (actual Jira Teams)
	var fallback []teamFieldInfo  // other team-named fields (dropdown, select, etc.)

	for _, f := range fields {
		if f.Schema == nil {
			continue
		}
		nameL := strings.ToLower(f.Name)
		customL := strings.ToLower(f.Schema.Custom)
		if nameL == "team" || strings.Contains(customL, "teams") {
			jqlName := f.ID
			if len(f.ClauseNames) > 0 {
				jqlName = f.ClauseNames[0]
			}
			info := teamFieldInfo{ID: f.ID, JQLName: jqlName}

			// Check if any clause name contains "[Team]" — that's the real Teams integration field.
			isTeamsField := false
			for _, cn := range f.ClauseNames {
				if strings.Contains(cn, "[Team]") {
					isTeamsField = true
					info.JQLName = cn // prefer the [Team] clause name
					break
				}
			}
			if isTeamsField || strings.Contains(customL, "teams") {
				preferred = append(preferred, info)
			} else {
				fallback = append(fallback, info)
			}
			log.Printf("[jira] discovered team field: id=%s, jqlName=%s, custom=%s, clauses=%v",
				f.ID, info.JQLName, f.Schema.Custom, f.ClauseNames)
		}
	}

	// Preferred fields first, then fallback.
	result := append(preferred, fallback...)
	if len(result) == 0 {
		return nil, fmt.Errorf("no Team custom field found")
	}
	return result, nil
}

// ResolveTeam attempts to find a team by name.
// It discovers all team-like fields and tries multiple strategies for each.
// Returns (fieldID, teamID, displayName, error).
func (c *Client) ResolveTeam(teamName string) (string, string, string, error) {
	fields, err := c.FindTeamFields()
	if err != nil {
		return "", "", "", fmt.Errorf("find team fields: %w", err)
	}

	core := coreQuery(teamName)
	log.Printf("[jira] resolving team %q (core: %q, candidates: %d)", teamName, core, len(fields))

	// --- Strategy 1: Jira Teams REST API (field-independent) ---
	if team, err := c.searchTeamsAPI(core); err == nil && team != nil {
		log.Printf("[jira] found team via Teams API: %s", team.displayName)
		return fields[0].ID, team.teamID, team.displayName, nil
	} else if err != nil {
		log.Printf("[jira] Teams API failed: %v", err)
	}

	// --- Strategy 2: For each discovered team field, scan existing issues ---
	for _, field := range fields {
		log.Printf("[jira] trying issue scan with field %s (jqlName: %s)", field.ID, field.JQLName)
		teamFromIssues, err := c.findTeamFromExistingIssues(&field, core)
		if err == nil && teamFromIssues != nil {
			log.Printf("[jira] found team via issue scan (field %s): %s", field.ID, teamFromIssues.displayName)
			return field.ID, teamFromIssues.teamID, teamFromIssues.displayName, nil
		} else if err != nil {
			log.Printf("[jira] issue scan failed for field %s: %v", field.ID, err)
		}
	}

	return "", "", "", fmt.Errorf("no team found matching %q (tried Teams API and issue-scan across %d fields)", teamName, len(fields))
}

// SetTeamField tries multiple value formats to set the team custom field on an issue.
// Atlassian Team fields accept different formats depending on the Jira/plugin version.
func (c *Client) SetTeamField(issueKey, fieldID, teamID string) error {
	// Try these formats in order — different Jira versions accept different ones.
	formats := []struct {
		name  string
		value interface{}
	}{
		{"bare ID", teamID},
		{"id object", map[string]string{"id": teamID}},
		{"teamId object", map[string]string{"teamId": teamID}},
	}

	var lastErr error
	for _, f := range formats {
		err := c.UpdateIssueFields(issueKey, map[string]interface{}{
			fieldID: f.value,
		})
		if err == nil {
			log.Printf("[jira] set team on %s using format %q", issueKey, f.name)
			return nil
		}
		log.Printf("[jira] set team on %s with format %q failed: %v", issueKey, f.name, err)
		lastErr = err
	}
	return lastErr
}

// findTeamFromExistingIssues searches for issues in the default project that have the
// Team field set, then filters client-side for a matching team name.
func (c *Client) findTeamFromExistingIssues(field *teamFieldInfo, query string) (*teamSearchResult, error) {
	// The Team custom field in Jira uses the clause name "Team[Team]" in JQL.
	// We try: the discovered JQL clause name (e.g. "Team[Team]"), then cf[XXXX] as fallback.
	jqlClause := fmt.Sprintf(`"%s"`, field.JQLName)
	cfFallback := fmt.Sprintf("cf[%s]", strings.TrimPrefix(field.ID, "customfield_"))

	jqlQueries := []string{
		// Try with the proper JQL clause name first (e.g. "Team[Team]").
		fmt.Sprintf(`project = %s AND %s IS NOT EMPTY ORDER BY updated DESC`, c.projectKey, jqlClause),
		// Fallback: cf[XXXX] syntax, project-scoped.
		fmt.Sprintf(`project = %s AND %s IS NOT EMPTY ORDER BY updated DESC`, c.projectKey, cfFallback),
		// Fallback: any issue with the team field set.
		fmt.Sprintf(`%s IS NOT EMPTY ORDER BY updated DESC`, jqlClause),
	}

	q := strings.ToLower(query)

	for _, jql := range jqlQueries {
		searchURL := fmt.Sprintf("%s/rest/api/3/search/jql?jql=%s&maxResults=50&fields=%s",
			c.baseURL, url.QueryEscape(jql), field.ID)

		req, err := http.NewRequest(http.MethodGet, searchURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.SetBasicAuth(c.email, c.apiToken)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
			log.Printf("[jira] JQL search failed (HTTP %d) for: %s — %s", resp.StatusCode, jql, string(respBody))
			continue
		}

		var result struct {
			Total  int `json:"total"`
			Issues []struct {
				Key    string                     `json:"key"`
				Fields map[string]json.RawMessage `json:"fields"`
			} `json:"issues"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			continue
		}

		log.Printf("[jira] JQL returned %d issues (total: %d) for team field scan", len(result.Issues), result.Total)

		// Scan returned issues for a team field value matching our query.
		for _, issue := range result.Issues {
			teamValue, ok := issue.Fields[field.ID]
			if !ok || string(teamValue) == "null" {
				continue
			}

			// Try to extract the team display name from different possible structures.
			name := extractTeamName(teamValue)
			if name == "" {
				continue
			}

			nameL := strings.ToLower(name)
			if nameL == q || strings.Contains(nameL, q) || strings.Contains(q, nameL) {
				// Extract the team ID for setting the field.
				teamID := extractTeamID(teamValue)
				if teamID == "" {
					log.Printf("[jira] matched team %q from issue %s but could not extract ID, raw: %s", name, issue.Key, string(teamValue))
					continue
				}
				// The Atlassian Team field accepts multiple formats depending on version.
				// We return the ID string; the caller will try different wrapping formats.
				log.Printf("[jira] matched team %q (id: %s) from issue %s", name, teamID, issue.Key)
				return &teamSearchResult{displayName: name, teamID: teamID}, nil
			}
		}
	}

	return nil, fmt.Errorf("no issue found with team matching %q", query)
}

// extractTeamID extracts the team UUID from a Jira team field value.
// The value can be {"id": "uuid", ...} or a plain string UUID.
func extractTeamID(data json.RawMessage) string {
	var obj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &obj); err == nil && obj.ID != "" {
		return obj.ID
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil && s != "" {
		return s
	}
	return ""
}

// extractTeamName tries to extract a human-readable team name from a Jira field value.
// Team fields can have various structures depending on the Jira configuration.
func extractTeamName(data json.RawMessage) string {
	// Try: {"name": "..."} or {"displayName": "..."} or {"title": "..."}
	var obj struct {
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
		Title       string `json:"title"`
	}
	if err := json.Unmarshal(data, &obj); err == nil {
		if obj.Name != "" {
			return obj.Name
		}
		if obj.DisplayName != "" {
			return obj.DisplayName
		}
		if obj.Title != "" {
			return obj.Title
		}
	}

	// Try: plain string value.
	var s string
	if err := json.Unmarshal(data, &s); err == nil && s != "" {
		return s
	}

	return ""
}

// teamSearchResult holds a resolved team from one of the search strategies.
type teamSearchResult struct {
	displayName string
	teamID      string // the team UUID
}

// searchTeamsAPI tries multiple Jira/Atlassian team search endpoints.
func (c *Client) searchTeamsAPI(query string) (*teamSearchResult, error) {
	// Endpoints to try, in order of likelihood.
	endpoints := []string{
		fmt.Sprintf("%s/rest/teams/1.0/teams/find?query=%s", c.baseURL, url.QueryEscape(query)),
		fmt.Sprintf("%s/rest/teams/1.0/teams?query=%s", c.baseURL, url.QueryEscape(query)),
	}

	for _, ep := range endpoints {
		req, err := http.NewRequest(http.MethodGet, ep, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.SetBasicAuth(c.email, c.apiToken)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
			continue
		}

		// Parse the response — it can be an array or an object with a "teams" key.
		teams := c.parseTeamsResponse(respBody)

		// Find best match by name.
		q := strings.ToLower(query)
		for _, t := range teams {
			dn := strings.ToLower(t.DisplayName)
			if dn == q || strings.Contains(dn, q) || strings.Contains(q, dn) {
				return &teamSearchResult{displayName: t.DisplayName, teamID: t.TeamID}, nil
			}
		}
	}

	return nil, fmt.Errorf("teams API search found no match for %q", query)
}

// jiraTeamEntry represents a team from the Jira Teams API response.
type jiraTeamEntry struct {
	TeamID      string `json:"teamId"`
	DisplayName string `json:"displayName"`
	// Some responses use different field names.
	ID    string `json:"id"`
	Name  string `json:"name"`
	Title string `json:"title"`
}

// effectiveID returns the team's ID, checking multiple possible field names.
func (t jiraTeamEntry) effectiveID() string {
	if t.TeamID != "" {
		return t.TeamID
	}
	return t.ID
}

// effectiveName returns the team's display name, checking multiple possible field names.
func (t jiraTeamEntry) effectiveName() string {
	if t.DisplayName != "" {
		return t.DisplayName
	}
	if t.Name != "" {
		return t.Name
	}
	return t.Title
}

// normalizedTeam returns a team entry with normalized fields.
type normalizedTeam struct {
	TeamID      string
	DisplayName string
}

// parseTeamsResponse parses teams from various response formats.
func (c *Client) parseTeamsResponse(data []byte) []normalizedTeam {
	var result []normalizedTeam

	// Try 1: direct array of team objects.
	var arr []jiraTeamEntry
	if err := json.Unmarshal(data, &arr); err == nil && len(arr) > 0 {
		for _, t := range arr {
			if id := t.effectiveID(); id != "" {
				result = append(result, normalizedTeam{TeamID: id, DisplayName: t.effectiveName()})
			}
		}
		if len(result) > 0 {
			return result
		}
	}

	// Try 2: object with "teams" key.
	var wrapper struct {
		Teams []jiraTeamEntry `json:"teams"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil && len(wrapper.Teams) > 0 {
		for _, t := range wrapper.Teams {
			if id := t.effectiveID(); id != "" {
				result = append(result, normalizedTeam{TeamID: id, DisplayName: t.effectiveName()})
			}
		}
		if len(result) > 0 {
			return result
		}
	}

	// Try 3: object with "values" key (paginated response).
	var paginated struct {
		Values []jiraTeamEntry `json:"values"`
	}
	if err := json.Unmarshal(data, &paginated); err == nil && len(paginated.Values) > 0 {
		for _, t := range paginated.Values {
			if id := t.effectiveID(); id != "" {
				result = append(result, normalizedTeam{TeamID: id, DisplayName: t.effectiveName()})
			}
		}
	}

	return result
}

// UpdateIssueFields sets arbitrary fields on an existing Jira issue.
func (c *Client) UpdateIssueFields(issueKey string, fields map[string]interface{}) error {
	payload := map[string]interface{}{
		"fields": fields,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	reqURL := fmt.Sprintf("%s/rest/api/3/issue/%s", c.baseURL, issueKey)
	req, err := http.NewRequest(http.MethodPut, reqURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.email, c.apiToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("jira API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}
