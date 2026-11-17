package models

import (
	"strings"

	"github.com/vendas/loja/database"
)

// Controle do e-mail de boas-vindas: welcome_sent_at marca quem já recebeu,
// para o envio aos já cadastrados nunca mandar duas vezes para a mesma pessoa.

// WelcomeTarget é uma conta que ainda não recebeu as boas-vindas.
type WelcomeTarget struct {
	Seller   bool
	ID       int
	Name     string
	Email    string
	Username string
}

// Contas excluídas (LGPD) ficam com e-mail @removido.invalid e nunca recebem.
const deletedEmailFilter = `email <> '' AND email NOT LIKE '%@removido.invalid'`

// PendingWelcomes lista clientes e vendedores ativos sem boas-vindas.
func PendingWelcomes() ([]WelcomeTarget, error) {
	var out []WelcomeTarget
	rows, err := database.DB.Query(`SELECT id, name, email FROM customers WHERE welcome_sent_at='' AND ` + deletedEmailFilter + ` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t WelcomeTarget
		if rows.Scan(&t.ID, &t.Name, &t.Email) == nil {
			out = append(out, t)
		}
	}
	rows.Close()
	rows, err = database.DB.Query(`SELECT id, name, email, username FROM sellers WHERE welcome_sent_at='' AND active=1 AND ` + deletedEmailFilter + ` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		t := WelcomeTarget{Seller: true}
		if rows.Scan(&t.ID, &t.Name, &t.Email, &t.Username) == nil {
			out = append(out, t)
		}
	}
	return out, rows.Err()
}

// CountPendingWelcomes conta clientes e vendedores sem boas-vindas.
func CountPendingWelcomes() (clientes, vendedores int) {
	database.DB.QueryRow(`SELECT COUNT(*) FROM customers WHERE welcome_sent_at='' AND ` + deletedEmailFilter).Scan(&clientes)
	database.DB.QueryRow(`SELECT COUNT(*) FROM sellers WHERE welcome_sent_at='' AND active=1 AND ` + deletedEmailFilter).Scan(&vendedores)
	return
}

// MarkWelcomeSent registra o envio das boas-vindas para o e-mail.
func MarkWelcomeSent(seller bool, email string) {
	table := "customers"
	if seller {
		table = "sellers"
	}
	database.DB.Exec(`UPDATE `+table+` SET welcome_sent_at=CURRENT_TIMESTAMP WHERE LOWER(email)=?`, strings.ToLower(strings.TrimSpace(email)))
}
