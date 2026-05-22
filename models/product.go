package models

import (
	"fmt"
	"github.com/vendas/loja/database"
)

type Product struct {
	ID          int
	Name        string
	Description string
	Price       float64
	Stock       int
	ImageURL    string
	Category    string
	Active      bool
}

func (p Product) FormattedPrice() string {
	return fmt.Sprintf("R$ %.2f", p.Price)
}

func ListProducts(category string) ([]Product, error) {
	query := `SELECT id, name, description, price, stock, image_url, category FROM products WHERE active=1`
	args := []any{}
	if category != "" && category != "Todos" {
		query += ` AND category=?`
		args = append(args, category)
	}
	query += ` ORDER BY id`
	rows, err := database.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var products []Product
	for rows.Next() {
		var p Product
		rows.Scan(&p.ID, &p.Name, &p.Description, &p.Price, &p.Stock, &p.ImageURL, &p.Category)
		p.Active = true
		products = append(products, p)
	}
	return products, nil
}

func GetProduct(id int) (*Product, error) {
	row := database.DB.QueryRow(`SELECT id, name, description, price, stock, image_url, category, active FROM products WHERE id=?`, id)
	var p Product
	var active int
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.Price, &p.Stock, &p.ImageURL, &p.Category, &active)
	if err != nil {
		return nil, err
	}
	p.Active = active == 1
	return &p, nil
}

func ListCategories() ([]string, error) {
	rows, err := database.DB.Query(`SELECT DISTINCT category FROM products WHERE active=1 ORDER BY category`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cats := []string{"Todos"}
	for rows.Next() {
		var c string
		rows.Scan(&c)
		cats = append(cats, c)
	}
	return cats, nil
}

func CreateProduct(p Product) error {
	_, err := database.DB.Exec(`INSERT INTO products (name, description, price, stock, image_url, category) VALUES (?,?,?,?,?,?)`,
		p.Name, p.Description, p.Price, p.Stock, p.ImageURL, p.Category)
	return err
}

func UpdateProduct(p Product) error {
	_, err := database.DB.Exec(`UPDATE products SET name=?, description=?, price=?, stock=?, image_url=?, category=?, active=? WHERE id=?`,
		p.Name, p.Description, p.Price, p.Stock, p.ImageURL, p.Category, boolToInt(p.Active), p.ID)
	return err
}

func DeleteProduct(id int) error {
	_, err := database.DB.Exec(`UPDATE products SET active=0 WHERE id=?`, id)
	return err
}

func ListAllProducts() ([]Product, error) {
	rows, err := database.DB.Query(`SELECT id, name, description, price, stock, image_url, category, active FROM products ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var products []Product
	for rows.Next() {
		var p Product
		var active int
		rows.Scan(&p.ID, &p.Name, &p.Description, &p.Price, &p.Stock, &p.ImageURL, &p.Category, &active)
		p.Active = active == 1
		products = append(products, p)
	}
	return products, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
