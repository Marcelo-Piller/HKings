package models

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/vendas/loja/database"
	"golang.org/x/crypto/bcrypt"
)

type Customer struct {
	ID           int
	Name         string
	Email        string
	PasswordHash string
	Phone        string
	Address      string
	AvatarURL    string
	GoogleSub    string // conta Google vinculada; vazio se entra só com senha
	CreatedAt    string
}

var ErrEmailInUse = errors.New("e-mail já cadastrado")
var ErrInvalidCredentials = errors.New("e-mail ou senha inválidos")

// ErrUseGoogleLogin: a conta existe, mas não tem senha — entra pelo Google.
var ErrUseGoogleLogin = errors.New("esta conta entra com o Google")

// CreateCustomer cadastra um novo cliente com a senha já hasheada.
func CreateCustomer(name, email, password string) (int64, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return 0, err
	}
	res, err := database.DB.Exec(
		`INSERT INTO customers (name, email, password_hash) VALUES (?, ?, ?)`,
		name, email, string(hash),
	)
	if err != nil {
		if isUniqueConstraintErr(err) {
			return 0, ErrEmailInUse
		}
		return 0, err
	}
	return res.LastInsertId()
}

// AuthenticateCustomer verifica e-mail e senha, retornando o cliente se corretos.
func AuthenticateCustomer(email, password string) (*Customer, error) {
	c, err := GetCustomerByEmail(email)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	if c.PasswordHash == "" {
		return nil, ErrUseGoogleLogin
	}
	if bcrypt.CompareHashAndPassword([]byte(c.PasswordHash), []byte(password)) != nil {
		return nil, ErrInvalidCredentials
	}
	return c, nil
}

func GetCustomerByEmail(email string) (*Customer, error) {
	row := database.DB.QueryRow(
		`SELECT id, name, email, password_hash, phone, address, avatar_url, google_sub, created_at FROM customers WHERE email=?`, email,
	)
	var c Customer
	err := row.Scan(&c.ID, &c.Name, &c.Email, &c.PasswordHash, &c.Phone, &c.Address, &c.AvatarURL, &c.GoogleSub, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func GetCustomerByID(id int) (*Customer, error) {
	row := database.DB.QueryRow(
		`SELECT id, name, email, password_hash, phone, address, avatar_url, google_sub, created_at FROM customers WHERE id=?`, id,
	)
	var c Customer
	err := row.Scan(&c.ID, &c.Name, &c.Email, &c.PasswordHash, &c.Phone, &c.Address, &c.AvatarURL, &c.GoogleSub, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// UpdateCustomerProfile atualiza os dados de contato do cliente (não a senha).
func UpdateCustomerProfile(id int, name, phone, address string) error {
	_, err := database.DB.Exec(
		`UPDATE customers SET name=?, phone=?, address=? WHERE id=?`,
		name, phone, address, id,
	)
	return err
}

// SetCustomerAvatar troca a foto de perfil e devolve a anterior, para que o
// arquivo antigo possa ser apagado do disco.
func SetCustomerAvatar(id int, avatarURL string) (old string, err error) {
	database.DB.QueryRow(`SELECT avatar_url FROM customers WHERE id=?`, id).Scan(&old)
	_, err = database.DB.Exec(`UPDATE customers SET avatar_url=? WHERE id=?`, avatarURL, id)
	return old, err
}

func isUniqueConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint")
}
