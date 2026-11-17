package models

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"github.com/vendas/loja/database"
	"golang.org/x/crypto/bcrypt"
)

type Seller struct {
	ID        int
	Name      string
	Username  string
	Email     string
	Document  string // CPF (11 dígitos) ou CNPJ (14 dígitos), só números
	Active    bool
	MPUserID  string // conta Mercado Pago conectada; vazio se não conectou
	AvatarURL string
	Profile   string // editora, sebo, livraria, autor ou leitor (seller_profile.go)
}

// ProfileLabel devolve o tipo de vendedor por extenso.
func (s Seller) ProfileLabel() string { return SellerProfileLabel(s.Profile) }

// MPConnected indica se o vendedor já conectou a conta do Mercado Pago e pode
// receber pelas vendas.
func (s Seller) MPConnected() bool {
	return s.MPUserID != ""
}

// Kind devolve "Loja" (CNPJ) ou "Pessoa física" (CPF).
func (s Seller) Kind() string {
	return DocumentKind(s.Document)
}

// DocumentLabel devolve o CPF/CNPJ já formatado para exibição.
func (s Seller) DocumentLabel() string {
	return FormatDocument(s.Document)
}

var (
	// ErrUsernameInUse permite ao formulário de cadastro mostrar uma mensagem
	// específica em vez do erro cru do banco quando o usuário já existe.
	// ErrEmailInUse (definido em customer.go) é reaproveitado aqui: significa
	// a mesma coisa, "e-mail já cadastrado", agora aplicado à tabela sellers.
	ErrUsernameInUse = errors.New("usuário já está em uso")

	// ErrInvalidSeller cobre nome, usuário, e-mail, documento ou senha fora
	// do exigido.
	ErrInvalidSeller = errors.New("dados de vendedor inválidos")
)

func AuthenticateSeller(username, password string) (*Seller, error) {
	var seller Seller
	var active int
	var hash string
	err := database.DB.QueryRow(
		`SELECT id, name, username, email, document, password, active, avatar_url FROM sellers WHERE username=? AND active=1`,
		username,
	).Scan(&seller.ID, &seller.Name, &seller.Username, &seller.Email, &seller.Document, &hash, &active, &seller.AvatarURL)
	if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return nil, fmt.Errorf("credenciais inválidas")
	}
	seller.Active = active == 1
	return &seller, nil
}

func ListSellers() ([]Seller, error) {
	rows, err := database.DB.Query(`SELECT id, name, username, email, document, active, CASE WHEN mp_access_token<>'' THEN mp_user_id ELSE '' END, avatar_url, seller_profile FROM sellers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sellers []Seller
	for rows.Next() {
		var seller Seller
		var active int
		if err := rows.Scan(&seller.ID, &seller.Name, &seller.Username, &seller.Email, &seller.Document, &active, &seller.MPUserID, &seller.AvatarURL, &seller.Profile); err != nil {
			return nil, err
		}
		seller.Active = active == 1
		sellers = append(sellers, seller)
	}
	return sellers, rows.Err()
}

// CreateSeller cadastra um vendedor, seja pelo admin, seja pelo próprio
// vendedor no autocadastro. A validação é a mesma nos dois caminhos: e-mail e
// CPF/CNPJ são exigidos e conferidos, e cada um só pode pertencer a uma conta
// — isso é o que impede alguém de recriar uma conta nova a cada bloqueio.
func CreateSeller(name, username, email, document, password string) error {
	name = strings.TrimSpace(name)
	username = strings.TrimSpace(username)
	if name == "" || username == "" {
		return fmt.Errorf("%w: informe nome e usuário", ErrInvalidSeller)
	}
	if len(password) < 12 {
		return fmt.Errorf("%w: a senha precisa de pelo menos 12 caracteres", ErrInvalidSeller)
	}

	normalizedEmail, err := normalizeEmail(email)
	if err != nil {
		return fmt.Errorf("%w: informe um e-mail válido", ErrInvalidSeller)
	}
	normalizedDocument, err := ValidateDocument(document)
	if err != nil {
		return fmt.Errorf("%w: informe um CPF ou CNPJ válido", ErrInvalidSeller)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), passwordHashCost)
	if err != nil {
		return err
	}

	_, err = database.DB.Exec(
		`INSERT INTO sellers (name, username, email, document, password) VALUES (?, ?, ?, ?, ?)`,
		name, username, normalizedEmail, normalizedDocument, string(hash),
	)
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "sellers.username"):
			return ErrUsernameInUse
		case strings.Contains(err.Error(), "sellers.email"):
			return ErrEmailInUse
		case strings.Contains(err.Error(), "sellers.document"):
			return ErrDocumentInUse
		default:
			return err
		}
	}
	return nil
}

func GetSeller(id int) (*Seller, error) {
	var seller Seller
	var active int
	err := database.DB.QueryRow(`SELECT id, name, username, email, document, active, CASE WHEN mp_access_token<>'' THEN mp_user_id ELSE '' END, avatar_url, seller_profile FROM sellers WHERE id=?`, id).
		Scan(&seller.ID, &seller.Name, &seller.Username, &seller.Email, &seller.Document, &active, &seller.MPUserID, &seller.AvatarURL, &seller.Profile)
	if err != nil {
		return nil, err
	}
	seller.Active = active == 1
	return &seller, nil
}

// SetSellerAvatar troca a foto de perfil e devolve a anterior.
func SetSellerAvatar(id int, avatarURL string) (old string, err error) {
	database.DB.QueryRow(`SELECT avatar_url FROM sellers WHERE id=?`, id).Scan(&old)
	_, err = database.DB.Exec(`UPDATE sellers SET avatar_url=? WHERE id=?`, avatarURL, id)
	return old, err
}

// normalizeEmail confere o formato e devolve o endereço em minúsculas, para
// que "Nome@Ex.com" e "nome@ex.com" sejam tratados como o mesmo e-mail.
func normalizeEmail(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("e-mail é obrigatório")
	}
	addr, err := mail.ParseAddress(raw)
	if err != nil {
		return "", fmt.Errorf("e-mail inválido")
	}
	return strings.ToLower(addr.Address), nil
}
