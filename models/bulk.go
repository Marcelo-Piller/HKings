package models

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/vendas/loja/database"
)

// Edição em massa do catálogo: preço, estoque e situação (à venda ou
// pausado) de vários livros de uma vez, sem planilha.

// BulkChange é a alteração de um livro.
type BulkChange struct {
	ID     int
	Price  float64
	Stock  int
	Active bool
}

// BulkUpdateProducts confere todas as alterações e grava todas juntas, ou
// nenhuma. Só mexe nos livros do dono (ownerID 0 = o próprio site). E-book
// não tem estoque. Devolve quantos livros mudaram.
func BulkUpdateProducts(ownerID int, changes []BulkChange) (int, error) {
	mine, err := ListProductsBySeller(ownerID)
	if err != nil {
		return 0, err
	}
	byID := map[int]Product{}
	for _, p := range mine {
		byID[p.ID] = p
	}
	for i := range changes {
		c := &changes[i]
		p, ok := byID[c.ID]
		if !ok {
			return 0, fmt.Errorf("%w: livro %d não é seu", ErrInvalidProduct, c.ID)
		}
		if c.Price <= 0 || c.Price > 1_000_000 || math.IsNaN(c.Price) || math.IsInf(c.Price, 0) {
			return 0, fmt.Errorf("%w: preço inválido em %q", ErrInvalidProduct, p.Name)
		}
		c.Price = math.Round(c.Price*100) / 100
		if p.IsDigital() {
			c.Stock = 0
		} else if c.Stock < 0 || c.Stock > 1_000_000 {
			return 0, fmt.Errorf("%w: estoque inválido em %q", ErrInvalidProduct, p.Name)
		}
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	changed := 0
	for _, c := range changes {
		p := byID[c.ID]
		if p.Price == c.Price && p.Stock == c.Stock && p.Active == c.Active {
			continue
		}
		if _, err := tx.Exec(`UPDATE products SET price=?, stock=?, active=? WHERE id=? AND seller_id=? AND archived=0`,
			c.Price, c.Stock, boolToInt(c.Active), c.ID, ownerID); err != nil {
			return 0, err
		}
		changed++
	}
	return changed, tx.Commit()
}

// PriceInput é o preço como o lojista digita: "39,90".
func (p Product) PriceInput() string {
	return strings.Replace(strconv.FormatFloat(p.Price, 'f', 2, 64), ".", ",", 1)
}
