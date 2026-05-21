package eventstore

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	pb "github.com/syntheticinc/syntheticbrew/api/proto/gen"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"github.com/syntheticinc/syntheticbrew/internal/service/eventformat"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

// StoredEvent represents a persisted session event with both proto and JSON representations.
type StoredEvent struct {
	ID        string
	SessionID string
	EventType string
	Proto     *pb.SessionEvent
	JSON      map[string]interface{}
	CreatedAt time.Time
}

// Store persists session events in PostgreSQL (GORM) for reliable replay on reconnect.
type Store struct {
	db *gorm.DB
}

// New creates a new event store.
func New(db *gorm.DB) (*Store, error) {
	return &Store{db: db}, nil
}

// Append persists a session event and returns the UUID.
// The proto is marshaled WITHOUT EventId (unknown pre-insert). Callers should
// set event.EventId = id after Append returns.
// jsonData is accepted for API compatibility but no longer persisted (json_data column dropped in migration 029).
func (s *Store) Append(sessionID, eventType string, event *pb.SessionEvent, jsonData map[string]interface{}) (string, error) {
	protoBytes, err := proto.Marshal(event)
	if err != nil {
		return "", fmt.Errorf("marshal proto: %w", err)
	}

	m := models.SessionEventLogModel{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		EventType: eventType,
		ProtoData: protoBytes,
	}

	if err := s.db.Create(&m).Error; err != nil {
		return "", fmt.Errorf("insert event: %w", err)
	}

	return m.ID, nil
}

// GetAfter returns all events for a session created after the given timestamp.
// If afterCreatedAt is zero, all events for the session are returned.
func (s *Store) GetAfter(sessionID string, afterCreatedAt time.Time) ([]StoredEvent, error) {
	if afterCreatedAt.IsZero() {
		return s.GetAll(sessionID)
	}

	var ms []models.SessionEventLogModel
	if err := s.db.
		Where("session_id = ? AND created_at > ?", sessionID, afterCreatedAt).
		Order("created_at ASC").
		Find(&ms).Error; err != nil {
		return nil, fmt.Errorf("query events after %v: %w", afterCreatedAt, err)
	}

	return scanEventModels(ms)
}

// GetAll returns all events for a session ordered by creation time.
func (s *Store) GetAll(sessionID string) ([]StoredEvent, error) {
	var ms []models.SessionEventLogModel
	if err := s.db.
		Where("session_id = ?", sessionID).
		Order("created_at ASC").
		Find(&ms).Error; err != nil {
		return nil, fmt.Errorf("query all events: %w", err)
	}

	return scanEventModels(ms)
}

// CleanupSession deletes all events for a session.
func (s *Store) CleanupSession(sessionID string) error {
	if err := s.db.Where("session_id = ?", sessionID).
		Delete(&models.SessionEventLogModel{}).Error; err != nil {
		return fmt.Errorf("cleanup session events: %w", err)
	}
	return nil
}

func scanEventModels(ms []models.SessionEventLogModel) ([]StoredEvent, error) {
	events := make([]StoredEvent, 0, len(ms))

	for _, m := range ms {
		pbEvent := &pb.SessionEvent{}
		if err := proto.Unmarshal(m.ProtoData, pbEvent); err != nil {
			return nil, fmt.Errorf("unmarshal proto for event %s: %w", m.ID, err)
		}
		pbEvent.EventId = m.ID

		// Generate JSON on-the-fly from proto (json_data column dropped in migration 029).
		jsonData := eventformat.SerializeSessionEvent(pbEvent)

		events = append(events, StoredEvent{
			ID:        m.ID,
			SessionID: m.SessionID,
			EventType: m.EventType,
			Proto:     pbEvent,
			JSON:      jsonData,
			CreatedAt: m.CreatedAt,
		})
	}

	return events, nil
}
