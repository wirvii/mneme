package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/store"
)

// nonAlphanumRe matches any character that is not a lowercase letter, digit,
// or hyphen. Used to sanitise title tokens when building a topic key suggestion.
var nonAlphanumRe = regexp.MustCompile(`[^a-z0-9-]+`)

// SuggestTopicKey generates a stable topic key suggestion based on the memory
// title and existing keys in the database. It returns any existing memories
// with similar topic keys so the caller can decide whether to update or create.
//
// Project defaults to the service's project when omitted.
func (svc *MemoryService) SuggestTopicKey(ctx context.Context, title, project string) (*model.TopicKeySuggestion, error) {
	if project == "" {
		project = svc.project
	}

	// Search existing memories with the title as the query to find similar records.
	// Search both project store and global store to surface all relevant keys.
	var existingMatches []model.TopicKeyMatch
	if title != "" {
		searchOpts := store.SearchOptions{
			Project: project,
			Limit:   10,
		}
		projectResults, err := svc.projectStore.FTS5Search(ctx, title, searchOpts)
		if err != nil {
			return nil, fmt.Errorf("service: suggest topic key: search project store: %w", err)
		}
		globalOpts := searchOpts
		globalOpts.Project = ""
		globalResults, err := svc.globalStore.FTS5Search(ctx, title, globalOpts)
		if err != nil {
			return nil, fmt.Errorf("service: suggest topic key: search global store: %w", err)
		}
		results := append(projectResults, globalResults...)

		// Collect unique topic keys from search results.
		seen := make(map[string]bool)
		for _, r := range results {
			if r.TopicKey == "" {
				continue
			}
			if seen[r.TopicKey] {
				continue
			}
			seen[r.TopicKey] = true
			existingMatches = append(existingMatches, model.TopicKeyMatch{
				TopicKey: r.TopicKey,
				Title:    r.Title,
				ID:       r.ID,
			})
		}
	}

	suggestion := buildTopicKey(title)

	return &model.TopicKeySuggestion{
		Suggestion:      suggestion,
		ExistingMatches: existingMatches,
		IsNewTopic:      len(existingMatches) == 0,
	}, nil
}

// buildTopicKey derives a canonical topic key from a human-readable title.
// It lowercases the title, replaces whitespace with hyphens, strips non-alphanumeric
// characters (except hyphens), and prepends a category prefix inferred from
// keywords in the title:
//   - contains "fix" or "bug"    → "bugfix/"
//   - contains "decide" or "decision" → "decision/"
//   - contains "architecture" or "arch" → "architecture/"
//   - contains "pattern"        → "pattern/"
//   - default                   → "discovery/"
func buildTopicKey(title string) string {
	lower := strings.ToLower(strings.TrimSpace(title))

	prefix := inferPrefix(lower)

	// Replace whitespace sequences with a single hyphen.
	slug := strings.Join(strings.Fields(lower), "-")

	// Remove any characters that are not lowercase letters, digits, or hyphens.
	slug = nonAlphanumRe.ReplaceAllString(slug, "")

	// Collapse consecutive hyphens that may result from stripping.
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	slug = strings.Trim(slug, "-")

	if slug == "" {
		slug = "untitled"
	}

	return prefix + slug
}

// inferPrefix selects the category prefix for a topic key based on keywords
// found in the lowercased title.
func inferPrefix(lower string) string {
	switch {
	case strings.Contains(lower, "fix") || strings.Contains(lower, "bug"):
		return "bugfix/"
	case strings.Contains(lower, "decide") || strings.Contains(lower, "decision"):
		return "decision/"
	case strings.Contains(lower, "architecture") || strings.Contains(lower, "arch"):
		return "architecture/"
	case strings.Contains(lower, "pattern"):
		return "pattern/"
	default:
		return "discovery/"
	}
}
