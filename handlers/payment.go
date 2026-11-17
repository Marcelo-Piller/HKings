package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vendas/loja/database"
	"github.com/vendas/loja/models"
	"github.com/vendas/loja/services"
)

// Todo o fluxo de pagamento passa pelo Mercado Pago: PIX com QR Code próprio e
// Checkout Pro para cartão e boleto. A confirmação nunca vem do navegador do
// cliente nem do corpo do webhook — apenas de uma consulta à API do provedor.

// PixPaymentPage exibe o QR Code e o copia-e-cola do PIX.
func PixPaymentPage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.URL.Query().Get("pedido"))
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	order, err := models.GetOrder(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !requireOrderAccess(w, r, order) {
		return
	}
	if order.IsPaid() {
		http.Redirect(w, r, "/pedido?id="+strconv.Itoa(id), http.StatusSeeOther)
		return
	}
	renderStore(w, r, "pix_payment.html", map[string]any{
		"Order":     order,
		"CartCount": 0,
	})
}

// PixStatusAPI alimenta o polling da página de PIX.
func PixStatusAPI(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.URL.Query().Get("pedido"))
	if err != nil || id <= 0 {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	order, err := models.GetOrder(id)
	if err != nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	if !requireOrderAccess(w, r, order) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	// "paid" cobre também o pedido só de e-books, que vai direto para "entregue".
	json.NewEncoder(w).Encode(map[string]any{"status": order.Status, "paid": order.IsPaid()})
}

// PaymentReturn recebe o cliente de volta do Checkout Pro. A URL de retorno é
// controlada pelo navegador, então ela nunca confirma pagamento: apenas mostra
// o estado que o webhook já gravou.
func PaymentReturn(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.URL.Query().Get("pedido"))
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	order, err := models.GetOrder(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !requireOrderAccess(w, r, order) {
		return
	}

	// O webhook pode ainda não ter chegado. Uma consulta direta evita que o
	// cliente veja "aguardando" logo após pagar com cartão aprovado na hora.
	if !order.IsPaid() {
		if paymentID := parsePaymentID(r.URL.Query().Get("payment_id")); paymentID > 0 {
			if err := reconcilePayment(paymentID, tokenForOrder(order)); err != nil {
				log.Printf("retorno: reconciliação do pagamento %d: %v", paymentID, err)
			}
			if refreshed, err := models.GetOrder(id); err == nil {
				order = refreshed
			}
		}
	}

	if order.IsPaid() {
		http.Redirect(w, r, "/pedido?id="+strconv.Itoa(id), http.StatusSeeOther)
		return
	}
	renderStore(w, r, "payment_processing.html", map[string]any{
		"Order":     order,
		"CartCount": 0,
	})
}

// PaymentRedirect - GET /pagamento/ir?pedido=N: página intermediária que leva o
// cliente ao Checkout Pro. Ver startCardCheckout.
func PaymentRedirect(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.URL.Query().Get("pedido"))
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	order, err := models.GetOrder(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !requireOrderAccess(w, r, order) {
		return
	}
	if order.IsPaid() || !isMercadoPagoCheckoutURL(order.MPCheckoutURL) {
		http.Redirect(w, r, "/pedido?id="+strconv.Itoa(id), http.StatusSeeOther)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	renderStore(w, r, "payment_redirect.html", map[string]any{
		"Order":       order,
		"CheckoutURL": order.MPCheckoutURL,
		"CartCount":   0,
	})
}

// isMercadoPagoCheckoutURL só deixa a página intermediária mandar o cliente para
// o Mercado Pago, mesmo que o endereço gravado no pedido venha adulterado.
func isMercadoPagoCheckoutURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "mercadopago.com.br" || strings.HasSuffix(host, ".mercadopago.com.br")
}

// MercadoPagoWebhook recebe as notificações de pagamento.
//
// A ordem importa: primeiro confirmamos a assinatura HMAC, depois consultamos a
// API. Sem a assinatura, qualquer pessoa que descubra a URL poderia disparar
// notificações; e como o corpo não é confiável, o valor e o status vêm sempre
// da consulta.
func MercadoPagoWebhook(w http.ResponseWriter, r *http.Request) {
	// O Mercado Pago reenvia a notificação quando não recebe 200. Responder 200
	// mesmo em erro interno evitaria o reenvio, então distinguimos os casos:
	// requisição inválida encerra em 200, falha nossa retorna 500 para reenvio.
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	notification, err := parseWebhookNotification(r)
	if err != nil {
		log.Printf("webhook: notificação ignorada: %v", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	secret := models.GetSetting("mp_webhook_secret")
	if secret == "" {
		log.Print("webhook: mp_webhook_secret não configurado; notificação recusada")
		http.Error(w, "webhook não configurado", http.StatusServiceUnavailable)
		return
	}
	if err := services.VerifyWebhookSignature(secret, r.Header.Get("x-signature"), r.Header.Get("x-request-id"), notification.DataID); err != nil {
		log.Printf("webhook: %v", err)
		http.Error(w, "assinatura inválida", http.StatusUnauthorized)
		return
	}

	// Pagamento de vendedor só é visível com o token dele; o aviso diz qual
	// conta recebeu. A assinatura já foi conferida acima, e a conferência do
	// recebedor em reconcilePayment impede que um user_id trocado libere nada.
	accessToken := tokenForCollector(notification.UserID)
	paymentIDs, err := notification.PaymentIDs(accessToken)
	if err != nil {
		log.Printf("webhook: %v", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	for _, paymentID := range paymentIDs {
		if err := reconcilePayment(paymentID, accessToken); err != nil {
			log.Printf("webhook: pagamento %d: %v", paymentID, err)
			// Erro de rede ou do provedor merece reenvio; dado inconsistente não.
			if errors.Is(err, errProviderUnavailable) {
				http.Error(w, "tente novamente", http.StatusInternalServerError)
				return
			}
		}
	}
	w.WriteHeader(http.StatusOK)
}

var errProviderUnavailable = errors.New("provedor indisponível")

// reconcilePayment consulta o pagamento no Mercado Pago e, se aprovado, com
// valor suficiente, recebido na conta certa e com a comissão do site retida,
// marca o pedido como pago. É idempotente: chamadas repetidas para o mesmo
// pagamento não duplicam efeitos.
func reconcilePayment(paymentID int64, accessToken string) error {
	if accessToken == "" {
		return fmt.Errorf("%w: access token não configurado", errProviderUnavailable)
	}

	payment, err := services.GetPayment(accessToken, paymentID)
	if err != nil {
		return fmt.Errorf("%w: %v", errProviderUnavailable, err)
	}
	if !payment.Approved() {
		log.Printf("pagamento %d com status %q (%s); pedido não liberado", paymentID, payment.Status, payment.StatusDetail)
		return nil
	}

	order, err := resolveOrder(payment)
	if err != nil {
		return err
	}
	if order.IsPaid() {
		return nil
	}
	if order.Status == "cancelado" {
		// O estoque já foi devolvido: não reabre o pedido sozinho.
		log.Printf("ALERTA: pagamento %d aprovado para o pedido #%d, que está cancelado. Confira e reembolse ou refaça o pedido.", paymentID, order.ID)
		return nil
	}

	expected, err := expectedCollectorFor(order)
	if err != nil {
		return fmt.Errorf("%w: %v", errProviderUnavailable, err)
	}
	if err := checkPaymentMatchesOrder(payment, order, expected); err != nil {
		log.Printf("ALERTA: pagamento %d não libera o pedido #%d: %v", paymentID, order.ID, err)
		return nil
	}

	method := "cartao"
	if payment.PaymentTypeID == "bank_transfer" || payment.PaymentMethodID == "pix" {
		method = "pix"
	}

	changed, err := models.MarkOrderPaid(order.ID, payment.PaidAmountCents, method)
	if err != nil {
		if errors.Is(err, models.ErrUnderpaid) {
			log.Printf("ALERTA: pedido #%d recebeu pagamento %d abaixo do total; conferir manualmente: %v", order.ID, paymentID, err)
			return nil
		}
		return err
	}
	if !changed {
		return nil
	}

	log.Printf("pedido #%d marcado como PAGO (pagamento %d, %s)", order.ID, paymentID, method)
	notifyStoreOwner(order)
	sendReceiptOnPayment(order.ID)
	return nil
}

// resolveOrder localiza o pedido pela referência externa, que é gravada na
// criação da cobrança. O pix_payment_id é apenas reserva para cobranças
// criadas antes desta mudança.
func resolveOrder(payment *services.PaymentDetails) (*models.Order, error) {
	if payment.ExternalReference != "" {
		orderID, err := services.ParseExternalReference(payment.ExternalReference)
		if err == nil {
			return models.GetOrder(int(orderID))
		}
	}
	order, err := models.GetOrderByPixPaymentID(payment.ID)
	if err != nil {
		return nil, fmt.Errorf("pedido não localizado para o pagamento %d", payment.ID)
	}
	return order, nil
}

// notifyStoreOwner avisa o lojista pelo WhatsApp. Roda em segundo plano para
// não segurar a resposta ao webhook, que tem tempo limite no provedor.
func notifyStoreOwner(order *models.Order) {
	phone := models.GetSetting("whatsapp_phone")
	apiKey := models.GetSetting("whatsapp_api_key")
	if phone == "" || apiKey == "" {
		return
	}
	storeName := models.GetSetting("store_name")
	if storeName == "" {
		storeName = "Livredo"
	}
	message := fmt.Sprintf(
		"✅ *Novo pedido PAGO - %s*\n\n📦 Pedido #%d\n👤 %s\n📞 %s\n💰 R$ %.2f\n\nAcesse o painel para processar o envio.",
		storeName, order.ID, order.CustomerName, order.CustomerPhone, order.Total,
	)
	go func() {
		if err := services.SendWhatsApp(phone, apiKey, message); err != nil {
			log.Printf("notificação WhatsApp: %v", err)
		}
	}()
}

// ── Leitura da notificação ──────────────────────────────────────────────────

type webhookNotification struct {
	Topic  string
	DataID string
	UserID string // conta Mercado Pago que recebeu o pagamento
}

// parseWebhookNotification aceita os dois formatos que o Mercado Pago usa: os
// parâmetros na query e o corpo JSON.
func parseWebhookNotification(r *http.Request) (*webhookNotification, error) {
	query := r.URL.Query()
	notification := &webhookNotification{
		Topic:  firstNonEmpty(query.Get("type"), query.Get("topic")),
		DataID: firstNonEmpty(query.Get("data.id"), query.Get("id")),
		UserID: query.Get("user_id"),
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err == nil && len(body) > 0 {
		var payload struct {
			Type   string `json:"type"`
			Topic  string `json:"topic"`
			Action string `json:"action"`
			Data   struct {
				ID json.RawMessage `json:"id"`
			} `json:"data"`
			Resource json.RawMessage `json:"resource"`
			UserID   json.RawMessage `json:"user_id"`
		}
		if json.Unmarshal(body, &payload) == nil {
			if notification.Topic == "" {
				notification.Topic = firstNonEmpty(payload.Type, payload.Topic, topicFromAction(payload.Action))
			}
			if notification.DataID == "" {
				notification.DataID = decodeFlexibleID(payload.Data.ID)
			}
			if notification.DataID == "" {
				notification.DataID = lastPathSegment(decodeFlexibleID(payload.Resource))
			}
			if notification.UserID == "" {
				notification.UserID = decodeFlexibleID(payload.UserID)
			}
		}
	}

	if notification.DataID == "" {
		return nil, fmt.Errorf("notificação sem identificador")
	}
	if notification.Topic == "" {
		notification.Topic = "payment"
	}
	return notification, nil
}

// PaymentIDs traduz a notificação para os IDs de pagamento a conferir. Uma
// merchant_order agrupa vários pagamentos e precisa ser expandida.
func (n *webhookNotification) PaymentIDs(accessToken string) ([]int64, error) {
	switch n.Topic {
	case "payment":
		id, err := strconv.ParseInt(n.DataID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("identificador de pagamento inválido: %q", n.DataID)
		}
		return []int64{id}, nil
	case "merchant_order":
		id, err := strconv.ParseInt(n.DataID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("identificador de merchant_order inválido: %q", n.DataID)
		}
		if accessToken == "" {
			return nil, fmt.Errorf("access token não configurado")
		}
		return services.GetMerchantOrderPaymentIDs(accessToken, id)
	default:
		return nil, fmt.Errorf("tópico ignorado: %q", n.Topic)
	}
}

func topicFromAction(action string) string {
	if strings.HasPrefix(action, "payment.") {
		return "payment"
	}
	return ""
}

// decodeFlexibleID lida com o campo id vindo ora como número, ora como texto.
func decodeFlexibleID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		return asString
	}
	var asNumber int64
	if json.Unmarshal(raw, &asNumber) == nil {
		return strconv.FormatInt(asNumber, 10)
	}
	return ""
}

func lastPathSegment(value string) string {
	value = strings.TrimRight(value, "/")
	if idx := strings.LastIndexByte(value, '/'); idx >= 0 {
		return value[idx+1:]
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func parsePaymentID(value string) int64 {
	id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// HealthCheck responde ao healthcheck do Docker e de proxies. Não expõe
// detalhes internos: apenas confirma que o processo e o banco respondem.
func HealthCheck(w http.ResponseWriter, r *http.Request) {
	if err := database.HealthCheck(); err != nil {
		http.Error(w, "indisponível", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}
