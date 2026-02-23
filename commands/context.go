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

	return cp.GetFreshChannelContext(channelID)
}

func (cp *ContextProvider) GetFreshChannelContext(channelID string) (string, error) {
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

	total := len(messages)
	var sb strings.Builder
	fmt.Fprintf(&sb, "Messages listed from NEWEST (message 1) to OLDEST (message %d):\n\n", total)
	idx := 1
	for i := 0; i < total; i++ {
		msg := messages[i]
		text := extractMessageContent(msg)
		if text == "" {
			continue
		}
		ts := msg.Timestamp
		if t, err := tsToTime(ts); err == nil {
			ts = t.Format("15:04:05")
		}
		sender := msg.User
		if sender == "" && msg.Username != "" {
			sender = msg.Username
		}
		if sender == "" && msg.BotID != "" {
			sender = "bot:" + msg.BotID
		}
		label := ""
		if idx == 1 {
			label = " [LATEST]"
		}
		fmt.Fprintf(&sb, "Message %d%s [%s @%s] (thread_ts=%s): %s\n", idx, label, ts, sender, msg.Timestamp, text)
		idx++
	}
	if idx == 1 {
		return "(no recent messages with content)"
	}
	return sb.String()
}

func extractMessageContent(msg slacklib.Message) string {
	var parts []string

	if msg.Text != "" {
		parts = append(parts, msg.Text)
	}

	for _, att := range msg.Attachments {
		var attParts []string
		if att.Pretext != "" {
			attParts = append(attParts, att.Pretext)
		}
		if att.Title != "" {
			title := att.Title
			if att.TitleLink != "" {
				title += " (" + att.TitleLink + ")"
			}
			attParts = append(attParts, title)
		}
		if att.Text != "" {
			attParts = append(attParts, att.Text)
		}
		for _, f := range att.Fields {
			attParts = append(attParts, f.Title+": "+f.Value)
		}
		for _, action := range att.Actions {
			if action.URL != "" {
				attParts = append(attParts, action.Text+": "+action.URL)
			}
		}
		attParts = append(attParts, extractBlockURLs(att.Blocks.BlockSet)...)
		if len(attParts) == 0 && att.Fallback != "" {
			attParts = append(attParts, att.Fallback)
		}
		if len(attParts) > 0 {
			parts = append(parts, strings.Join(attParts, "\n"))
		}
	}

	parts = append(parts, extractBlockURLs(msg.Blocks.BlockSet)...)

	return strings.Join(parts, "\n---\n")
}

func extractBlockURLs(blocks []slacklib.Block) []string {
	var urls []string
	for _, block := range blocks {
		switch b := block.(type) {
		case *slacklib.ActionBlock:
			if b.Elements != nil {
				for _, elem := range b.Elements.ElementSet {
					if btn, ok := elem.(*slacklib.ButtonBlockElement); ok && btn.URL != "" {
						label := btn.ActionID
						if btn.Text != nil {
							label = btn.Text.Text
						}
						urls = append(urls, label+": "+btn.URL)
					}
				}
			}
		case *slacklib.SectionBlock:
			if b.Accessory != nil && b.Accessory.ButtonElement != nil && b.Accessory.ButtonElement.URL != "" {
				btn := b.Accessory.ButtonElement
				label := btn.ActionID
				if btn.Text != nil {
					label = btn.Text.Text
				}
				urls = append(urls, label+": "+btn.URL)
			}
		}
	}
	return urls
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
