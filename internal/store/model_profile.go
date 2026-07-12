package store

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// CreateModelProfileInput 描述创建模型配置档案所需的字段。
type CreateModelProfileInput struct {
	Name                string
	Provider            string
	BaseURL             string
	ModelName           string
	EncryptedAPIKey     string
	MaxCompletionTokens int
	TimeoutSeconds      int
	Temperature         float32
	IsDefault           bool
	Enabled             bool
}

// UpdateModelProfileInput 描述更新模型配置档案所需的字段。
type UpdateModelProfileInput struct {
	Name                string
	Provider            string
	BaseURL             string
	ModelName           string
	EncryptedAPIKey     *string
	MaxCompletionTokens *int
	TimeoutSeconds      *int
	Temperature         *float32
	IsDefault           *bool
	Enabled             *bool
}

// CreateModelProfile 创建模型配置。第一条启用配置会自动成为默认模型。
func (s *Store) CreateModelProfile(input CreateModelProfileInput) (*ModelProfile, error) {
	profile := &ModelProfile{
		Name:                strings.TrimSpace(input.Name),
		Provider:            strings.TrimSpace(input.Provider),
		BaseURL:             strings.TrimRight(strings.TrimSpace(input.BaseURL), "/"),
		ModelName:           strings.TrimSpace(input.ModelName),
		EncryptedAPIKey:     input.EncryptedAPIKey,
		MaxCompletionTokens: input.MaxCompletionTokens,
		TimeoutSeconds:      input.TimeoutSeconds,
		Temperature:         input.Temperature,
		Enabled:             input.Enabled,
	}
	if profile.Provider == "" {
		profile.Provider = "openai"
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&ModelProfile{}).Count(&count).Error; err != nil {
			return err
		}
		profile.IsDefault = profile.Enabled && (input.IsDefault || count == 0)
		if profile.IsDefault {
			if err := tx.Model(&ModelProfile{}).Where("is_default = ?", true).Update("is_default", false).Error; err != nil {
				return err
			}
		}
		return tx.Create(profile).Error
	})
	if err != nil {
		return nil, err
	}
	return profile, nil
}

// ListModelProfiles 返回所有模型配置档案。
func (s *Store) ListModelProfiles() ([]ModelProfile, error) {
	var profiles []ModelProfile
	err := s.db.Order("is_default desc, id asc").Find(&profiles).Error
	return profiles, err
}

// GetModelProfile 返回单个模型配置档案。
func (s *Store) GetModelProfile(id uint) (*ModelProfile, error) {
	var profile ModelProfile
	if err := s.db.First(&profile, id).Error; err != nil {
		return nil, err
	}
	return &profile, nil
}

// GetDefaultModelProfile 返回当前启用的默认模型配置档案。
func (s *Store) GetDefaultModelProfile() (*ModelProfile, error) {
	var profile ModelProfile
	if err := s.db.Where("is_default = ? AND enabled = ?", true, true).First(&profile).Error; err != nil {
		return nil, err
	}
	return &profile, nil
}

// GetFirstEnabledModelProfile 返回第一条启用的模型配置档案。
func (s *Store) GetFirstEnabledModelProfile() (*ModelProfile, error) {
	var profile ModelProfile
	if err := s.db.Where("enabled = ?", true).Order("id asc").First(&profile).Error; err != nil {
		return nil, err
	}
	return &profile, nil
}

// UpdateModelProfile 更新模型配置。设置为默认时会自动取消其他默认标记。
func (s *Store) UpdateModelProfile(id uint, input UpdateModelProfileInput) (*ModelProfile, error) {
	var profile ModelProfile
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&profile, id).Error; err != nil {
			return err
		}
		if value := strings.TrimSpace(input.Name); value != "" {
			profile.Name = value
		}
		if value := strings.TrimSpace(input.Provider); value != "" {
			profile.Provider = value
		}
		if value := strings.TrimSpace(input.BaseURL); value != "" {
			profile.BaseURL = strings.TrimRight(value, "/")
		}
		if value := strings.TrimSpace(input.ModelName); value != "" {
			profile.ModelName = value
		}
		if input.EncryptedAPIKey != nil {
			profile.EncryptedAPIKey = *input.EncryptedAPIKey
		}
		if input.MaxCompletionTokens != nil {
			profile.MaxCompletionTokens = *input.MaxCompletionTokens
		}
		if input.TimeoutSeconds != nil {
			profile.TimeoutSeconds = *input.TimeoutSeconds
		}
		if input.Temperature != nil {
			profile.Temperature = *input.Temperature
		}
		if input.Enabled != nil {
			profile.Enabled = *input.Enabled
			if !profile.Enabled {
				profile.IsDefault = false
			}
		}
		if input.IsDefault != nil {
			if *input.IsDefault {
				if !profile.Enabled {
					return fmt.Errorf("禁用的模型不能设置为默认")
				}
				if err := tx.Model(&ModelProfile{}).Where("is_default = ?", true).Update("is_default", false).Error; err != nil {
					return err
				}
				profile.IsDefault = true
			} else {
				profile.IsDefault = false
			}
		}
		return tx.Save(&profile).Error
	})
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

// SetDefaultModelProfile 将指定启用模型设置为默认模型。
func (s *Store) SetDefaultModelProfile(id uint) (*ModelProfile, error) {
	var profile ModelProfile
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&profile, id).Error; err != nil {
			return err
		}
		if !profile.Enabled {
			return fmt.Errorf("禁用的模型不能设置为默认")
		}
		if err := tx.Model(&ModelProfile{}).Where("is_default = ?", true).Update("is_default", false).Error; err != nil {
			return err
		}
		profile.IsDefault = true
		return tx.Save(&profile).Error
	})
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

// ClearDefaultModelProfiles 清空所有数据库模型的默认标记。
func (s *Store) ClearDefaultModelProfiles() error {
	return s.db.Model(&ModelProfile{}).Where("is_default = ?", true).Update("is_default", false).Error
}

// DeleteModelProfile 删除模型配置。如果删除的是默认模型,会选择下一条启用配置作为默认。
func (s *Store) DeleteModelProfile(id uint) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var profile ModelProfile
		if err := tx.First(&profile, id).Error; err != nil {
			return err
		}
		if err := tx.Delete(&profile).Error; err != nil {
			return err
		}
		if !profile.IsDefault {
			return nil
		}
		var next ModelProfile
		if err := tx.Where("enabled = ?", true).Order("id asc").First(&next).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return nil
			}
			return err
		}
		return tx.Model(&ModelProfile{}).Where("id = ?", next.ID).Update("is_default", true).Error
	})
}
