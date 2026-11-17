package models

type CartItem struct {
	Product  Product
	Quantity int
	// Edition é a edição do livro digital: e-book, audiobook ou combo.
	Edition string `json:",omitempty"`
}

// UnitPrice é o preço da edição escolhida (o pedido confere no banco).
func (ci CartItem) UnitPrice() float64 { return ci.Product.PriceFor(ci.Edition) }

func (ci CartItem) FormattedUnitPrice() string { return FormatBRL(ci.UnitPrice()) }

// EditionLabel descreve a edição na sacola.
func (ci CartItem) EditionLabel() string { return EditionLabel(ci.Edition) }

func (ci CartItem) Subtotal() float64 {
	return ci.UnitPrice() * float64(ci.Quantity)
}

func (ci CartItem) FormattedSubtotal() string {
	return FormatBRL(ci.Subtotal())
}

type Cart struct {
	Items []CartItem
}

func (c *Cart) Add(p Product, qty int, edition string) {
	// E-book: uma cópia basta, então a quantidade fica sempre em 1. Escolher
	// outra edição do mesmo livro troca a que estava na sacola.
	if p.IsDigital() {
		qty = 1
	} else {
		edition = ""
	}
	for i, item := range c.Items {
		if item.Product.ID == p.ID {
			if p.IsDigital() {
				c.Items[i].Product, c.Items[i].Edition = p, edition
			} else {
				c.Items[i].Quantity += qty
			}
			return
		}
	}
	c.Items = append(c.Items, CartItem{Product: p, Quantity: qty, Edition: edition})
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
			if item.Product.IsDigital() {
				qty = 1
			}
			c.Items[i].Quantity = qty
			return
		}
	}
}

// HasDigital diz se há e-book na sacola (o checkout exige conta).
func (c *Cart) HasDigital() bool {
	for _, item := range c.Items {
		if item.Product.IsDigital() {
			return true
		}
	}
	return false
}

// HasPhysical diz se há livro para enviar (o checkout pede endereço).
func (c *Cart) HasPhysical() bool {
	for _, item := range c.Items {
		if !item.Product.IsDigital() {
			return true
		}
	}
	return false
}

func (c *Cart) Total() float64 {
	var total float64
	for _, item := range c.Items {
		total += item.Subtotal()
	}
	return total
}

// TotalCents é o total dos livros em centavos (sem frete).
func (c *Cart) TotalCents() int64 {
	var cents int64
	for _, item := range c.Items {
		cents += int64(item.UnitPrice()*100+0.5) * int64(item.Quantity)
	}
	return cents
}

func (c *Cart) FormattedTotal() string {
	return FormatBRL(c.Total())
}

func (c *Cart) Count() int {
	n := 0
	for _, item := range c.Items {
		n += item.Quantity
	}
	return n
}

// Seller devolve o vendedor dos livros na sacola (0 = o próprio site) e se a
// sacola tem algum livro. Uma compra é paga a um único recebedor, então a
// sacola só aceita livros de um vendedor por vez.
func (c *Cart) Seller() (sellerID int, name string, ok bool) {
	if len(c.Items) == 0 {
		return 0, "", false
	}
	p := c.Items[0].Product
	return p.SellerID, p.SellerLabel(), true
}

func (c *Cart) Clear() {
	c.Items = nil
}
