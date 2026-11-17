package models

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/vendas/loja/database"
)

var (
	// ErrGoogleEmailUnverified: o Google não confirmou o e-mail da conta, então
	// ele não serve para identificar ninguém.
	ErrGoogleEmailUnverified = errors.New("e-mail da conta Google não verificado")
	// ErrGoogleAccountConflict: o e-mail já pertence a um cliente vinculado a
	// outra conta Google.
	ErrGoogleAccountConflict = errors.New("e-mail já vinculado a outra conta Google")
)

// GoogleLoginResult diz o que aconteceu no login com Google.
type GoogleLoginResult struct {
	Customer *Customer
	Created  bool // conta nova criada agora
	Linked   bool // conta com senha que passou a entrar pelo Google
}

// LoginWithGoogle encontra ou cria o cliente da conta Google.
//
// Quando o e-mail já tem cadastro com senha, a conta é vinculada ao Google e a
// senha antiga é desativada. O cadastro por senha não confirma o e-mail, então
// qualquer pessoa poderia ter criado antes uma conta com o e-mail de outra e
// ficado com a senha. Quem entra pelo Google provou ser dono do e-mail; manter
// a senha antiga deixaria a porta aberta para quem a criou.
func LoginWithGoogle(sub, email, name string, emailVerified bool) (*GoogleLoginResult, error) {
	sub = strings.TrimSpace(sub)
	email = strings.ToLower(strings.TrimSpace(email))
	if sub == "" || email == "" {
		return nil, ErrInvalidCredentials
	}

	if c, err := customerWhere(`google_sub=?`, sub); err == nil {
		return &GoogleLoginResult{Customer: c}, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	if !emailVerified {
		return nil, ErrGoogleEmailUnverified
	}

	existing, err := customerWhere(`LOWER(email)=?`, email)
	switch {
	case err == nil:
		if existing.GoogleSub != "" {
			return nil, ErrGoogleAccountConflict
		}
		res, err := database.DB.Exec(
			`UPDATE customers SET google_sub=?, password_hash='' WHERE id=? AND google_sub=''`, sub, existing.ID)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, ErrGoogleAccountConflict
		}
		c, err := GetCustomerByID(existing.ID)
		if err != nil {
			return nil, err
		}
		return &GoogleLoginResult{Customer: c, Linked: true}, nil
	case !errors.Is(err, sql.ErrNoRows):
		return nil, err
	}

	name = strings.TrimSpace(name)
	if name == "" {
		name = strings.SplitN(email, "@", 2)[0]
	}
	res, err := database.DB.Exec(
		`INSERT INTO customers (name, email, password_hash, google_sub) VALUES (?, ?, '', ?)`, name, email, sub)
	if err != nil {
		if isUniqueConstraintErr(err) {
			return nil, ErrGoogleAccountConflict
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	c, err := GetCustomerByID(int(id))
	if err != nil {
		return nil, err
	}
	return &GoogleLoginResult{Customer: c, Created: true}, nil
}

func customerWhere(cond string, arg any) (*Customer, error) {
	var c Customer
	err := database.DB.QueryRow(
		`SELECT id, name, email, password_hash, phone, address, avatar_url, google_sub, created_at FROM customers WHERE `+cond+` LIMIT 1`, arg,
	).Scan(&c.ID, &c.Name, &c.Email, &c.PasswordHash, &c.Phone, &c.Address, &c.AvatarURL, &c.GoogleSub, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}
