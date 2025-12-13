// Package planner provides AI-powered scenario generation from lab intents.
package planner

import (
	"context"
	"fmt"
)

// Client wraps the Anthropic API client.
type Client struct {
	apiKey      string
	model       string
	maxTokens   int
	temperature float64
}

// NewClient creates a new API client.
func NewClient(apiKey, model string, maxTokens int, temperature float64) *Client {
	return &Client{
		apiKey:      apiKey,
		model:       model,
		maxTokens:   maxTokens,
		temperature: temperature,
	}
}

// Generate sends a prompt to the AI and returns the response.
// This is a placeholder that will be implemented with the actual Anthropic SDK.
func (c *Client) Generate(ctx context.Context, prompt string) (string, error) {
	// TODO: Implement actual Anthropic API call
	// For now, return an error indicating AI integration is not yet complete
	//
	// The implementation would look like:
	//
	// client := anthropic.NewClient(c.apiKey)
	// resp, err := client.Messages.Create(ctx, &anthropic.MessageCreateParams{
	//     Model:       c.model,
	//     MaxTokens:   c.maxTokens,
	//     Temperature: c.temperature,
	//     System:      SystemPrompt,
	//     Messages: []anthropic.Message{
	//         {Role: "user", Content: prompt},
	//     },
	// })
	// if err != nil {
	//     return "", fmt.Errorf("API call failed: %w", err)
	// }
	// return resp.Content[0].Text, nil

	return "", fmt.Errorf("AI integration not yet implemented - scenarios must be provided directly")
}

// GenerateWithSystemPrompt generates with a custom system prompt.
func (c *Client) GenerateWithSystemPrompt(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	// TODO: Implement with custom system prompt
	return "", fmt.Errorf("AI integration not yet implemented")
}
