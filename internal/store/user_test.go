package store

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func TestLegacyRoleCannotAuthenticateOrReuseSession(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	hash, err := HashPassword("legacy-secret")
	if err != nil {
		t.Fatalf("生成测试密码失败: %v", err)
	}
	legacy := &User{
		Username:               "legacy-inspector",
		DisplayName:            "旧巡检员",
		PasswordHash:           hash,
		Role:                   "inspector",
		Status:                 UserStatusActive,
		PasswordChangeRequired: false,
	}
	if err := st.db.Create(legacy).Error; err != nil {
		t.Fatalf("写入遗留用户失败: %v", err)
	}
	if _, err := st.AuthenticateUser(legacy.Username, "legacy-secret"); !errors.Is(err, ErrUserRoleRequiresMigration) {
		t.Fatalf("遗留角色即使密码正确也应要求管理员迁移: %v", err)
	}
	after, err := st.GetUser(legacy.ID)
	if err != nil {
		t.Fatalf("读取遗留用户失败: %v", err)
	}
	if after.LastLoginAt != nil {
		t.Fatalf("拒绝遗留角色登录时不应更新最后登录时间: %+v", after)
	}

	token := "legacy-session-token"
	if err := st.db.Create(&AuthSession{
		UserID: legacy.ID, TokenHash: hashToken(token), ExpiresAt: time.Now().Add(time.Hour),
	}).Error; err != nil {
		t.Fatalf("写入遗留会话失败: %v", err)
	}
	if _, err := st.GetUserBySessionToken(token); !errors.Is(err, ErrUserRoleRequiresMigration) {
		t.Fatalf("遗留角色旧会话不应继续可用: %v", err)
	}
	if _, _, err := st.TouchAuthSession(token, time.Hour); !errors.Is(err, ErrUserRoleRequiresMigration) {
		t.Fatalf("遗留角色旧会话不应被续期: %v", err)
	}
}

func TestMigratingLegacyRoleRevokesOldSessionAndRestoresLogin(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cluster, err := st.CreateCluster(CreateClusterInput{Name: "迁移目标", Slug: "legacy-migration", Mode: ClusterModeKubeconfigData})
	if err != nil {
		t.Fatalf("创建测试集群失败: %v", err)
	}
	hash, _ := HashPassword("legacy-secret")
	legacy := &User{Username: "legacy", DisplayName: "legacy", PasswordHash: hash, Role: "inspector", Status: UserStatusActive}
	if err := st.db.Create(legacy).Error; err != nil {
		t.Fatalf("写入遗留用户失败: %v", err)
	}
	token := "old-token"
	if err := st.db.Create(&AuthSession{UserID: legacy.ID, TokenHash: hashToken(token), ExpiresAt: time.Now().Add(time.Hour)}).Error; err != nil {
		t.Fatalf("写入旧会话失败: %v", err)
	}
	role := UserRoleClusterAdmin
	clusterIDs := []uint{cluster.ID}
	updated, err := st.UpdateUser(legacy.ID, UpdateUserInput{Role: &role, ClusterIDs: &clusterIDs})
	if err != nil {
		t.Fatalf("显式迁移遗留角色失败: %v", err)
	}
	if updated.Role != UserRoleClusterAdmin || len(updated.ClusterGrants) != 1 {
		t.Fatalf("迁移结果不正确: %+v", updated)
	}
	if _, err := st.GetUserBySessionToken(token); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("角色变化后旧会话必须撤销: %v", err)
	}
	if _, err := st.AuthenticateUser(legacy.Username, "legacy-secret"); err != nil {
		t.Fatalf("管理员完成迁移后用户应可重新登录: %v", err)
	}
}

func TestUserPasswordAndSessionLifecycle(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	user, err := st.CreateUser(CreateUserInput{
		Username:               "admin",
		DisplayName:            "平台管理员",
		Password:               "admin-secret",
		Role:                   UserRoleGlobalAdmin,
		Status:                 UserStatusActive,
		PasswordChangeRequired: boolValue(false),
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

	token, session, err := st.CreateAuthSession(authed, time.Hour)
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

func TestAuthenticateUserRejectsConcurrentSecurityChanges(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(db *gorm.DB, userID uint) error
		verify func(t *testing.T, user *User)
	}{
		{
			name: "并发停用",
			mutate: func(db *gorm.DB, userID uint) error {
				return db.Model(&User{}).Where("id = ?", userID).UpdateColumn("status", UserStatusDisabled).Error
			},
			verify: func(t *testing.T, user *User) {
				if user.Status != UserStatusDisabled {
					t.Fatalf("并发停用状态不得被旧登录覆盖: %+v", user)
				}
			},
		},
		{
			name: "并发变更角色",
			mutate: func(db *gorm.DB, userID uint) error {
				return db.Model(&User{}).Where("id = ?", userID).UpdateColumn("role", UserRoleClusterAdmin).Error
			},
			verify: func(t *testing.T, user *User) {
				if user.Role != UserRoleClusterAdmin {
					t.Fatalf("并发角色变更不得被旧登录覆盖: %+v", user)
				}
			},
		},
		{
			name: "并发设置有效期",
			mutate: func(db *gorm.DB, userID uint) error {
				expiresAt := time.Now().AddDate(1, 0, 0)
				return db.Model(&User{}).Where("id = ?", userID).UpdateColumn("expires_at", &expiresAt).Error
			},
			verify: func(t *testing.T, user *User) {
				if user.ExpiresAt == nil {
					t.Fatalf("并发有效期变更不得被旧登录覆盖: %+v", user)
				}
			},
		},
		{
			name: "并发重置密码",
			mutate: func(db *gorm.DB, userID uint) error {
				hash, err := HashPassword("reset-secret")
				if err != nil {
					return err
				}
				return db.Model(&User{}).Where("id = ?", userID).UpdateColumn("password_hash", hash).Error
			},
			verify: func(t *testing.T, user *User) {
				if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte("reset-secret")); err != nil {
					t.Fatalf("并发密码重置不得被旧登录覆盖: %v", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
			if err != nil {
				t.Fatalf("打开测试数据库失败: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })
			user, err := st.CreateUser(CreateUserInput{
				Username: "concurrent-auth", Password: "original-secret",
				Role: UserRoleGlobalAdmin, Status: UserStatusActive,
			})
			if err != nil {
				t.Fatalf("创建测试用户失败: %v", err)
			}

			var fired atomic.Bool
			var callbackErr error
			const callbackName = "test:mutate_user_after_auth_query"
			if err := st.db.Callback().Query().After("gorm:query").Register(callbackName, func(*gorm.DB) {
				if fired.CompareAndSwap(false, true) {
					callbackErr = tc.mutate(st.db.Session(&gorm.Session{SkipHooks: true}), user.ID)
				}
			}); err != nil {
				t.Fatalf("注册并发认证回调失败: %v", err)
			}
			authed, authErr := st.AuthenticateUser(user.Username, "original-secret")
			if err := st.db.Callback().Query().Remove(callbackName); err != nil {
				t.Fatalf("移除并发认证回调失败: %v", err)
			}
			if callbackErr != nil {
				t.Fatalf("模拟并发安全变更失败: %v", callbackErr)
			}
			if authed != nil || !errors.Is(authErr, ErrInvalidCredentials) {
				t.Fatalf("安全字段并发变化后旧认证必须失败: user=%+v err=%v", authed, authErr)
			}
			after, err := st.GetUser(user.ID)
			if err != nil {
				t.Fatalf("读取并发认证后的用户失败: %v", err)
			}
			tc.verify(t, after)
		})
	}
}

func TestAuthenticateUserFailedAttemptOnlyUpdatesAttemptMetadata(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, err := st.CreateUser(CreateUserInput{
		Username: "failed-auth", Email: "before@example.com", Password: "original-secret",
		Role: UserRoleGlobalAdmin, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("创建测试用户失败: %v", err)
	}
	if err := st.db.Model(&User{}).Where("id = ?", user.ID).UpdateColumn("password_attempt_count", 2).Error; err != nil {
		t.Fatalf("准备密码失败次数失败: %v", err)
	}

	var fired atomic.Bool
	var callbackErr error
	const callbackName = "test:update_email_after_auth_query"
	if err := st.db.Callback().Query().After("gorm:query").Register(callbackName, func(*gorm.DB) {
		if fired.CompareAndSwap(false, true) {
			callbackErr = st.db.Session(&gorm.Session{SkipHooks: true}).Model(&User{}).
				Where("id = ?", user.ID).UpdateColumn("email", "concurrent@example.com").Error
		}
	}); err != nil {
		t.Fatalf("注册失败认证回调失败: %v", err)
	}
	_, authErr := st.AuthenticateUser(user.Username, "wrong-secret")
	if err := st.db.Callback().Query().Remove(callbackName); err != nil {
		t.Fatalf("移除失败认证回调失败: %v", err)
	}
	if callbackErr != nil {
		t.Fatalf("模拟并发邮箱更新失败: %v", callbackErr)
	}
	if !errors.Is(authErr, ErrInvalidCredentials) {
		t.Fatalf("错误密码必须返回认证失败: %v", authErr)
	}
	after, err := st.GetUser(user.ID)
	if err != nil {
		t.Fatalf("读取失败认证后的用户失败: %v", err)
	}
	if after.Email != "concurrent@example.com" {
		t.Fatalf("失败认证不得用整行保存覆盖并发字段: %+v", after)
	}
	if after.PasswordAttemptCount != 3 || after.PasswordAttemptedAt == nil {
		t.Fatalf("失败认证只应原子更新密码尝试元数据: %+v", after)
	}
}

func TestAuthenticateUserFailedAttemptRejectsStalePasswordSnapshot(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, err := st.CreateUser(CreateUserInput{
		Username: "stale-failed-auth", Password: "original-secret",
		Role: UserRoleGlobalAdmin, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("创建测试用户失败: %v", err)
	}
	if err := st.db.Model(&User{}).Where("id = ?", user.ID).UpdateColumn("password_attempt_count", 2).Error; err != nil {
		t.Fatalf("准备密码失败次数失败: %v", err)
	}
	resetHash, err := HashPassword("reset-secret")
	if err != nil {
		t.Fatalf("生成重置密码哈希失败: %v", err)
	}

	var fired atomic.Bool
	var callbackErr error
	const callbackName = "test:reset_password_after_failed_auth_query"
	if err := st.db.Callback().Query().After("gorm:query").Register(callbackName, func(*gorm.DB) {
		if fired.CompareAndSwap(false, true) {
			callbackErr = st.db.Session(&gorm.Session{SkipHooks: true}).Model(&User{}).
				Where("id = ?", user.ID).UpdateColumn("password_hash", resetHash).Error
		}
	}); err != nil {
		t.Fatalf("注册失败认证重置回调失败: %v", err)
	}
	_, authErr := st.AuthenticateUser(user.Username, "wrong-secret")
	if err := st.db.Callback().Query().Remove(callbackName); err != nil {
		t.Fatalf("移除失败认证重置回调失败: %v", err)
	}
	if callbackErr != nil {
		t.Fatalf("模拟并发密码重置失败: %v", callbackErr)
	}
	if !errors.Is(authErr, ErrInvalidCredentials) {
		t.Fatalf("旧密码快照失败认证必须返回安全错误: %v", authErr)
	}
	after, err := st.GetUser(user.ID)
	if err != nil {
		t.Fatalf("读取并发重置后的用户失败: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(after.PasswordHash), []byte("reset-secret")); err != nil {
		t.Fatalf("失败认证不得恢复已被重置的旧密码: %v", err)
	}
	if after.PasswordAttemptCount != 2 {
		t.Fatalf("密码快照失效后不得记录到新密码版本: %+v", after)
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
	token, session, err := st.CreateAuthSession(user, time.Minute)
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

func TestTouchAuthSessionAcceptsActiveSessionWhenDriverReportsNoChangedRows(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	user, err := st.CreateUser(CreateUserInput{
		Username: "mysql-noop-touch", Password: "admin-secret",
		Role: UserRoleGlobalAdmin, Status: UserStatusActive, PasswordChangeRequired: boolValue(false),
	})
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	token, _, err := st.CreateAuthSession(user, time.Minute)
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}

	var forced atomic.Bool
	const callbackName = "test:simulate_mysql_noop_update"
	if err := st.db.Callback().Update().After("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Schema != nil && tx.Statement.Schema.Name == "AuthSession" {
			forced.Store(true)
			tx.RowsAffected = 0
		}
	}); err != nil {
		t.Fatalf("注册 MySQL 同值更新模拟回调失败: %v", err)
	}
	t.Cleanup(func() { _ = st.db.Callback().Update().Remove(callbackName) })

	sessionUser, touched, err := st.TouchAuthSession(token, 30*time.Minute)
	if !forced.Load() {
		t.Fatal("测试未触发会话续期 UPDATE")
	}
	if err != nil {
		t.Fatalf("有效会话的幂等续期不应被误判为失效: %v", err)
	}
	if sessionUser.ID != user.ID || touched == nil || time.Until(touched.ExpiresAt) < 29*time.Minute {
		t.Fatalf("幂等续期应返回有效用户和实际过期时间: user=%+v session=%+v", sessionUser, touched)
	}
}

func TestRefreshAuthSessionExpiryDoesNotShortenNewerConcurrentExpiry(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	user, err := st.CreateUser(CreateUserInput{
		Username: "monotonic-session-expiry", Password: "admin-secret",
		Role: UserRoleGlobalAdmin, Status: UserStatusActive, PasswordChangeRequired: boolValue(false),
	})
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	_, session, err := st.CreateAuthSession(user, time.Minute)
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}

	now := time.Now()
	newerExpiry := now.Add(45 * time.Minute).Truncate(time.Millisecond)
	if err := st.db.Model(&AuthSession{}).Where("id = ?", session.ID).
		UpdateColumn("expires_at", newerExpiry).Error; err != nil {
		t.Fatalf("准备较新的并发续期结果失败: %v", err)
	}
	olderExpiry := newerExpiry.Add(-5 * time.Minute)
	effectiveExpiry, err := refreshAuthSessionExpiry(st.db, session.ID, now, olderExpiry)
	if err != nil {
		t.Fatalf("较旧的并发续期请求不应失败: %v", err)
	}
	if !effectiveExpiry.Equal(newerExpiry) {
		t.Fatalf("较旧请求不得缩短会话有效期: got=%s want=%s", effectiveExpiry, newerExpiry)
	}

	var persisted AuthSession
	if err := st.db.First(&persisted, session.ID).Error; err != nil {
		t.Fatalf("读取会话最终状态失败: %v", err)
	}
	if !persisted.ExpiresAt.Equal(newerExpiry) {
		t.Fatalf("数据库中的较新有效期不得被覆盖: got=%s want=%s", persisted.ExpiresAt, newerExpiry)
	}
}

func TestRefreshAuthSessionExpiryRejectsExpiredSession(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	user, err := st.CreateUser(CreateUserInput{
		Username: "expired-session-touch", Password: "admin-secret",
		Role: UserRoleGlobalAdmin, Status: UserStatusActive, PasswordChangeRequired: boolValue(false),
	})
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	_, session, err := st.CreateAuthSession(user, time.Minute)
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}

	now := time.Now()
	if err := st.db.Model(&AuthSession{}).Where("id = ?", session.ID).
		UpdateColumn("expires_at", now.Add(-time.Second)).Error; err != nil {
		t.Fatalf("准备过期会话失败: %v", err)
	}
	if _, err := refreshAuthSessionExpiry(st.db, session.ID, now, now.Add(30*time.Minute)); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("已过期会话不得通过零行复查分支续期: %v", err)
	}
}

func TestStaleAuthSessionCannotReviveRevokedToken(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	user, err := st.CreateUser(CreateUserInput{
		Username: "session-race-admin", Password: "admin-secret",
		Role: UserRoleGlobalAdmin, Status: UserStatusActive, PasswordChangeRequired: boolValue(false),
	})
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	token, session, err := st.CreateAuthSession(user, time.Hour)
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	var stale AuthSession
	if err := st.db.First(&stale, session.ID).Error; err != nil {
		t.Fatalf("读取旧会话快照失败: %v", err)
	}
	if err := st.RevokeAuthSession(token); err != nil {
		t.Fatalf("撤销会话失败: %v", err)
	}

	now := time.Now()
	_, err = refreshAuthSessionExpiry(st.db, stale.ID, now, now.Add(10*time.Minute))
	if !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("旧会话快照不得续期已撤销会话: %v", err)
	}
	var persisted AuthSession
	if err := st.db.First(&persisted, session.ID).Error; err != nil {
		t.Fatalf("读取会话最终状态失败: %v", err)
	}
	if persisted.RevokedAt == nil {
		t.Fatal("条件续期不得把 revoked_at 写回空值")
	}
}

func TestCreateAuthSessionForAuthenticatedUserRejectsStaleSecuritySnapshot(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(st *Store, user *User) error
		expectedErr error
	}{
		{
			name: "密码已重置",
			mutate: func(st *Store, user *User) error {
				_, err := st.ResetUserPassword(user.ID)
				return err
			},
			expectedErr: ErrInvalidCredentials,
		},
		{
			name: "账号已停用",
			mutate: func(st *Store, user *User) error {
				return st.db.Model(&User{}).Where("id = ?", user.ID).UpdateColumn("status", UserStatusDisabled).Error
			},
			expectedErr: ErrUserDisabled,
		},
		{
			name: "角色已移除",
			mutate: func(st *Store, user *User) error {
				return st.db.Model(&User{}).Where("id = ?", user.ID).UpdateColumn("role", "inspector").Error
			},
			expectedErr: ErrUserRoleRequiresMigration,
		},
		{
			name: "账号已过期",
			mutate: func(st *Store, user *User) error {
				expiresAt := time.Now().Add(-time.Hour)
				return st.db.Model(&User{}).Where("id = ?", user.ID).UpdateColumn("expires_at", &expiresAt).Error
			},
			expectedErr: ErrUserExpired,
		},
		{
			name: "账号已删除",
			mutate: func(st *Store, user *User) error {
				deletedAt := time.Now()
				return st.db.Model(&User{}).Where("id = ?", user.ID).UpdateColumn("deleted_at", &deletedAt).Error
			},
			expectedErr: ErrInvalidCredentials,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
			if err != nil {
				t.Fatalf("打开测试数据库失败: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })
			user, err := st.CreateUser(CreateUserInput{
				Username: "session-snapshot", Password: "original-secret",
				Role: UserRoleGlobalAdmin, Status: UserStatusActive,
				PasswordChangeRequired: boolValue(false),
			})
			if err != nil {
				t.Fatalf("创建测试用户失败: %v", err)
			}
			authed, err := st.AuthenticateUser(user.Username, "original-secret")
			if err != nil {
				t.Fatalf("准备认证快照失败: %v", err)
			}
			if err := tc.mutate(st, user); err != nil {
				t.Fatalf("模拟认证后安全变更失败: %v", err)
			}

			if _, _, err := st.CreateAuthSession(authed, time.Hour); !errors.Is(err, tc.expectedErr) {
				t.Fatalf("认证后安全字段变化必须阻止创建新会话: %v", err)
			}
			var count int64
			if err := st.db.Model(&AuthSession{}).Where("user_id = ? AND revoked_at IS NULL", user.ID).Count(&count).Error; err != nil {
				t.Fatalf("统计会话失败: %v", err)
			}
			if count != 0 {
				t.Fatalf("安全字段变化后不得遗留新会话: count=%d", count)
			}
		})
	}
}

func TestSecurityChangeRevokesSessionCreatedFromCurrentSnapshot(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, err := st.CreateUser(CreateUserInput{
		Username: "session-before-reset", Password: "original-secret",
		Role: UserRoleGlobalAdmin, Status: UserStatusActive,
		PasswordChangeRequired: boolValue(false),
	})
	if err != nil {
		t.Fatalf("创建测试用户失败: %v", err)
	}
	authed, err := st.AuthenticateUser(user.Username, "original-secret")
	if err != nil {
		t.Fatalf("认证测试用户失败: %v", err)
	}
	token, _, err := st.CreateAuthSession(authed, time.Hour)
	if err != nil {
		t.Fatalf("当前安全快照应允许创建会话: %v", err)
	}
	if _, err := st.ResetUserPassword(user.ID); err != nil {
		t.Fatalf("重置密码失败: %v", err)
	}
	if _, err := st.GetUserBySessionToken(token); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("会话先创建时，后续安全变更必须撤销它: %v", err)
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
		Role:       UserRoleClusterAdmin,
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
	oldToken := "password-change-old-session"
	if err := st.db.Create(&AuthSession{
		UserID: user.ID, TokenHash: hashToken(oldToken), ExpiresAt: time.Now().Add(time.Hour),
	}).Error; err != nil {
		t.Fatalf("准备改密前遗留会话失败: %v", err)
	}
	if err := st.ChangeUserPassword(user.ID, DefaultInitialUserPassword, "new-secret"); err != nil {
		t.Fatalf("用户首次改密失败: %v", err)
	}
	if _, err := st.GetUserBySessionToken(oldToken); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("修改密码后旧会话必须撤销: %v", err)
	}
	changed, err := st.AuthenticateUser("ops", "new-secret")
	if err != nil {
		t.Fatalf("新密码应认证成功: %v", err)
	}
	if changed.PasswordChangeRequired {
		t.Fatalf("改密后不应继续要求修改密码: %+v", changed)
	}
}

func TestResetUserPasswordRollsBackWhenSessionRevocationFails(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, err := st.CreateUser(CreateUserInput{
		Username: "reset-rollback", Password: "original-secret",
		Role: UserRoleGlobalAdmin, Status: UserStatusActive, PasswordChangeRequired: boolValue(false),
	})
	if err != nil {
		t.Fatalf("创建测试用户失败: %v", err)
	}
	token, _, err := st.CreateAuthSession(user, time.Hour)
	if err != nil {
		t.Fatalf("创建待撤销会话失败: %v", err)
	}
	if err := st.db.Exec(`
		CREATE TRIGGER fail_auth_session_revoke
		BEFORE UPDATE OF revoked_at ON auth_sessions
		BEGIN
			SELECT RAISE(FAIL, 'forced revoke failure');
		END;
	`).Error; err != nil {
		t.Fatalf("创建会话撤销失败触发器失败: %v", err)
	}

	if _, err := st.ResetUserPassword(user.ID); err == nil {
		t.Fatal("会话撤销失败时重置密码必须整体失败")
	}
	if _, err := st.AuthenticateUser(user.Username, "original-secret"); err != nil {
		t.Fatalf("事务失败后原密码必须保留: %v", err)
	}
	if _, err := st.AuthenticateUser(user.Username, DefaultInitialUserPassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("事务失败后默认密码不得生效: %v", err)
	}
	if _, err := st.GetUserBySessionToken(token); err != nil {
		t.Fatalf("事务失败后原会话也必须保持有效: %v", err)
	}
}

func TestUserRolesAndClusterGrants(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	validRoles := strings.Join(ValidRoles(), ",")
	if strings.Contains(validRoles, "viewer") || strings.Contains(validRoles, "approver") || strings.Contains(validRoles, "inspector") {
		t.Fatalf("有效角色集合不应包含已移除的角色(观察者/审批人/巡检执行者): %s", validRoles)
	}
	if _, err := normalizeRole("viewer"); err == nil {
		t.Fatal("不应允许创建已移除的只读观察者角色")
	}
	if _, err := normalizeRole("approver"); err == nil {
		t.Fatal("不应允许创建已移除的审批人角色")
	}
	if _, err := normalizeRole("inspector"); err == nil {
		t.Fatal("不应允许创建已移除的巡检执行者角色")
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

func TestClusterAdminRejectsZeroOnlyClusterGrants(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if _, err := st.CreateUser(CreateUserInput{
		Username:   "zero-grant-create",
		Password:   "admin-secret",
		Role:       UserRoleClusterAdmin,
		Status:     UserStatusActive,
		ClusterIDs: []uint{0},
	}); err == nil {
		t.Fatal("仅包含零值 ID 的授权列表不应创建无授权集群管理员")
	}

	cluster, err := st.CreateCluster(CreateClusterInput{
		Name: "零值授权测试集群", Slug: "zero-grant-cluster", Mode: ClusterModeKubeconfigData,
	})
	if err != nil {
		t.Fatalf("创建测试集群失败: %v", err)
	}
	user, err := st.CreateUser(CreateUserInput{
		Username:   "zero-grant-update",
		Password:   "admin-secret",
		Role:       UserRoleClusterAdmin,
		Status:     UserStatusActive,
		ClusterIDs: []uint{cluster.ID},
	})
	if err != nil {
		t.Fatalf("创建有授权的集群管理员失败: %v", err)
	}
	zeroOnly := []uint{0}
	if _, err := st.UpdateUser(user.ID, UpdateUserInput{ClusterIDs: &zeroOnly}); err == nil {
		t.Fatal("更新时仅包含零值 ID 不应清空集群管理员的全部授权")
	}
	ids, err := st.ListUserClusterIDs(user.ID)
	if err != nil {
		t.Fatalf("读取更新失败后的授权失败: %v", err)
	}
	if len(ids) != 1 || ids[0] != cluster.ID {
		t.Fatalf("失败更新必须回滚并保留原授权: %v", ids)
	}
}

func TestUpdateUserOnlyRevokesSessionsWhenExpiryActuallyChanges(t *testing.T) {
	tests := []struct {
		name          string
		initialExpiry func() *time.Time
		updateExpiry  func(initial *time.Time) *time.Time
		wantRevoked   bool
	}{
		{
			name:          "重复提交永不过期",
			initialExpiry: func() *time.Time { return nil },
			updateExpiry:  func(*time.Time) *time.Time { return nil },
			wantRevoked:   false,
		},
		{
			name: "重复提交相同时间",
			initialExpiry: func() *time.Time {
				value := time.Now().AddDate(1, 0, 0).UTC().Truncate(time.Second)
				return &value
			},
			updateExpiry: func(initial *time.Time) *time.Time {
				value := initial.In(time.FixedZone("same-instant", 8*60*60))
				return &value
			},
			wantRevoked: false,
		},
		{
			name: "清空有限有效期",
			initialExpiry: func() *time.Time {
				value := time.Now().AddDate(1, 0, 0).UTC().Truncate(time.Second)
				return &value
			},
			updateExpiry: func(*time.Time) *time.Time { return nil },
			wantRevoked:  true,
		},
		{
			name: "延长有限有效期",
			initialExpiry: func() *time.Time {
				value := time.Now().AddDate(1, 0, 0).UTC().Truncate(time.Second)
				return &value
			},
			updateExpiry: func(initial *time.Time) *time.Time {
				value := initial.AddDate(1, 0, 0)
				return &value
			},
			wantRevoked: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
			if err != nil {
				t.Fatalf("打开测试数据库失败: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })
			if _, err := st.CreateUser(CreateUserInput{
				Username: "persistent-anchor", Password: "admin-secret",
				Role: UserRoleGlobalAdmin, Status: UserStatusActive,
			}); err != nil {
				t.Fatalf("创建持久管理员锚点失败: %v", err)
			}
			initialExpiry := tc.initialExpiry()
			target, err := st.CreateUser(CreateUserInput{
				Username: "expiry-target", Password: "admin-secret",
				Role: UserRoleGlobalAdmin, Status: UserStatusActive, ExpiresAt: initialExpiry,
				PasswordChangeRequired: boolValue(false),
			})
			if err != nil {
				t.Fatalf("创建有效期测试用户失败: %v", err)
			}
			token, _, err := st.CreateAuthSession(target, time.Hour)
			if err != nil {
				t.Fatalf("创建有效期测试会话失败: %v", err)
			}

			updateExpiry := tc.updateExpiry(initialExpiry)
			if _, err := st.UpdateUser(target.ID, UpdateUserInput{
				ExpiresAt: updateExpiry, ExpiresAtSet: true,
			}); err != nil {
				t.Fatalf("更新用户有效期失败: %v", err)
			}
			_, sessionErr := st.GetUserBySessionToken(token)
			if tc.wantRevoked && !errors.Is(sessionErr, ErrInvalidSession) {
				t.Fatalf("有效期实际变化后旧会话必须撤销: %v", sessionErr)
			}
			if !tc.wantRevoked && sessionErr != nil {
				t.Fatalf("重复提交相同有效期不得撤销旧会话: %v", sessionErr)
			}
		})
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
		Username:               "deleted-user",
		Password:               "user-secret",
		Role:                   UserRoleClusterAdmin,
		Status:                 UserStatusActive,
		PasswordChangeRequired: boolValue(false),
		ClusterIDs:             []uint{cluster.ID},
	})
	if err != nil {
		t.Fatalf("创建待删除用户失败: %v", err)
	}
	token, _, err := st.CreateAuthSession(user, time.Hour)
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

func TestLastPersistentGlobalAdminCannotBecomeNonPersistent(t *testing.T) {
	tests := []struct {
		name   string
		update func(clusterID uint) UpdateUserInput
	}{
		{
			name: "降级角色",
			update: func(clusterID uint) UpdateUserInput {
				role := UserRoleClusterAdmin
				clusterIDs := []uint{clusterID}
				return UpdateUserInput{Role: &role, ClusterIDs: &clusterIDs}
			},
		},
		{
			name: "停用账号",
			update: func(uint) UpdateUserInput {
				status := UserStatusDisabled
				return UpdateUserInput{Status: &status}
			},
		},
		{
			name: "有效期设为未来时间",
			update: func(uint) UpdateUserInput {
				expiresAt := time.Now().AddDate(1, 0, 0)
				return UpdateUserInput{ExpiresAt: &expiresAt}
			},
		},
		{
			name: "有效期设为过去",
			update: func(uint) UpdateUserInput {
				expiresAt := time.Now().Add(-time.Hour)
				return UpdateUserInput{ExpiresAt: &expiresAt}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
			if err != nil {
				t.Fatalf("打开测试数据库失败: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })
			cluster, err := st.CreateCluster(CreateClusterInput{
				Name: "管理员保护测试集群", Slug: "admin-invariant", Mode: ClusterModeKubeconfigData,
			})
			if err != nil {
				t.Fatalf("创建测试集群失败: %v", err)
			}
			admin, err := st.CreateUser(CreateUserInput{
				Username: "only-persistent-admin", Password: "admin-secret",
				Role: UserRoleGlobalAdmin, Status: UserStatusActive,
			})
			if err != nil {
				t.Fatalf("创建可用管理员失败: %v", err)
			}
			disabled, err := st.CreateUser(CreateUserInput{
				Username: "disabled-admin", Password: "admin-secret",
				Role: UserRoleGlobalAdmin, Status: UserStatusDisabled,
			})
			if err != nil || disabled == nil {
				t.Fatalf("创建停用管理员失败: user=%+v err=%v", disabled, err)
			}
			expiredAt := time.Now().Add(-time.Hour)
			expired, err := st.CreateUser(CreateUserInput{
				Username: "expired-admin", Password: "admin-secret",
				Role: UserRoleGlobalAdmin, Status: UserStatusActive, ExpiresAt: &expiredAt,
			})
			if err != nil || expired == nil {
				t.Fatalf("创建过期管理员失败: user=%+v err=%v", expired, err)
			}
			futureExpiry := time.Now().AddDate(1, 0, 0)
			limited, err := st.CreateUser(CreateUserInput{
				Username: "limited-admin", Password: "admin-secret",
				Role: UserRoleGlobalAdmin, Status: UserStatusActive, ExpiresAt: &futureExpiry,
			})
			if err != nil || limited == nil {
				t.Fatalf("创建有限期限管理员失败: user=%+v err=%v", limited, err)
			}

			if _, err := st.UpdateUser(admin.ID, tc.update(cluster.ID)); !errors.Is(err, ErrLastUsableGlobalAdmin) {
				t.Fatalf("最后一个持久全局管理员%s必须被拒绝: %v", tc.name, err)
			}
			after, err := st.GetUser(admin.ID)
			if err != nil {
				t.Fatalf("读取失败更新后的管理员失败: %v", err)
			}
			if after.Role != UserRoleGlobalAdmin || after.Status != UserStatusActive || after.ExpiresAt != nil {
				t.Fatalf("失败更新必须完整回滚: %+v", after)
			}
			count, err := st.CountGlobalAdminsExcluding(0)
			if err != nil || count != 1 {
				t.Fatalf("只有未设置有效期的活跃全局管理员可计入持久管理员: count=%d err=%v", count, err)
			}
		})
	}
}

func TestLastPersistentGlobalAdminCannotBeSoftDeleted(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	admin, err := st.CreateUser(CreateUserInput{
		Username: "last-admin", Password: "admin-secret",
		Role: UserRoleGlobalAdmin, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("创建管理员失败: %v", err)
	}
	futureExpiry := time.Now().AddDate(1, 0, 0)
	if _, err := st.CreateUser(CreateUserInput{
		Username: "limited-admin", Password: "admin-secret",
		Role: UserRoleGlobalAdmin, Status: UserStatusActive, ExpiresAt: &futureExpiry,
	}); err != nil {
		t.Fatalf("创建有限期限管理员失败: %v", err)
	}
	if err := st.SoftDeleteUser(admin.ID); !errors.Is(err, ErrLastUsableGlobalAdmin) {
		t.Fatalf("不得软删除最后一个持久全局管理员: %v", err)
	}
	after, err := st.GetUser(admin.ID)
	if err != nil || after.Status != UserStatusActive || after.DeletedAt != nil {
		t.Fatalf("失败删除必须完整回滚: user=%+v err=%v", after, err)
	}
}

func TestLegacyFiniteGlobalAdminCannotReduceUsableAdminsBeforeRepair(t *testing.T) {
	actions := []struct {
		name   string
		mutate func(st *Store, userID, clusterID uint) error
	}{
		{
			name: "降级有限期管理员",
			mutate: func(st *Store, userID, clusterID uint) error {
				role := UserRoleClusterAdmin
				clusterIDs := []uint{clusterID}
				_, err := st.UpdateUser(userID, UpdateUserInput{Role: &role, ClusterIDs: &clusterIDs})
				return err
			},
		},
		{
			name: "停用有限期管理员",
			mutate: func(st *Store, userID, _ uint) error {
				status := UserStatusDisabled
				_, err := st.UpdateUser(userID, UpdateUserInput{Status: &status})
				return err
			},
		},
		{
			name: "使有限期管理员过期",
			mutate: func(st *Store, userID, _ uint) error {
				expiresAt := time.Now().Add(-time.Hour)
				_, err := st.UpdateUser(userID, UpdateUserInput{ExpiresAt: &expiresAt, ExpiresAtSet: true})
				return err
			},
		},
		{
			name: "删除有限期管理员",
			mutate: func(st *Store, userID, _ uint) error {
				return st.SoftDeleteUser(userID)
			},
		},
	}

	for _, tc := range actions {
		t.Run(tc.name, func(t *testing.T) {
			st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
			if err != nil {
				t.Fatalf("打开测试数据库失败: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })
			cluster, err := st.CreateCluster(CreateClusterInput{
				Name: "遗留管理员修复集群", Slug: "legacy-finite-admin", Mode: ClusterModeKubeconfigData,
			})
			if err != nil {
				t.Fatalf("创建测试集群失败: %v", err)
			}
			futureExpiry := time.Now().AddDate(1, 0, 0)
			target, err := st.CreateUser(CreateUserInput{
				Username: "legacy-finite-target", Password: "admin-secret",
				Role: UserRoleGlobalAdmin, Status: UserStatusActive, ExpiresAt: &futureExpiry,
			})
			if err != nil {
				t.Fatalf("创建遗留有限期管理员失败: %v", err)
			}
			otherExpiry := time.Now().AddDate(2, 0, 0)
			if _, err := st.CreateUser(CreateUserInput{
				Username: "legacy-finite-other", Password: "admin-secret",
				Role: UserRoleGlobalAdmin, Status: UserStatusActive, ExpiresAt: &otherExpiry,
			}); err != nil {
				t.Fatalf("创建另一有限期管理员失败: %v", err)
			}

			if err := tc.mutate(st, target.ID, cluster.ID); !errors.Is(err, ErrLastUsableGlobalAdmin) {
				t.Fatalf("零持久管理员时不得减少可用管理员，必须先修复永不过期账号: %v", err)
			}
			after, err := st.GetUser(target.ID)
			if err != nil {
				t.Fatalf("失败操作必须保留目标管理员: %v", err)
			}
			if after.Role != UserRoleGlobalAdmin || after.Status != UserStatusActive || after.ExpiresAt == nil || !after.ExpiresAt.After(time.Now()) {
				t.Fatalf("失败操作必须完整回滚有限期管理员: %+v", after)
			}
		})
	}
}

func TestLegacyFiniteGlobalAdminCanBeRepairedToPersistent(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	futureExpiry := time.Now().AddDate(1, 0, 0)
	admin, err := st.CreateUser(CreateUserInput{
		Username: "legacy-repair", Password: "admin-secret",
		Role: UserRoleGlobalAdmin, Status: UserStatusActive, ExpiresAt: &futureExpiry,
	})
	if err != nil {
		t.Fatalf("创建遗留有限期管理员失败: %v", err)
	}
	updated, err := st.UpdateUser(admin.ID, UpdateUserInput{ExpiresAtSet: true})
	if err != nil {
		t.Fatalf("必须允许将有限期管理员修复为永不过期: %v", err)
	}
	if !isPersistentGlobalAdmin(updated) {
		t.Fatalf("清空有效期后必须成为持久管理员: %+v", updated)
	}
}

func TestGlobalAdminInvariantAllowsMutationWhenAnotherPersistentAdminExists(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	first, err := st.CreateUser(CreateUserInput{
		Username: "first-admin", Password: "admin-secret",
		Role: UserRoleGlobalAdmin, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("创建第一个管理员失败: %v", err)
	}
	if _, err := st.CreateUser(CreateUserInput{
		Username: "second-admin", Password: "admin-secret",
		Role: UserRoleGlobalAdmin, Status: UserStatusActive,
	}); err != nil {
		t.Fatalf("创建第二个管理员失败: %v", err)
	}
	futureExpiry := time.Now().AddDate(1, 0, 0)
	updated, err := st.UpdateUser(first.ID, UpdateUserInput{ExpiresAt: &futureExpiry})
	if err != nil {
		t.Fatalf("存在另一持久管理员时应允许设置有限有效期: %v", err)
	}
	if updated.ExpiresAt == nil {
		t.Fatalf("全局管理员的有限有效期功能必须保留: %+v", updated)
	}
	if err := st.SoftDeleteUser(first.ID); err != nil {
		t.Fatalf("存在另一持久管理员时应允许删除: %v", err)
	}
}

func TestConcurrentGlobalAdminMutationsKeepOnePersistentAdmin(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cluster, err := st.CreateCluster(CreateClusterInput{
		Name: "并发管理员保护测试集群", Slug: "concurrent-admin-invariant", Mode: ClusterModeKubeconfigData,
	})
	if err != nil {
		t.Fatalf("创建测试集群失败: %v", err)
	}
	first, err := st.CreateUser(CreateUserInput{
		Username: "concurrent-first-admin", Password: "admin-secret",
		Role: UserRoleGlobalAdmin, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("创建第一个管理员失败: %v", err)
	}
	second, err := st.CreateUser(CreateUserInput{
		Username: "concurrent-second-admin", Password: "admin-secret",
		Role: UserRoleGlobalAdmin, Status: UserStatusActive,
	})
	if err != nil {
		t.Fatalf("创建第二个管理员失败: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	go func() {
		ready.Done()
		<-start
		role := UserRoleClusterAdmin
		clusterIDs := []uint{cluster.ID}
		_, err := st.UpdateUser(first.ID, UpdateUserInput{Role: &role, ClusterIDs: &clusterIDs})
		results <- err
	}()
	go func() {
		ready.Done()
		<-start
		results <- st.SoftDeleteUser(second.ID)
	}()
	ready.Wait()
	close(start)

	succeeded := 0
	for range 2 {
		if err := <-results; err == nil {
			succeeded++
		}
	}
	if succeeded > 1 {
		t.Fatal("并发降级和删除不能同时成功")
	}
	count, err := st.CountGlobalAdminsExcluding(0)
	if err != nil {
		t.Fatalf("统计并发修改后的可用管理员失败: %v", err)
	}
	if count < 1 {
		t.Fatalf("并发修改后必须至少保留一个持久全局管理员: count=%d", count)
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
		{UserRoleClusterAdmin, PermissionManageUsers, false},
		{UserRoleClusterAdmin, PermissionManageModels, false},
		{"viewer", PermissionViewClusters, false},
		{"approver", PermissionApproveRemediation, false},
	}
	for _, tt := range tests {
		if got := RoleHasPermission(tt.role, tt.permission); got != tt.want {
			t.Fatalf("角色权限不正确: role=%s permission=%s got=%v want=%v", tt.role, tt.permission, got, tt.want)
		}
	}
}
