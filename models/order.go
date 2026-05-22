package models

import (
	"fmt"
	"github.com/vendas/loja/database"
)

type Order struct {
	ID            int
	CustomerName  string
	CustomerEmail string
	CustomerPhone string
	Address       string
	Total         float64
	Status        string
	CreatedAt     string
	Items         []OrderItem
}

type OrderItem struct {
	ID          int
	OrderID     int
	ProductID   int
	ProductName string
	Quantity    int
	Price       float64
}

func (o Order) FormattedTotal() string {
	return fmt.Sprintf("R$ %.2f", o.Total)
}

func CreateOrder(o *Order) (int64, error) {
	res, err := database.DB.Exec(
		`INSERT INTO orders (customer_name, customer_email, customer_phone, address, total, status) VALUES (?,?,?,?,?,?)`,
		o.CustomerName, o.CustomerEmail, o.CustomerPhone, o.Address, o.Total, "pendente",
	)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	for _, item := range o.Items {
		_, err = database.DB.Exec(
			`INSERT INTO order_items (order_id, product_id, product_name, quantity, price) VALUES (?,?,?,?,?)`,
			id, item.ProductID, item.ProductName, item.Quantity, item.Price,
		)
		if err != nil {
			return id, err
		}
		database.DB.Exec(`UPDATE products SET stock = stock - ? WHERE id=?`, item.Quantity, item.ProductID)
	}
	return id, nil
}

func GetOrder(id int) (*Order, error) {
	row := database.DB.QueryRow(`SELECT id, customer_name, customer_email, customer_phone, address, total, status, created_at FROM orders WHERE id=?`, id)
	var o Order
	err := row.Scan(&o.ID, &o.CustomerName, &o.CustomerEmail, &o.CustomerPhone, &o.Address, &o.Total, &o.Status, &o.CreatedAt)
	if err != nil {
		return nil, err
	}
	rows, err := database.DB.Query(`SELECT id, order_id, product_id, product_name, quantity, price FROM order_items WHERE order_id=?`, id)
	if err != nil {
		return &o, nil
	}
	defer rows.Close()
	for rows.Next() {
		var item OrderItem
		rows.Scan(&item.ID, &item.OrderID, &item.ProductID, &item.ProductName, &item.Quantity, &item.Price)
		o.Items = append(o.Items, item)
	}
	return &o, nil
}

func ListOrders() ([]Order, error) {
	rows, err := database.DB.Query(`SELECT id, customer_name, customer_email, total, status, created_at FROM orders ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orders []Order
	for rows.Next() {
		var o Order
		rows.Scan(&o.ID, &o.CustomerName, &o.CustomerEmail, &o.Total, &o.Status, &o.CreatedAt)
		orders = append(orders, o)
	}
	return orders, nil
}

func UpdateOrderStatus(id int, status string) error {
	_, err := database.DB.Exec(`UPDATE orders SET status=? WHERE id=?`, status, id)
	return err
}
