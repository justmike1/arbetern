package commands

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	maxConversationTurns = 10
	conversationTTL      = 10 * time.Minute
)

type ConversationMemory struct {
	mu    sync.Mutex
	convs map[string]*conversation
}

type conversation struct {
	turns     []turn
	updatedAt time.Time
}

type turn struct {
	User      string
	Assistant string
}

func NewConversationMemory() *ConversationMemory {
	return &ConversationMemory{
		convs: make(map[string]*conversation),
	}
}

func conversationKey(channelID, userID string) string {
	return channelID + ":" + userID
}

func (cm *ConversationMemory) AddUserMessage(channelID, userID, text string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	key := conversationKey(channelID, userID)
	conv, ok := cm.convs[key]
	if !ok || time.Since(conv.updatedAt) > conversationTTL {
		conv = &conversation{}
		cm.convs[key] = conv
	}

	conv.turns = append(conv.turns, turn{User: text})
	conv.updatedAt = time.Now()

	if len(conv.turns) > maxConversationTurns {
		conv.turns = conv.turns[len(conv.turns)-maxConversationTurns:]
	}
}

func (cm *ConversationMemory) SetAssistantResponse(channelID, userID, text string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	key := conversationKey(channelID, userID)
	conv, ok := cm.convs[key]
	if !ok || len(conv.turns) == 0 {
		return
	}

	last := &conv.turns[len(conv.turns)-1]
	last.Assistant = text
	conv.updatedAt = time.Now()
}

func (cm *ConversationMemory) GetHistory(channelID, userID string) string {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	key := conversationKey(channelID, userID)
	conv, ok := cm.convs[key]
	if !ok || time.Since(conv.updatedAt) > conversationTTL {
		return ""
	}

	if len(conv.turns) <= 1 {
		return ""
	}

	var sb strings.Builder
	for _, t := range conv.turns[:len(conv.turns)-1] {
		fmt.Fprintf(&sb, "User: %s\n", t.User)
		if t.Assistant != "" {
			fmt.Fprintf(&sb, "Assistant: %s\n", t.Assistant)
		}
	}
	return sb.String()
}
