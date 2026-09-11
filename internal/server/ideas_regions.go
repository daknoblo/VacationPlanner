package server

import (
	"sort"
	"strings"

	"github.com/daknoblo/vacationplanner/internal/models"
)

type ideaRegionGroup struct {
	Key    string
	Region string
	Items  []models.Item
}

func groupIdeas(items []models.Item) []ideaRegionGroup {
	groups := make([]ideaRegionGroup, 0)
	index := make(map[string]int)
	for _, item := range items {
		region := strings.TrimSpace(item.Region)
		key := "region:" + strings.ToLower(region)
		i, exists := index[key]
		if !exists {
			i = len(groups)
			index[key] = i
			groups = append(groups, ideaRegionGroup{Key: key, Region: region})
		}
		groups[i].Items = append(groups[i].Items, item)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Region == "" || groups[j].Region == "" {
			return groups[j].Region == "" && groups[i].Region != ""
		}
		return groups[i].Key < groups[j].Key
	})
	return groups
}
