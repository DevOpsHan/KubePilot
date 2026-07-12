// Package security 提供本地敏感配置的加解密能力。
// 当前用于 kubeconfig、模型密钥等需要落库但不能明文保存的数据。
package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

// EncryptString 使用 AES-GCM 加密字符串,返回 base64(nonce+ciphertext)。
func EncryptString(plainText, key string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("生成随机 nonce 失败: %w", err)
	}
	cipherText := gcm.Seal(nil, nonce, []byte(plainText), nil)
	out := append(nonce, cipherText...)
	return base64.StdEncoding.EncodeToString(out), nil
}

// DecryptString 解密 EncryptString 产生的密文。
func DecryptString(encodedCipherText, key string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(encodedCipherText)
	if err != nil {
		return "", fmt.Errorf("密文 base64 解码失败: %w", err)
	}
	if len(raw) <= gcm.NonceSize() {
		return "", fmt.Errorf("密文长度无效")
	}
	nonce := raw[:gcm.NonceSize()]
	cipherText := raw[gcm.NonceSize():]
	plainText, err := gcm.Open(nil, nonce, cipherText, nil)
	if err != nil {
		return "", fmt.Errorf("解密失败: %w", err)
	}
	return string(plainText), nil
}

func newGCM(key string) (cipher.AEAD, error) {
	keyBytes := []byte(key)
	if len(keyBytes) != 32 {
		return nil, fmt.Errorf("加密密钥长度必须为 32 字节")
	}
	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("创建 AES cipher 失败: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("创建 GCM 失败: %w", err)
	}
	return gcm, nil
}
