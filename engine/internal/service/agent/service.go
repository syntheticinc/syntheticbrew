package agent

import (
	"context"
	"log/slog"

	pb "github.com/syntheticinc/syntheticbrew/api/proto/gen"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agents/react"
	"github.com/syntheticinc/syntheticbrew/internal/service/turnexecutor"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
	"github.com/syntheticinc/syntheticbrew/pkg/errors"
	"github.com/cloudwego/eino/components/model"
)

// ChatModel defines interface for LLM chat model
type ChatModel interface {
	model.ToolCallingChatModel
}

// ClientOperationsProxy defines interface for gRPC client operations.
// AskUserQuestionnaire was removed alongside the legacy ask_user tool —
// show_structured_output in form mode is non-blocking and emits an event
// directly via the session event stream rather than via this proxy.
type ClientOperationsProxy interface {
	ReadFile(ctx context.Context, sessionID, filePath string, startLine, endLine int32) (string, error)
	WriteFile(ctx context.Context, sessionID, filePath, content string) (string, error)
	EditFile(ctx context.Context, sessionID, filePath, oldString, newString string, replaceAll bool) (string, error)
	SearchCode(ctx context.Context, sessionID, query, projectKey string, limit int32, minScore float32) ([]byte, error)
	GetProjectTree(ctx context.Context, sessionID, projectKey, path string, maxDepth int) (string, error)
	GrepSearch(ctx context.Context, sessionID, pattern string, limit int32, fileTypes []string, ignoreCase bool) (string, error)
	GlobSearch(ctx context.Context, sessionID, pattern string, limit int32) (string, error)
	SymbolSearch(ctx context.Context, sessionID, symbolName string, limit int32, symbolTypes []string) (string, error)
	ExecuteSubQueries(ctx context.Context, sessionID string, subQueries []*pb.SubQuery) ([]*pb.SubResult, error)
	ExecuteCommand(ctx context.Context, sessionID, command, cwd string, timeout int32) (string, error)
	ExecuteCommandFull(ctx context.Context, sessionID string, arguments map[string]string) (string, error)
	LspRequest(ctx context.Context, sessionID, symbolName, operation string) (string, error)
}

// AgentPoolManager defines interface for Code Agent pool (consumer-side)
type AgentPoolManager interface{}

// Service handles agent orchestration and flow execution
type Service struct {
	chatModel        ChatModel
	agentPool        AgentPoolManager
	contextReminders []turnexecutor.ContextReminderProvider
	toolCallHistory  *ToolCallHistoryReminder
	maxSteps         int
	agentConfig      *config.AgentConfig
	modelName        string
	streaming        bool // Enable streaming mode
	supervisorMode   bool // Supervisor mode with Code Agents
}

// Config holds configuration for Agent Service
type Config struct {
	ChatModel        ChatModel
	AgentPool        AgentPoolManager
	ContextReminders []turnexecutor.ContextReminderProvider
	MaxSteps         int
	AgentConfig      *config.AgentConfig
	ModelName        string // Model name for reasoning extraction
	Streaming        bool   // Enable streaming mode
}

// New creates a new Agent Service
func New(cfg Config) (*Service, error) {
	if cfg.ChatModel == nil {
		return nil, errors.New(errors.CodeInvalidInput, "chat model is required")
	}

	// MaxSteps = 0 means no limit, use value from config as-is
	maxSteps := cfg.MaxSteps

	// Use provided AgentConfig or default
	agentConfig := cfg.AgentConfig
	if agentConfig == nil {
		agentConfig = config.DefaultAgentConfig()
	}

	// Create tool call history reminder
	toolCallHistory := NewToolCallHistoryReminder()

	// Add it to context reminders
	contextReminders := cfg.ContextReminders
	contextReminders = append(contextReminders, toolCallHistory)

	return &Service{
		chatModel:        cfg.ChatModel,
		agentPool:        cfg.AgentPool,
		contextReminders: contextReminders,
		toolCallHistory:  toolCallHistory,
		maxSteps:         maxSteps,
		agentConfig:      agentConfig,
		modelName:        cfg.ModelName,
		streaming:        cfg.Streaming,
		supervisorMode:   cfg.AgentPool != nil,
	}, nil
}

// SetEnvironmentContext sets environment metadata (project root, platform)
// that will be injected into the LLM context as a reminder.
// Replaces any existing EnvironmentContextReminder.
func (s *Service) SetEnvironmentContext(projectRoot, platform string) {
	if projectRoot == "" && platform == "" {
		return
	}

	reminder := NewEnvironmentContextReminder(projectRoot, platform)

	// Replace existing EnvironmentContextReminder if any
	var newReminders []turnexecutor.ContextReminderProvider
	for _, r := range s.contextReminders {
		if _, ok := r.(*EnvironmentContextReminder); !ok {
			newReminders = append(newReminders, r)
		}
	}
	newReminders = append(newReminders, reminder)
	s.contextReminders = newReminders

	// Propagate to AgentPool so Code Agents inherit environment context
	s.propagateContextRemindersToPool()
}

// SetTestingStrategy sets project-level testing strategy
// that will be injected into the LLM context as a reminder.
// Replaces any existing TestingStrategyReminder.
func (s *Service) SetTestingStrategy(yamlContent string) {
	if yamlContent == "" {
		return
	}

	strategy, err := ParseTestingStrategy(yamlContent)
	if err != nil {
		slog.WarnContext(context.Background(), "failed to parse testing strategy", "error", err)
		return
	}

	reminder := NewTestingStrategyReminder(strategy)

	// Replace existing TestingStrategyReminder if any
	var newReminders []turnexecutor.ContextReminderProvider
	for _, r := range s.contextReminders {
		if _, ok := r.(*TestingStrategyReminder); !ok {
			newReminders = append(newReminders, r)
		}
	}
	newReminders = append(newReminders, reminder)
	s.contextReminders = newReminders

	s.propagateContextRemindersToPool()
}

// propagateContextRemindersToPool sends current context reminders to AgentPool
// so Code Agents inherit environment context (project root, platform).
func (s *Service) propagateContextRemindersToPool() {
	if s.agentPool == nil {
		return
	}
	pool, ok := s.agentPool.(*AgentPool)
	if !ok {
		return
	}

	var reactReminders []react.ContextReminderProvider
	for _, r := range s.contextReminders {
		reactReminders = append(reactReminders, r)
	}
	pool.SetContextReminders(reactReminders)
}

// GetToolCallRecorder returns the tool call recorder for callback integration
func (s *Service) GetToolCallRecorder() ToolCallRecorder {
	return s.toolCallHistory
}

// GetToolCallHistoryReminder returns the tool call history reminder for session cleanup
func (s *Service) GetToolCallHistoryReminder() *ToolCallHistoryReminder {
	return s.toolCallHistory
}

// GetContextReminders returns the context reminders for Engine integration
func (s *Service) GetContextReminders() []turnexecutor.ContextReminderProvider {
	return s.contextReminders
}

