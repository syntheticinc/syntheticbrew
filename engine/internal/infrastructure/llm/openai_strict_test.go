package llm

import "testing"

func TestIsOpenAIStrictRoute(t *testing.T) {
	cases := []struct {
		name         string
		providerType string
		modelName    string
		baseURL      string
		want         bool
	}{
		// providerType "openai" — always strict.
		{"openai direct any model", "openai", "gpt-4o-mini", "https://api.openai.com/v1", true},
		{"openai direct empty model", "openai", "", "", true},

		// providerType "openai_compatible" + OpenAI base_url.
		{"openai_compatible at api.openai.com", "openai_compatible", "gpt-4o-mini", "https://api.openai.com/v1", true},
		{"openai_compatible at azure", "openai_compatible", "anything", "https://my-resource.openai.azure.com/openai/deployments/d/", true},
		{"openai_compatible at upper-case openai", "openai_compatible", "qwen/qwen3-coder", "https://API.OPENAI.COM/v1", true},

		// providerType "openai_compatible" + OpenRouter slug routing to OpenAI.
		{"openrouter openai/ slug", "openai_compatible", "openai/gpt-4o-mini", "https://openrouter.ai/api/v1", true},
		{"openrouter azure/ slug", "openai_compatible", "azure/gpt-4o", "https://openrouter.ai/api/v1", true},

		// providerType "openai_compatible" + bare OpenAI model names.
		{"bare gpt-4o-mini", "openai_compatible", "gpt-4o-mini", "https://openrouter.ai/api/v1", true},
		{"bare gpt-4", "openai_compatible", "gpt-4", "https://openrouter.ai/api/v1", true},
		{"bare o1", "openai_compatible", "o1-preview", "https://openrouter.ai/api/v1", true},
		{"bare o3-mini", "openai_compatible", "o3-mini", "https://openrouter.ai/api/v1", true},
		{"bare chatgpt-", "openai_compatible", "chatgpt-4o-latest", "https://openrouter.ai/api/v1", true},

		// providerType "openai_compatible" + non-OpenAI — must be FALSE.
		{"qwen via OpenRouter", "openai_compatible", "qwen/qwen3-coder", "https://openrouter.ai/api/v1", false},
		{"glm via OpenRouter", "openai_compatible", "z-ai/glm-4.7", "https://openrouter.ai/api/v1", false},
		{"anthropic via OpenRouter", "openai_compatible", "anthropic/claude-haiku-4.5", "https://openrouter.ai/api/v1", false},
		{"local vllm", "openai_compatible", "mistralai/Mixtral-8x7B", "http://vllm.svc.cluster.local:8000/v1", false},
		{"deepseek-coder bare", "openai_compatible", "deepseek-coder", "https://openrouter.ai/api/v1", false},

		// Other provider types — always FALSE.
		{"anthropic native", "anthropic", "claude-haiku-4.5", "https://api.anthropic.com/v1", false},
		{"ollama", "ollama", "llama3", "http://ollama:11434/v1", false},
		{"google", "google", "gemini-2.0", "", false},
		{"azure_openai native", "azure_openai", "gpt-4o", "https://x.openai.azure.com", false},

		// Empty inputs — FALSE.
		{"all empty", "", "", "", false},
		{"empty providerType with openai model", "", "gpt-4o-mini", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsOpenAIStrictRoute(tc.providerType, tc.modelName, tc.baseURL)
			if got != tc.want {
				t.Errorf("IsOpenAIStrictRoute(%q, %q, %q) = %v, want %v",
					tc.providerType, tc.modelName, tc.baseURL, got, tc.want)
			}
		})
	}
}
