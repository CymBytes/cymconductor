// Package actions provides predefined action implementations for the agent.
package actions

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/rs/zerolog"
)

// BrowsingHandler handles simulate_browsing actions.
type BrowsingHandler struct {
	config BrowsingConfig
	logger zerolog.Logger
}

// NewBrowsingHandler creates a new browsing handler.
func NewBrowsingHandler(cfg BrowsingConfig, logger zerolog.Logger) *BrowsingHandler {
	return &BrowsingHandler{
		config: cfg,
		logger: logger.With().Str("action", "browsing").Logger(),
	}
}

// Execute simulates web browsing activity.
func (h *BrowsingHandler) Execute(ctx context.Context, params map[string]interface{}) (*Result, error) {
	startTime := time.Now()
	h.logger.Info().Interface("params", params).Msg("Starting browsing simulation")

	// Parse parameters
	urls, _ := getStringSlice(params, "urls")
	if len(urls) == 0 {
		return nil, fmt.Errorf("urls parameter is required")
	}

	durationSec, _ := getInt(params, "duration_seconds")
	if durationSec == 0 {
		durationSec = 60
	}

	clickLinks, _ := getBool(params, "click_links")
	scrollBehavior, _ := getString(params, "scroll_behavior")
	if scrollBehavior == "" {
		scrollBehavior = "natural"
	}

	// Simulate browsing activity
	// In a real implementation, this would use Chrome DevTools Protocol or similar
	// For now, we simulate the activity timing

	urlsVisited := 0
	pagesLoaded := 0
	linksClicked := 0

	deadline := time.Now().Add(time.Duration(durationSec) * time.Second)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			h.logger.Info().Msg("Browsing cancelled")
			break
		default:
			// Simulate visiting a URL
			url := urls[rand.Intn(len(urls))]
			h.logger.Debug().Str("url", url).Msg("Simulating visit")

			// Simulate page load time
			loadTime := time.Duration(500+rand.Intn(2000)) * time.Millisecond
			time.Sleep(loadTime)
			pagesLoaded++
			urlsVisited++

			// Simulate scroll behavior
			if scrollBehavior == "natural" || scrollBehavior == "aggressive" {
				scrollTime := time.Duration(1000+rand.Intn(3000)) * time.Millisecond
				time.Sleep(scrollTime)
			}

			// Simulate clicking links
			if clickLinks && rand.Float32() < 0.5 {
				clickTime := time.Duration(500+rand.Intn(1500)) * time.Millisecond
				time.Sleep(clickTime)
				linksClicked++
				pagesLoaded++
			}

			// Random pause between activities
			pauseTime := time.Duration(2000+rand.Intn(5000)) * time.Millisecond
			time.Sleep(pauseTime)
		}
	}

	duration := time.Since(startTime)
	h.logger.Info().
		Int("urls_visited", urlsVisited).
		Int("pages_loaded", pagesLoaded).
		Int("links_clicked", linksClicked).
		Dur("duration", duration).
		Msg("Browsing simulation complete")

	return &Result{
		Data: map[string]interface{}{
			"urls_visited":  urlsVisited,
			"pages_loaded":  pagesLoaded,
			"links_clicked": linksClicked,
		},
		Summary:    fmt.Sprintf("Visited %d URLs, loaded %d pages, clicked %d links", urlsVisited, pagesLoaded, linksClicked),
		DurationMs: duration.Milliseconds(),
	}, nil
}
