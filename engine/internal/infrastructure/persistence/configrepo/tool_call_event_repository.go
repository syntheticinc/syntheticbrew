package configrepo

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	pb "github.com/syntheticinc/syntheticbrew/api/proto/gen"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"github.com/syntheticinc/syntheticbrew/internal/service/eventformat"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

const maxOutputSize = 10 * 1024 // 10KB

// ToolCallFilters holds optional filters for querying tool call events.
type ToolCallFilters struct {
	SessionID string
	AgentName string
	ToolName  string
	Status    string // "completed" or "failed"
	UserID    string
	From      *time.Time
	To        *time.Time
}

// ToolCallEntry represents a single tool call with its result.
type ToolCallEntry struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id"`
	AgentName  string    `json:"agent_name"`
	ToolName   string    `json:"tool_name"`
	Input      string    `json:"input"`
	Output     string    `json:"output"`
	Status     string    `json:"status"`
	DurationMs int64     `json:"duration_ms"`
	UserID     string    `json:"user_id"`
	CreatedAt  time.Time `json:"created_at"`
}

// ToolCallEventRepository queries session_event_log for tool call audit data.
type ToolCallEventRepository struct {
	db *gorm.DB
}

// NewToolCallEventRepository creates a new ToolCallEventRepository.
func NewToolCallEventRepository(db *gorm.DB) *ToolCallEventRepository {
	return &ToolCallEventRepository{db: db}
}

// QueryToolCalls returns tool call entries matching the filters with pagination.
// All queries are scoped to the current tenant via tenantScope.
func (r *ToolCallEventRepository) QueryToolCalls(ctx context.Context, filters ToolCallFilters, page, perPage int) ([]ToolCallEntry, int64, error) {
	// Step 1: Query tool_call_start events with filters.
	startQuery := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Model(&models.SessionEventLogModel{}).
		Where("event_type = ?", "tool_call_start")

	if filters.SessionID != "" {
		startQuery = startQuery.Where("session_id = ?", filters.SessionID)
	}
	if filters.From != nil {
		startQuery = startQuery.Where("created_at >= ?", *filters.From)
	}
	if filters.To != nil {
		startQuery = startQuery.Where("created_at <= ?", *filters.To)
	}

	// Count total before pagination (we'll refine after JSON filtering).
	var startEvents []models.SessionEventLogModel
	if err := startQuery.Order("created_at DESC").Find(&startEvents).Error; err != nil {
		return nil, 0, fmt.Errorf("query tool call start events: %w", err)
	}

	// Step 2: Query all tool_call_end events for the same sessions.
	sessionIDs := uniqueSessionIDs(startEvents)
	endMap := make(map[string]models.SessionEventLogModel)
	if len(sessionIDs) > 0 {
		var endEvents []models.SessionEventLogModel
		if err := r.db.WithContext(ctx).
			Scopes(tenantScope(ctx)).
			Where("event_type = ? AND session_id IN ?", "tool_call_end", sessionIDs).
			Find(&endEvents).Error; err != nil {
			return nil, 0, fmt.Errorf("query tool call end events: %w", err)
		}
		for _, e := range endEvents {
			callID := extractCallIDFromProto(e.ProtoData)
			if callID != "" {
				endMap[e.SessionID+"::"+callID] = e
			}
		}
	}

	// Step 3: Correlate start/end, apply JSON-level filters, build entries.
	var allEntries []ToolCallEntry
	for _, start := range startEvents {
		entry, ok := r.buildEntry(start, endMap)
		if !ok {
			continue
		}

		if filters.ToolName != "" && entry.ToolName != filters.ToolName {
			continue
		}
		if filters.AgentName != "" && entry.AgentName != filters.AgentName {
			continue
		}
		if filters.Status != "" && entry.Status != filters.Status {
			continue
		}

		allEntries = append(allEntries, entry)
	}

	total := int64(len(allEntries))

	// Step 4: Paginate.
	offset := (page - 1) * perPage
	if offset >= len(allEntries) {
		return []ToolCallEntry{}, total, nil
	}
	end := offset + perPage
	if end > len(allEntries) {
		end = len(allEntries)
	}

	return allEntries[offset:end], total, nil
}

func (r *ToolCallEventRepository) buildEntry(start models.SessionEventLogModel, endMap map[string]models.SessionEventLogModel) (ToolCallEntry, bool) {
	startData := protoToJSON(start.ProtoData)
	if startData == nil {
		return ToolCallEntry{}, false
	}

	callID, _ := startData["call_id"].(string)
	toolName, _ := startData["tool_name"].(string)
	agentName, _ := startData["agent_id"].(string)

	input := formatArguments(startData["arguments"])

	entry := ToolCallEntry{
		ID:        start.ID,
		SessionID: start.SessionID,
		AgentName: agentName,
		ToolName:  toolName,
		Input:     input,
		Status:    "completed",
		CreatedAt: start.CreatedAt,
	}

	endKey := start.SessionID + "::" + callID
	if endEvt, found := endMap[endKey]; found && callID != "" {
		endData := protoToJSON(endEvt.ProtoData)
		if endData != nil {
			summary, _ := endData["result_summary"].(string)
			entry.Output = truncate(summary, maxOutputSize)

			if hasErr, ok := endData["has_error"].(bool); ok && hasErr {
				entry.Status = "failed"
			}
		}
		entry.DurationMs = endEvt.CreatedAt.Sub(start.CreatedAt).Milliseconds()
	}

	return entry, true
}

func uniqueSessionIDs(events []models.SessionEventLogModel) []string {
	seen := make(map[string]struct{}, len(events))
	result := make([]string, 0, len(events))
	for _, e := range events {
		if _, exists := seen[e.SessionID]; exists {
			continue
		}
		seen[e.SessionID] = struct{}{}
		result = append(result, e.SessionID)
	}
	return result
}

// protoToJSON deserializes ProtoData and converts to JSON map via eventformat.SerializeSessionEvent.
func protoToJSON(protoData []byte) map[string]interface{} {
	if len(protoData) == 0 {
		return nil
	}
	pbEvent := &pb.SessionEvent{}
	if err := proto.Unmarshal(protoData, pbEvent); err != nil {
		return nil
	}
	return eventformat.SerializeSessionEvent(pbEvent)
}

// extractCallIDFromProto extracts call_id from ProtoData via JSON conversion.
func extractCallIDFromProto(protoData []byte) string {
	data := protoToJSON(protoData)
	if data == nil {
		return ""
	}
	callID, _ := data["call_id"].(string)
	return callID
}

func formatArguments(v interface{}) string {
	if v == nil {
		return "{}"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...[truncated]"
}
