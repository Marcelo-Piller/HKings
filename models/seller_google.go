package models

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/vendas/loja/database"
)

// Login de vendedor com Google.
//
// Diferente do comprador, o vendedor nunca é vinculado pelo e-mail: alguém
// poderia cadastrar uma loja com o e-mail de outra pessoa (o cadastro não
// confirma o e-mail) e já ter ligado a conta Mercado Pago dele. O vínculo só
// acontece de dentro do painel, com o vendedor logado pela senha.

// ErrSellerGoogleNotLinked: nenhum vendedor ativo com essa conta Google.
var ErrSellerGoogleNotLinked = errors.New("conta Google não vinculada a nenhum vendedor")

// SellerByGoogle devolve o vendedor ativo vinculado à conta Google.
func SellerByGoogle(sub string) (*Seller, error) {
	sub = strings.TrimSpace(sub)
	if sub == "" {
		return nil, ErrSellerGoogleNotLinked
	}
	var id int
	err := database.DB.QueryRow(`SELECT id FROM sellers WHERE google_sub=? AND active=1`, sub).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSellerGoogleNotLinked
	}
	if err != nil {
		return nil, err
	}
	return GetSeller(id)
}

// LinkSellerGoogle vincula a conta Google ao vendedor logado. Uma conta
// Google entra em um único vendedor.
func LinkSellerGoogle(sellerID int, sub string) error {
	sub = strings.TrimSpace(sub)
	if sellerID <= 0 || sub == "" {
		return ErrInvalidSeller
	}
	_, err := database.DB.Exec(`UPDATE sellers SET google_sub=? WHERE id=?`, sub, sellerID)
	if isUniqueConstraintErr(err) {
		return ErrGoogleAccountConflict
	}
	return err
}

// UnlinkSellerGoogle desfaz o vínculo; o vendedor volta a entrar só com senha.
func UnlinkSellerGoogle(sellerID int) error {
	_, err := database.DB.Exec(`UPDATE sellers SET google_sub='' WHERE id=?`, sellerID)
	return err
}

// SellerGoogleLinked diz se o vendedor já entra com Google.
func SellerGoogleLinked(sellerID int) bool {
	var sub string
	database.DB.QueryRow(`SELECT google_sub FROM sellers WHERE id=?`, sellerID).Scan(&sub)
	return sub != ""
}
