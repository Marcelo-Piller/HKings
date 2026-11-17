package models

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/vendas/loja/database"
	"golang.org/x/crypto/bcrypt"
)

const passwordHashCost = 12

var ErrInvalidAdminCredentials = errors.New("usuário ou senha inválidos")

// AuthenticateAdmin confere a senha usando bcrypt. A coluna password contém
// exclusivamente hashes; seu nome é mantido por compatibilidade com bancos antigos.
func AuthenticateAdmin(username, password string) error {
	var hash string
	err := database.DB.QueryRow(`SELECT password FROM admins WHERE username=?`, username).Scan(&hash)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return ErrInvalidAdminCredentials
	}
	return nil
}

// MigrateLegacyPasswordHashes converte credenciais antigas em texto puro para
// bcrypt antes de qualquer autenticação. Contas de demonstração conhecidas são
// removidas na migração do banco, antes desta etapa.
func MigrateLegacyPasswordHashes() error {
	for _, table := range []string{"admins", "sellers"} {
		if err := migrateLegacyPasswordTable(table); err != nil {
			return err
		}
	}
	return nil
}

func migrateLegacyPasswordTable(table string) error {
	rows, err := database.DB.Query(`SELECT id, password FROM ` + table)
	if err != nil {
		return err
	}
	type legacyCredential struct {
		id       int
		password string
	}
	var credentials []legacyCredential
	for rows.Next() {
		var credential legacyCredential
		if err := rows.Scan(&credential.id, &credential.password); err != nil {
			rows.Close()
			return err
		}
		if !strings.HasPrefix(credential.password, "$2") {
			credentials = append(credentials, credential)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, credential := range credentials {
		hash, err := bcrypt.GenerateFromPassword([]byte(credential.password), passwordHashCost)
		if err != nil {
			return err
		}
		if _, err := database.DB.Exec(`UPDATE `+table+` SET password=? WHERE id=? AND password=?`, string(hash), credential.id, credential.password); err != nil {
			return err
		}
	}
	return nil
}

// EnsureInitialAdmin cria o primeiro administrador exclusivamente a partir de
// variáveis de ambiente. Não há mais credenciais padrão no código ou no banco.
func EnsureInitialAdmin() error {
	var count int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM admins`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	username := strings.TrimSpace(os.Getenv("ADMIN_USERNAME"))
	password := os.Getenv("ADMIN_PASSWORD")
	if username == "" || len(password) < 12 {
		return fmt.Errorf("configure ADMIN_USERNAME e ADMIN_PASSWORD (com pelo menos 12 caracteres) para criar o primeiro administrador")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), passwordHashCost)
	if err != nil {
		return err
	}
	_, err = database.DB.Exec(`INSERT INTO admins (username, password) VALUES (?, ?)`, username, string(hash))
	return err
}

// CreateAdmin é mantida para futuros fluxos administrativos e evita que
// credenciais em texto puro sejam inseridas acidentalmente.
func CreateAdmin(username, password string) error {
	if strings.TrimSpace(username) == "" || len(password) < 12 {
		return fmt.Errorf("usuário e senha de pelo menos 12 caracteres são obrigatórios")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), passwordHashCost)
	if err != nil {
		return err
	}
	_, err = database.DB.Exec(`INSERT INTO admins (username, password) VALUES (?, ?)`, username, string(hash))
	return err
}
