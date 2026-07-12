// Package clustersync 负责把 Kubernetes 资源只读采样结果同步到数据库。
package clustersync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"aiops/internal/clusterruntime"
	"aiops/internal/collector"
	"aiops/internal/store"
)

// Result 表示单个集群的一次同步结果。
type Result struct {
	ClusterID uint                        `json:"cluster_id"`
	Status    string                      `json:"status"`
	Error     string                      `json:"error,omitempty"`
	Resources []store.ClusterSyncResource `json:"resources"`
}

// SyncCluster 对指定集群执行一次只读资源同步,并更新集群状态。
func SyncCluster(ctx context.Context, st *store.Store, runtime *clusterruntime.Runtime) (*Result, error) {
	return SyncClusterWithSource(ctx, st, runtime, store.SyncSourceManual)
}

// SyncClusterWithSource 对指定集群执行一次只读资源同步,并记录触发来源。
func SyncClusterWithSource(ctx context.Context, st *store.Store, runtime *clusterruntime.Runtime, source string) (*Result, error) {
	if st == nil {
		return nil, fmt.Errorf("数据库未就绪")
	}
	if runtime == nil || runtime.ClusterID == 0 {
		return nil, fmt.Errorf("集群运行时未就绪")
	}
	if runtime.Collector == nil {
		errText := "集群采集器未就绪"
		_ = st.UpdateClusterStatus(runtime.ClusterID, store.ClusterStatusError, errText)
		return &Result{ClusterID: runtime.ClusterID, Status: store.ClusterStatusError, Error: errText}, errors.New(errText)
	}

	task, err := st.StartClusterSyncTask(runtime.ClusterID, source)
	if err != nil {
		return nil, err
	}
	if err := runtime.Collector.Ping(ctx); err != nil {
		errText := fmt.Sprintf("集群 API Server 连接失败: %v", err)
		return finishDisconnectedSync(st, task.ID, runtime.ClusterID, errText, nil)
	}
	started := time.Now()
	previousRounds := map[string]int{}
	previousResourceVersions := map[string]string{}
	if existing, err := st.ListClusterSyncResources(runtime.ClusterID); err == nil {
		for _, row := range existing {
			previousRounds[row.ResourceName] = row.IncrementalRounds
			previousResourceVersions[row.ResourceName] = row.LastResourceVersion
		}
	}

	watchResults := runtime.Collector.WatchResourceChanges(ctx, previousResourceVersions)
	snapshots := runtime.Collector.SyncResourceSnapshots(ctx)
	var syncErrors []string
	okCount := 0 // 成功拉取(有响应)的资源数,用于区分"完全连不上"与"部分异常"
	for _, snapshot := range snapshots {
		if snapshot.Error != "" {
			syncErrors = append(syncErrors, fmt.Sprintf("%s: %s", snapshot.ResourceName, snapshot.Error))
		} else if snapshot.Count >= 0 {
			okCount++ // 有正常返回(N/A 资源 Count 为 -1,不计入)
		}
	}
	if len(syncErrors) > 0 && okCount == 0 {
		finished := time.Now()
		attemptedRows := make([]store.ClusterSyncResource, 0, len(snapshots))
		for _, snapshot := range snapshots {
			attemptedRows = append(attemptedRows, toSyncRow(
				snapshot,
				started,
				finished,
				previousRounds[snapshot.ResourceName],
				watchResults[snapshot.ResourceName],
				source,
			))
		}
		return finishDisconnectedSync(
			st,
			task.ID,
			runtime.ClusterID,
			strings.Join(syncErrors, "; "),
			attemptedRows,
		)
	}

	objectSnapshots := toStoreObjectSnapshots(runtime.Collector.SyncResourceObjectSnapshots(ctx))
	resourceNames := resourceNamesForSync(snapshots, objectSnapshots)
	changeCounts, err := st.ReplaceClusterResourceSnapshots(runtime.ClusterID, resourceNames, objectSnapshots)
	if err != nil {
		_ = st.FinishClusterSyncTask(task.ID, store.SyncStatusFailed, len(objectSnapshots), 0, err.Error())
		return nil, err
	}
	finished := time.Now()
	rows := make([]store.ClusterSyncResource, 0, len(snapshots))
	var version string
	changedTotal := 0
	for _, snapshot := range snapshots {
		if snapshot.Version != "" && version == "" {
			version = snapshot.Version
		}
		row := toSyncRow(snapshot, started, finished, previousRounds[snapshot.ResourceName], watchResults[snapshot.ResourceName], source)
		if count, ok := changeCounts[snapshot.ResourceName]; ok && count > row.LastChangeCount {
			row.LastChangeCount = count
		}
		changedTotal += row.LastChangeCount
		rows = append(rows, row)
	}
	if err := st.ReplaceClusterSyncResources(runtime.ClusterID, rows); err != nil {
		_ = st.FinishClusterSyncTask(task.ID, store.SyncStatusFailed, len(objectSnapshots), changedTotal, err.Error())
		return nil, err
	}
	if version != "" {
		_ = st.UpdateClusterVersion(runtime.ClusterID, version)
	}

	status := store.ClusterStatusReady
	taskStatus := store.SyncStatusSuccess
	lastError := ""
	if len(syncErrors) > 0 {
		taskStatus = store.SyncStatusFailed
		lastError = strings.Join(syncErrors, "; ")
		// 全部资源都拉取失败 = 集群 API Server 不可达(断连);仅部分失败 = 局部异常(降级)
		if okCount == 0 {
			status = store.ClusterStatusDisconnected
		} else {
			status = store.ClusterStatusDegraded
		}
	}
	if err := st.UpdateClusterStatus(runtime.ClusterID, status, lastError); err != nil {
		_ = st.FinishClusterSyncTask(task.ID, store.SyncStatusFailed, len(objectSnapshots), changedTotal, err.Error())
		return nil, err
	}
	if err := st.FinishClusterSyncTask(task.ID, taskStatus, len(objectSnapshots), changedTotal, lastError); err != nil {
		return nil, err
	}
	return &Result{ClusterID: runtime.ClusterID, Status: status, Error: lastError, Resources: rows}, nil
}

func finishDisconnectedSync(st *store.Store, taskID, clusterID uint, errorText string, resources []store.ClusterSyncResource) (*Result, error) {
	if err := st.UpdateClusterStatus(clusterID, store.ClusterStatusDisconnected, errorText); err != nil {
		_ = st.FinishClusterSyncTask(taskID, store.SyncStatusFailed, 0, 0, err.Error())
		return nil, err
	}
	if err := st.FinishClusterSyncTask(taskID, store.SyncStatusFailed, 0, 0, errorText); err != nil {
		return nil, err
	}
	return &Result{
		ClusterID: clusterID,
		Status:    store.ClusterStatusDisconnected,
		Error:     errorText,
		Resources: resources,
	}, nil
}

func toSyncRow(snapshot collector.ResourceSyncSnapshot, started, finished time.Time, previousRounds int, watchResult collector.ResourceWatchResult, source string) store.ClusterSyncResource {
	if strings.TrimSpace(source) == "" {
		source = store.SyncSourceManual
	}
	row := store.ClusterSyncResource{
		APIVersion:            snapshot.APIVersion,
		Kind:                  snapshot.Kind,
		ResourceName:          snapshot.ResourceName,
		ItemCount:             snapshot.Count,
		FullStartedAt:         &started,
		FullFinishedAt:        &finished,
		FullStatus:            store.SyncStatusSuccess,
		IncrementalStatus:     store.SyncStatusNA,
		IncrementalRounds:     previousRounds,
		IncrementalStartedAt:  nil,
		IncrementalFinishedAt: nil,
		LastSource:            source,
		LastResourceVersion:   snapshot.ResourceVersion,
	}
	if !snapshot.Supported {
		row.FullStatus = store.SyncStatusNA
		row.ItemCount = -1
		return row
	}
	if snapshot.Error != "" {
		row.FullStatus = store.SyncStatusFailed
		row.FullError = snapshot.Error
		row.ItemCount = -1
		return row
	}
	if snapshot.IncrementalSupported {
		row.IncrementalRounds = previousRounds + 1
		if watchResult.ResourceName != "" {
			row.IncrementalStartedAt = &watchResult.StartedAt
			row.IncrementalFinishedAt = &watchResult.FinishedAt
			row.LastChangeCount = watchResult.ChangeCount
			if watchResult.Status == store.SyncStatusFailed {
				row.IncrementalStatus = store.SyncStatusFailed
				row.FullError = strings.TrimSpace(watchResult.Error)
			} else if watchResult.Status == store.SyncStatusNA {
				row.IncrementalStatus = store.SyncStatusNA
			} else {
				row.IncrementalStatus = store.SyncStatusSuccess
			}
		} else {
			row.IncrementalStartedAt = &finished
			row.IncrementalFinishedAt = &finished
			row.IncrementalStatus = store.SyncStatusSuccess
		}
	}
	return row
}

func toStoreObjectSnapshots(rows []collector.ResourceObjectSnapshot) []store.ClusterResourceSnapshot {
	result := make([]store.ClusterResourceSnapshot, 0, len(rows))
	for _, row := range rows {
		result = append(result, store.ClusterResourceSnapshot{
			APIVersion:      row.APIVersion,
			Kind:            row.Kind,
			ResourceName:    row.ResourceName,
			Namespace:       row.Namespace,
			Name:            row.Name,
			UID:             row.UID,
			ResourceVersion: row.ResourceVersion,
			Status:          row.Status,
			Ready:           row.Ready,
			NodeName:        row.NodeName,
			Summary:         row.Summary,
			ContentHash:     row.ContentHash,
		})
	}
	return result
}

func resourceNamesForSync(snapshots []collector.ResourceSyncSnapshot, rows []store.ClusterResourceSnapshot) []string {
	seen := map[string]struct{}{}
	names := make([]string, 0, len(snapshots)+len(rows))
	for _, snapshot := range snapshots {
		if strings.TrimSpace(snapshot.ResourceName) == "" {
			continue
		}
		if _, ok := seen[snapshot.ResourceName]; ok {
			continue
		}
		seen[snapshot.ResourceName] = struct{}{}
		names = append(names, snapshot.ResourceName)
	}
	for _, row := range rows {
		if strings.TrimSpace(row.ResourceName) == "" {
			continue
		}
		if _, ok := seen[row.ResourceName]; ok {
			continue
		}
		seen[row.ResourceName] = struct{}{}
		names = append(names, row.ResourceName)
	}
	return names
}
