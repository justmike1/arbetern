package commands

import (
	"fmt"
	"strings"
	"sync"
	"time"

	slacklib "github.com/slack-go/slack"
)

const (
	contextMessageLimit = 30
	contextCacheTTL     = 30 * time.Second
)

type ContextProvider struct {
	slackClient SlackClient
	mu          sync.Mutex
	cache       map[string]*contextEntry
}

type contextEntry struct {
	messages  []slacklib.Message
	fetchedAt time.Time
}

func NewContextProvider(slackClient SlackClient) *ContextProvider {
	return &ContextProvider{
		slackClient: slackClient,
		cache:       make(map[string]*contextEntry),
	}
}

func (cp *ContextProvider) GetChannelContext(channelID string) (string, error) {
	cp.mu.Lock()
	entry, ok := cp.cache[channelID]
	if ok && time.Since(entry.fetchedAt) < contextCacheTTL {
		cp.mu.Unlock()
		return formatMessages(entry.messages), nil
	}
	cp.mu.Unlock()

	messages, err := cp.slackClient.FetchChannelHistory(channelID, contextMessageLimit)
	if err != nil {
		return "", fmt.Errorf("failed to fetch channel context: %w", err)
	}

	cp.mu.Lock()
	cp.cache[channelID] = &contextEntry{
		messages:  messages,
		fetchedAt: time.Now(),
	}
	cp.mu.Unlock()

	return formatMessages(messages), nil
}

func formatMessages(messages []slacklib.Message) string {
	if len(messages) == 0 {
		return "(no recent messages)"
	}

	var sb strings.Builder
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		ts := msg.Timestamp
		if t, err := tsToTime(ts); err == nil {
			ts = t.Format("15:04:05")
		}
		fmt.Fprintf(&sb, "[%s @%s]: %s\n", ts, msg.User, msg.Text)
	}
	return sb.String()
}

func tsToTime(ts string) (time.Time, error) {
	parts := strings.SplitN(ts, ".", 2)
	if len(parts) == 0 {
		return time.Time{}, fmt.Errorf("invalid timestamp")
	}
	var sec int64
	for _, c := range parts[0] {
		sec = sec*10 + int64(c-'0')
	}
	return time.Unix(sec, 0), nil
}
