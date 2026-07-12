package security

import (
	"strings"
	"testing"
)

func TestEncryptDecryptStringRoundTrip(t *testing.T) {
	key := strings.Repeat("test", 8)
	plain := "apiVersion: v1\nkind: Config\nusers:\n- name: test"

	cipherText, err := EncryptString(plain, key)
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	if cipherText == "" || cipherText == plain {
		t.Fatalf("密文不应为空或等于明文: %q", cipherText)
	}

	got, err := DecryptString(cipherText, key)
	if err != nil {
		t.Fatalf("解密失败: %v", err)
	}
	if got != plain {
		t.Fatalf("解密结果不一致: got %q want %q", got, plain)
	}
}

func TestEncryptStringRejectsInvalidKey(t *testing.T) {
	if _, err := EncryptString("secret", "short"); err == nil {
		t.Fatal("短密钥应被拒绝")
	}
}

func TestDecryptStringRejectsWrongKey(t *testing.T) {
	cipherText, err := EncryptString("secret", strings.Repeat("test", 8))
	if err != nil {
		t.Fatalf("准备密文失败: %v", err)
	}
	if _, err := DecryptString(cipherText, strings.Repeat("fail", 8)); err == nil {
		t.Fatal("错误密钥应解密失败")
	}
}
