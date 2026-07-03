package llm

import (
	"testing"

	"github.com/cloudwego/eino/components/model"
)

type nilModel struct{ model.ToolCallingChatModel }

func TestSetDefault_ReplacesFallback(t *testing.T) {
	orig := &nilModel{}
	repl := &nilModel{}
	s := NewModelSelector(orig, "orig")
	if s.Select("agent-x") != orig || s.ModelName("agent-x") != "orig" {
		t.Fatal("initial default not returned")
	}
	s.SetDefault(repl, "platform-free")
	if s.Select("agent-x") != repl {
		t.Fatal("SetDefault did not replace fallback model")
	}
	if s.ModelName("agent-x") != "platform-free" {
		t.Fatal("SetDefault did not replace fallback name")
	}
	// A per-agent model still wins over the default.
	specific := &nilModel{}
	s.SetModel("agent-x", specific, "specific")
	if s.Select("agent-x") != specific {
		t.Fatal("per-agent model must win over default")
	}
}
