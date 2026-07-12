package server

import (
	"net/http"
	"os"

	"github.com/labstack/echo/v4"
)

type securityStatusResponse struct {
	SecretKeyReady bool     `json:"secret_key_ready"`
	RequiredFor    []string `json:"required_for"`
	Message        string   `json:"message,omitempty"`
}

func (s *Server) handleSecurityStatus(c echo.Context) error {
	ready := secretKeyReady()
	resp := securityStatusResponse{
		SecretKeyReady: ready,
		RequiredFor: []string{
			"保存模型 API key",
			"保存 kubeconfig 内容",
			"重启后恢复已加密的模型和 kubeconfig",
		},
	}
	if !ready {
		resp.Message = "服务端未配置 32 字节 AIOPS_SECRET_KEY,无法保存或恢复加密凭据"
	}
	return c.JSON(http.StatusOK, resp)
}

func secretKeyReady() bool {
	return len([]byte(os.Getenv("AIOPS_SECRET_KEY"))) == 32
}
