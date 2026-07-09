package service

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/scoring"
	"github.com/wirvii/mneme/internal/store"
)

// nonAlphanumRe matches any character that is not a lowercase letter, digit,
// or hyphen. Used to sanitise title tokens when building a topic key suggestion.
var nonAlphanumRe = regexp.MustCompile(`[^a-z0-9-]+`)

// SuggestTopicKey generates a stable topic key suggestion based on the memory
// title and existing keys in the database. It also searches unresolved knowledge
// gaps so that agents are guided toward filling existing gaps rather than
// creating new, divergent keys.
//
// The response includes:
//   - ExistingMatches: memories with similar topic keys (scored by Jaccard).
//   - GapMatches: unresolved gaps whose topic_key is similar (scored by Jaccard
//     plus a configurable boost and log-scaled pending-count adjustment).
//   - Suggestion: the highest-scoring gap key when it beats all existing matches;
//     otherwise the buildTopicKey() slug derived from the title.
//
// When the title tokenises to nothing (all stopwords or too short), the function
// falls back to the pre-SPEC-014 behaviour: FTS5 search only, no gap matching.
func (svc *MemoryService) SuggestTopicKey(ctx context.Context, req model.SuggestTopicKeyRequest) (*model.TopicKeySuggestion, error) {
	project := req.Project
	if project == "" {
		project = svc.project
	}

	cfg := svc.config.Suggestions
	queryTokens := scoring.Tokenize(req.Title)

	// --- Existing matches (FTS5 search, always executed) ---
	var existingMatches []model.TopicKeyMatch
	if req.Title != "" {
		searchOpts := store.SearchOptions{
			Project: project,
			Limit:   cfg.MaxResults,
		}
		projectResults, err := svc.projectStore.FTS5Search(ctx, req.Title, searchOpts)
		if err != nil {
			return nil, fmt.Errorf("service: suggest topic key: search project store: %w", err)
		}
		globalOpts := searchOpts
		globalOpts.Project = ""
		globalResults, err := svc.globalStore.FTS5Search(ctx, req.Title, globalOpts)
		if err != nil {
			return nil, fmt.Errorf("service: suggest topic key: search global store: %w", err)
		}
		results := append(projectResults, globalResults...)

		seen := make(map[string]bool)
		for _, r := range results {
			if r.TopicKey == "" || seen[r.TopicKey] {
				continue
			}
			seen[r.TopicKey] = true

			match := model.TopicKeyMatch{
				TopicKey: r.TopicKey,
				Title:    r.Title,
				ID:       r.ID,
			}

			if len(queryTokens) > 0 {
				tkTokens := scoring.Tokenize(r.TopicKey)
				j := scoring.JaccardSimilarity(queryTokens, tkTokens)
				if j >= cfg.GapJaccardThreshold {
					match.Score = j
					match.Reason = "similar existing topic key"
				} else {
					// FTS5 found it via text — keep with a floor score.
					match.Score = 0.1
					match.Reason = "FTS5 text match"
				}
			}

			existingMatches = append(existingMatches, match)
		}

		// Sort existing matches by Score descending, then topic_key ascending.
		sort.Slice(existingMatches, func(i, j int) bool {
			if existingMatches[i].Score != existingMatches[j].Score {
				return existingMatches[i].Score > existingMatches[j].Score
			}
			return existingMatches[i].TopicKey < existingMatches[j].TopicKey
		})
		if len(existingMatches) > cfg.MaxResults {
			existingMatches = existingMatches[:cfg.MaxResults]
		}
	}

	// --- Gap matches (new in SPEC-014) ---
	var gapMatches []model.TopicKeyMatch
	if len(queryTokens) > 0 && cfg.MaxGapsToConsider > 0 {
		gaps, _, err := svc.projectStore.ListGaps(ctx, project, cfg.MaxGapsToConsider, 1)
		if err != nil {
			return nil, fmt.Errorf("service: suggest topic key: list gaps: %w", err)
		}

		for _, gap := range gaps {
			gapTokens := scoring.Tokenize(gap.TargetTopicKey)
			j := scoring.JaccardSimilarity(queryTokens, gapTokens)
			if j < cfg.GapJaccardThreshold {
				continue
			}
			// score = jaccard + boost + log2(pending_count+1) * weight
			gapScore := j + cfg.GapScoreBoost +
				math.Log2(float64(gap.TotalMentions+1))*cfg.GapPendingWeight

			gapMatches = append(gapMatches, model.TopicKeyMatch{
				TopicKey:     gap.TargetTopicKey,
				Title:        gap.TargetTopicKey,
				Score:        gapScore,
				FromGap:      true,
				PendingCount: gap.TotalMentions,
				Reason:       fmt.Sprintf("unresolved gap, %d pending mentions", gap.TotalMentions),
			})
		}

		// Sort gap matches by Score descending, then topic_key ascending for stability.
		sort.Slice(gapMatches, func(i, j int) bool {
			if gapMatches[i].Score != gapMatches[j].Score {
				return gapMatches[i].Score > gapMatches[j].Score
			}
			return gapMatches[i].TopicKey < gapMatches[j].TopicKey
		})
		if len(gapMatches) > cfg.MaxResults {
			gapMatches = gapMatches[:cfg.MaxResults]
		}
	}

	// --- Primary suggestion ---
	// When the top gap scores higher than all existing matches, suggest the gap's
	// topic_key so the agent is nudged to fill it. Otherwise fall back to the
	// slug derived from the title (unchanged pre-SPEC-014 behaviour).
	suggestion := buildTopicKey(req.Title)
	if len(gapMatches) > 0 {
		topGapScore := gapMatches[0].Score
		topExistingScore := 0.0
		if len(existingMatches) > 0 {
			topExistingScore = existingMatches[0].Score
		}
		if topGapScore > topExistingScore {
			suggestion = gapMatches[0].TopicKey
		}
	}

	isNew := len(existingMatches) == 0 && len(gapMatches) == 0

	return &model.TopicKeySuggestion{
		Suggestion:      suggestion,
		ExistingMatches: existingMatches,
		GapMatches:      gapMatches,
		IsNewTopic:      isNew,
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
