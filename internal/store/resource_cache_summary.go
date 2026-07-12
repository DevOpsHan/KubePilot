package store

import "aiops/internal/inspect"

// ResourceCacheSummaryFromSyncResources 将数据库同步状态压缩成巡检报告可读的缓存摘要。
func ResourceCacheSummaryFromSyncResources(rows []ClusterSyncResource) []inspect.ResourceCacheSummary {
	result := make([]inspect.ResourceCacheSummary, 0, len(rows))
	for _, row := range rows {
		lastSyncedAt := row.FullFinishedAt
		if row.IncrementalFinishedAt != nil && (lastSyncedAt == nil || row.IncrementalFinishedAt.After(*lastSyncedAt)) {
			lastSyncedAt = row.IncrementalFinishedAt
		}
		result = append(result, inspect.ResourceCacheSummary{
			APIVersion:        row.APIVersion,
			Kind:              row.Kind,
			ResourceName:      row.ResourceName,
			ItemCount:         row.ItemCount,
			LastChangeCount:   row.LastChangeCount,
			LastSource:        row.LastSource,
			FullStatus:        row.FullStatus,
			IncrementalStatus: row.IncrementalStatus,
			LastSyncedAt:      lastSyncedAt,
		})
	}
	return result
}
