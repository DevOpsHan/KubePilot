package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	UserRoleGlobalAdmin  = "global_admin"
	UserRoleClusterAdmin = "cluster_admin"
)

const (
	UserStatusActive   = "active"
	UserStatusDisabled = "disabled"
)

const (
	DefaultInitialUserPassword = "k8s@ops123"
	UserDeletedRetention       = 7 * 24 * time.Hour
)

const (
	PermissionViewClusters       = "clusters:view"
	PermissionManageClusters     = "clusters:manage"
	PermissionManageModels       = "models:manage"
	PermissionManageUsers        = "users:manage"
	PermissionRunInspect         = "inspect:run"
	PermissionApproveRemediation = "remediation:approve"
	PermissionViewApprovals      = "approvals:view"
	PermissionViewAlerts         = "alerts:view"
	PermissionManageAlerts       = "alerts:manage"
	// PermissionClusterExec 高危写权限:允许进入 Pod 执行命令(交互式终端)。
	// 仅全局管理员与集群管理员默认持有,巡检员/查看者不得开终端。
	PermissionClusterExec = "clusters:exec"
	// PermissionAIDiagnostics 允许 AI 助手深入现场排查(读日志/取证据/Pod 内跑只读命令)。
	// 仅全局管理员与集群管理员默认持有;巡检员只做只读查询,不含此权限。
	PermissionAIDiagnostics = "ai:diagnose"
	// PermissionManageNotifications 仅全局管理员持有(不加入其它角色映射):管理外部通知设置。
	PermissionManageNotifications = "notifications:manage"
	// PermissionManageInspection 仅全局管理员持有:管理巡检规则(启停/阈值)。
	PermissionManageInspection = "inspection:manage"
)

var (
	ErrInvalidCredentials        = errors.New("用户名或密码不正确")
	ErrUserDisabled              = errors.New("用户已停用")
	ErrUserExpired               = errors.New("用户已过期")
	ErrUserDeleted               = errors.New("用户已删除")
	ErrInvalidRole               = errors.New("无效的用户角色")
	ErrUserRoleRequiresMigration = errors.New("用户角色已失效，请联系全局管理员重新分配角色")
	ErrInvalidUserStatus         = errors.New("无效的用户状态")
	ErrInvalidSession            = errors.New("登录会话无效或已过期")
	ErrLastUsableGlobalAdmin     = errors.New("至少必须保留一个启用且永不过期的全局管理员")
)

type CreateUserInput struct {
	Username               string
	DisplayName            string
	Email                  string
	Password               string
	Role                   string
	Status                 string
	ExpiresAt              *time.Time
	PasswordChangeRequired *bool
	ClusterIDs             []uint
}

type UpdateUserInput struct {
	DisplayName            *string
	Email                  *string
	Password               *string
	Role                   *string
	Status                 *string
	ExpiresAt              *time.Time
	ExpiresAtSet           bool
	PasswordChangeRequired *bool
	ClusterIDs             *[]uint
}

func RoleHasPermission(role, permission string) bool {
	if role == UserRoleGlobalAdmin {
		return true
	}
	permissions := map[string]map[string]struct{}{
		UserRoleClusterAdmin: {
			PermissionViewClusters:       {},
			PermissionManageClusters:     {},
			PermissionClusterExec:        {},
			PermissionAIDiagnostics:      {},
			PermissionRunInspect:         {},
			PermissionViewApprovals:      {},
			PermissionApproveRemediation: {},
			PermissionViewAlerts:         {},
			PermissionManageAlerts:       {},
		},
	}
	allowed, ok := permissions[role]
	if !ok {
		return false
	}
	_, ok = allowed[permission]
	return ok
}

func ValidRoles() []string {
	return []string{UserRoleGlobalAdmin, UserRoleClusterAdmin}
}

func normalizeRole(role string) (string, error) {
	role = strings.TrimSpace(role)
	if role == "" {
		return UserRoleClusterAdmin, nil
	}
	for _, item := range ValidRoles() {
		if role == item {
			return role, nil
		}
	}
	return "", ErrInvalidRole
}

func validatePersistedUserRole(role string) error {
	role = strings.TrimSpace(role)
	for _, item := range ValidRoles() {
		if role == item {
			return nil
		}
	}
	return ErrUserRoleRequiresMigration
}

func normalizeUserStatus(status string) (string, error) {
	status = strings.TrimSpace(status)
	if status == "" {
		return UserStatusActive, nil
	}
	switch status {
	case UserStatusActive, UserStatusDisabled:
		return status, nil
	default:
		return "", ErrInvalidUserStatus
	}
}

func (s *Store) CountUsers() (int64, error) {
	var count int64
	err := s.db.Model(&User{}).Where("deleted_at IS NULL").Count(&count).Error
	return count, err
}

func (s *Store) CreateUser(input CreateUserInput) (*User, error) {
	username := strings.TrimSpace(input.Username)
	if username == "" {
		return nil, fmt.Errorf("用户名不能为空")
	}
	password := strings.TrimSpace(input.Password)
	if password == "" {
		password = DefaultInitialUserPassword
	}
	role, err := normalizeRole(input.Role)
	if err != nil {
		return nil, err
	}
	status, err := normalizeUserStatus(input.Status)
	if err != nil {
		return nil, err
	}
	passwordHash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	requireChange := true
	if input.PasswordChangeRequired != nil {
		requireChange = *input.PasswordChangeRequired
	}
	user := &User{
		Username:               username,
		DisplayName:            strings.TrimSpace(input.DisplayName),
		Email:                  strings.TrimSpace(input.Email),
		PasswordHash:           passwordHash,
		Role:                   role,
		Status:                 status,
		ExpiresAt:              input.ExpiresAt,
		PasswordChangeRequired: requireChange,
	}
	if user.DisplayName == "" {
		user.DisplayName = username
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(user).Error; err != nil {
			return err
		}
		return s.replaceUserClusterGrants(tx, user.ID, role, input.ClusterIDs)
	})
	if err != nil {
		return nil, err
	}
	return s.GetUser(user.ID)
}

func (s *Store) UpdateUser(id uint, input UpdateUserInput) (*User, error) {
	var normalizedRole *string
	if input.Role != nil {
		role, err := normalizeRole(*input.Role)
		if err != nil {
			return nil, err
		}
		normalizedRole = &role
	}
	var normalizedStatus *string
	if input.Status != nil {
		status, err := normalizeUserStatus(*input.Status)
		if err != nil {
			return nil, err
		}
		normalizedStatus = &status
	}
	var passwordHash *string
	if input.Password != nil && strings.TrimSpace(*input.Password) != "" {
		hash, err := HashPassword(*input.Password)
		if err != nil {
			return nil, err
		}
		passwordHash = &hash
	}

	var user User
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := lockGlobalAdminAnchor(tx); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND deleted_at IS NULL", id).First(&user).Error; err != nil {
			return err
		}
		now := time.Now()
		originalRole := user.Role
		wasPersistentGlobalAdmin := isPersistentGlobalAdmin(&user)
		wasUsableGlobalAdmin := isUsableGlobalAdmin(&user, now)
		if input.DisplayName != nil {
			user.DisplayName = strings.TrimSpace(*input.DisplayName)
			if user.DisplayName == "" {
				user.DisplayName = user.Username
			}
		}
		if normalizedRole != nil {
			user.Role = *normalizedRole
		}
		if input.Email != nil {
			user.Email = strings.TrimSpace(*input.Email)
		}
		expiresAtSet := input.ExpiresAtSet || input.ExpiresAt != nil
		expiresAtChanged := expiresAtSet && !optionalTimesEqual(user.ExpiresAt, input.ExpiresAt)
		if expiresAtSet {
			user.ExpiresAt = input.ExpiresAt
		}
		if normalizedStatus != nil {
			user.Status = *normalizedStatus
		}
		if passwordHash != nil {
			user.PasswordHash = *passwordHash
		}
		passwordChangeRequiredChanged := input.PasswordChangeRequired != nil &&
			user.PasswordChangeRequired != *input.PasswordChangeRequired
		if input.PasswordChangeRequired != nil {
			user.PasswordChangeRequired = *input.PasswordChangeRequired
		}
		persistentAdminReduced := wasPersistentGlobalAdmin && !isPersistentGlobalAdmin(&user)
		usableAdminReduced := wasUsableGlobalAdmin && !isUsableGlobalAdmin(&user, now)
		if persistentAdminReduced || usableAdminReduced {
			count, err := countPersistentGlobalAdminsExcluding(tx, user.ID)
			if err != nil {
				return err
			}
			if count == 0 {
				return ErrLastUsableGlobalAdmin
			}
		}
		if err := tx.Save(&user).Error; err != nil {
			return err
		}
		if input.ClusterIDs != nil {
			if err := s.replaceUserClusterGrants(tx, user.ID, user.Role, *input.ClusterIDs); err != nil {
				return err
			}
		} else if roleRequiresClusterGrants(user.Role) {
			count, err := s.countUserClusterGrants(tx, user.ID)
			if err != nil {
				return err
			}
			if count == 0 {
				return fmt.Errorf("该角色必须至少授权一个集群")
			}
		} else if user.Role == UserRoleGlobalAdmin {
			if err := tx.Where("user_id = ?", user.ID).Delete(&UserClusterGrant{}).Error; err != nil {
				return err
			}
		}
		revokeSessions := originalRole != user.Role ||
			normalizedStatus != nil && user.Status == UserStatusDisabled ||
			expiresAtChanged || passwordHash != nil || passwordChangeRequiredChanged
		if revokeSessions {
			if err := revokeAuthSessionsForUser(tx, user.ID); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return s.GetUser(user.ID)
}

func optionalTimesEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func (s *Store) ListUsers() ([]User, error) {
	var users []User
	err := s.db.Preload("ClusterGrants.Cluster").Where("deleted_at IS NULL").Order("id asc").Find(&users).Error
	return users, err
}

func (s *Store) GetUser(id uint) (*User, error) {
	var user User
	if err := s.db.Preload("ClusterGrants.Cluster").Where("id = ? AND deleted_at IS NULL", id).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Store) ListUserClusterIDs(userID uint) ([]uint, error) {
	var grants []UserClusterGrant
	if err := s.db.Where("user_id = ?", userID).Order("cluster_id asc").Find(&grants).Error; err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(grants))
	for _, grant := range grants {
		ids = append(ids, grant.ClusterID)
	}
	return ids, nil
}

func (s *Store) UserHasClusterAccess(userID, clusterID uint) (bool, error) {
	if clusterID == 0 {
		return false, nil
	}
	user, err := s.GetUser(userID)
	if err != nil {
		return false, err
	}
	if user.Role == UserRoleGlobalAdmin {
		return true, nil
	}
	var count int64
	err = s.db.Model(&UserClusterGrant{}).Where("user_id = ? AND cluster_id = ?", userID, clusterID).Count(&count).Error
	return count > 0, err
}

func (s *Store) replaceUserClusterGrants(tx *gorm.DB, userID uint, role string, clusterIDs []uint) error {
	if err := tx.Where("user_id = ?", userID).Delete(&UserClusterGrant{}).Error; err != nil {
		return err
	}
	if role == UserRoleGlobalAdmin {
		return nil
	}
	if roleRequiresClusterGrants(role) && len(clusterIDs) == 0 {
		return fmt.Errorf("该角色必须至少授权一个集群")
	}
	ids, err := s.normalizeClusterGrantIDs(tx, clusterIDs)
	if err != nil {
		return err
	}
	if roleRequiresClusterGrants(role) && len(ids) == 0 {
		return fmt.Errorf("该角色必须至少授权一个有效集群")
	}
	grants := make([]UserClusterGrant, 0, len(ids))
	for _, clusterID := range ids {
		grants = append(grants, UserClusterGrant{UserID: userID, ClusterID: clusterID})
	}
	if len(grants) == 0 {
		return nil
	}
	return tx.Create(&grants).Error
}

func (s *Store) normalizeClusterGrantIDs(tx *gorm.DB, clusterIDs []uint) ([]uint, error) {
	seen := make(map[uint]struct{}, len(clusterIDs))
	ids := make([]uint, 0, len(clusterIDs))
	for _, clusterID := range clusterIDs {
		if clusterID == 0 {
			continue
		}
		if _, ok := seen[clusterID]; ok {
			continue
		}
		seen[clusterID] = struct{}{}
		ids = append(ids, clusterID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) == 0 {
		return ids, nil
	}
	var count int64
	if err := tx.Model(&Cluster{}).Where("id IN ?", ids).Count(&count).Error; err != nil {
		return nil, err
	}
	if count != int64(len(ids)) {
		return nil, fmt.Errorf("授权集群不存在")
	}
	return ids, nil
}

func (s *Store) countUserClusterGrants(tx *gorm.DB, userID uint) (int64, error) {
	var count int64
	err := tx.Model(&UserClusterGrant{}).Where("user_id = ?", userID).Count(&count).Error
	return count, err
}

func RoleRequiresClusterGrants(role string) bool {
	return role == UserRoleClusterAdmin
}

func roleRequiresClusterGrants(role string) bool {
	return RoleRequiresClusterGrants(role)
}

func (s *Store) GetDeletedUser(id uint) (*User, error) {
	var user User
	if err := s.db.Where("id = ?", id).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Store) AuthenticateUser(username, password string) (*User, error) {
	var user User
	if err := s.db.Where("username = ? AND deleted_at IS NULL", strings.TrimSpace(username)).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if user.Status != UserStatusActive {
		return nil, ErrUserDisabled
	}
	if user.ExpiresAt != nil && time.Now().After(*user.ExpiresAt) {
		return nil, ErrUserExpired
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		now := time.Now()
		if err := recordFailedAuthentication(s.db, &user, now); err != nil {
			return nil, ErrInvalidCredentials
		}
		return nil, ErrInvalidCredentials
	}
	// 角色检查放在密码校验之后，避免仅凭用户名探测遗留账号；同时禁止
	// 已移除的 inspector/viewer/approver 继续登录成“零权限用户”。
	if err := validatePersistedUserRole(user.Role); err != nil {
		return nil, err
	}
	now := time.Now()
	if err := recordSuccessfulAuthentication(s.db, &user, now); err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	user.LastLoginAt = &now
	user.PasswordAttemptedAt = nil
	user.PasswordAttemptCount = 0
	return &user, nil
}

func authenticationSnapshotScope(db *gorm.DB, user *User, now time.Time) *gorm.DB {
	query := db.Model(&User{}).
		Where("id = ? AND deleted_at IS NULL", user.ID).
		Where("password_hash = ? AND status = ? AND role = ?", user.PasswordHash, UserStatusActive, user.Role)
	if user.ExpiresAt == nil {
		return query.Where("expires_at IS NULL")
	}
	return query.Where("expires_at = ? AND expires_at > ?", *user.ExpiresAt, now)
}

func recordFailedAuthentication(db *gorm.DB, user *User, now time.Time) error {
	result := authenticationSnapshotScope(db, user, now).UpdateColumns(map[string]any{
		"password_attempted_at":  &now,
		"password_attempt_count": gorm.Expr("password_attempt_count + ?", 1),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrInvalidCredentials
	}
	return nil
}

func recordSuccessfulAuthentication(db *gorm.DB, user *User, now time.Time) error {
	result := authenticationSnapshotScope(db, user, now).UpdateColumns(map[string]any{
		"last_login_at":          &now,
		"password_attempted_at":  nil,
		"password_attempt_count": 0,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrInvalidCredentials
	}
	return nil
}

func (s *Store) ChangeUserPassword(id uint, currentPassword, newPassword string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND deleted_at IS NULL", id).First(&user).Error; err != nil {
			return err
		}
		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPassword)); err != nil {
			return ErrInvalidCredentials
		}
		if strings.TrimSpace(currentPassword) == strings.TrimSpace(newPassword) {
			return fmt.Errorf("新密码不能与当前密码相同")
		}
		hash, err := HashPassword(newPassword)
		if err != nil {
			return err
		}
		user.PasswordHash = hash
		user.PasswordChangeRequired = false
		user.PasswordAttemptedAt = nil
		user.PasswordAttemptCount = 0
		if err := tx.Save(&user).Error; err != nil {
			return err
		}
		return revokeAuthSessionsForUser(tx, id)
	})
}

func (s *Store) ResetUserPassword(id uint) (*User, error) {
	hash, err := HashPassword(DefaultInitialUserPassword)
	if err != nil {
		return nil, err
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND deleted_at IS NULL", id).First(&user).Error; err != nil {
			return err
		}
		user.PasswordHash = hash
		user.PasswordChangeRequired = true
		user.PasswordAttemptedAt = nil
		user.PasswordAttemptCount = 0
		if err := tx.Save(&user).Error; err != nil {
			return err
		}
		return revokeAuthSessionsForUser(tx, id)
	}); err != nil {
		return nil, err
	}
	return s.GetUser(id)
}

func (s *Store) CountGlobalAdminsExcluding(excludedID uint) (int64, error) {
	return countPersistentGlobalAdminsExcluding(s.db, excludedID)
}

func countPersistentGlobalAdminsExcluding(db *gorm.DB, excludedID uint) (int64, error) {
	var count int64
	err := db.Model(&User{}).
		Where("deleted_at IS NULL AND role = ? AND status = ? AND id <> ?", UserRoleGlobalAdmin, UserStatusActive, excludedID).
		Where("expires_at IS NULL").
		Count(&count).Error
	return count, err
}

func isPersistentGlobalAdmin(user *User) bool {
	return user != nil && user.DeletedAt == nil &&
		user.Role == UserRoleGlobalAdmin && user.Status == UserStatusActive &&
		user.ExpiresAt == nil
}

func isUsableGlobalAdmin(user *User, now time.Time) bool {
	return user != nil && user.DeletedAt == nil &&
		user.Role == UserRoleGlobalAdmin && user.Status == UserStatusActive &&
		(user.ExpiresAt == nil || user.ExpiresAt.After(now))
}

// lockGlobalAdminAnchor 先锁定 ID 最小的未删除全局管理员。
// 所有可能改变管理员可用性的事务都遵循同一加锁顺序，避免并发请求各自通过复查。
func lockGlobalAdminAnchor(tx *gorm.DB) error {
	var anchor User
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id").
		Where("deleted_at IS NULL AND role = ?", UserRoleGlobalAdmin).
		Order("id ASC").
		First(&anchor).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return err
}

func (s *Store) SoftDeleteUser(id uint) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := lockGlobalAdminAnchor(tx); err != nil {
			return err
		}
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND deleted_at IS NULL", id).First(&user).Error; err != nil {
			return err
		}
		now := time.Now()
		if isPersistentGlobalAdmin(&user) || isUsableGlobalAdmin(&user, now) {
			count, err := countPersistentGlobalAdminsExcluding(tx, user.ID)
			if err != nil {
				return err
			}
			if count == 0 {
				return ErrLastUsableGlobalAdmin
			}
		}
		user.DeletedAt = &now
		user.Status = UserStatusDisabled
		if err := tx.Save(&user).Error; err != nil {
			return err
		}
		return revokeAuthSessionsForUser(tx, id)
	})
}

func (s *Store) PurgeDeletedUsers(retention time.Duration, now time.Time) (int64, error) {
	if retention <= 0 {
		retention = UserDeletedRetention
	}
	cutoff := now.Add(-retention)
	result := s.db.Where("deleted_at IS NOT NULL AND deleted_at <= ?", cutoff).Delete(&User{})
	return result.RowsAffected, result.Error
}

// CreateAuthSession 仅为仍与认证快照一致的用户签发会话。
// 用户行锁保证签发与停用、改角色、改有效期、改密/重置密码互斥：先签发则
// 后续安全变更会撤销该会话，安全变更先完成则快照校验失败。
func (s *Store) CreateAuthSession(expected *User, ttl time.Duration) (string, *AuthSession, error) {
	if expected == nil || expected.ID == 0 {
		return "", nil, ErrInvalidCredentials
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	token, err := randomToken()
	if err != nil {
		return "", nil, err
	}
	var session *AuthSession
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		var current User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND deleted_at IS NULL", expected.ID).First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInvalidCredentials
			}
			return err
		}
		now := time.Now()
		if current.Status != UserStatusActive {
			return ErrUserDisabled
		}
		if current.ExpiresAt != nil && !current.ExpiresAt.After(now) {
			return ErrUserExpired
		}
		if err := validatePersistedUserRole(current.Role); err != nil {
			return err
		}
		matchesSnapshot := current.ID == expected.ID &&
			current.PasswordHash == expected.PasswordHash &&
			current.Role == expected.Role &&
			current.Status == expected.Status &&
			optionalTimesEqual(current.ExpiresAt, expected.ExpiresAt) &&
			current.PasswordChangeRequired == expected.PasswordChangeRequired
		if !matchesSnapshot || current.PasswordChangeRequired {
			return ErrInvalidCredentials
		}
		session = &AuthSession{
			UserID:    current.ID,
			TokenHash: hashToken(token),
			ExpiresAt: now.Add(ttl),
		}
		return tx.Create(session).Error
	}); err != nil {
		return "", nil, err
	}
	return token, session, nil
}

func (s *Store) GetUserBySessionToken(token string) (*User, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrInvalidSession
	}
	var session AuthSession
	err := s.db.Where("token_hash = ? AND revoked_at IS NULL AND expires_at > ?", hashToken(token), time.Now()).First(&session).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInvalidSession
		}
		return nil, err
	}
	user, err := s.GetUser(session.UserID)
	if err != nil {
		return nil, err
	}
	if user.Status != UserStatusActive {
		return nil, ErrUserDisabled
	}
	if user.ExpiresAt != nil && time.Now().After(*user.ExpiresAt) {
		return nil, ErrUserExpired
	}
	if err := validatePersistedUserRole(user.Role); err != nil {
		return nil, err
	}
	return user, nil
}

func (s *Store) TouchAuthSession(token string, ttl time.Duration) (*User, *AuthSession, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, nil, ErrInvalidSession
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	now := time.Now()
	var session AuthSession
	err := s.db.Where("token_hash = ? AND revoked_at IS NULL AND expires_at > ?", hashToken(token), now).First(&session).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrInvalidSession
		}
		return nil, nil, err
	}
	user, err := s.GetUser(session.UserID)
	if err != nil {
		return nil, nil, err
	}
	if user.Status != UserStatusActive {
		return nil, nil, ErrUserDisabled
	}
	if user.ExpiresAt != nil && now.After(*user.ExpiresAt) {
		return nil, nil, ErrUserExpired
	}
	if err := validatePersistedUserRole(user.Role); err != nil {
		return nil, nil, err
	}
	newExpiry := now.Add(ttl)
	if err := refreshAuthSessionExpiry(s.db, session.ID, now, newExpiry); err != nil {
		return nil, nil, err
	}
	session.ExpiresAt = newExpiry
	return user, &session, nil
}

func refreshAuthSessionExpiry(db *gorm.DB, sessionID uint, now, expiresAt time.Time) error {
	result := db.Model(&AuthSession{}).
		Where("id = ? AND revoked_at IS NULL AND expires_at > ?", sessionID, now).
		UpdateColumn("expires_at", expiresAt)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrInvalidSession
	}
	return nil
}

func (s *Store) RevokeAuthSession(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrInvalidSession
	}
	now := time.Now()
	return s.db.Model(&AuthSession{}).
		Where("token_hash = ? AND revoked_at IS NULL", hashToken(token)).
		Update("revoked_at", &now).Error
}

func (s *Store) RevokeAuthSessionsForUser(userID uint) error {
	return revokeAuthSessionsForUser(s.db, userID)
}

func revokeAuthSessionsForUser(db *gorm.DB, userID uint) error {
	now := time.Now()
	return db.Model(&AuthSession{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", &now).Error
}

func HashPassword(password string) (string, error) {
	raw := strings.TrimSpace(password)
	if raw == "" {
		return "", fmt.Errorf("密码不能为空")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(raw), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
