package repository

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// usageFilterOptionLimit bounds how many values one dimension returns. The
// usage filters are dropdowns, not exports: a tenant with thousands of chats
// should still open the page quickly, and the combobox narrows the list with
// ?search rather than shipping everything.
const usageFilterOptionLimit = 500

// usageFilterOptionSearchLimit is the candidate window when the caller is
// searching. Labels (usernames, chat names, release versions) are resolved
// after the distinct query, so filtering by them happens in Go: widen the
// window enough that a name match is unlikely to sit outside it, then return
// the bounded matches.
const (
	usageFilterOptionSearchLimit  = 2000
	usageFilterOptionSearchResult = 50
)

// UsageFilterOption is one selectable filter value plus the human label to show
// for it. IDs stay in Value so the report endpoints keep receiving exactly the
// identifier they already filter on.
type UsageFilterOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// UsageFilterOptions lists the values that actually occur in usage data for the
// caller's scope. The report filters were free-text boxes, so operators had to
// remember a model alias, a chat ID or a 32-char user ID; a dropdown filled from
// the data itself removes that guessing without inventing a second vocabulary.
type UsageFilterOptions struct {
	Models     []UsageFilterOption `json:"models"`
	Scenarios  []UsageFilterOption `json:"scenarios"`
	Users      []UsageFilterOption `json:"users"`
	Keys       []UsageFilterOption `json:"keys"`
	Chats      []UsageFilterOption `json:"chats"`
	Tenants    []UsageFilterOption `json:"tenants"`
	Projects   []UsageFilterOption `json:"projects"`
	Assistants []UsageFilterOption `json:"assistants"`
	Releases   []UsageFilterOption `json:"releases"`
}

// UsageFilterOptions collects the distinct dimensions present in the cost
// ledger (request-level attribution) and the governance trace (operational
// attribution). Non-governance callers only see their own tenant.
//
// `search` narrows every dimension to labels or values containing the term
// (case-insensitive), which is what the searchable assistant/chat comboboxes
// send while the operator types.
func (s *store) UsageFilterOptions(ctx context.Context, tenantID string, scopeAll bool, search string) (UsageFilterOptions, error) {
	out := UsageFilterOptions{
		Models:     []UsageFilterOption{},
		Scenarios:  []UsageFilterOption{},
		Users:      []UsageFilterOption{},
		Keys:       []UsageFilterOption{},
		Chats:      []UsageFilterOption{},
		Tenants:    []UsageFilterOption{},
		Projects:   []UsageFilterOption{},
		Assistants: []UsageFilterOption{},
		Releases:   []UsageFilterOption{},
	}

	// Each dimension builds its own query: sharing one builder across Count and
	// Find is exactly the bug class gormguard now blocks.
	costQuery := func() *gorm.DB {
		q := s.WithContext(ctx).Model(&model.CostMetric{})
		if !scopeAll {
			q = q.Where("tenant_id = ?", tenantID)
		}
		return q
	}
	traceQuery := func() *gorm.DB {
		q := s.WithContext(ctx).Model(&model.TraceRun{})
		if !scopeAll {
			q = q.Where("tenant_id = ?", tenantID)
		}
		return q
	}
	limit := usageFilterOptionLimit
	if strings.TrimSpace(search) != "" {
		limit = usageFilterOptionSearchLimit
	}
	distinct := func(build func() *gorm.DB, column string) ([]string, error) {
		var values []string
		err := build().
			Where(fmt.Sprintf("%s <> ''", column)).
			Distinct().
			Order(column).
			Limit(limit).
			Pluck(column, &values).Error
		return values, err
	}

	models, err := distinct(costQuery, "model")
	if err != nil {
		return out, err
	}
	out.Models = plainOptions(models)

	costScenarios, err := distinct(costQuery, "scenario")
	if err != nil {
		return out, err
	}
	traceScenarios, err := distinct(traceQuery, "app_type")
	if err != nil {
		return out, err
	}
	out.Scenarios = plainOptions(unionStrings(costScenarios, traceScenarios))

	userIDs, err := distinct(costQuery, "user_id")
	if err != nil {
		return out, err
	}
	if err := s.appendNamedOptions(&out.Users, userIDs, func(ids []string) (map[string]string, error) {
		var users []model.User
		if err := s.WithContext(ctx).Where("id IN ?", ids).Find(&users).Error; err != nil {
			return nil, err
		}
		labels := make(map[string]string, len(users))
		for _, user := range users {
			labels[user.ID] = user.Username
		}
		return labels, nil
	}); err != nil {
		return out, err
	}

	keyIDs, err := distinct(costQuery, "key_id")
	if err != nil {
		return out, err
	}
	if err := s.appendNamedOptions(&out.Keys, keyIDs, func(ids []string) (map[string]string, error) {
		var keys []model.APIKey
		if err := s.WithContext(ctx).Where("id IN ?", ids).Find(&keys).Error; err != nil {
			return nil, err
		}
		labels := make(map[string]string, len(keys))
		for _, key := range keys {
			if key.Name != "" {
				labels[key.ID] = fmt.Sprintf("%s (%s)", key.Name, key.Prefix)
			}
		}
		return labels, nil
	}); err != nil {
		return out, err
	}

	chatIDs, err := distinct(costQuery, "chat_id")
	if err != nil {
		return out, err
	}
	if err := s.appendNamedOptions(&out.Chats, chatIDs, func(ids []string) (map[string]string, error) {
		var chats []model.ChatShadow
		if err := s.WithContext(ctx).Where("id IN ?", ids).Find(&chats).Error; err != nil {
			return nil, err
		}
		labels := make(map[string]string, len(chats))
		for _, chat := range chats {
			labels[chat.ID] = chat.Name
		}
		return labels, nil
	}); err != nil {
		return out, err
	}

	projectIDs, err := distinct(traceQuery, "project_id")
	if err != nil {
		return out, err
	}
	if err := s.appendNamedOptions(&out.Projects, projectIDs, func(ids []string) (map[string]string, error) {
		var projects []model.Project
		if err := s.WithContext(ctx).Where("id IN ?", ids).Find(&projects).Error; err != nil {
			return nil, err
		}
		labels := make(map[string]string, len(projects))
		for _, project := range projects {
			labels[project.ID] = project.Name
		}
		return labels, nil
	}); err != nil {
		return out, err
	}

	assistantIDs, err := distinct(traceQuery, "assistant_id")
	if err != nil {
		return out, err
	}
	assistantNames := map[string]string{}
	if err := s.appendNamedOptions(&out.Assistants, assistantIDs, func(ids []string) (map[string]string, error) {
		var assistants []model.Assistant
		if err := s.WithContext(ctx).Where("id IN ?", ids).Find(&assistants).Error; err != nil {
			return nil, err
		}
		for _, assistant := range assistants {
			assistantNames[assistant.ID] = assistant.Name
		}
		labels := make(map[string]string, len(assistants))
		for _, assistant := range assistants {
			labels[assistant.ID] = assistant.Name
		}
		return labels, nil
	}); err != nil {
		return out, err
	}

	releaseIDs, err := distinct(traceQuery, "assistant_release_id")
	if err != nil {
		return out, err
	}
	if err := s.appendNamedOptions(&out.Releases, releaseIDs, func(ids []string) (map[string]string, error) {
		var releases []model.AssistantRelease
		if err := s.WithContext(ctx).Where("id IN ?", ids).Find(&releases).Error; err != nil {
			return nil, err
		}
		labels := make(map[string]string, len(releases))
		for _, release := range releases {
			name := assistantNames[release.AssistantID]
			if name == "" {
				labels[release.ID] = fmt.Sprintf("v%d", release.ReleaseVersion)
				continue
			}
			labels[release.ID] = fmt.Sprintf("%s v%d", name, release.ReleaseVersion)
		}
		return labels, nil
	}); err != nil {
		return out, err
	}

	if scopeAll {
		costTenants, err := distinct(costQuery, "tenant_id")
		if err != nil {
			return out, err
		}
		traceTenants, err := distinct(traceQuery, "tenant_id")
		if err != nil {
			return out, err
		}
		tenantIDs := unionStrings(costTenants, traceTenants)
		if err := s.appendNamedOptions(&out.Tenants, tenantIDs, func(ids []string) (map[string]string, error) {
			var tenants []model.Tenant
			if err := s.WithContext(ctx).Where("id IN ?", ids).Find(&tenants).Error; err != nil {
				return nil, err
			}
			labels := make(map[string]string, len(tenants))
			for _, tenant := range tenants {
				labels[tenant.ID] = tenant.Name
			}
			return labels, nil
		}); err != nil {
			return out, err
		}
	}

	return filterUsageFilterOptions(out, search), nil
}

// filterUsageFilterOptions keeps options whose label or value contains the
// search term and caps the result so a combobox never renders an unbounded
// list. An empty search returns the options untouched.
func filterUsageFilterOptions(options UsageFilterOptions, search string) UsageFilterOptions {
	term := strings.ToLower(strings.TrimSpace(search))
	if term == "" {
		return options
	}
	match := func(option UsageFilterOption) bool {
		return strings.Contains(strings.ToLower(option.Label), term) ||
			strings.Contains(strings.ToLower(option.Value), term)
	}
	narrow := func(list []UsageFilterOption) []UsageFilterOption {
		out := make([]UsageFilterOption, 0, len(list))
		for _, option := range list {
			if !match(option) {
				continue
			}
			out = append(out, option)
			if len(out) == usageFilterOptionSearchResult {
				break
			}
		}
		return out
	}
	return UsageFilterOptions{
		Models:     narrow(options.Models),
		Scenarios:  narrow(options.Scenarios),
		Users:      narrow(options.Users),
		Keys:       narrow(options.Keys),
		Chats:      narrow(options.Chats),
		Tenants:    narrow(options.Tenants),
		Projects:   narrow(options.Projects),
		Assistants: narrow(options.Assistants),
		Releases:   narrow(options.Releases),
	}
}

// appendNamedOptions turns IDs into {value,label} entries, keeping the ID as the
// label when the referenced row is gone (deleted chat, removed user).
func (s *store) appendNamedOptions(
	dst *[]UsageFilterOption,
	ids []string,
	lookup func([]string) (map[string]string, error),
) error {
	if len(ids) == 0 {
		return nil
	}
	labels, err := lookup(ids)
	if err != nil {
		return err
	}
	options := make([]UsageFilterOption, 0, len(ids))
	for _, id := range ids {
		label := labels[id]
		if label == "" {
			label = id
		}
		options = append(options, UsageFilterOption{Value: id, Label: label})
	}
	sort.SliceStable(options, func(i, j int) bool {
		return options[i].Label < options[j].Label
	})
	*dst = options
	return nil
}

func plainOptions(values []string) []UsageFilterOption {
	options := make([]UsageFilterOption, 0, len(values))
	for _, value := range values {
		options = append(options, UsageFilterOption{Value: value, Label: value})
	}
	return options
}

func unionStrings(lists ...[]string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, list := range lists {
		for _, value := range list {
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}
