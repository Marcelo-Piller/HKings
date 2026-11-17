package models

import "github.com/vendas/loja/database"

// Tipo de vendedor, informado no cadastro. É só dado de perfil: não muda
// regra nenhuma (a venda de e-books tem a sua própria aprovação).
var SellerProfiles = []struct {
	Key   string
	Label string
	Hint  string
}{
	{"editora", "Editora", "Publica e vende livros de seu catálogo"},
	{"sebo", "Sebo", "Compra e revende livros usados"},
	{"livraria", "Livraria / livreiro", "Loja ou livreiro de livros novos"},
	{"autor", "Autor(a) independente", "Vende as próprias obras"},
	{"leitor", "Leitor(a) vendendo o próprio acervo", "Pessoa que desapega dos livros que já leu"},
}

// ValidSellerProfile diz se o tipo é um dos aceitos.
func ValidSellerProfile(key string) bool {
	return SellerProfileLabel(key) != ""
}

// SellerProfileLabel devolve o nome do tipo, ou vazio se não informado.
func SellerProfileLabel(key string) string {
	for _, p := range SellerProfiles {
		if p.Key == key {
			return p.Label
		}
	}
	return ""
}

// SetSellerProfile grava o tipo do vendedor.
func SetSellerProfile(sellerID int, key string) error {
	if !ValidSellerProfile(key) {
		return ErrInvalidSeller
	}
	_, err := database.DB.Exec(`UPDATE sellers SET seller_profile=? WHERE id=?`, key, sellerID)
	return err
}
