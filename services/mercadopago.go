// Package services concentra as integrações externas da loja.
//
// Este arquivo cobre o Mercado Pago, o único provedor de pagamento do sistema:
// cobrança PIX com QR Code, Checkout Pro (cartão, boleto e demais meios) e a
// verificação de autenticidade das notificações de webhook.
package services

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const mpBaseURL = "https://api.mercadopago.com"

// mpClient tem timeout curto: o checkout trava enquanto espera, e o cliente
// prefere um erro rápido a uma página pendurada.
var mpClient = &http.Client{Timeout: 15 * time.Second}

// ── Tipos compartilhados ────────────────────────────────────────────────────

// PaymentItem descreve uma linha do pedido enviada ao Mercado Pago.
type PaymentItem struct {
	Title     string
	Quantity  int
	UnitPrice float64
}

// PixPaymentResult traz o que a página de pagamento precisa exibir.
type PixPaymentResult struct {
	ID           int64
	Status       string
	QRCode       string // copia e cola
	QRCodeBase64 string // imagem para <img src="data:image/png;base64,...">
	ExpiresAt    time.Time
}

// CheckoutPreference é o resultado do Checkout Pro: a URL para onde o cliente
// é redirecionado e o identificador usado para reconciliar o webhook.
type CheckoutPreference struct {
	ID           string
	InitPoint    string
	SandboxPoint string
}

// PaymentDetails é a fonte da verdade sobre um pagamento. Nunca confiamos no
// corpo do webhook: consultamos a API e decidimos a partir desta resposta.
type PaymentDetails struct {
	ID                int64
	Status            string
	StatusDetail      string
	PaidAmountCents   int64
	AmountCents       int64
	ExternalReference string
	PaymentMethodID   string
	PaymentTypeID     string
	// CollectorID é a conta que recebeu o dinheiro; ApplicationFeeCents, a
	// comissão do site retida pelo Mercado Pago (zero em venda do próprio site).
	CollectorID         int64
	ApplicationFeeCents int64
}

// Approved indica pagamento liquidado. "authorized" é apenas reserva de limite
// no cartão e não libera o pedido.
func (p PaymentDetails) Approved() bool { return p.Status == "approved" }

type mpErrorResponse struct {
	Message string `json:"message"`
	Error   string `json:"error"`
	Status  int    `json:"status"`
}

// ── PIX ─────────────────────────────────────────────────────────────────────

type pixPaymentRequest struct {
	TransactionAmount float64  `json:"transaction_amount"`
	Description       string   `json:"description"`
	PaymentMethodID   string   `json:"payment_method_id"`
	Payer             pixPayer `json:"payer"`
	ExternalReference string   `json:"external_reference"`
	NotificationURL   string   `json:"notification_url,omitempty"`
	DateOfExpiration  string   `json:"date_of_expiration,omitempty"`
	// ApplicationFee é a comissão do site numa cobrança feita com o token do
	// vendedor. Omitida em venda do próprio site.
	ApplicationFee float64 `json:"application_fee,omitempty"`
}

type pixPayer struct {
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
}

type mpPaymentResponse struct {
	ID                int64   `json:"id"`
	Status            string  `json:"status"`
	StatusDetail      string  `json:"status_detail"`
	TransactionAmount float64 `json:"transaction_amount"`
	ExternalReference string  `json:"external_reference"`
	PaymentMethodID   string  `json:"payment_method_id"`
	PaymentTypeID     string  `json:"payment_type_id"`
	DateOfExpiration  string  `json:"date_of_expiration"`
	CollectorID       int64   `json:"collector_id"`
	FeeDetails        []struct {
		Type   string  `json:"type"`
		Amount float64 `json:"amount"`
	} `json:"fee_details"`
	TransactionDetails struct {
		TotalPaidAmount float64 `json:"total_paid_amount"`
	} `json:"transaction_details"`
	PointOfInteraction struct {
		TransactionData struct {
			QRCode       string `json:"qr_code"`
			QRCodeBase64 string `json:"qr_code_base64"`
		} `json:"transaction_data"`
	} `json:"point_of_interaction"`
	Message string `json:"message"`
}

// CreatePixPayment abre uma cobrança PIX e devolve o QR Code.
//
// A expiração é explícita: sem ela o QR Code fica válido por muito tempo e o
// estoque reservado no pedido trava sem necessidade.
//
// applicationFee é a comissão do site quando accessToken é o de um vendedor;
// zero numa venda do próprio site.
func CreatePixPayment(accessToken string, amount float64, description string, orderID int64, customerEmail, notifURL string, ttl time.Duration, applicationFee float64) (*PixPaymentResult, error) {
	if strings.TrimSpace(accessToken) == "" {
		return nil, fmt.Errorf("access token do Mercado Pago não configurado")
	}
	if amount <= 0 {
		return nil, fmt.Errorf("valor do pagamento inválido")
	}
	if applicationFee < 0 || applicationFee >= amount {
		return nil, fmt.Errorf("comissão inválida para o valor do pagamento")
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	expiresAt := time.Now().Add(ttl)

	payload := pixPaymentRequest{
		TransactionAmount: roundCurrency(amount),
		Description:       description,
		PaymentMethodID:   "pix",
		Payer:             pixPayer{Email: customerEmail, FirstName: "Cliente"},
		ExternalReference: ExternalReference(orderID),
		NotificationURL:   notifURL,
		DateOfExpiration:  expiresAt.Format("2006-01-02T15:04:05.000-07:00"),
		ApplicationFee:    roundCurrency(applicationFee),
	}

	// A chave de idempotência é derivada do pedido, não do relógio: se a rede
	// cair no meio da chamada, repetir a requisição reaproveita a mesma
	// cobrança em vez de criar uma segunda para o mesmo pedido.
	idempotencyKey := fmt.Sprintf("pix-order-%d", orderID)

	var result mpPaymentResponse
	if err := mpPost(accessToken, "/v1/payments", idempotencyKey, payload, &result); err != nil {
		return nil, err
	}

	qr := result.PointOfInteraction.TransactionData
	if qr.QRCode == "" {
		return nil, fmt.Errorf("Mercado Pago não retornou o código PIX (status %q)", result.Status)
	}

	return &PixPaymentResult{
		ID:           result.ID,
		Status:       result.Status,
		QRCode:       qr.QRCode,
		QRCodeBase64: qr.QRCodeBase64,
		ExpiresAt:    expiresAt,
	}, nil
}

// ── Checkout Pro (cartão, boleto e demais meios) ────────────────────────────

type preferenceRequest struct {
	Items             []preferenceItem  `json:"items"`
	Payer             preferencePayer   `json:"payer"`
	BackURLs          preferenceBackURL `json:"back_urls"`
	AutoReturn        string            `json:"auto_return,omitempty"`
	ExternalReference string            `json:"external_reference"`
	NotificationURL   string            `json:"notification_url,omitempty"`
	StatementDescr    string            `json:"statement_descriptor,omitempty"`
	ExpiresFlag       bool              `json:"expires"`
	ExpirationDateTo  string            `json:"expiration_date_to,omitempty"`
	PaymentMethods    paymentMethodsCfg `json:"payment_methods"`
	// MarketplaceFee é a comissão do site no Checkout Pro de um vendedor.
	MarketplaceFee float64 `json:"marketplace_fee,omitempty"`
}

type preferenceItem struct {
	Title      string  `json:"title"`
	Quantity   int     `json:"quantity"`
	UnitPrice  float64 `json:"unit_price"`
	CurrencyID string  `json:"currency_id"`
}

type preferencePayer struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

type preferenceBackURL struct {
	Success string `json:"success"`
	Pending string `json:"pending"`
	Failure string `json:"failure"`
}

type paymentMethodsCfg struct {
	// PIX é oferecido no fluxo próprio da loja; aqui o Checkout Pro cobre
	// cartão e boleto, evitando dois caminhos diferentes para o mesmo meio.
	ExcludedPaymentTypes []excludedType `json:"excluded_payment_types,omitempty"`
	Installments         int            `json:"installments,omitempty"`
}

type excludedType struct {
	ID string `json:"id"`
}

type preferenceResponse struct {
	ID            string `json:"id"`
	InitPoint     string `json:"init_point"`
	SandboxPoint  string `json:"sandbox_init_point"`
	Message       string `json:"message"`
	ExternalRefer string `json:"external_reference"`
}

// CreateCheckoutPreference cria a preferência do Checkout Pro para pagamento
// com cartão ou boleto e devolve a URL de redirecionamento.
//
// marketplaceFee é a comissão do site quando accessToken é o de um vendedor;
// zero numa venda do próprio site.
func CreateCheckoutPreference(accessToken string, items []PaymentItem, orderID int64, customerName, customerEmail, baseURL, notifURL, storeName string, maxInstallments int, ttl time.Duration, marketplaceFee float64) (*CheckoutPreference, error) {
	if strings.TrimSpace(accessToken) == "" {
		return nil, fmt.Errorf("access token do Mercado Pago não configurado")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("pedido sem itens")
	}
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("URL base da loja não configurada; necessária para o retorno do Checkout Pro")
	}
	if maxInstallments <= 0 {
		maxInstallments = 12
	}
	if ttl <= 0 {
		ttl = time.Hour
	}

	prefItems := make([]preferenceItem, 0, len(items))
	for _, item := range items {
		if item.Quantity <= 0 || item.UnitPrice <= 0 {
			return nil, fmt.Errorf("item inválido no pedido")
		}
		prefItems = append(prefItems, preferenceItem{
			Title:      item.Title,
			Quantity:   item.Quantity,
			UnitPrice:  roundCurrency(item.UnitPrice),
			CurrencyID: "BRL",
		})
	}

	baseURL = strings.TrimRight(baseURL, "/")
	payload := preferenceRequest{
		Items:             prefItems,
		Payer:             preferencePayer{Name: customerName, Email: customerEmail},
		ExternalReference: ExternalReference(orderID),
		NotificationURL:   notifURL,
		StatementDescr:    statementDescriptor(storeName),
		ExpiresFlag:       true,
		ExpirationDateTo:  time.Now().Add(ttl).Format("2006-01-02T15:04:05.000-07:00"),
		BackURLs: preferenceBackURL{
			Success: fmt.Sprintf("%s/pagamento/retorno?pedido=%d", baseURL, orderID),
			Pending: fmt.Sprintf("%s/pagamento/retorno?pedido=%d", baseURL, orderID),
			Failure: fmt.Sprintf("%s/pagamento/retorno?pedido=%d", baseURL, orderID),
		},
		AutoReturn: "approved",
		PaymentMethods: paymentMethodsCfg{
			ExcludedPaymentTypes: []excludedType{{ID: "bank_transfer"}},
			Installments:         maxInstallments,
		},
		MarketplaceFee: roundCurrency(marketplaceFee),
	}

	var result preferenceResponse
	if err := mpPost(accessToken, "/checkout/preferences", fmt.Sprintf("pref-order-%d", orderID), payload, &result); err != nil {
		return nil, err
	}
	if result.InitPoint == "" {
		return nil, fmt.Errorf("Mercado Pago não retornou a URL de checkout")
	}
	return &CheckoutPreference{
		ID:           result.ID,
		InitPoint:    result.InitPoint,
		SandboxPoint: result.SandboxPoint,
	}, nil
}

// ── Consulta de pagamento ───────────────────────────────────────────────────

// GetPayment consulta um pagamento na API. Toda decisão de liberar pedido passa
// por aqui, jamais pelo conteúdo do webhook.
func GetPayment(accessToken string, paymentID int64) (*PaymentDetails, error) {
	if strings.TrimSpace(accessToken) == "" {
		return nil, fmt.Errorf("access token do Mercado Pago não configurado")
	}
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v1/payments/%d", mpBaseURL, paymentID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := mpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("erro ao consultar Mercado Pago: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Mercado Pago retornou %d ao consultar pagamento: %s", resp.StatusCode, describeError(body))
	}

	var result mpPaymentResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("resposta inválida do Mercado Pago: %w", err)
	}

	paid := result.TransactionDetails.TotalPaidAmount
	if paid <= 0 {
		paid = result.TransactionAmount
	}
	var applicationFee float64
	for _, fee := range result.FeeDetails {
		if fee.Type == "application_fee" {
			applicationFee += fee.Amount
		}
	}

	return &PaymentDetails{
		ID:                result.ID,
		Status:            result.Status,
		StatusDetail:      result.StatusDetail,
		PaidAmountCents:   toCents(paid),
		AmountCents:       toCents(result.TransactionAmount),
		ExternalReference: result.ExternalReference,
		PaymentMethodID:   result.PaymentMethodID,
		PaymentTypeID:     result.PaymentTypeID,

		CollectorID:         result.CollectorID,
		ApplicationFeeCents: toCents(applicationFee),
	}, nil
}

// GetMerchantOrderPaymentIDs resolve a notificação de merchant_order, que o
// Checkout Pro envia no lugar de payment em parte dos fluxos, para os IDs de
// pagamento que ela agrupa.
func GetMerchantOrderPaymentIDs(accessToken string, merchantOrderID int64) ([]int64, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/merchant_orders/%d", mpBaseURL, merchantOrderID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := mpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Mercado Pago retornou %d em merchant_orders: %s", resp.StatusCode, describeError(body))
	}

	var result struct {
		Payments []struct {
			ID int64 `json:"id"`
		} `json:"payments"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(result.Payments))
	for _, p := range result.Payments {
		ids = append(ids, p.ID)
	}
	return ids, nil
}

// ── Autenticidade do webhook ────────────────────────────────────────────────

// VerifyWebhookSignature confere a assinatura HMAC que o Mercado Pago envia no
// cabeçalho x-signature. Sem essa checagem, qualquer pessoa que descubra a URL
// pode disparar notificações forjadas para o endpoint.
//
// O manifesto assinado é "id:<data.id>;request-id:<x-request-id>;ts:<ts>;",
// com data.id em minúsculas.
func VerifyWebhookSignature(secret, signatureHeader, requestID, dataID string) error {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return fmt.Errorf("segredo do webhook não configurado")
	}
	ts, v1 := parseSignatureHeader(signatureHeader)
	if ts == "" || v1 == "" {
		return fmt.Errorf("cabeçalho x-signature ausente ou malformado")
	}

	// Rejeita notificações antigas para que uma requisição capturada não possa
	// ser reenviada indefinidamente.
	if seconds, err := strconv.ParseInt(ts, 10, 64); err == nil {
		age := time.Since(time.Unix(seconds, 0))
		if age > 10*time.Minute || age < -10*time.Minute {
			return fmt.Errorf("notificação fora da janela de tempo aceita")
		}
	}

	manifest := fmt.Sprintf("id:%s;request-id:%s;ts:%s;", strings.ToLower(dataID), requestID, ts)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(manifest))
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(expected), []byte(strings.ToLower(v1))) {
		return fmt.Errorf("assinatura do webhook inválida")
	}
	return nil
}

func parseSignatureHeader(header string) (ts, v1 string) {
	for _, part := range strings.Split(header, ",") {
		key, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "ts":
			ts = strings.TrimSpace(value)
		case "v1":
			v1 = strings.TrimSpace(value)
		}
	}
	return ts, v1
}

// ── Utilidades ──────────────────────────────────────────────────────────────

// ExternalReference é o vínculo entre a cobrança no Mercado Pago e o pedido
// local. Usar um formato único evita reconciliação por valor, que confundiria
// dois pedidos de mesmo preço.
func ExternalReference(orderID int64) string {
	return fmt.Sprintf("pedido-%d", orderID)
}

// ParseExternalReference recupera o ID do pedido a partir da referência.
func ParseExternalReference(reference string) (int64, error) {
	rest, found := strings.CutPrefix(reference, "pedido-")
	if !found {
		return 0, fmt.Errorf("referência externa inesperada: %q", reference)
	}
	return strconv.ParseInt(rest, 10, 64)
}

func mpPost(accessToken, path, idempotencyKey string, payload, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, mpBaseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("X-Idempotency-Key", idempotencyKey)
	}

	resp, err := mpClient.Do(req)
	if err != nil {
		return fmt.Errorf("erro ao chamar Mercado Pago: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("Mercado Pago retornou %d: %s", resp.StatusCode, describeError(raw))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("resposta inválida do Mercado Pago: %w", err)
	}
	return nil
}

// describeError extrai a mensagem do provedor sem repassar o corpo inteiro,
// que pode conter dados que não queremos em log.
func describeError(body []byte) string {
	var parsed mpErrorResponse
	if err := json.Unmarshal(body, &parsed); err == nil {
		if parsed.Message != "" {
			return parsed.Message
		}
		if parsed.Error != "" {
			return parsed.Error
		}
	}
	if len(body) > 200 {
		body = body[:200]
	}
	return string(body)
}

// statementDescriptor é o texto que aparece na fatura do cartão. O Mercado Pago
// limita a 13 caracteres simples. Se o nome não couber, as primeiras palavras
// saem antes de cortar no meio: "Sebo Casa Amarela" vira "CASAAMARELA", e não
// "SEBOCASAAMARE".
func statementDescriptor(storeName string) string {
	var words []string
	for _, word := range strings.Fields(strings.ToUpper(storeName)) {
		var b strings.Builder
		for _, r := range word {
			if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			}
		}
		if b.Len() > 0 {
			words = append(words, b.String())
		}
	}
	for len(words) > 1 && len(strings.Join(words, "")) > 13 {
		words = words[1:]
	}
	descriptor := strings.Join(words, "")
	if len(descriptor) > 13 {
		descriptor = descriptor[:13]
	}
	return descriptor
}

func roundCurrency(v float64) float64 {
	return math.Round(v*100) / 100
}

func toCents(v float64) int64 {
	return int64(math.Round(v * 100))
}

// ── Parcelamento ────────────────────────────────────────────────────────────

// InstallmentPlan é a maior opção de parcelamento disponível para um valor,
// exatamente como o Mercado Pago vai oferecer na página de pagamento.
type InstallmentPlan struct {
	Installments int
	Amount       float64 // valor de cada parcela
	Total        float64 // total pago pelo comprador, com juros se houver
	InterestFree bool
}

type installmentsResponse []struct {
	PayerCosts []struct {
		Installments      int     `json:"installments"`
		InstallmentRate   float64 `json:"installment_rate"`
		InstallmentAmount float64 `json:"installment_amount"`
		TotalAmount       float64 `json:"total_amount"`
	} `json:"payer_costs"`
}

// GetInstallmentPlan consulta as parcelas que a conta que vai receber oferece
// para o valor, limitadas a max. As taxas e o valor mínimo por parcela são da
// conta do Mercado Pago, então só a própria API sabe a resposta certa.
func GetInstallmentPlan(accessToken string, amount float64, max int) (*InstallmentPlan, error) {
	if strings.TrimSpace(accessToken) == "" || amount <= 0 {
		return nil, fmt.Errorf("consulta de parcelas sem token ou valor")
	}
	endpoint := fmt.Sprintf("%s/v1/payment_methods/installments?amount=%.2f&payment_method_id=visa&payment_type_id=credit_card", mpBaseURL, amount)
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := mpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Mercado Pago retornou %d em parcelas: %s", resp.StatusCode, describeError(body))
	}
	var parsed installmentsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	best := pickInstallmentPlan(parsed, max)
	if best == nil {
		return nil, fmt.Errorf("Mercado Pago não informou parcelas para R$ %.2f", amount)
	}
	return best, nil
}

// pickInstallmentPlan escolhe a maior quantidade de parcelas até max.
func pickInstallmentPlan(parsed installmentsResponse, max int) *InstallmentPlan {
	var best *InstallmentPlan
	for _, option := range parsed {
		for _, cost := range option.PayerCosts {
			if cost.Installments > max || (best != nil && cost.Installments <= best.Installments) {
				continue
			}
			best = &InstallmentPlan{
				Installments: cost.Installments,
				Amount:       roundCurrency(cost.InstallmentAmount),
				Total:        roundCurrency(cost.TotalAmount),
				InterestFree: cost.InstallmentRate == 0,
			}
		}
		break // as bandeiras têm as mesmas faixas; a primeira basta
	}
	return best
}
