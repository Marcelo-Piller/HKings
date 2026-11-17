package models

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vendas/loja/database"
)

// Categorias são curadas pelo administrador. Antes o vendedor digitava o texto
// livremente, o que produzia "Roupas", "roupas" e "Roupa" como categorias
// distintas e quebrava os filtros da loja.

type Category struct {
	ID   int
	Name string
}

var ErrCategoryInUse = errors.New("categoria em uso por produtos")
var ErrInvalidCategory = errors.New("categoria inválida")

const maxCategoryName = 40

// ListCategoryNames devolve apenas os nomes, no formato que os formulários
// usam para montar o campo de seleção.
func ListCategoryNames() ([]string, error) {
	categories, err := ListCategoriesFull()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(categories))
	for _, c := range categories {
		names = append(names, c.Name)
	}
	return names, nil
}

func ListCategoriesFull() ([]Category, error) {
	rows, err := database.DB.Query(`SELECT id, name FROM categories ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var categories []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name); err != nil {
			return nil, err
		}
		categories = append(categories, c)
	}
	return categories, rows.Err()
}

// CategoryExists valida a categoria enviada por um formulário. Toda gravação de
// produto passa por aqui, de modo que nem o vendedor nem um POST forjado
// conseguem inventar uma categoria nova.
func CategoryExists(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	var count int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM categories WHERE name=?`, name).Scan(&count); err != nil {
		return false
	}
	return count > 0
}

func CreateCategory(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > maxCategoryName {
		return fmt.Errorf("%w: informe um nome de até %d caracteres", ErrInvalidCategory, maxCategoryName)
	}
	_, err := database.DB.Exec(`INSERT INTO categories (name) VALUES (?)`, name)
	if err != nil && isUniqueConstraintErr(err) {
		return fmt.Errorf("%w: já existe", ErrInvalidCategory)
	}
	return err
}

// DeleteCategory recusa a remoção enquanto houver produtos na categoria, para
// não deixar produtos apontando para uma categoria inexistente.
func DeleteCategory(id int) error {
	var name string
	if err := database.DB.QueryRow(`SELECT name FROM categories WHERE id=?`, id).Scan(&name); err != nil {
		return err
	}
	var inUse int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM products WHERE category=? AND archived=0`, name).Scan(&inUse); err != nil {
		return err
	}
	if inUse > 0 {
		return fmt.Errorf("%w: %d produto(s) em %q", ErrCategoryInUse, inUse, name)
	}
	_, err := database.DB.Exec(`DELETE FROM categories WHERE id=?`, id)
	return err
}

// DefaultCategory é o destino de produtos cujo formulário chegou sem categoria
// válida, garantindo que o campo nunca fique vazio.
func DefaultCategory() string {
	names, err := ListCategoryNames()
	if err != nil || len(names) == 0 {
		return "Geral"
	}
	for _, n := range names {
		if n == "Geral" {
			return n
		}
	}
	return names[0]
}
