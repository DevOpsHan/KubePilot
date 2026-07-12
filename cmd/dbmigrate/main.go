// Command dbmigrate 一次性把 SQLite 数据整体迁移到 MySQL(保留主键 ID 与加密字段)。
// 用法:AIOPS_SQLITE_PATH=aiops.db AIOPS_MYSQL_DSN='...' go run ./cmd/dbmigrate
// 前提:目标 MySQL 为空库(工具会先 AutoMigrate 建表,再逐表复制)。
package main

import (
	"fmt"
	"os"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"aiops/internal/store"
)

func main() {
	sqlitePath := envOr("AIOPS_SQLITE_PATH", "aiops.db")
	mysqlDSN := envOr("AIOPS_MYSQL_DSN", "aiops:aiops_password@tcp(127.0.0.1:3306)/aiops?charset=utf8mb4&parseTime=True&loc=Local")

	src, err := gorm.Open(sqlite.Open(sqlitePath), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fatal("打开 SQLite 失败: %v", err)
	}
	dst, err := gorm.Open(mysql.Open(mysqlDSN), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fatal("连接 MySQL 失败: %v", err)
	}

	// 目标库建表(与应用一致的模型集合)
	if err := dst.AutoMigrate(models()...); err != nil {
		fatal("MySQL 建表失败: %v", err)
	}
	// 清理旧单列唯一索引(与 store.Open 迁移一致),兼容重复运行
	_ = dst.Exec("DROP INDEX idx_inspection_rule_settings_rule_name ON inspection_rule_settings").Error

	fmt.Println("开始迁移 SQLite -> MySQL ...")
	// 按依赖顺序复制(GORM 默认不建外键约束,顺序仅为可读性)
	copyTable[store.Cluster](src, dst, "clusters")
	copyTable[store.ClusterCredential](src, dst, "cluster_credentials")
	copyTable[store.ClusterPolicy](src, dst, "cluster_policies")
	copyTable[store.ClusterSyncTask](src, dst, "cluster_sync_tasks")
	copyTable[store.ClusterSyncResource](src, dst, "cluster_sync_resources")
	copyTable[store.ClusterResourceSnapshot](src, dst, "cluster_resource_snapshots")
	copyTable[store.ClusterDeletedResource](src, dst, "cluster_deleted_resources")
	copyTable[store.ModelProfile](src, dst, "model_profiles")
	copyTable[store.User](src, dst, "users")
	copyTable[store.UserClusterGrant](src, dst, "user_cluster_grants")
	copyTable[store.AuthSession](src, dst, "auth_sessions")
	copyTable[store.ReportRecord](src, dst, "report_records")
	copyTable[store.AnomalyRecord](src, dst, "anomaly_records")
	copyTable[store.ApprovalTicket](src, dst, "approval_tickets")
	copyTable[store.AuditLog](src, dst, "audit_logs")
	copyTable[store.Alert](src, dst, "alerts")
	copyTable[store.AlertSilence](src, dst, "alert_silences")
	copyTable[store.NotificationSetting](src, dst, "notification_settings")
	copyTable[store.InspectionRuleSetting](src, dst, "inspection_rule_settings")
	copyTable[store.GlobalPrometheusSetting](src, dst, "global_prometheus_settings")
	copyTable[store.CustomMetricRule](src, dst, "custom_metric_rules")
	fmt.Println("迁移完成。")
}

// models 返回与应用 store.Open 一致的模型集合。
func models() []any {
	return []any{
		&store.Cluster{}, &store.ClusterCredential{}, &store.ClusterPolicy{},
		&store.ClusterSyncTask{}, &store.ClusterSyncResource{}, &store.ClusterResourceSnapshot{},
		&store.ClusterDeletedResource{}, &store.ModelProfile{}, &store.User{},
		&store.UserClusterGrant{}, &store.AuthSession{}, &store.ReportRecord{},
		&store.AnomalyRecord{}, &store.ApprovalTicket{}, &store.AuditLog{},
		&store.Alert{}, &store.AlertSilence{}, &store.NotificationSetting{},
		&store.InspectionRuleSetting{}, &store.GlobalPrometheusSetting{}, &store.CustomMetricRule{},
	}
}

// copyTable 读源库整表并批量写入目标库(保留主键;空表跳过)。
func copyTable[T any](src, dst *gorm.DB, name string) {
	var rows []T
	if err := src.Find(&rows).Error; err != nil {
		fmt.Printf("  %s: 读取失败 %v\n", name, err)
		return
	}
	if len(rows) == 0 {
		fmt.Printf("  %s: 0(跳过)\n", name)
		return
	}
	if err := dst.Session(&gorm.Session{CreateBatchSize: 200}).Create(&rows).Error; err != nil {
		fmt.Printf("  %s: 写入失败 %v\n", name, err)
		return
	}
	fmt.Printf("  %s: %d 行已迁移\n", name, len(rows))
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
