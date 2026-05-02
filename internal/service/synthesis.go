// Package service synthesis.go — deterministic community summary generator
// and lifecycle orchestrator (SPEC-021).
//
// GenerateSynthesisContent is a pure function that builds reproducible Markdown
// from a sorted member slice. GenerateCommunitySyntheses is the service method
// that drives create/update/skip/delete for every community in a project.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/store"
)

// SynthesisResult summarises the outcome of a single synthesis generation cycle.
// All counters are non-negative.
type SynthesisResult struct {
	// Created is the number of new synthesis memories written.
	Created int `json:"synthesis_created"`

	// Updated is the number of existing synthesis memories whose content changed.
	Updated int `json:"synthesis_updated"`

	// Deleted is the number of synthesis memories soft-deleted because their
	// community was removed.
	Deleted int `json:"synthesis_deleted"`

	// Skipped is the number of communities whose synthesis content was identical
	// to the existing record (no-op).
	Skipped int `json:"synthesis_skipped"`

	// Duration is the wall-clock time taken by the full cycle.
	Duration time.Duration `json:"synthesis_duration"`
}

// GenerateSynthesisContent builds a deterministic Markdown summary for a
// community from its member memories. The output is reproducible: given the
// same members in the same order, it produces byte-identical content.
//
// The members slice must be pre-sorted by importance DESC, then created_at DESC
// (stable sort). This function does not sort internally — determinism is the
// caller's responsibility.
//
// topN controls how many members appear in the title and the "Top Members"
// section. maxMembers caps the "All Members" table; communities with more
// members receive a truncation note. Returns ("", "") when len(members) == 0.
func GenerateSynthesisContent(communityID string, members []*model.Memory, topN, maxMembers int) (title, content string) {
	if len(members) == 0 {
		return "", ""
	}

	// Clamp topN to the available member count.
	n := topN
	if n > len(members) {
		n = len(members)
	}

	// ── Title ──────────────────────────────────────────────────────────────────
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		parts[i] = members[i].Title
	}
	rawTitle := "Cluster: " + strings.Join(parts, " + ")
	if len(rawTitle) > 80 {
		rawTitle = rawTitle[:77] + "..."
	}
	title = rawTitle

	// ── Content ────────────────────────────────────────────────────────────────
	var buf strings.Builder

	// Section 1: cluster overview.
	shortID := communityID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	buf.WriteString("## Cluster Overview\n\n")
	fmt.Fprintf(&buf,
		"Community `%s` contains **%d memories** spanning the following knowledge areas.\n\n",
		shortID, len(members),
	)

	// Section 2: top members with detail.
	buf.WriteString("## Top Members\n\n")
	for i := 0; i < n; i++ {
		m := members[i]
		fmt.Fprintf(&buf, "### %d. %s\n\n", i+1, m.Title)
		if m.TopicKey != "" {
			fmt.Fprintf(&buf, "- **Topic:** [[%s]]\n", m.TopicKey)
		}
		fmt.Fprintf(&buf, "- **Type:** %s\n", m.Type)
		fmt.Fprintf(&buf, "- **Importance:** %.2f\n", m.Importance)
		excerpt := m.Content
		if len(excerpt) > 200 {
			excerpt = excerpt[:197] + "..."
		}
		fmt.Fprintf(&buf, "- **Excerpt:** %s\n\n", excerpt)
	}

	// Section 3: all members table (truncated to maxMembers).
	buf.WriteString("## All Members\n\n")
	buf.WriteString("| # | Title | Type | Importance | Topic Key |\n")
	buf.WriteString("|---|-------|------|------------|----------|\n")
	tableCount := len(members)
	if tableCount > maxMembers {
		tableCount = maxMembers
	}
	for i := 0; i < tableCount; i++ {
		m := members[i]
		tk := m.TopicKey
		if tk != "" {
			tk = fmt.Sprintf("[[%s]]", tk)
		}
		fmt.Fprintf(&buf, "| %d | %s | %s | %.2f | %s |\n",
			i+1, m.Title, m.Type, m.Importance, tk,
		)
	}
	if len(members) > maxMembers {
		fmt.Fprintf(&buf, "\n> Truncated: showing top %d of %d members.\n", maxMembers, len(members))
	}

	// Section 4: aggregate metadata.
	buf.WriteString("\n## Aggregate Metadata\n\n")
	typeCount := make(map[model.MemoryType]int)
	var sumImportance float64
	var oldest, newest time.Time
	for i, m := range members {
		typeCount[m.Type]++
		sumImportance += m.Importance
		if i == 0 {
			oldest = m.CreatedAt
			newest = m.CreatedAt
			continue
		}
		if m.CreatedAt.Before(oldest) {
			oldest = m.CreatedAt
		}
		if m.CreatedAt.After(newest) {
			newest = m.CreatedAt
		}
	}
	fmt.Fprintf(&buf, "- **Total memories:** %d\n", len(members))
	fmt.Fprintf(&buf, "- **Types:** %s\n", formatTypeCounts(typeCount))
	fmt.Fprintf(&buf, "- **Average importance:** %.2f\n", sumImportance/float64(len(members)))
	fmt.Fprintf(&buf, "- **Date range:** %s to %s\n",
		oldest.Format("2006-01-02"), newest.Format("2006-01-02"),
	)

	content = buf.String()
	return title, content
}

// formatTypeCounts formats a type-count map as an alphabetically sorted string
// like "architecture: 2, decision: 3, discovery: 5". The sort guarantees
// that two identical input maps always produce identical output.
func formatTypeCounts(counts map[model.MemoryType]int) string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)

	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s: %d", k, counts[model.MemoryType(k)])
	}
	return strings.Join(parts, ", ")
}

// GenerateCommunitySyntheses creates, updates, or soft-deletes synthesis
// memories for every community in the given scope/project.
//
// Lifecycle rules (SPEC-021 D4):
//   - New community     → create synthesis via Save (upsert by topic_key).
//   - Stable community  → regenerate content; upsert if changed, skip if identical.
//   - Deleted community → soft-delete the synthesis via Forget.
//
// The method returns immediately with a zero SynthesisResult when
// config.Graph.SynthesisEnabled is false.
//
// detectionResult is currently unused beyond future-proofing; the method
// inspects the live community table directly to handle all three cases
// uniformly regardless of whether detection ran in this cycle.
func (svc *MemoryService) GenerateCommunitySyntheses(
	ctx context.Context,
	scope model.Scope,
	project string,
	_ *model.DetectionResult,
) (*SynthesisResult, error) {
	start := time.Now()
	result := &SynthesisResult{}

	if !svc.config.Graph.SynthesisEnabled {
		return result, nil
	}

	st := svc.storeFor(scope)
	topN := svc.config.Graph.SynthesisTopN
	maxMembers := svc.config.Graph.SynthesisMaxMembers

	// Load all communities for this project.
	communities, err := st.ListCommunities(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("service: generate syntheses: list communities: %w", err)
	}

	// Process each community: create or update its synthesis.
	for _, comm := range communities {
		entityIDs, err := st.GetCommunityEntityIDs(ctx, comm.ID)
		if err != nil {
			slog.ErrorContext(ctx, "synthesis_get_members_error",
				"community_id", comm.ID, "error", err)
			continue
		}

		members, err := st.GetMemoriesByEntityIDs(ctx, entityIDs, project)
		if err != nil {
			slog.ErrorContext(ctx, "synthesis_resolve_members_error",
				"community_id", comm.ID, "error", err)
			continue
		}
		if len(members) == 0 {
			continue
		}

		// Sort: importance DESC, created_at DESC (stable for determinism).
		sort.SliceStable(members, func(i, j int) bool {
			if members[i].Importance != members[j].Importance {
				return members[i].Importance > members[j].Importance
			}
			return members[i].CreatedAt.After(members[j].CreatedAt)
		})

		title, content := GenerateSynthesisContent(comm.ID, members, topN, maxMembers)

		topicKey := fmt.Sprintf("synthesis/community-%s", comm.ID)

		// Check whether a synthesis already exists for this community.
		existing, err := st.GetByTopicKey(ctx, topicKey, project)
		if err != nil {
			slog.ErrorContext(ctx, "synthesis_lookup_error",
				"topic_key", topicKey, "error", err)
			continue
		}

		if existing != nil && existing.Content == content {
			// Content unchanged — no-op.
			result.Skipped++
			continue
		}

		// Create or update via topic_key upsert.
		_, saveErr := svc.Save(ctx, model.SaveRequest{
			Title:    title,
			Content:  content,
			Type:     model.TypeSynthesis,
			Scope:    scope,
			TopicKey: topicKey,
			Project:  project,
		})
		if saveErr != nil {
			slog.ErrorContext(ctx, "synthesis_save_error",
				"community_id", comm.ID, "error", saveErr)
			continue
		}

		if existing != nil {
			result.Updated++
		} else {
			result.Created++
		}

		// D12: populate the community's human-readable label with the generated title.
		if labelErr := st.UpdateCommunityLabel(ctx, comm.ID, title); labelErr != nil {
			slog.ErrorContext(ctx, "synthesis_update_label_error",
				"community_id", comm.ID, "error", labelErr)
		}
	}

	// Soft-delete syntheses whose community no longer exists (deleted communities).
	syntheses, err := st.List(ctx, store.ListOptions{
		Project: project,
		Type:    model.TypeSynthesis,
		Limit:   1000,
	})
	if err != nil {
		slog.ErrorContext(ctx, "synthesis_list_error", "error", err)
	} else {
		communityIDs := make(map[string]bool, len(communities))
		for _, c := range communities {
			communityIDs[c.ID] = true
		}

		for _, syn := range syntheses {
			// Extract community ID from topic_key "synthesis/community-{uuid}".
			commID := strings.TrimPrefix(syn.TopicKey, "synthesis/community-")
			if commID == syn.TopicKey {
				continue // topic_key does not follow the synthesis pattern
			}
			if !communityIDs[commID] {
				// Community no longer exists — soft-delete the synthesis.
				if forgetErr := svc.Forget(ctx, syn.ID, "community deleted"); forgetErr != nil {
					slog.ErrorContext(ctx, "synthesis_forget_error",
						"synthesis_id", syn.ID, "error", forgetErr)
					continue
				}
				result.Deleted++
			}
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}
