package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

func TestNewAzureOpenAIChatModel_Validation(t *testing.T) {
	tests := []struct {
		name       string
		baseURL    string
		apiKey     string
		modelName  string
		apiVersion string
		wantErr    string
	}{
		{
			name:    "missing base_url",
			baseURL: "",
			apiKey:  "key",
			wantErr: "base_url is required",
		},
		{
			name:      "missing api_key",
			baseURL:   "https://myresource.openai.azure.com",
			apiKey:    "",
			modelName: "gpt-4o",
			wantErr:   "api_key is required",
		},
		{
			name:      "missing model_name",
			baseURL:   "https://myresource.openai.azure.com",
			apiKey:    "key",
			modelName: "",
			wantErr:   "model_name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewAzureOpenAIChatModel(tt.baseURL, tt.apiKey, tt.modelName, tt.apiVersion)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestNewAzureOpenAIChatModel_DefaultAPIVersion(t *testing.T) {
	client, err := NewAzureOpenAIChatModel(
		"https://myresource.openai.azure.com",
		"test-key",
		"gpt-4o",
		"",
	)
	require.NoError(t, err)
	assert.Equal(t, defaultAzureAPIVersion, client.APIVersion())
	assert.Equal(t, "https://myresource.openai.azure.com", client.BaseURL())
	assert.Equal(t, "gpt-4o", client.ModelName())
}

func TestNewAzureOpenAIChatModel_CustomAPIVersion(t *testing.T) {
	client, err := NewAzureOpenAIChatModel(
		"https://myresource.openai.azure.com",
		"test-key",
		"gpt-4o",
		"2025-01-01",
	)
	require.NoError(t, err)
	assert.Equal(t, "2025-01-01", client.APIVersion())
}

func TestNewAzureOpenAIChatModel_TrimsTrailingSlash(t *testing.T) {
	client, err := NewAzureOpenAIChatModel(
		"https://myresource.openai.azure.com/",
		"test-key",
		"gpt-4o",
		"",
	)
	require.NoError(t, err)
	// BaseURL accessor retains the original value passed in
	assert.Equal(t, "https://myresource.openai.azure.com/", client.BaseURL())
}

func TestNewAzureOpenAIChatModel_WithTools(t *testing.T) {
	client, err := NewAzureOpenAIChatModel(
		"https://myresource.openai.azure.com",
		"test-key",
		"gpt-4o",
		"2024-10-21",
	)
	require.NoError(t, err)
	require.NotNil(t, client)

	// Verify WithTools returns a new AzureOpenAIChatModel wrapping the inner with tools
	newClient, err := client.WithTools(nil)
	require.NoError(t, err)
	require.NotNil(t, newClient)

	azureClient, ok := newClient.(*AzureOpenAIChatModel)
	require.True(t, ok)
	assert.Equal(t, "gpt-4o", azureClient.ModelName())
	assert.Equal(t, "2024-10-21", azureClient.APIVersion())
}

func TestCreateClientFromDBModel_AzureOpenAI(t *testing.T) {
	tests := []struct {
		name       string
		apiVersion string
	}{
		{
			name:       "with explicit api_version",
			apiVersion: "2025-01-01",
		},
		{
			name:       "defaults api_version when empty",
			apiVersion: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := models.LLMProviderModel{
				Type:            "azure_openai",
				BaseURL:         "https://myresource.openai.azure.com",
				ModelName:       "gpt-4o",
				APIKeyEncrypted: "test-key",
				APIVersion:      tt.apiVersion,
			}
			client, err := CreateClientFromDBModel(m, nil)
			require.NoError(t, err)
			require.NotNil(t, client)
		})
	}
}
