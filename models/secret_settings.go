package models

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/vendas/loja/database"
)

const encryptedSettingPrefix = "enc:v1:"

// Segredos nunca ficam em texto puro no banco. A variável de ambiente, quando
// presente, tem precedência — é o caminho recomendado em produção, já que o
// segredo não chega a ser gravado.
var secretSettingEnvs = map[string]string{
	"mp_access_token":   "MERCADOPAGO_ACCESS_TOKEN",
	"mp_webhook_secret": "MERCADOPAGO_WEBHOOK_SECRET",
	// Credenciais do aplicativo, usadas no OAuth para conectar os vendedores
	// (split de pagamentos).
	"mp_client_id":     "MERCADOPAGO_CLIENT_ID",
	"mp_client_secret": "MERCADOPAGO_CLIENT_SECRET",
	"whatsapp_api_key": "WHATSAPP_API_KEY",
	// Senha de app do Gmail, para enviar comprovantes aos compradores.
	"smtp_password": "SMTP_PASSWORD",
	// Chave secreta do cliente OAuth do Google (login de compradores).
	"google_client_secret": "GOOGLE_CLIENT_SECRET",
	"google_books_api_key": "GOOGLE_BOOKS_API_KEY",
	// Aplicativo da loja no Melhor Envio (frete e etiquetas).
	"melhorenvio_client_secret": "MELHORENVIO_CLIENT_SECRET",
}

func isSecretSetting(key string) bool {
	_, ok := secretSettingEnvs[key]
	return ok
}

func getSecretSetting(key, stored string) string {
	if env := os.Getenv(secretSettingEnvs[key]); env != "" {
		return env
	}
	plain, err := decryptSetting(stored)
	if err != nil {
		return ""
	}
	return plain
}

func setSecretSetting(key, value string) error {
	encrypted, err := encryptSetting(value)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	_, err = database.DB.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES (?,?)`, key, encrypted)
	return err
}

func settingsEncryptionKey() ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv("SETTINGS_ENCRYPTION_KEY"))
	if raw == "" {
		return nil, fmt.Errorf("SETTINGS_ENCRYPTION_KEY não configurada")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("SETTINGS_ENCRYPTION_KEY deve ser Base64 de 32 bytes")
	}
	return key, nil
}

func encryptSetting(value string) (string, error) {
	key, err := settingsEncryptionKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(value), nil)
	return encryptedSettingPrefix + base64.RawStdEncoding.EncodeToString(ciphertext), nil
}

func decryptSetting(value string) (string, error) {
	if !strings.HasPrefix(value, encryptedSettingPrefix) {
		return "", fmt.Errorf("valor legado não criptografado")
	}
	key, err := settingsEncryptionKey()
	if err != nil {
		return "", err
	}
	data, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(value, encryptedSettingPrefix))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("valor criptografado inválido")
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// MigrateSensitiveSettings protege valores existentes na primeira execução
// configurada com SETTINGS_ENCRYPTION_KEY. Valores legados não são lidos em
// claro pelo aplicativo antes da migração.
func MigrateSensitiveSettings() error {
	for key := range secretSettingEnvs {
		var value string
		if err := database.DB.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&value); err != nil {
			continue
		}
		if strings.HasPrefix(value, encryptedSettingPrefix) {
			continue
		}
		if err := setSecretSetting(key, value); err != nil {
			return err
		}
	}
	return nil
}
