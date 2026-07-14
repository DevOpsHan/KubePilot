package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrClusterResourceSnapshotGenerationChanged = errors.New("资源快照代次已变化")

const legacyClusterResourceSnapshotGeneration = "legacy"

// ClusterResourceSnapshotPage 表示同一快照代次下的一页资源对象。
type ClusterResourceSnapshotPage struct {
	Generation string
	Snapshots  []ClusterResourceSnapshot
}

// DefaultClusterInput 描述从启动配置推导出的默认集群。
type DefaultClusterInput struct {
	Name           string
	Mode           string
	KubeconfigPath string
}

// CreateClusterInput 描述新接入集群的持久化参数。
type CreateClusterInput struct {
	Name                string
	Slug                string
	Description         string
	Mode                string
	ContextName         string
	APIServerHost       string
	Version             string
	Status              string
	Enabled             *bool
	KubeconfigPath      string
	EncryptedKubeconfig string
	KubeconfigHash      string
	RemediationEnabled  bool
}

// UpdateClusterInput 描述已有集群可编辑字段。EncryptedKubeconfig 非空时会替换访问凭据。
type UpdateClusterInput struct {
	Name                string
	Slug                string
	Description         string
	Mode                string
	ContextName         string
	APIServerHost       string
	EncryptedKubeconfig string
	KubeconfigHash      string
}

// EnsureDefaultCluster 确保历史单集群模式有一条默认集群记录。
func (s *Store) EnsureDefaultCluster(input DefaultClusterInput) (*Cluster, error) {
	var cluster Cluster
	if err := s.db.Where("slug = ?", DefaultClusterSlug).First(&cluster).Error; err == nil {
		return &cluster, nil
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = "默认集群"
	}
	mode := strings.TrimSpace(input.Mode)
	if mode == "" {
		mode = ClusterModeInCluster
	}
	enabled := true
	return s.CreateCluster(CreateClusterInput{
		Name:           name,
		Slug:           DefaultClusterSlug,
		Mode:           mode,
		Status:         ClusterStatusUnknown,
		Enabled:        &enabled,
		KubeconfigPath: input.KubeconfigPath,
	})
}

// BackfillDefaultCluster 把历史单集群时期没有 cluster_id 的数据归属到默认集群。
func (s *Store) BackfillDefaultCluster(clusterID uint) error {
	if clusterID == 0 {
		return gorm.ErrMissingWhereClause
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&ReportRecord{}).Where("cluster_id IS NULL").Update("cluster_id", clusterID).Error; err != nil {
			return err
		}
		if err := tx.Model(&AnomalyRecord{}).Where("cluster_id IS NULL").Update("cluster_id", clusterID).Error; err != nil {
			return err
		}
		if err := tx.Model(&ApprovalTicket{}).Where("cluster_id IS NULL").Update("cluster_id", clusterID).Error; err != nil {
			return err
		}
		if err := tx.Model(&AuditLog{}).Where("cluster_id IS NULL").Update("cluster_id", clusterID).Error; err != nil {
			return err
		}
		return nil
	})
}

// CreateCluster 创建集群,并同步创建凭据与策略记录。
func (s *Store) CreateCluster(input CreateClusterInput) (*Cluster, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = strings.TrimSpace(input.Slug)
	}
	slug := strings.TrimSpace(input.Slug)
	if slug == "" {
		slug = normalizeSlug(name)
	}
	mode := strings.TrimSpace(input.Mode)
	if mode == "" {
		mode = ClusterModeKubeconfigData
	}
	status := strings.TrimSpace(input.Status)
	if status == "" {
		status = ClusterStatusUnknown
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	cluster := &Cluster{
		Name:          name,
		Slug:          slug,
		Description:   input.Description,
		Mode:          mode,
		ContextName:   input.ContextName,
		APIServerHost: input.APIServerHost,
		Version:       input.Version,
		Status:        status,
		Enabled:       enabled,
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(cluster).Error; err != nil {
			return err
		}
		if input.Enabled != nil && !enabled {
			if err := tx.Model(cluster).Update("enabled", false).Error; err != nil {
				return err
			}
			cluster.Enabled = false
		}
		credential := &ClusterCredential{
			ClusterID:           cluster.ID,
			KubeconfigPath:      input.KubeconfigPath,
			EncryptedKubeconfig: input.EncryptedKubeconfig,
			KubeconfigHash:      input.KubeconfigHash,
			Version:             1,
		}
		if err := tx.Create(credential).Error; err != nil {
			return err
		}
		policy := &ClusterPolicy{
			ClusterID:          cluster.ID,
			RemediationEnabled: input.RemediationEnabled,
		}
		if err := tx.Create(policy).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return cluster, nil
}

// ListClusters 返回所有集群。
func (s *Store) ListClusters() ([]Cluster, error) {
	var clusters []Cluster
	err := s.db.Order("id asc").Find(&clusters).Error
	return clusters, err
}

// GetCluster 返回单个集群。
func (s *Store) GetCluster(id uint) (*Cluster, error) {
	var cluster Cluster
	if err := s.db.First(&cluster, id).Error; err != nil {
		return nil, err
	}
	return &cluster, nil
}

// UpdateCluster 更新集群基础信息,必要时替换 kubeconfig 凭据。
func (s *Store) UpdateCluster(clusterID uint, input UpdateClusterInput) (*Cluster, error) {
	if clusterID == 0 {
		return nil, gorm.ErrMissingWhereClause
	}
	var updated Cluster
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var cluster Cluster
		if err := tx.First(&cluster, clusterID).Error; err != nil {
			return err
		}
		name := strings.TrimSpace(input.Name)
		if name == "" {
			return fmt.Errorf("集群名称不能为空")
		}
		slug := strings.TrimSpace(input.Slug)
		if slug == "" {
			slug = cluster.Slug
		}
		if slug == "" {
			slug = normalizeSlug(name)
		}
		mode := strings.TrimSpace(input.Mode)
		if mode == "" {
			mode = cluster.Mode
		}
		updates := map[string]any{
			"name":            name,
			"slug":            slug,
			"description":     strings.TrimSpace(input.Description),
			"mode":            mode,
			"context_name":    strings.TrimSpace(input.ContextName),
			"api_server_host": strings.TrimSpace(input.APIServerHost),
		}
		if input.EncryptedKubeconfig != "" || input.KubeconfigHash != "" {
			updates["status"] = ClusterStatusUnknown
			updates["last_error"] = ""
			updates["last_validated_at"] = nil
		}
		if err := tx.Model(&cluster).Updates(updates).Error; err != nil {
			return err
		}
		if input.EncryptedKubeconfig != "" || input.KubeconfigHash != "" {
			if err := tx.Model(&ClusterCredential{}).
				Where("cluster_id = ?", clusterID).
				Updates(map[string]any{
					"encrypted_kubeconfig": strings.TrimSpace(input.EncryptedKubeconfig),
					"kubeconfig_hash":      strings.TrimSpace(input.KubeconfigHash),
					"version":              gorm.Expr("version + ?", 1),
				}).Error; err != nil {
				return err
			}
		}
		return tx.First(&updated, clusterID).Error
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// GetClusterCredential 返回集群凭据。
func (s *Store) GetClusterCredential(clusterID uint) (*ClusterCredential, error) {
	var credential ClusterCredential
	if err := s.db.Where("cluster_id = ?", clusterID).First(&credential).Error; err != nil {
		return nil, err
	}
	return &credential, nil
}

// GetClusterPolicy 返回集群策略。
func (s *Store) GetClusterPolicy(clusterID uint) (*ClusterPolicy, error) {
	var policy ClusterPolicy
	if err := s.db.Where("cluster_id = ?", clusterID).First(&policy).Error; err != nil {
		return nil, err
	}
	return &policy, nil
}

// UpdateClusterPrometheusURL upsert 集群独立 Prometheus 地址(空串=清除,回退全局配置)。
func (s *Store) UpdateClusterPrometheusURL(clusterID uint, url string) error {
	var policy ClusterPolicy
	err := s.db.Where("cluster_id = ?", clusterID).First(&policy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return s.db.Create(&ClusterPolicy{ClusterID: clusterID, PrometheusURL: url}).Error
	}
	if err != nil {
		return err
	}
	policy.PrometheusURL = url
	return s.db.Save(&policy).Error
}

// UpdateClusterStatus 更新集群连通性状态与最近校验时间。
func (s *Store) UpdateClusterStatus(clusterID uint, status, lastError string) error {
	now := time.Now()
	return s.db.Model(&Cluster{}).
		Where("id = ?", clusterID).
		Updates(map[string]any{
			"status":            status,
			"last_error":        lastError,
			"last_validated_at": &now,
		}).Error
}

// UpdateClusterVersion 更新集群版本信息。
func (s *Store) UpdateClusterVersion(clusterID uint, version string) error {
	return s.db.Model(&Cluster{}).Where("id = ?", clusterID).Update("version", version).Error
}

// DeleteCluster 删除集群及其凭据、策略。历史报告和审计保留 cluster_id 用于追溯。
func (s *Store) DeleteCluster(clusterID uint) error {
	if clusterID == 0 {
		return gorm.ErrMissingWhereClause
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("cluster_id = ?", clusterID).Delete(&ClusterCredential{}).Error; err != nil {
			return err
		}
		if err := tx.Where("cluster_id = ?", clusterID).Delete(&ClusterPolicy{}).Error; err != nil {
			return err
		}
		if err := tx.Where("cluster_id = ?", clusterID).Delete(&ClusterSyncResource{}).Error; err != nil {
			return err
		}
		if err := tx.Where("cluster_id = ?", clusterID).Delete(&ClusterSyncTask{}).Error; err != nil {
			return err
		}
		if err := tx.Where("cluster_id = ?", clusterID).Delete(&ClusterResourceSnapshot{}).Error; err != nil {
			return err
		}
		if err := tx.Where("cluster_id = ?", clusterID).Delete(&ClusterDeletedResource{}).Error; err != nil {
			return err
		}
		return tx.Delete(&Cluster{}, clusterID).Error
	})
}

// CountClusters 返回已接入集群数量。
func (s *Store) CountClusters() (int64, error) {
	var count int64
	err := s.db.Model(&Cluster{}).Count(&count).Error
	return count, err
}

// ListClusterSyncResources 返回指定集群的资源同步状态。
func (s *Store) ListClusterSyncResources(clusterID uint) ([]ClusterSyncResource, error) {
	var rows []ClusterSyncResource
	err := s.db.Where("cluster_id = ?", clusterID).Order("id asc").Find(&rows).Error
	return rows, err
}

// ReplaceClusterSyncResources 用一次同步结果替换指定集群的资源同步状态。
func (s *Store) ReplaceClusterSyncResources(clusterID uint, rows []ClusterSyncResource) error {
	if clusterID == 0 {
		return gorm.ErrMissingWhereClause
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("cluster_id = ?", clusterID).Delete(&ClusterSyncResource{}).Error; err != nil {
			return err
		}
		for i := range rows {
			rows[i].ClusterID = clusterID
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.Create(&rows).Error
	})
}

// StartClusterSyncTask 创建同步任务记录。
func (s *Store) StartClusterSyncTask(clusterID uint, source string) (*ClusterSyncTask, error) {
	if clusterID == 0 {
		return nil, gorm.ErrMissingWhereClause
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = SyncSourceManual
	}
	task := &ClusterSyncTask{
		ClusterID: clusterID,
		Source:    source,
		Status:    SyncStatusRunning,
		StartedAt: time.Now(),
	}
	if err := s.db.Create(task).Error; err != nil {
		return nil, err
	}
	return task, nil
}

// FinishClusterSyncTask 更新同步任务结束状态。
func (s *Store) FinishClusterSyncTask(taskID uint, status string, resourceCount, changedCount int, errorText string) error {
	if taskID == 0 {
		return gorm.ErrMissingWhereClause
	}
	if strings.TrimSpace(status) == "" {
		status = SyncStatusSuccess
	}
	finishedAt := time.Now()
	return s.db.Model(&ClusterSyncTask{}).
		Where("id = ?", taskID).
		Updates(map[string]any{
			"status":         status,
			"resource_count": resourceCount,
			"changed_count":  changedCount,
			"error":          errorText,
			"finished_at":    &finishedAt,
		}).Error
}

// ListClusterSyncTasks 返回最近的同步任务记录。
func (s *Store) ListClusterSyncTasks(clusterID uint, limit int) ([]ClusterSyncTask, error) {
	var tasks []ClusterSyncTask
	q := s.db.Where("cluster_id = ?", clusterID).Order("id desc")
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Find(&tasks).Error
	return tasks, err
}

// ReplaceClusterResourceSnapshots 替换指定资源类型的对象快照,并返回各资源类型的变更数量。
func (s *Store) ReplaceClusterResourceSnapshots(clusterID uint, resourceNames []string, rows []ClusterResourceSnapshot) (map[string]int, error) {
	if clusterID == 0 {
		return nil, gorm.ErrMissingWhereClause
	}
	generation, err := newClusterResourceSnapshotGeneration()
	if err != nil {
		return nil, err
	}
	names := uniqueResourceNames(resourceNames, rows)
	changes := make(map[string]int, len(names))
	for _, name := range names {
		changes[name] = 0
	}
	now := time.Now()

	err = s.db.Transaction(func(tx *gorm.DB) error {
		var cluster Cluster
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").First(&cluster, clusterID).Error; err != nil {
			return err
		}
		var existing []ClusterResourceSnapshot
		q := tx.Where("cluster_id = ?", clusterID)
		if len(names) > 0 {
			q = q.Where("resource_name IN ?", names)
		}
		if err := q.Find(&existing).Error; err != nil {
			return err
		}

		existingByKey := make(map[string]ClusterResourceSnapshot, len(existing))
		for _, row := range existing {
			existingByKey[snapshotKey(row)] = row
		}

		seen := make(map[string]struct{}, len(rows))
		for i := range rows {
			rows[i].ClusterID = clusterID
			rows[i].LastSeenAt = now
			key := snapshotKey(rows[i])
			seen[key] = struct{}{}
			old, ok := existingByKey[key]
			if !ok || old.ContentHash != rows[i].ContentHash {
				changes[rows[i].ResourceName]++
			}
		}

		for key, old := range existingByKey {
			if _, ok := seen[key]; !ok {
				changes[old.ResourceName]++
				if err := tx.Create(&ClusterDeletedResource{
					ClusterID:       clusterID,
					APIVersion:      old.APIVersion,
					Kind:            old.Kind,
					ResourceName:    old.ResourceName,
					Namespace:       old.Namespace,
					Name:            old.Name,
					UID:             old.UID,
					ResourceVersion: old.ResourceVersion,
					LastSnapshot:    old.Summary,
					DeletedAt:       now,
				}).Error; err != nil {
					return err
				}
			}
		}

		deleteQuery := tx.Where("cluster_id = ?", clusterID)
		if len(names) > 0 {
			deleteQuery = deleteQuery.Where("resource_name IN ?", names)
		}
		if err := deleteQuery.Delete(&ClusterResourceSnapshot{}).Error; err != nil {
			return err
		}
		if len(rows) > 0 {
			if err := tx.Create(&rows).Error; err != nil {
				return err
			}
		}
		return tx.Model(&Cluster{}).
			Where("id = ?", clusterID).
			UpdateColumn("resource_snapshot_generation", generation).Error
	})
	if err != nil {
		return nil, err
	}
	return changes, nil
}

// ListClusterResourceSnapshotsPage 在同一数据库快照中校验代次并返回一页对象。
func (s *Store) ListClusterResourceSnapshotsPage(clusterID uint, resourceName, generation string, offset, limit int) (*ClusterResourceSnapshotPage, error) {
	page := &ClusterResourceSnapshotPage{}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var cluster Cluster
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).
			Select("id", "resource_snapshot_generation").
			First(&cluster, clusterID).Error; err != nil {
			return err
		}
		currentGeneration := strings.TrimSpace(cluster.ResourceSnapshotGeneration)
		if currentGeneration == "" {
			currentGeneration = legacyClusterResourceSnapshotGeneration
		}
		if expected := strings.TrimSpace(generation); expected != "" && expected != currentGeneration {
			return ErrClusterResourceSnapshotGenerationChanged
		}

		var rows []ClusterResourceSnapshot
		q := tx.Where("cluster_id = ?", clusterID)
		if resourceName = strings.TrimSpace(resourceName); resourceName != "" {
			q = q.Where("resource_name = ?", resourceName)
		}
		q = q.Order("resource_name asc, namespace asc, name asc, id asc")
		if offset > 0 {
			q = q.Offset(offset)
		}
		if limit > 0 {
			q = q.Limit(limit)
		}
		if err := q.Find(&rows).Error; err != nil {
			return err
		}
		page.Generation = currentGeneration
		page.Snapshots = rows
		return nil
	})
	if err != nil {
		return nil, err
	}
	return page, nil
}

// ListClusterResourceSnapshots 返回指定集群、指定资源类型的对象快照。
func (s *Store) ListClusterResourceSnapshots(clusterID uint, resourceName string, limit int) ([]ClusterResourceSnapshot, error) {
	return s.ListClusterResourceSnapshotsWithOffset(clusterID, resourceName, 0, limit)
}

// ListClusterResourceSnapshotsWithOffset 返回指定集群、指定资源类型的一页对象快照。
func (s *Store) ListClusterResourceSnapshotsWithOffset(clusterID uint, resourceName string, offset, limit int) ([]ClusterResourceSnapshot, error) {
	var rows []ClusterResourceSnapshot
	q := s.db.Where("cluster_id = ?", clusterID)
	if strings.TrimSpace(resourceName) != "" {
		q = q.Where("resource_name = ?", strings.TrimSpace(resourceName))
	}
	q = q.Order("resource_name asc, namespace asc, name asc, id asc")
	if offset > 0 {
		q = q.Offset(offset)
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Find(&rows).Error
	return rows, err
}

// ListClusterDeletedResources 返回指定集群的资源删除历史。
func (s *Store) ListClusterDeletedResources(clusterID uint, resourceName string, limit int) ([]ClusterDeletedResource, error) {
	var rows []ClusterDeletedResource
	q := s.db.Where("cluster_id = ?", clusterID)
	if strings.TrimSpace(resourceName) != "" {
		q = q.Where("resource_name = ?", strings.TrimSpace(resourceName))
	}
	q = q.Order("deleted_at desc, id desc")
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Find(&rows).Error
	return rows, err
}

func uniqueResourceNames(names []string, rows []ClusterResourceSnapshot) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(names)+len(rows))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	for _, row := range rows {
		name := strings.TrimSpace(row.ResourceName)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result
}

func snapshotKey(row ClusterResourceSnapshot) string {
	return row.ResourceName + "\x00" + row.Namespace + "\x00" + row.Name
}

func newClusterResourceSnapshotGeneration() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("生成资源快照代次失败: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func normalizeSlug(name string) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	slug = strings.ReplaceAll(slug, " ", "-")
	if slug == "" {
		return DefaultClusterSlug
	}
	return slug
}
