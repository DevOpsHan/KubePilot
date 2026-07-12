package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	"aiops/internal/ai"
	"aiops/internal/inspect"
	"aiops/internal/security"
	"aiops/internal/store"
)

type modelProfileRequest struct {
	Name                string   `json:"name"`
	Provider            string   `json:"provider"`
	BaseURL             string   `json:"base_url"`
	ModelName           string   `json:"model_name"`
	APIKey              string   `json:"api_key"`
	MaxCompletionTokens *int     `json:"max_completion_tokens"`
	TimeoutSeconds      *int     `json:"timeout_seconds"`
	Temperature         *float32 `json:"temperature"`
	IsDefault           *bool    `json:"is_default"`
	Enabled             *bool    `json:"enabled"`
}

type modelProfileResponse struct {
	ID                  uint      `json:"id"`
	Source              string    `json:"source"`
	ReadOnly            bool      `json:"read_only"`
	Name                string    `json:"name"`
	Provider            string    `json:"provider"`
	BaseURL             string    `json:"base_url"`
	ModelName           string    `json:"model_name"`
	MaxCompletionTokens int       `json:"max_completion_tokens"`
	TimeoutSeconds      int       `json:"timeout_seconds"`
	Temperature         float32   `json:"temperature"`
	IsDefault           bool      `json:"is_default"`
	Enabled             bool      `json:"enabled"`
	HasAPIKey           bool      `json:"has_api_key"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// ModelRuntimeStatus 描述当前后端实际生效的大模型来源。
type ModelRuntimeStatus struct {
	Ready     bool   `json:"ready"`
	Source    string `json:"source"` // database | runtime | none
	ProfileID uint   `json:"profile_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Provider  string `json:"provider,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`
	ModelName string `json:"model_name,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

// ApplyModelClient 将模型客户端注入到对话 Agent 和所有巡检引擎。
func (s *Server) ApplyModelClient(client *ai.Client) {
	status := ModelRuntimeStatus{Source: "runtime"}
	if client != nil {
		status.Ready = true
		status.ModelName = client.ModelName()
	} else {
		status.Source = "none"
	}
	s.ApplyModelRuntime(client, status)
}

// ApplyModelRuntime 将模型客户端和来源状态注入到运行时。
func (s *Server) ApplyModelRuntime(client *ai.Client, status ModelRuntimeStatus) {
	if client == nil {
		status.Ready = false
		if status.Source == "" {
			status.Source = "none"
		}
	} else {
		status.Ready = true
		if status.Source == "" {
			status.Source = "runtime"
		}
		if status.ModelName == "" {
			status.ModelName = client.ModelName()
		}
	}
	s.modelMu.Lock()
	s.modelClient = client
	s.activeModel = status
	s.modelMu.Unlock()

	if s.agent != nil {
		s.agent.SetClient(client)
	}
	diagnoser := diagnoserFromClient(client)
	if s.engine != nil {
		s.engine.SetDiagnoser(diagnoser)
	}
	if s.runtimeManager != nil {
		for _, runtime := range s.runtimeManager.List() {
			if runtime.Engine != nil {
				runtime.Engine.SetDiagnoser(diagnoser)
			}
		}
	}
}

// CurrentModelRuntimeStatus 返回当前实际生效模型的快照。
func (s *Server) CurrentModelRuntimeStatus() ModelRuntimeStatus {
	s.modelMu.RLock()
	defer s.modelMu.RUnlock()
	return s.activeModel
}

// SyncDefaultModelFromStore 用数据库默认模型覆盖当前运行时模型。没有默认模型时不清空已有配置。
func (s *Server) SyncDefaultModelFromStore() error {
	return s.syncDefaultModelFromStore(false)
}

func (s *Server) syncDefaultModelFromStore(clearWhenMissing bool) error {
	if s.store == nil {
		return nil
	}
	profile, err := s.store.GetDefaultModelProfile()
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if !clearWhenMissing {
				first, ferr := s.store.GetFirstEnabledModelProfile()
				if ferr == nil {
					profile, serr := s.store.SetDefaultModelProfile(first.ID)
					if serr != nil {
						return serr
					}
					return s.applyModelProfile(profile)
				}
				if !errors.Is(ferr, gorm.ErrRecordNotFound) {
					return ferr
				}
			}
			if clearWhenMissing {
				s.ApplyModelRuntime(nil, ModelRuntimeStatus{Source: "none"})
			}
			return nil
		}
		return err
	}
	return s.applyModelProfile(profile)
}

func (s *Server) applyModelProfile(profile *store.ModelProfile) error {
	if profile == nil || !profile.Enabled {
		s.ApplyModelRuntime(nil, ModelRuntimeStatus{Source: "none"})
		return nil
	}
	apiKey, err := decryptModelAPIKey(profile.EncryptedAPIKey)
	if err != nil {
		s.ApplyModelRuntime(nil, ModelRuntimeStatus{
			Ready:     false,
			Source:    "database",
			ProfileID: profile.ID,
			Name:      profile.Name,
			Provider:  profile.Provider,
			BaseURL:   profile.BaseURL,
			ModelName: profile.ModelName,
			LastError: err.Error(),
		})
		return err
	}
	client := ai.NewClient(
		profile.BaseURL,
		apiKey,
		profile.ModelName,
		defaultInt(profile.MaxCompletionTokens, 4096),
		defaultInt(profile.TimeoutSeconds, 120),
		profile.Temperature,
	)
	s.ApplyModelRuntime(client, ModelRuntimeStatus{
		Ready:     true,
		Source:    "database",
		ProfileID: profile.ID,
		Name:      profile.Name,
		Provider:  profile.Provider,
		BaseURL:   profile.BaseURL,
		ModelName: profile.ModelName,
	})
	return nil
}

func (s *Server) currentDiagnoser() inspect.Diagnoser {
	s.modelMu.RLock()
	client := s.modelClient
	s.modelMu.RUnlock()
	return diagnoserFromClient(client)
}

func diagnoserFromClient(client *ai.Client) inspect.Diagnoser {
	if client == nil {
		return nil
	}
	return ai.NewDiagnoser(client)
}

func (s *Server) handleModelStatus(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]any{"current": s.CurrentModelRuntimeStatus()})
}

func (s *Server) handleListModels(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	profiles, err := s.store.ListModelProfiles()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	items := make([]modelProfileResponse, 0, len(profiles))
	current := s.CurrentModelRuntimeStatus()
	for _, profile := range profiles {
		items = append(items, toModelProfileResponse(profile, current))
	}
	return c.JSON(http.StatusOK, map[string]any{"count": len(items), "models": items})
}

func (s *Server) handleCreateModel(c echo.Context) error {
	if err := requirePermission(c, store.PermissionManageModels); err != nil {
		return err
	}
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	var req modelProfileRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
	}
	if err := validateCreateModelRequest(req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if err := s.ensureModelLimitAvailable(); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	encrypted, err := encryptModelAPIKey(req.APIKey)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	profile, err := s.store.CreateModelProfile(store.CreateModelProfileInput{
		Name:                req.Name,
		Provider:            req.Provider,
		BaseURL:             req.BaseURL,
		ModelName:           req.ModelName,
		EncryptedAPIKey:     encrypted,
		MaxCompletionTokens: intValue(req.MaxCompletionTokens, 4096),
		TimeoutSeconds:      intValue(req.TimeoutSeconds, 120),
		Temperature:         floatValue(req.Temperature, 0),
		IsDefault:           boolValue(req.IsDefault, false),
		Enabled:             boolValue(req.Enabled, true),
	})
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if profile.IsDefault {
		if err := s.applyModelProfile(profile); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	}
	return c.JSON(http.StatusCreated, map[string]any{"model": toModelProfileResponse(*profile, s.CurrentModelRuntimeStatus())})
}

func (s *Server) handleUpdateModel(c echo.Context) error {
	if err := requirePermission(c, store.PermissionManageModels); err != nil {
		return err
	}
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	var req modelProfileRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
	}
	input := store.UpdateModelProfileInput{
		Name:                req.Name,
		Provider:            req.Provider,
		BaseURL:             req.BaseURL,
		ModelName:           req.ModelName,
		MaxCompletionTokens: req.MaxCompletionTokens,
		TimeoutSeconds:      req.TimeoutSeconds,
		Temperature:         req.Temperature,
		IsDefault:           req.IsDefault,
		Enabled:             req.Enabled,
	}
	if strings.TrimSpace(req.APIKey) != "" {
		encrypted, err := encryptModelAPIKey(req.APIKey)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		input.EncryptedAPIKey = &encrypted
	}
	profile, err := s.store.UpdateModelProfile(id, input)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if err := s.syncDefaultModelFromStore(true); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"model": toModelProfileResponse(*profile, s.CurrentModelRuntimeStatus())})
}

func (s *Server) handleSetDefaultModel(c echo.Context) error {
	if err := requirePermission(c, store.PermissionManageModels); err != nil {
		return err
	}
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	profile, err := s.store.SetDefaultModelProfile(id)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if err := s.applyModelProfile(profile); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"model": toModelProfileResponse(*profile, s.CurrentModelRuntimeStatus())})
}

func (s *Server) handleDeleteModel(c echo.Context) error {
	if err := requirePermission(c, store.PermissionManageModels); err != nil {
		return err
	}
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if err := s.store.DeleteModelProfile(id); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if err := s.syncDefaultModelFromStore(true); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleTestModel(c echo.Context) error {
	if err := requirePermission(c, store.PermissionManageModels); err != nil {
		return err
	}
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	profile, err := s.store.GetModelProfile(id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "模型配置不存在"})
	}
	apiKey, err := decryptModelAPIKey(profile.EncryptedAPIKey)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	timeout := defaultInt(profile.TimeoutSeconds, 30)
	if timeout > 30 {
		timeout = 30
	}
	client := ai.NewClient(profile.BaseURL, apiKey, profile.ModelName, 16, timeout, 0)
	ctx, cancel := context.WithTimeout(c.Request().Context(), time.Duration(timeout)*time.Second)
	defer cancel()
	started := time.Now()
	reply, err := client.Chat(ctx, "你是模型连通性检测程序。", "请只回复 OK")
	latency := time.Since(started).Milliseconds()
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]any{
			"ok":         false,
			"latency_ms": latency,
			"error":      err.Error(),
		})
	}
	return c.JSON(http.StatusOK, map[string]any{
		"ok":         true,
		"latency_ms": latency,
		"message":    truncateModelTestReply(reply),
	})
}

func (s *Server) ensureModelLimitAvailable() error {
	const maxVisibleModels = 10
	if s.store == nil {
		return nil
	}
	profiles, err := s.store.ListModelProfiles()
	if err != nil {
		return err
	}
	if len(profiles) >= maxVisibleModels {
		return fmt.Errorf("最多只能配置 10 个模型")
	}
	return nil
}

func validateCreateModelRequest(req modelProfileRequest) error {
	if strings.TrimSpace(req.Name) == "" {
		return fmt.Errorf("模型名称不能为空")
	}
	if strings.TrimSpace(req.BaseURL) == "" {
		return fmt.Errorf("Base URL 不能为空")
	}
	if strings.TrimSpace(req.ModelName) == "" {
		return fmt.Errorf("模型标识不能为空")
	}
	if strings.TrimSpace(req.APIKey) == "" {
		return fmt.Errorf("API key 不能为空")
	}
	return nil
}

func encryptModelAPIKey(apiKey string) (string, error) {
	key := os.Getenv("AIOPS_SECRET_KEY")
	if len([]byte(key)) != 32 {
		return "", fmt.Errorf("AIOPS_SECRET_KEY 必须配置为 32 字节后才能保存模型 API key")
	}
	return security.EncryptString(apiKey, key)
}

func decryptModelAPIKey(encrypted string) (string, error) {
	key := os.Getenv("AIOPS_SECRET_KEY")
	if len([]byte(key)) != 32 {
		return "", fmt.Errorf("AIOPS_SECRET_KEY 必须配置为 32 字节后才能启用模型配置")
	}
	return security.DecryptString(encrypted, key)
}

func toModelProfileResponse(profile store.ModelProfile, current ModelRuntimeStatus) modelProfileResponse {
	return modelProfileResponse{
		ID:                  profile.ID,
		Source:              "database",
		ReadOnly:            false,
		Name:                profile.Name,
		Provider:            profile.Provider,
		BaseURL:             profile.BaseURL,
		ModelName:           profile.ModelName,
		MaxCompletionTokens: profile.MaxCompletionTokens,
		TimeoutSeconds:      profile.TimeoutSeconds,
		Temperature:         profile.Temperature,
		IsDefault:           current.Source == "database" && current.ProfileID == profile.ID,
		Enabled:             profile.Enabled,
		HasAPIKey:           profile.EncryptedAPIKey != "",
		CreatedAt:           profile.CreatedAt,
		UpdatedAt:           profile.UpdatedAt,
	}
}

func parseUintParam(c echo.Context, name string) (uint, error) {
	raw := c.Param(name)
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		return 0, fmt.Errorf("无效的 ID")
	}
	return uint(id), nil
}

func intValue(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

func defaultInt(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

func floatValue(value *float32, fallback float32) float32 {
	if value == nil {
		return fallback
	}
	return *value
}

func boolValue(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func truncateModelTestReply(reply string) string {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return "模型已响应"
	}
	if len(reply) > 120 {
		return reply[:120] + "..."
	}
	return reply
}
