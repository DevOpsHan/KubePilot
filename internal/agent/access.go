package agent

import (
	"fmt"

	"aiops/internal/ai"
	"aiops/internal/store"
)

// ChatAccessScope 是服务端根据认证用户生成的对话工具访问范围。
type ChatAccessScope struct {
	AllClusters bool
	ClusterIDs  []uint
	Permissions []string
}

// AllowsCluster 判断指定集群是否在当前用户的授权范围内。
func (s ChatAccessScope) AllowsCluster(clusterID uint) bool {
	if clusterID == 0 {
		return false
	}
	if s.AllClusters {
		return true
	}
	for _, allowedID := range s.ClusterIDs {
		if allowedID == clusterID {
			return true
		}
	}
	return false
}

func (s ChatAccessScope) allowsTool(name string) bool {
	permission, mapped := permissionForTool(name)
	if !mapped {
		return false
	}
	for _, allowed := range s.Permissions {
		if allowed == permission {
			return true
		}
	}
	return false
}

func (s ChatAccessScope) deniedToolMessage(name string) string {
	return fmt.Sprintf("当前用户没有权限执行 AI 工具 %q。", name)
}

func permissionForTool(name string) (string, bool) {
	switch name {
	case "list_clusters", "cluster_overview", "list_pods", "list_nodes", "node_metrics":
		return store.PermissionViewClusters, true
	case "run_inspection":
		return store.PermissionRunInspect, true
	case "read_pod_logs", "pod_evidence", "exec_diagnostic":
		return store.PermissionAIDiagnostics, true
	case "propose_remediation":
		return store.PermissionApproveRemediation, true
	default:
		return "", false
	}
}

func definitionsForAccess(scope ChatAccessScope) []ai.Tool {
	definitions := Definitions()
	allowed := make([]ai.Tool, 0, len(definitions))
	for _, definition := range definitions {
		if scope.allowsTool(definition.Function.Name) {
			allowed = append(allowed, definition)
		}
	}
	return allowed
}
