package llm

import "strings"

// IsOpenAIStrictRoute reports whether requests will hit an OpenAI-strict
// endpoint — one that enforces the tool-name regex and rejects bare
// {"type":"object"} schemas. True for providerType "openai", or
// "openai_compatible" with an OpenAI/Azure base URL or OpenAI-family
// model name; false for the rest of the openai_compatible bucket
// (qwen, glm, ollama, vLLM, etc.).
func IsOpenAIStrictRoute(providerType, modelName, baseURL string) bool {
	if providerType == "openai" {
		return true
	}
	if providerType != "openai_compatible" {
		return false
	}
	if isOpenAIBaseURL(baseURL) {
		return true
	}
	return isOpenAIModelSlug(modelName)
}

func isOpenAIBaseURL(baseURL string) bool {
	if baseURL == "" {
		return false
	}
	lower := strings.ToLower(baseURL)
	return strings.Contains(lower, "api.openai.com") ||
		strings.Contains(lower, ".openai.azure.com")
}

// openAIModelPrefixes covers OpenRouter slugs (openai/, azure/) and bare
// OpenAI model names. Generous on purpose — false positive is cheaper than
// a silent upstream 400.
var openAIModelPrefixes = []string{
	"openai/",
	"azure/",
	"gpt-",
	"gpt4",
	"o1",
	"o3",
	"o4",
	"chatgpt-",
	"text-davinci-",
}

func isOpenAIModelSlug(modelName string) bool {
	if modelName == "" {
		return false
	}
	lower := strings.ToLower(modelName)
	for _, p := range openAIModelPrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}
