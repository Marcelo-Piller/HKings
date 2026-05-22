package models

import "fmt"

type CartItem struct {
	Product  Product
	Quantity int
}

func (ci CartItem) Subtotal() float64 {
	return ci.Product.Price * float64(ci.Quantity)
}

func (ci CartItem) FormattedSubtotal() string {
	return fmt.Sprintf("R$ %.2f", ci.Subtotal())
}

type Cart struct {
	Items []CartItem
}

func (c *Cart) Add(p Product, qty int) {
	for i, item := range c.Items {
		if item.Product.ID == p.ID {
			c.Items[i].Quantity += qty
			return
		}
	}
	c.Items = append(c.Items, CartItem{Product: p, Quantity: qty})
}

func (c *Cart) Remove(productID int) {
	items := c.Items[:0]
	for _, item := range c.Items {
		if item.Product.ID != productID {
			items = append(items, item)
		}
	}
	c.Items = items
}

func (c *Cart) UpdateQty(productID, qty int) {
	if qty <= 0 {
		c.Remove(productID)
		return
	}
	for i, item := range c.Items {
		if item.Product.ID == productID {
			c.Items[i].Quantity = qty
			return
		}
	}
}

func (c *Cart) Total() float64 {
	var total float64
	for _, item := range c.Items {
		total += item.Subtotal()
	}
	return total
}

func (c *Cart) FormattedTotal() string {
	return fmt.Sprintf("R$ %.2f", c.Total())
}

func (c *Cart) Count() int {
	n := 0
	for _, item := range c.Items {
		n += item.Quantity
	}
	return n
}

func (c *Cart) Clear() {
	c.Items = nil
}
