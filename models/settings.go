package models

import "github.com/vendas/loja/database"

func GetSetting(key string) string {
	var val string
	database.DB.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&val)
	if isSecretSetting(key) {
		return getSecretSetting(key, val)
	}
	return val
}

func SetSetting(key, value string) error {
	if isSecretSetting(key) {
		return setSecretSetting(key, value)
	}
	_, err := database.DB.Exec(
		`INSERT OR REPLACE INTO settings (key, value) VALUES (?,?)`, key, value,
	)
	return err
}
