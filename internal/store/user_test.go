package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUserPasswordAndSessionLifecycle(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	user, err := st.CreateUser(CreateUserInput{
		Username:    "admin",
		DisplayName: "平台管理员",
		Password:    "admin-secret",
		Role:        UserRoleGlobalAdmin,
		Status:      UserStatusActive,
	})
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	if user.PasswordHash == "" || user.PasswordHash == "admin-secret" {
		t.Fatalf("密码必须哈希保存: %+v", user)
	}

	if _, err := st.AuthenticateUser("admin", "wrong-secret"); err == nil {
		t.Fatal("错误密码不应认证成功")
	}
	authed, err := st.AuthenticateUser("admin", "admin-secret")
	if err != nil {
		t.Fatalf("正确密码应认证成功: %v", err)
	}
	if authed.ID != user.ID || authed.LastLoginAt == nil {
		t.Fatalf("认证成功后应返回用户并更新最后登录时间: %+v", authed)
	}

	token, session, err := st.CreateAuthSession(user.ID, time.Hour)
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	if token == "" || session.TokenHash == "" || session.TokenHash == token {
		t.Fatalf("会话只应保存令牌哈希: token=%q session=%+v", token, session)
	}
	sessionUser, err := st.GetUserBySessionToken(token)
	if err != nil {
		t.Fatalf("令牌应能解析到用户: %v", err)
	}
	if sessionUser.ID != user.ID {
		t.Fatalf("令牌解析用户不正确: %+v", sessionUser)
	}
	if err := st.RevokeAuthSession(token); err != nil {
		t.Fatalf("撤销会话失败: %v", err)
	}
	if _, err := st.GetUserBySessionToken(token); err == nil {
		t.Fatal("撤销后的令牌不应继续可用")
	}
}

func TestTouchAuthSessionRefreshesExpiry(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	user, err := st.CreateUser(CreateUserInput{
		Username:               "admin",
		Password:               "admin-secret",
		Role:                   UserRoleGlobalAdmin,
		Status:                 UserStatusActive,
		PasswordChangeRequired: boolValue(false),
	})
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	token, session, err := st.CreateAuthSession(user.ID, time.Minute)
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	session.ExpiresAt = time.Now().Add(2 * time.Second)
	if err := st.db.Save(session).Error; err != nil {
		t.Fatalf("准备即将过期的会话失败: %v", err)
	}

	sessionUser, touched, err := st.TouchAuthSession(token, 10*time.Minute)
	if err != nil {
		t.Fatalf("刷新会话失败: %v", err)
	}
	if sessionUser.ID != user.ID {
		t.Fatalf("刷新会话返回用户不正确: %+v", sessionUser)
	}
	if time.Until(touched.ExpiresAt) < 9*time.Minute {
		t.Fatalf("刷新后的会话应接近 10 分钟后过期: %+v", touched)
	}
}

func boolValue(value bool) *bool {
	return &value
}

func TestUserDefaultPasswordChangeAndLoginAttempts(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	expiresAt := time.Now().AddDate(0, 3, 0)
	cluster, err := st.CreateCluster(CreateClusterInput{Name: "巡检集群", Slug: "ops-cluster", Mode: ClusterModeKubeconfigData})
	if err != nil {
		t.Fatalf("创建巡检集群失败: %v", err)
	}
	user, err := st.CreateUser(CreateUserInput{
		Username:   "ops",
		Email:      "ops@example.com",
		Role:       UserRoleInspector,
		Status:     UserStatusActive,
		ExpiresAt:  &expiresAt,
		ClusterIDs: []uint{cluster.ID},
	})
	if err != nil {
		t.Fatalf("创建默认密码用户失败: %v", err)
	}
	if user.Email != "ops@example.com" || user.ExpiresAt == nil {
		t.Fatalf("用户邮箱和失效日期应保存: %+v", user)
	}
	if !user.PasswordChangeRequired {
		t.Fatalf("新增用户应要求首次登录修改密码: %+v", user)
	}
	if _, err := st.AuthenticateUser("ops", "wrong"); err == nil {
		t.Fatal("错误密码不应认证成功")
	}
	afterFail, err := st.GetUser(user.ID)
	if err != nil {
		t.Fatalf("读取失败尝试后的用户失败: %v", err)
	}
	if afterFail.PasswordAttemptCount != 1 || afterFail.PasswordAttemptedAt == nil {
		t.Fatalf("错误密码应记录尝试次数和时间: %+v", afterFail)
	}
	authed, err := st.AuthenticateUser("ops", DefaultInitialUserPassword)
	if err != nil {
		t.Fatalf("默认密码应认证成功: %v", err)
	}
	if authed.PasswordAttemptCount != 0 || authed.PasswordAttemptedAt != nil {
		t.Fatalf("认证成功应清零失败尝试记录: %+v", authed)
	}
	if err := st.ChangeUserPassword(user.ID, DefaultInitialUserPassword, "new-secret"); err != nil {
		t.Fatalf("用户首次改密失败: %v", err)
	}
	changed, err := st.AuthenticateUser("ops", "new-secret")
	if err != nil {
		t.Fatalf("新密码应认证成功: %v", err)
	}
	if changed.PasswordChangeRequired {
		t.Fatalf("改密后不应继续要求修改密码: %+v", changed)
	}
}

func TestUserRolesAndClusterGrants(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	validRoles := strings.Join(ValidRoles(), ",")
	if strings.Contains(validRoles, UserRoleViewer) || strings.Contains(validRoles, UserRoleApprover) {
		t.Fatalf("新角色集合不应继续包含只读观察者或审批人: %s", validRoles)
	}
	if _, err := normalizeRole(UserRoleViewer); err == nil {
		t.Fatal("不应继续允许创建只读观察者角色")
	}
	if _, err := normalizeRole(UserRoleApprover); err == nil {
		t.Fatal("不应继续允许创建审批人角色")
	}

	clusterA, err := st.CreateCluster(CreateClusterInput{Name: "集群 A", Slug: "grant-a", Mode: ClusterModeKubeconfigData})
	if err != nil {
		t.Fatalf("创建集群 A 失败: %v", err)
	}
	clusterB, err := st.CreateCluster(CreateClusterInput{Name: "集群 B", Slug: "grant-b", Mode: ClusterModeKubeconfigData})
	if err != nil {
		t.Fatalf("创建集群 B 失败: %v", err)
	}
	user, err := st.CreateUser(CreateUserInput{
		Username:   "cluster-admin",
		Password:   "admin-secret",
		Role:       UserRoleClusterAdmin,
		Status:     UserStatusActive,
		ClusterIDs: []uint{clusterA.ID},
	})
	if err != nil {
		t.Fatalf("创建集群管理员失败: %v", err)
	}
	ids, err := st.ListUserClusterIDs(user.ID)
	if err != nil {
		t.Fatalf("读取授权集群失败: %v", err)
	}
	if len(ids) != 1 || ids[0] != clusterA.ID {
		t.Fatalf("授权集群保存不正确: %+v", ids)
	}
	if ok, err := st.UserHasClusterAccess(user.ID, clusterA.ID); err != nil || !ok {
		t.Fatalf("用户应拥有集群 A 权限: ok=%v err=%v", ok, err)
	}
	if ok, err := st.UserHasClusterAccess(user.ID, clusterB.ID); err != nil || ok {
		t.Fatalf("用户不应拥有集群 B 权限: ok=%v err=%v", ok, err)
	}
	if _, err := st.UpdateUser(user.ID, UpdateUserInput{ClusterIDs: &[]uint{clusterB.ID}}); err != nil {
		t.Fatalf("动态修改授权集群失败: %v", err)
	}
	ids, err = st.ListUserClusterIDs(user.ID)
	if err != nil {
		t.Fatalf("修改后读取授权集群失败: %v", err)
	}
	if len(ids) != 1 || ids[0] != clusterB.ID {
		t.Fatalf("动态修改授权集群未生效: %+v", ids)
	}
}

func TestUserSoftDeleteAndPurgeAfterRetention(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	admin, err := st.CreateUser(CreateUserInput{
		Username: "admin",
		Password: "admin-secret",
		Role:     UserRoleGlobalAdmin,
		Status:   UserStatusActive,
	})
	if err != nil {
		t.Fatalf("创建管理员失败: %v", err)
	}
	cluster, err := st.CreateCluster(CreateClusterInput{Name: "删除测试集群", Slug: "delete-user-cluster", Mode: ClusterModeKubeconfigData})
	if err != nil {
		t.Fatalf("创建删除测试集群失败: %v", err)
	}
	user, err := st.CreateUser(CreateUserInput{
		Username:   "deleted-user",
		Password:   "user-secret",
		Role:       UserRoleInspector,
		Status:     UserStatusActive,
		ClusterIDs: []uint{cluster.ID},
	})
	if err != nil {
		t.Fatalf("创建待删除用户失败: %v", err)
	}
	token, _, err := st.CreateAuthSession(user.ID, time.Hour)
	if err != nil {
		t.Fatalf("创建待删除用户会话失败: %v", err)
	}
	if err := st.SoftDeleteUser(user.ID); err != nil {
		t.Fatalf("软删除用户失败: %v", err)
	}
	users, err := st.ListUsers()
	if err != nil {
		t.Fatalf("查询用户列表失败: %v", err)
	}
	if len(users) != 1 || users[0].ID != admin.ID {
		t.Fatalf("软删除用户不应出现在列表中: %+v", users)
	}
	if _, err := st.GetUserBySessionToken(token); err == nil {
		t.Fatal("删除用户后已有会话应立即失效")
	}
	if _, err := st.AuthenticateUser("deleted-user", "user-secret"); err == nil {
		t.Fatal("删除用户不应继续登录")
	}
	if deleted, err := st.GetDeletedUser(user.ID); err != nil || deleted.DeletedAt == nil || deleted.DeletedAt.IsZero() {
		t.Fatalf("软删除记录应保留 deleted_at: user=%+v err=%v", deleted, err)
	}

	if n, err := st.PurgeDeletedUsers(UserDeletedRetention, time.Now().AddDate(0, 0, 6)); err != nil || n != 0 {
		t.Fatalf("未满 7 天不应物理删除: n=%d err=%v", n, err)
	}
	if n, err := st.PurgeDeletedUsers(UserDeletedRetention, time.Now().AddDate(0, 0, 8)); err != nil || n != 1 {
		t.Fatalf("满 7 天后应物理删除: n=%d err=%v", n, err)
	}
	if _, err := st.GetDeletedUser(user.ID); err == nil {
		t.Fatal("物理删除后不应再能读取用户记录")
	}
}

func TestRolePermissions(t *testing.T) {
	tests := []struct {
		role       string
		permission string
		want       bool
	}{
		{UserRoleGlobalAdmin, PermissionManageUsers, true},
		{UserRoleGlobalAdmin, PermissionApproveRemediation, true},
		{UserRoleClusterAdmin, PermissionManageClusters, true},
		{UserRoleClusterAdmin, PermissionApproveRemediation, true},
		{UserRoleClusterAdmin, PermissionManageUsers, true},
		{UserRoleClusterAdmin, PermissionManageModels, false},
		{UserRoleInspector, PermissionRunInspect, true},
		{UserRoleInspector, PermissionManageClusters, false},
		{UserRoleViewer, PermissionViewClusters, false},
		{UserRoleApprover, PermissionApproveRemediation, false},
	}
	for _, tt := range tests {
		if got := RoleHasPermission(tt.role, tt.permission); got != tt.want {
			t.Fatalf("角色权限不正确: role=%s permission=%s got=%v want=%v", tt.role, tt.permission, got, tt.want)
		}
	}
}
