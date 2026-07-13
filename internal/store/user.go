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
)

const (
	UserRoleGlobalAdmin  = "global_admin"
	UserRoleClusterAdmin = "cluster_admin"
	UserRoleViewer       = "viewer"
	UserRoleInspector    = "inspector"
	UserRoleApprover     = "approver"
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
	// PermissionManageNotifications 仅全局管理员持有(不加入其它角色映射):管理外部通知设置。
	PermissionManageNotifications = "notifications:manage"
	// PermissionManageInspection 仅全局管理员持有:管理巡检规则(启停/阈值)。
	PermissionManageInspection = "inspection:manage"
)

var (
	ErrInvalidCredentials = errors.New("用户名或密码不正确")
	ErrUserDisabled       = errors.New("用户已停用")
	ErrUserExpired        = errors.New("用户已过期")
	ErrUserDeleted        = errors.New("用户已删除")
	ErrInvalidRole        = errors.New("无效的用户角色")
	ErrInvalidUserStatus  = errors.New("无效的用户状态")
	ErrInvalidSession     = errors.New("登录会话无效或已过期")
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
			PermissionRunInspect:         {},
			PermissionViewApprovals:      {},
			PermissionApproveRemediation: {},
			PermissionManageUsers:        {},
			PermissionViewAlerts:         {},
			PermissionManageAlerts:       {},
		},
		UserRoleInspector: {
			PermissionViewClusters: {},
			PermissionRunInspect:   {},
			PermissionViewAlerts:   {},
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
	return []string{UserRoleGlobalAdmin, UserRoleClusterAdmin, UserRoleInspector}
}

func normalizeRole(role string) (string, error) {
	role = strings.TrimSpace(role)
	if role == "" {
		return UserRoleInspector, nil
	}
	for _, item := range ValidRoles() {
		if role == item {
			return role, nil
		}
	}
	return "", ErrInvalidRole
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
	var user User
	if err := s.db.Where("id = ? AND deleted_at IS NULL", id).First(&user).Error; err != nil {
		return nil, err
	}
	if input.DisplayName != nil {
		user.DisplayName = strings.TrimSpace(*input.DisplayName)
		if user.DisplayName == "" {
			user.DisplayName = user.Username
		}
	}
	if input.Role != nil {
		role, err := normalizeRole(*input.Role)
		if err != nil {
			return nil, err
		}
		user.Role = role
	}
	if input.Email != nil {
		user.Email = strings.TrimSpace(*input.Email)
	}
	if input.ExpiresAt != nil {
		user.ExpiresAt = input.ExpiresAt
	}
	if input.Status != nil {
		status, err := normalizeUserStatus(*input.Status)
		if err != nil {
			return nil, err
		}
		user.Status = status
	}
	if input.Password != nil && strings.TrimSpace(*input.Password) != "" {
		hash, err := HashPassword(*input.Password)
		if err != nil {
			return nil, err
		}
		user.PasswordHash = hash
	}
	if input.PasswordChangeRequired != nil {
		user.PasswordChangeRequired = *input.PasswordChangeRequired
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
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
		return nil
	}); err != nil {
		return nil, err
	}
	if input.Status != nil && user.Status == UserStatusDisabled {
		_ = s.RevokeAuthSessionsForUser(user.ID)
	}
	return s.GetUser(user.ID)
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
	return role == UserRoleClusterAdmin || role == UserRoleInspector
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
		user.PasswordAttemptedAt = &now
		user.PasswordAttemptCount++
		_ = s.db.Save(&user).Error
		return nil, ErrInvalidCredentials
	}
	now := time.Now()
	user.LastLoginAt = &now
	user.PasswordAttemptedAt = nil
	user.PasswordAttemptCount = 0
	if err := s.db.Save(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Store) ChangeUserPassword(id uint, currentPassword, newPassword string) error {
	user, err := s.GetUser(id)
	if err != nil {
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
	return s.db.Save(user).Error
}

func (s *Store) ResetUserPassword(id uint) (*User, error) {
	hash, err := HashPassword(DefaultInitialUserPassword)
	if err != nil {
		return nil, err
	}
	user, err := s.GetUser(id)
	if err != nil {
		return nil, err
	}
	user.PasswordHash = hash
	user.PasswordChangeRequired = true
	user.PasswordAttemptedAt = nil
	user.PasswordAttemptCount = 0
	if err := s.db.Save(user).Error; err != nil {
		return nil, err
	}
	_ = s.RevokeAuthSessionsForUser(id)
	return user, nil
}

func (s *Store) CountGlobalAdminsExcluding(excludedID uint) (int64, error) {
	var count int64
	err := s.db.Model(&User{}).
		Where("deleted_at IS NULL AND role = ? AND id <> ?", UserRoleGlobalAdmin, excludedID).
		Count(&count).Error
	return count, err
}

func (s *Store) SoftDeleteUser(id uint) error {
	user, err := s.GetUser(id)
	if err != nil {
		return err
	}
	now := time.Now()
	user.DeletedAt = &now
	user.Status = UserStatusDisabled
	if err := s.db.Save(user).Error; err != nil {
		return err
	}
	return s.RevokeAuthSessionsForUser(id)
}

func (s *Store) PurgeDeletedUsers(retention time.Duration, now time.Time) (int64, error) {
	if retention <= 0 {
		retention = UserDeletedRetention
	}
	cutoff := now.Add(-retention)
	result := s.db.Where("deleted_at IS NOT NULL AND deleted_at <= ?", cutoff).Delete(&User{})
	return result.RowsAffected, result.Error
}

func (s *Store) CreateAuthSession(userID uint, ttl time.Duration) (string, *AuthSession, error) {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	token, err := randomToken()
	if err != nil {
		return "", nil, err
	}
	session := &AuthSession{
		UserID:    userID,
		TokenHash: hashToken(token),
		ExpiresAt: time.Now().Add(ttl),
	}
	if err := s.db.Create(session).Error; err != nil {
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
	session.ExpiresAt = now.Add(ttl)
	if err := s.db.Save(&session).Error; err != nil {
		return nil, nil, err
	}
	return user, &session, nil
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
	now := time.Now()
	return s.db.Model(&AuthSession{}).
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
