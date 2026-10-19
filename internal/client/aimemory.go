package client

import (
	"fmt"
	"net/http"
	"time"
)

// What the assistant has stored about the calling user: the cross-session
// memories Ankra's chat extracts from conversations and recalls into later
// ones. The routes are self-scoped, so a token only ever sees and clears its
// own user's memories in the organisation it is acting in.

const aiMemoriesPath = "/api/v1/org/ai-memory/memories"

// AIMemoryListCap is the most memories one list call returns. A list that
// comes back exactly this long may hold more.
const AIMemoryListCap = 200

// AIMemory is one stored memory on the wire.
type AIMemory struct {
	ID                   string    `json:"id" yaml:"id"`
	Content              string    `json:"content" yaml:"content"`
	ClusterID            *string   `json:"cluster_id" yaml:"cluster_id"`
	SourceConversationID *string   `json:"source_conversation_id" yaml:"source_conversation_id"`
	CreatedAt            time.Time `json:"created_at" yaml:"created_at"`
}

// ListAIMemories reads the calling user's memories, newest first, up to limit.
func (c *Client) ListAIMemories(limit int) ([]AIMemory, error) {
	var response struct {
		Memories []AIMemory `json:"memories"`
	}
	requestURL := fmt.Sprintf("%s%s?limit=%d", c.BaseURL, aiMemoriesPath, limit)
	if err := c.getJSON(requestURL, &response); err != nil {
		return nil, fmt.Errorf("listing AI memories: %w", err)
	}
	if response.Memories == nil {
		return nil, fmt.Errorf("listing AI memories: the platform answered without a memories list")
	}
	return response.Memories, nil
}

// ClearAIMemories deletes every memory stored for the calling user and
// reports how many the platform removed.
func (c *Client) ClearAIMemories() (int, error) {
	var response struct {
		Deleted *int `json:"deleted"`
	}
	if err := c.sendJSON(http.MethodDelete, c.BaseURL+aiMemoriesPath, nil, &response); err != nil {
		return 0, fmt.Errorf("clearing AI memories: %w", err)
	}
	if response.Deleted == nil {
		return 0, fmt.Errorf("clearing AI memories: the platform answered without a deleted count")
	}
	return *response.Deleted, nil
}
