package domain

// HITLToolNames lists tools whose tool_call halts the react loop and waits
// for the user's next message before resuming.
func HITLToolNames() []string {
	return []string{"show_structured_output"}
}

func IsHITLTool(name string) bool {
	for _, n := range HITLToolNames() {
		if n == name {
			return true
		}
	}
	return false
}

func HasAnyHITLTool(toolNames []string) bool {
	for _, n := range toolNames {
		if IsHITLTool(n) {
			return true
		}
	}
	return false
}

