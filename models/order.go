package models

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/vendas/loja/database"
)

var ErrInvalidOrder = errors.New("pedido inválido")
var ErrInsufficientStock = errors.New("estoque insuficiente")

// ErrUnderpaid protege contra um pagamento aprovado por valor inferior ao do
// pedido, que não pode liberar a entrega.
var ErrUnderpaid = errors.New("valor pago menor que o total do pedido")

// ErrMixedSellers: um pagamento do Mercado Pago tem um único recebedor, então
// um pedido só pode ter livros de um mesmo vendedor (ou só do próprio site).
var ErrMixedSellers = errors.New("pedido com livros de vendedores diferentes")

// ErrSellerCannotReceive: o vendedor ainda não conectou a conta do Mercado
// Pago, então não há para onde mandar o pagamento.
var ErrSellerCannotReceive = errors.New("vendedor ainda não pode receber pagamentos")

type Order struct {
	ID              int
	CustomerID      int
	CustomerName    string
	CustomerEmail   string
	CustomerPhone   string
	Address         string
	Total           float64
	Status          string
	PixPaymentID    string
	PixQRCode       string
	PixQRCodeImg    string
	PaymentMethod   string // "pix" ou "cartao"
	MPPreferenceID  string // preferência do Checkout Pro
	MPCheckoutURL   string // URL de redirecionamento do Checkout Pro
	PaidAmountCents int64  // valor efetivamente liquidado, em centavos
	// SellerID é quem recebe (0 = o próprio site). MarketplaceFeeCents é a
	// comissão do site combinada na compra; zero em venda do próprio site.
	SellerID            int
	MarketplaceFeeCents int64
	MPCollectorID       string // conta Mercado Pago que deve receber
	PaidAt              string // vazio enquanto não pago
	CreatedAt           string
	Items               []OrderItem
	// Resumo lista os livros do pedido ("Dom Casmurro (2×) · Iracema"),
	// para identificar o pedido nas listagens.
	Resumo string

	// Frete e entrega. Total já inclui o frete; a comissão do site incide só
	// sobre os livros (ItemsTotal).
	Shipping         Shipping
	ShipTo           Address
	CustomerDocument string // CPF/CNPJ do destinatário, exigido na etiqueta
	MEOrderID        string // pedido da etiqueta no Melhor Envio
	LabelURL         string // PDF da etiqueta
	TrackingCode     string
}

// MarketplaceFee é a comissão do site em reais.
func (o Order) MarketplaceFee() float64 {
	return float64(o.MarketplaceFeeCents) / 100
}

// PaidInFull compara o valor liquidado com o total do pedido. Um pagamento
// aprovado por valor menor que o pedido não pode liberar a entrega.
func (o Order) PaidInFull() bool {
	return o.PaidAmountCents >= OrderAmountCents(&o)
}

type OrderItem struct {
	ID          int
	OrderID     int
	ProductID   int
	ProductName string
	Quantity    int
	Price       float64
	Digital     bool   // e-book: entregue por download, sem frete
	Edition     string // e-book: "ebook", "audio" ou "combo" (audio.go)
}

// DigitalDeliveryAddress vai no lugar do endereço em pedido só de e-books.
const DigitalDeliveryAddress = "Entrega digital: download em Minha biblioteca"

// HasPhysical diz se o pedido tem algum livro para enviar.
func (o Order) HasPhysical() bool {
	for _, item := range o.Items {
		if !item.Digital {
			return true
		}
	}
	return false
}

// HasDigital diz se o pedido tem algum e-book.
func (o Order) HasDigital() bool {
	for _, item := range o.Items {
		if item.Digital {
			return true
		}
	}
	return false
}

// DigitalOnly: só e-books, nada a enviar.
func (o Order) DigitalOnly() bool {
	return len(o.Items) > 0 && !o.HasPhysical()
}

func (o Order) FormattedTotal() string {
	return FormatBRL(o.Total)
}

// IsPaid indica pedido com pagamento confirmado, inclusive os já enviados.
// É o que libera o comprovante.
func (o Order) IsPaid() bool {
	return o.Status == "pago" || o.Status == "enviado" || o.Status == "entregue"
}

// PaymentMethodLabel descreve a forma de pagamento para o comprovante.
func (o Order) PaymentMethodLabel() string {
	switch o.PaymentMethod {
	case "pix":
		return "PIX"
	case "cartao":
		return "Cartão ou boleto (Mercado Pago)"
	default:
		return "Mercado Pago"
	}
}

func (o Order) StatusLabel() string {
	switch o.Status {
	case "pago":
		return "Pago"
	case StatusReady:
		return "Pronto para envio"
	case "enviado":
		return "Enviado"
	case "entregue":
		return "Entregue"
	case "cancelado":
		return "Cancelado"
	case "aguardando_pagamento":
		return "Aguardando Pagamento"
	default:
		return "Pendente"
	}
}

func CreateOrder(o *Order) (int64, error) {
	if o == nil || o.CustomerName == "" || o.CustomerEmail == "" || len(o.Items) == 0 {
		return 0, ErrInvalidOrder
	}
	tx, err := database.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	validatedItems := make([]OrderItem, 0, len(o.Items))
	var total float64
	sellerID := -1
	for _, item := range o.Items {
		if item.ProductID <= 0 || item.Quantity <= 0 || item.Quantity > 1000 {
			return 0, ErrInvalidOrder
		}
		var current OrderItem
		var stock, active, productSeller, tracks int
		var format string
		var audioPrice, bundlePrice float64
		if err := tx.QueryRow(`SELECT name, price, stock, active, seller_id, product_format, audio_price, bundle_price,
				(SELECT COUNT(*) FROM audio_tracks WHERE product_id = products.id)
			FROM products WHERE id=? AND archived=0`, item.ProductID).Scan(&current.ProductName, &current.Price, &stock, &active, &productSeller, &format,
			&audioPrice, &bundlePrice, &tracks); err != nil {
			return 0, ErrInvalidOrder
		}
		current.Digital = format == FormatDigital
		if current.Digital {
			// Preço da edição vem do banco, nunca da sacola.
			p := Product{Format: format, Price: current.Price, AudioPrice: audioPrice, BundlePrice: bundlePrice, AudioTracks: tracks}
			edition, ok := p.ValidEdition(item.Edition)
			if !ok {
				return 0, ErrEditionInvalid
			}
			current.Edition, current.Price = edition, p.PriceFor(edition)
			if edition != EditionEbook {
				current.ProductName += " (" + EditionLabel(edition) + ")"
			}
		}
		// O vendedor vem do banco, não da sacola: um cookie adulterado não
		// consegue juntar livros de dois recebedores num pagamento só.
		if sellerID == -1 {
			sellerID = productSeller
		} else if sellerID != productSeller {
			return 0, ErrMixedSellers
		}
		current.ProductID = item.ProductID
		current.Quantity = item.Quantity
		if current.Digital {
			// O e-book fica na biblioteca da conta: sem conta, quem pagou
			// perderia o livro ao trocar de navegador. Uma cópia basta.
			if o.CustomerID <= 0 {
				return 0, ErrDigitalNeedsAccount
			}
			if active != 1 {
				return 0, ErrInsufficientStock
			}
			// A aprovação pode ter sido revogada depois de o livro ir à sacola.
			if productSeller != 0 && !sellerSellsDigital(productSeller) {
				return 0, ErrDigitalUnavailable
			}
			if CustomerOwnsEdition(o.CustomerID, item.ProductID, current.Edition) {
				return 0, ErrAlreadyOwned
			}
			current.Quantity = 1
		} else if active != 1 || stock < item.Quantity {
			return 0, ErrInsufficientStock
		}
		validatedItems = append(validatedItems, current)
		total += current.Price * float64(current.Quantity)
	}
	if total <= 0 {
		return 0, ErrInvalidOrder
	}
	// Endereço e frete só existem quando há livro para enviar.
	digitalOnly := true
	for _, item := range validatedItems {
		if !item.Digital {
			digitalOnly = false
		}
	}
	o.Address = strings.TrimSpace(o.Address)
	switch {
	case digitalOnly:
		o.Address = DigitalDeliveryAddress
		o.ShipTo, o.Shipping, o.CustomerDocument = Address{}, Shipping{}, ""
	case o.ShipTo.PostalCode != "" || o.ShipTo.Street != "":
		if err := o.ShipTo.Normalize(); err != nil {
			return 0, err
		}
		o.Address = o.ShipTo.String()
	case o.Address == "":
		return 0, ErrInvalidOrder
	}
	if o.Shipping.Cents < 0 || o.Shipping.Cents > 1_000_000 {
		return 0, ErrInvalidOrder
	}
	itemsTotal := total
	total += o.Shipping.Reais()
	if !SellerCanReceive(sellerID) {
		return 0, ErrSellerCannotReceive
	}
	// Comissão do site só em venda de vendedor; venda do site é 100% do site.
	var feeCents int64
	if sellerID != 0 {
		// Reais × percentual = centavos. O percentual já desconta o
		// benefício do nível do vendedor, quando ele vale (commission.go).
		feeCents = int64(math.Round(itemsTotal * SellerFeePercent(sellerID)))
	}
	res, err := tx.Exec(
		`INSERT INTO orders (customer_id, customer_name, customer_email, customer_phone, address, total, status, seller_id, marketplace_fee_cents,
			shipping_cents, shipping_service_id, shipping_service, shipping_days,
			ship_postal_code, ship_street, ship_number, ship_complement, ship_district, ship_city, ship_state, customer_document)
		 VALUES (?,?,?,?,?,?,?,?,?, ?,?,?,?, ?,?,?,?,?,?,?,?)`,
		o.CustomerID, o.CustomerName, o.CustomerEmail, o.CustomerPhone, o.Address, total, "aguardando_pagamento", sellerID, feeCents,
		o.Shipping.Cents, o.Shipping.ServiceID, o.Shipping.Service, o.Shipping.Days,
		o.ShipTo.PostalCode, o.ShipTo.Street, o.ShipTo.Number, o.ShipTo.Complement, o.ShipTo.District, o.ShipTo.City, o.ShipTo.State,
		o.CustomerDocument,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, item := range validatedItems {
		if _, err := tx.Exec(`INSERT INTO order_items (order_id, product_id, product_name, quantity, price, edition) VALUES (?,?,?,?,?,?)`, id, item.ProductID, item.ProductName, item.Quantity, item.Price, item.Edition); err != nil {
			return 0, err
		}
		if item.Digital {
			continue // e-book não tem estoque
		}
		result, err := tx.Exec(`UPDATE products SET stock = stock - ? WHERE id=? AND active=1 AND stock >= ?`, item.Quantity, item.ProductID, item.Quantity)
		if err != nil {
			return 0, err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		if rows != 1 {
			return 0, ErrInsufficientStock
		}
	}
	o.Total = total
	o.Items = validatedItems
	o.SellerID = sellerID
	o.MarketplaceFeeCents = feeCents
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func UpdateOrderPix(id int64, pixPaymentID int64, qrcode, qrcodeImg string) error {
	_, err := database.DB.Exec(
		`UPDATE orders SET pix_payment_id=?, pix_qrcode=?, pix_qrcode_img=? WHERE id=?`,
		fmt.Sprintf("%d", pixPaymentID), qrcode, qrcodeImg, id,
	)
	return err
}

func OrderAmountCents(o *Order) int64 {
	return int64(math.Round(o.Total * 100))
}

func OrderItemsAmountCents(o *Order) int64 {
	var total int64
	for _, item := range o.Items {
		total += int64(math.Round(item.Price*100)) * int64(item.Quantity)
	}
	return total
}

// SetOrderCollector grava a conta Mercado Pago que deve receber o pedido.
func SetOrderCollector(id int64, collectorID string) error {
	_, err := database.DB.Exec(
		`UPDATE orders SET mp_collector_id=? WHERE id=? AND status IN ('pendente','aguardando_pagamento')`,
		collectorID, id,
	)
	return err
}

// SetOrderPaymentMethod registra o meio escolhido no checkout antes de
// redirecionar o cliente ao provedor.
func SetOrderPaymentMethod(id int64, method string) error {
	if method != "pix" && method != "cartao" {
		return ErrInvalidOrder
	}
	_, err := database.DB.Exec(
		`UPDATE orders SET payment_method=? WHERE id=? AND status IN ('pendente','aguardando_pagamento')`,
		method, id,
	)
	return err
}

// SetOrderCheckout guarda a preferência do Checkout Pro para que o webhook
// possa reconciliar o pagamento e o cliente possa retomar o pagamento.
func SetOrderCheckout(id int64, preferenceID, checkoutURL string) error {
	_, err := database.DB.Exec(
		`UPDATE orders SET mp_preference_id=?, mp_checkout_url=?, payment_method='cartao', status='aguardando_pagamento'
		 WHERE id=? AND status IN ('pendente','aguardando_pagamento')`,
		preferenceID, checkoutURL, id,
	)
	return err
}

// MarkOrderPaid confirma o pagamento de forma idempotente: só avança pedidos
// ainda não pagos, e apenas quando o valor liquidado cobre o total. Retorna
// true somente na transição efetiva, para que a notificação ao lojista seja
// disparada uma única vez mesmo com webhooks repetidos.
func MarkOrderPaid(id int, paidAmountCents int64, paymentMethod string) (bool, error) {
	var totalCents int64
	if err := database.DB.QueryRow(
		`SELECT CAST(ROUND(total * 100) AS INTEGER) FROM orders WHERE id=?`, id,
	).Scan(&totalCents); err != nil {
		return false, err
	}
	if paidAmountCents < totalCents {
		return false, fmt.Errorf("%w: pago %d centavos de %d", ErrUnderpaid, paidAmountCents, totalCents)
	}

	// Pedido só de e-books não tem o que enviar: já nasce entregue, e o
	// download fica liberado na hora.
	result, err := database.DB.Exec(
		`UPDATE orders SET paid_at=CURRENT_TIMESTAMP, paid_amount_cents=?, payment_method=COALESCE(NULLIF(?,''), payment_method),
			status = CASE WHEN EXISTS (
				SELECT 1 FROM order_items oi JOIN products p ON p.id = oi.product_id
				WHERE oi.order_id = orders.id AND p.product_format <> 'digital'
			) THEN 'pago' ELSE 'entregue' END
		 WHERE id=? AND status IN ('pendente','aguardando_pagamento')`,
		paidAmountCents, paymentMethod, id,
	)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if rows == 1 {
		// Começa a contar o prazo de postagem do vendedor.
		SetShipDeadline(id)
	}
	return rows == 1, nil
}

func orderColumns() string {
	return `id, customer_id, customer_name, customer_email, customer_phone, address, total, status, pix_payment_id, pix_qrcode, pix_qrcode_img, payment_method, mp_preference_id, mp_checkout_url, paid_amount_cents, seller_id, marketplace_fee_cents, mp_collector_id, COALESCE(paid_at, ''), created_at,
		shipping_cents, shipping_service_id, shipping_service, shipping_days,
		ship_postal_code, ship_street, ship_number, ship_complement, ship_district, ship_city, ship_state,
		customer_document, me_order_id, label_url, tracking_code`
}

func GetOrderByPixPaymentID(pixPaymentID int64) (*Order, error) {
	row := database.DB.QueryRow(
		`SELECT `+orderColumns()+` FROM orders WHERE pix_payment_id=?`,
		fmt.Sprintf("%d", pixPaymentID),
	)
	var o Order
	err := row.Scan(orderScanArgs(&o)...)
	return &o, err
}

func GetOrder(id int) (*Order, error) {
	row := database.DB.QueryRow(
		`SELECT `+orderColumns()+` FROM orders WHERE id=?`, id,
	)
	var o Order
	err := row.Scan(orderScanArgs(&o)...)
	if err != nil {
		return nil, err
	}
	rows, err := database.DB.Query(`SELECT oi.id, oi.order_id, oi.product_id, oi.product_name, oi.quantity, oi.price,
		COALESCE(p.product_format, 'fisico') = 'digital'
		FROM order_items oi LEFT JOIN products p ON p.id = oi.product_id WHERE oi.order_id=?`, id)
	if err != nil {
		return &o, nil
	}
	defer rows.Close()
	for rows.Next() {
		var item OrderItem
		rows.Scan(&item.ID, &item.OrderID, &item.ProductID, &item.ProductName, &item.Quantity, &item.Price, &item.Digital)
		o.Items = append(o.Items, item)
	}
	return &o, nil
}

func GetOrderForSeller(id, sellerID int) (*Order, error) {
	o, err := GetOrder(id)
	if err != nil {
		return nil, err
	}
	rows, err := database.DB.Query(`
		SELECT oi.id, oi.order_id, oi.product_id, oi.product_name, oi.quantity, oi.price, p.product_format = 'digital'
		FROM order_items oi JOIN products p ON p.id=oi.product_id
		WHERE oi.order_id=? AND p.seller_id=?`, id, sellerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	o.Items = nil
	o.Total = 0
	for rows.Next() {
		var item OrderItem
		if err := rows.Scan(&item.ID, &item.OrderID, &item.ProductID, &item.ProductName, &item.Quantity, &item.Price, &item.Digital); err != nil {
			return nil, err
		}
		o.Items = append(o.Items, item)
		o.Total += item.Price * float64(item.Quantity)
	}
	if len(o.Items) == 0 {
		return nil, fmt.Errorf("pedido não pertence ao vendedor")
	}
	return o, rows.Err()
}

func orderScanArgs(o *Order) []any {
	return []any{&o.ID, &o.CustomerID, &o.CustomerName, &o.CustomerEmail, &o.CustomerPhone, &o.Address, &o.Total, &o.Status, &o.PixPaymentID, &o.PixQRCode, &o.PixQRCodeImg, &o.PaymentMethod, &o.MPPreferenceID, &o.MPCheckoutURL, &o.PaidAmountCents, &o.SellerID, &o.MarketplaceFeeCents, &o.MPCollectorID, &o.PaidAt, &o.CreatedAt,
		&o.Shipping.Cents, &o.Shipping.ServiceID, &o.Shipping.Service, &o.Shipping.Days,
		&o.ShipTo.PostalCode, &o.ShipTo.Street, &o.ShipTo.Number, &o.ShipTo.Complement, &o.ShipTo.District, &o.ShipTo.City, &o.ShipTo.State,
		&o.CustomerDocument, &o.MEOrderID, &o.LabelURL, &o.TrackingCode}
}

func ListOrders() ([]Order, error) {
	rows, err := database.DB.Query(`SELECT id, customer_name, customer_email, total, status, created_at, COALESCE((SELECT GROUP_CONCAT(product_name || CASE WHEN quantity > 1 THEN ' (' || quantity || '×)' ELSE '' END, ' · ') FROM order_items WHERE order_id = orders.id), '') FROM orders ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orders []Order
	for rows.Next() {
		var o Order
		rows.Scan(&o.ID, &o.CustomerName, &o.CustomerEmail, &o.Total, &o.Status, &o.CreatedAt, &o.Resumo)
		orders = append(orders, o)
	}
	return orders, nil
}

func ListOrdersBySeller(sellerID int) ([]Order, error) {
	rows, err := database.DB.Query(`
		SELECT DISTINCT o.id, o.customer_name, o.customer_email, o.total, o.status, o.created_at,
			COALESCE((SELECT GROUP_CONCAT(product_name || CASE WHEN quantity > 1 THEN ' (' || quantity || '×)' ELSE '' END, ' · ') FROM order_items WHERE order_id = o.id), '')
		FROM orders o
		JOIN order_items oi ON oi.order_id=o.id
		JOIN products p ON p.id=oi.product_id
		WHERE p.seller_id=? ORDER BY o.id DESC`, sellerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orders []Order
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.CustomerName, &o.CustomerEmail, &o.Total, &o.Status, &o.CreatedAt, &o.Resumo); err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

func OrderBelongsToSeller(orderID, sellerID int) (bool, error) {
	var exists int
	err := database.DB.QueryRow(`
		SELECT 1 FROM order_items oi JOIN products p ON p.id=oi.product_id
		WHERE oi.order_id=? AND p.seller_id=? LIMIT 1`, orderID, sellerID).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists == 1, nil
}

// TotalSpentByCustomer soma os pedidos confirmados (pagos, enviados ou entregues)
// de um cliente, usado para calcular o nível de fidelidade.
func TotalSpentByCustomer(customerID int) (float64, error) {
	var total float64
	err := database.DB.QueryRow(
		`SELECT COALESCE(SUM(total), 0) FROM orders WHERE customer_id=? AND status IN ('pago', 'pronto', 'enviado', 'entregue')`,
		customerID,
	).Scan(&total)
	return total, err
}

func ListOrdersByCustomer(customerID int) ([]Order, error) {
	rows, err := database.DB.Query(
		`SELECT id, customer_name, customer_email, total, status, created_at, COALESCE((SELECT GROUP_CONCAT(product_name || CASE WHEN quantity > 1 THEN ' (' || quantity || '×)' ELSE '' END, ' · ') FROM order_items WHERE order_id = orders.id), '') FROM orders WHERE customer_id=? ORDER BY id DESC`,
		customerID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orders []Order
	for rows.Next() {
		var o Order
		rows.Scan(&o.ID, &o.CustomerName, &o.CustomerEmail, &o.Total, &o.Status, &o.CreatedAt, &o.Resumo)
		orders = append(orders, o)
	}
	return orders, nil
}

// ErrOrderCancelled: pedido cancelado não volta para outra situação, porque
// o estoque dele já foi devolvido.
var ErrOrderCancelled = errors.New("pedido cancelado não pode mudar de situação")

func UpdateOrderStatus(id int, status string) error {
	if !validOrderStatus(status) {
		return ErrInvalidOrder
	}
	if status == "cancelado" {
		_, err := CancelOrder(id)
		return err
	}
	res, err := database.DB.Exec(`UPDATE orders SET status=? WHERE id=? AND status<>'cancelado'`, status, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrOrderCancelled
	}
	// Datas do despacho, se ainda não marcadas.
	switch status {
	case StatusReady:
		database.DB.Exec(`UPDATE orders SET ready_at=CURRENT_TIMESTAMP WHERE id=? AND ready_at=''`, id)
	case "enviado":
		now := dbTime(time.Now())
		database.DB.Exec(`UPDATE orders SET posted_at=?, late = CASE WHEN ship_deadline <> '' AND ? > ship_deadline THEN 1 ELSE 0 END
			WHERE id=? AND posted_at=''`, now, now, id)
	case "entregue":
		database.DB.Exec(`UPDATE orders SET delivered_at=? WHERE id=? AND delivered_at=''`, dbTime(time.Now()), id)
	}
	return nil
}

// CancelOrder cancela o pedido e devolve ao estoque os livros físicos dele,
// na mesma transação e uma única vez: cancelar de novo não soma estoque.
// E-book não tem estoque. Devolve true quando o pedido foi de fato cancelado
// agora.
func CancelOrder(id int) (bool, error) {
	tx, err := database.DB.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE orders SET status='cancelado' WHERE id=? AND status<>'cancelado'`, id)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, tx.Commit()
	}
	if _, err := tx.Exec(`UPDATE products SET stock = stock + (
			SELECT COALESCE(SUM(oi.quantity), 0) FROM order_items oi WHERE oi.order_id = ? AND oi.product_id = products.id)
		WHERE product_format <> 'digital' AND id IN (SELECT product_id FROM order_items WHERE order_id = ?)`, id, id); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// ExpireUnpaidOrders cancela pedidos que ficaram sem pagamento por mais
// tempo do que o limite, devolvendo o estoque reservado. O limite precisa
// ser bem maior que a validade do pagamento (PIX 30 min, cartão 24 h), para
// um pagamento feito no último minuto nunca cair num pedido já cancelado.
func ExpireUnpaidOrders(olderThanHours int) (int, error) {
	rows, err := database.DB.Query(`SELECT id FROM orders WHERE status IN ('pendente','aguardando_pagamento')
		AND created_at < datetime('now', ?)`, fmt.Sprintf("-%d hours", olderThanHours))
	if err != nil {
		return 0, err
	}
	var ids []int
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	n := 0
	for _, id := range ids {
		if ok, err := CancelOrder(id); err != nil {
			return n, err
		} else if ok {
			n++
		}
	}
	return n, nil
}

// UpdateSellerOrderStatus limita o vendedor à evolução de entrega. A
// confirmação de pagamento continua restrita aos webhooks do provedor.
func UpdateSellerOrderStatus(id int, status string) error {
	var err error
	switch status {
	case StatusReady:
		err = MarkOrderReady(id)
	case "enviado":
		err = MarkOrderPosted(id, time.Time{})
	case "entregue":
		err = MarkOrderDelivered(id, time.Time{})
	default:
		return ErrInvalidOrder
	}
	if errors.Is(err, ErrDispatchStep) {
		return ErrInvalidOrder
	}
	return err
}

func validOrderStatus(status string) bool {
	switch status {
	case "pendente", "aguardando_pagamento", "pago", StatusReady, "enviado", "entregue", "cancelado":
		return true
	default:
		return false
	}
}
