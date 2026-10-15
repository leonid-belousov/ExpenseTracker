package agent

import (
	"expensetracker/internal/config"
	"expensetracker/internal/llm"
)

// NewModel builds the language model selected by the configuration. The
// "mock" provider is the offline RuleModel.
func NewModel(c config.LLM) llm.Model {
	switch c.Provider {
	case "anthropic":
		return llm.NewAnthropic(c.APIKey, c.Model, c.BaseURL, c.Timeout)
	case "openai":
		return llm.NewOpenAICompatible(c.BaseURL, c.APIKey, c.Model, c.Timeout)
	}
	return RuleModel{}
}
